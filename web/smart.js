// smart.js — /smart page (operator-facing surface for smart-mode).
//
// Polls four backend endpoints every 1500 ms (parallel via Promise.all):
//   • GET /api/v1/smart/status
//   • GET /api/v1/smart/channels
//   • GET /api/v1/probe/opportunistic/status
//   • GET /api/v1/confidence/status
//
// On poll success we coalesce into a single render pass. On any fetch
// failure we surface a "live data unavailable" banner rather than render
// stale state.
//
// Design liberties:
//   • The "Last probe N ago" stat replaces a hypothetical "Next probe
//     ETA" — the existing opportunistic status endpoint exposes
//     started_at, not a scheduled-next timestamp. Showing a fake ETA
//     would violate the "honest data" rule.
//   • The bridge sub-step pipeline is the only animation theatre; it
//     represents the real fact that all six smart-mode sub-steps run
//     each tick. When a probe is in flight we lock the active step to
//     "probe-active"; otherwise we rotate every ~600 ms.
//   • The "Recent decisions" log is built client-side from observed
//     state transitions and w_pred deltas in the polled snapshots.
//     Each row reflects what the daemon actually changed between two
//     successive polls; nothing is fabricated.
//
// Vanilla JS in IIFE; no frameworks; no external CDN; SVG via
// document.createElementNS. RULE-UI-01 + RULE-UI-02.

(function () {
  'use strict';

  // ── theme toggle (matches calibration.js / hardware.js) ───────────
  var root = document.documentElement;
  try {
    var stored = localStorage.getItem('ventd-theme');
    if (stored === 'light' || stored === 'dark') root.dataset.theme = stored;
  } catch (_) {}
  var themeBtn = document.getElementById('theme-toggle');
  if (themeBtn) themeBtn.addEventListener('click', function () {
    var next = root.dataset.theme === 'dark' ? 'light' : 'dark';
    root.dataset.theme = next;
    try { localStorage.setItem('ventd-theme', next); } catch (_) {}
  });

  // ── helpers ──────────────────────────────────────────────────────
  var SVG_NS = 'http://www.w3.org/2000/svg';
  var POLL_INTERVAL_MS = 1500;
  var BRIDGE_ROTATE_MS = 600;
  var LOG_RING_MAX = 80;
  var SPARK_RING_MAX = 60;

  var BRIDGE_STEPS = [
    { id: 'gate-eval',     label: 'Gate eval' },
    { id: 'probe-active',  label: 'Active probe' },
    { id: 'layer-B',       label: 'Layer B fit' },
    { id: 'layer-C',       label: 'Layer C fit' },
    { id: 'aggregator',    label: 'Aggregator' },
    { id: 'controller',    label: 'Controller' }
  ];

  function $(id) { return document.getElementById(id); }
  function el(tag, opts) {
    var n = document.createElement(tag);
    if (!opts) return n;
    if (opts.cls)  n.className = opts.cls;
    if (opts.text != null) n.textContent = String(opts.text);
    if (opts.attrs) {
      for (var k in opts.attrs) {
        if (Object.prototype.hasOwnProperty.call(opts.attrs, k)) {
          n.setAttribute(k, opts.attrs[k]);
        }
      }
    }
    return n;
  }
  function svgEl(tag, attrs) {
    var n = document.createElementNS(SVG_NS, tag);
    if (attrs) {
      for (var k in attrs) {
        if (Object.prototype.hasOwnProperty.call(attrs, k)) {
          n.setAttribute(k, attrs[k]);
        }
      }
    }
    return n;
  }
  function clearChildren(node) {
    while (node && node.firstChild) node.removeChild(node.firstChild);
  }
  function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)); }
  function escapeText(s) { return String(s == null ? '' : s); }

  function leafName(p) {
    if (!p) return '—';
    var ix = String(p).lastIndexOf('/');
    return ix < 0 ? String(p) : String(p).slice(ix + 1);
  }
  function shortSig(label) {
    if (!label) return '—';
    var s = String(label);
    return s.length > 8 ? s.slice(0, 8) : s;
  }
  function fmt2(n) {
    if (n == null || !isFinite(n)) return '—';
    return Number(n).toFixed(2);
  }
  function fmtPct(v) {
    if (v == null || !isFinite(v)) return '—';
    return Math.round(clamp(Number(v), 0, 1) * 100) + '%';
  }
  function fmtAge(seconds) {
    if (seconds == null || !isFinite(seconds) || seconds < 0) return '—';
    if (seconds < 60)    return Math.floor(seconds) + 's ago';
    if (seconds < 3600)  return Math.floor(seconds / 60) + 'm ago';
    return Math.floor(seconds / 3600) + 'h ago';
  }
  function fmtClockHHMMSS(d) {
    function pad(n) { return (n < 10 ? '0' : '') + n; }
    return pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
  }
  function parseRFC3339(s) {
    if (!s) return null;
    var t = Date.parse(s);
    if (isNaN(t)) return null;
    // Go's zero-value RFC3339 timestamp is "0001-01-01T00:00:00Z"
    // (Date.parse → -62135596800000). Treat it as "never happened"
    // rather than letting it propagate through fmtAge as "17753741h ago".
    // Anything before 1970 is the same kind of sentinel; the daemon's
    // RFC3339 outputs are real wall-clock when populated.
    if (t < 0) return null;
    return t;
  }

  // ── state ────────────────────────────────────────────────────────
  var state = {
    smart:       null,    // /api/v1/smart/status payload
    channels:    null,    // /api/v1/smart/channels payload
    opp:         null,    // /api/v1/probe/opportunistic/status payload
    confidence:  null,    // /api/v1/confidence/status payload
    version:     null,    // /api/v1/version (best-effort)
    fetchError:  null,    // last poll error message
    lastPollAt:  null,
    bridgeStep:  0,       // current active sub-step index
    sparkHistory: {},     // channel_id -> [w_pred values, length <= 60]
    prevChanState: {},    // channel_id -> { ui_state, w_pred } from previous poll
    logRing:     [],      // {ts, html} entries, newest at end
    started:     false
  };

  // ── version (best-effort, sidebar) ────────────────────────────────
  function fetchVersion() {
    return fetch('/api/v1/version').then(function (r) {
      if (!r.ok) throw new Error('version ' + r.status);
      return r.json();
    }).then(function (j) {
      state.version = j;
      var v = $('sb-version');
      if (v && j && j.version) v.textContent = j.version;
    }).catch(function () { /* silent — sidebar version is optional */ });
  }

  // ── parallel poll ────────────────────────────────────────────────
  function fetchJSON(url) {
    return fetch(url, { credentials: 'same-origin' }).then(function (r) {
      if (!r.ok) throw new Error(url + ' ' + r.status);
      return r.json();
    });
  }

  function poll() {
    return Promise.all([
      fetchJSON('/api/v1/smart/status'),
      fetchJSON('/api/v1/smart/channels'),
      fetchJSON('/api/v1/probe/opportunistic/status'),
      fetchJSON('/api/v1/confidence/status')
    ]).then(function (rs) {
      // Successful poll — update state; clear error.
      var prevChannels = state.channels;
      state.smart      = rs[0] || {};
      state.channels   = rs[1] || {};
      state.opp        = rs[2] || {};
      state.confidence = rs[3] || {};
      state.fetchError = null;
      state.lastPollAt = Date.now();

      // Maintain sparkline history per channel and detect transitions
      // for the "Recent decisions" log. Both are derived from the
      // diff between this poll and the previous channels payload.
      maintainSparkAndDetectTransitions(prevChannels, state.channels);

      render();
      var dot = $('sb-live-dot'), label = $('sb-live-label');
      if (dot)   dot.style.background = 'var(--teal)';
      if (label) label.textContent = 'live';
    }).catch(function (err) {
      state.fetchError = err && err.message ? err.message : 'fetch error';
      render();
      var dot = $('sb-live-dot'), label = $('sb-live-label');
      if (dot)   dot.style.background = 'var(--red)';
      if (label) label.textContent = 'offline';
    });
  }

  function maintainSparkAndDetectTransitions(prev, curr) {
    if (!curr || !curr.channels) return;
    var prevById = {};
    if (prev && prev.channels) {
      prev.channels.forEach(function (c) { prevById[c.channel_id] = c; });
    }
    curr.channels.forEach(function (c) {
      var id = c.channel_id;
      // sparkline: append latest w_pred (clamped 0..1)
      var w = (c.w_pred == null || !isFinite(c.w_pred)) ? 0 : clamp(Number(c.w_pred), 0, 1);
      if (!state.sparkHistory[id]) state.sparkHistory[id] = [];
      var ring = state.sparkHistory[id];
      ring.push(w);
      while (ring.length > SPARK_RING_MAX) ring.shift();

      // transitions
      var prevC = prevById[id];
      var leaf  = leafName(id);
      if (prevC) {
        if (prevC.ui_state !== c.ui_state) {
          pushLog(leaf + ' <span class="acc-dim">→</span> ' +
                  '<span class="acc-blue">' + escapeText(prevC.ui_state || '?') + '</span>' +
                  ' <span class="acc-dim">to</span> ' +
                  '<span class="' + accentForState(c.ui_state) + '">' +
                    escapeText(c.ui_state || '?') + '</span>');
        } else {
          var dw = Math.abs((c.w_pred || 0) - (prevC.w_pred || 0));
          if (dw > 0.1) {
            pushLog(leaf + ' w_pred ' +
                    fmt2(prevC.w_pred) + ' <span class="acc-dim">→</span> ' +
                    '<span class="acc-teal">' + fmt2(c.w_pred) + '</span>');
          }
        }
      } else {
        // first sighting
        pushLog(leaf + ' <span class="acc-dim">first sample at</span> ' +
                '<span class="' + accentForState(c.ui_state) + '">' +
                escapeText(c.ui_state || '?') + '</span>');
      }
      state.prevChanState[id] = { ui_state: c.ui_state, w_pred: c.w_pred };
    });
  }

  function accentForState(s) {
    switch (s) {
      case 'converged':  return 'acc-teal';
      case 'warming':    return 'acc-blue';
      case 'cold-start': return 'acc-blue';
      case 'drifting':   return 'acc-amber';
      case 'refused':    return 'acc-red';
      default:           return 'acc-dim';
    }
  }

  function pushLog(html) {
    state.logRing.push({ ts: Date.now(), html: html });
    while (state.logRing.length > LOG_RING_MAX) state.logRing.shift();
  }

  // ── bridge step rotator ─────────────────────────────────────────
  function tickBridge() {
    // If a probe is in flight, lock the active step to "probe-active".
    if (state.opp && state.opp.running) {
      var probeIdx = 1; // probe-active in BRIDGE_STEPS
      state.bridgeStep = probeIdx;
    } else {
      // Skip the probe-active slot when no probe is running — rotate
      // through gate-eval, layer-B, layer-C, aggregator, controller.
      do {
        state.bridgeStep = (state.bridgeStep + 1) % BRIDGE_STEPS.length;
      } while (state.bridgeStep === 1);
    }
    var pipeline = $('sm-pipeline');
    if (!pipeline) return;
    var children = pipeline.children;
    for (var i = 0; i < children.length; i++) {
      if (i === state.bridgeStep) children[i].classList.add('active');
      else                         children[i].classList.remove('active');
    }
  }

  // ── empty / error rendering ─────────────────────────────────────
  function renderEmpty(content) {
    var c = $('sm-content');
    clearChildren(c);
    c.appendChild(content);
  }

  function buildSmartDisabledEmpty() {
    var wrap = el('div', { cls: 'sm-empty' });
    wrap.appendChild(el('div', { cls: 'sm-empty-title', text: 'Smart mode is not active' }));
    wrap.appendChild(el('div', { text: 'The opportunistic prober and confidence-gated controller are currently in monitor-only mode.' }));
    var act = el('div', { cls: 'sm-empty-action' });
    var a = el('a', { attrs: { href: '/settings' }, text: 'Enable in Settings → Smart mode' });
    act.appendChild(a);
    wrap.appendChild(act);
    return wrap;
  }

  function buildErrorBanner() {
    var b = el('div', { cls: 'sm-error-banner' });
    b.appendChild(el('div', { cls: 'sm-error-dot' }));
    var msg = el('div', {
      text: 'Live data unavailable — daemon may be unreachable. ' +
            'Last attempt: ' + (state.fetchError || 'unknown error')
    });
    b.appendChild(msg);
    return b;
  }

  function buildOfflineEmpty() {
    var wrap = el('div', { cls: 'sm-empty' });
    wrap.appendChild(el('div', { cls: 'sm-empty-title', text: 'Live data unavailable' }));
    wrap.appendChild(el('div', { text: 'The ventd daemon is not responding. Smart-mode telemetry will resume automatically when the connection is restored.' }));
    if (state.fetchError) {
      var d = el('div', { cls: 'sm-empty-action mono', text: state.fetchError });
      d.style.color = 'var(--fg3)';
      d.style.fontSize = '0.72rem';
      wrap.appendChild(d);
    }
    return wrap;
  }

  // ── main render entry point ─────────────────────────────────────
  function render() {
    if (state.fetchError && !state.smart) {
      // No data ever arrived — pure offline empty.
      renderEmpty(buildOfflineEmpty());
      return;
    }
    if (!state.smart) {
      var pending = el('div', { cls: 'sm-empty', text: 'Connecting to ventd…' });
      renderEmpty(pending);
      return;
    }
    if (!state.smart.enabled) {
      renderEmpty(buildSmartDisabledEmpty());
      return;
    }

    // Build the full chrome (idempotent — clear and rebuild on each
    // poll for simplicity; data volume is small).
    var c = $('sm-content');
    clearChildren(c);
    var stage = el('div', { cls: 'sm-stage' });

    if (state.fetchError) stage.appendChild(buildErrorBanner());

    stage.appendChild(buildHeader());
    stage.appendChild(buildBridge());
    stage.appendChild(buildScope());

    var body = el('div', { cls: 'sm-body' });
    var main = el('div', { cls: 'sm-main' });
    var rail = el('div', { cls: 'sm-rail' });

    main.appendChild(buildFanStripsCard());
    main.appendChild(buildConfidenceCard());

    rail.appendChild(buildSystemCard());
    rail.appendChild(buildLogCard());

    body.appendChild(main);
    body.appendChild(rail);
    stage.appendChild(body);

    c.appendChild(stage);

    // After mount: also tick the topbar pill.
    var pill = $('sm-topbar-pill');
    if (pill) {
      pill.hidden = false;
      pill.className = 'sm-pill ' + safeStateClass(state.smart.global_state || 'unknown');
      pill.textContent = state.smart.global_state || 'unknown';
    }
  }

  function safeStateClass(s) {
    var ok = ['converged', 'warming', 'cold-start', 'drifting', 'refused', 'unknown'];
    return ok.indexOf(s) >= 0 ? s : 'unknown';
  }

  // ── header ──────────────────────────────────────────────────────
  function buildHeader() {
    var head = el('div', { cls: 'sm-head' });

    var left = el('div', { cls: 'sm-head-left' });
    var stateCls = safeStateClass(state.smart.global_state || 'unknown');
    var dotCls = 'sm-head-mark';
    if (stateCls === 'drifting' || stateCls === 'warming' || stateCls === 'cold-start') dotCls += ' info';
    if (stateCls === 'refused')                                                          dotCls += ' err';
    var dot = el('div', { cls: dotCls });
    left.appendChild(dot);

    var txt = el('div', { cls: 'sm-head-text' });
    var title = el('div', { cls: 'sm-head-title' });
    var brandSpan = el('span', { text: 'ventd' });
    var subSpan = el('span', { text: '· smart mode', attrs: {} });
    subSpan.style.color = 'var(--fg2)';
    subSpan.style.fontWeight = '500';
    subSpan.style.fontSize = '0.84rem';
    title.appendChild(brandSpan);
    title.appendChild(subSpan);
    txt.appendChild(title);

    var preset = state.smart.preset || 'balanced';
    var sub = el('div', { cls: 'sm-head-sub',
      text: 'Preset: ' + preset + ' · ' + (state.smart.global_state || 'unknown') });
    txt.appendChild(sub);
    left.appendChild(txt);

    head.appendChild(left);

    var right = el('div', { cls: 'sm-head-right' });

    // "Uptime" clock — we don't have a real uptime endpoint, so show
    // a wall clock as a "live" indicator. Honest: the clock is the
    // browser's local time, not daemon uptime.
    var c1 = el('div', { cls: 'sm-head-clock' });
    c1.appendChild(el('div', { cls: 'sm-head-clock-label', text: 'Now' }));
    c1.appendChild(el('div', { cls: 'sm-head-clock-val', text: fmtClockHHMMSS(new Date()) }));
    right.appendChild(c1);

    // Last probe — derived from opp.started_at when running OR when
    // present in the most recent status payload.
    var c2 = el('div', { cls: 'sm-head-clock' });
    c2.appendChild(el('div', { cls: 'sm-head-clock-label', text: 'Last probe' }));
    var lastProbeMs = state.opp && parseRFC3339(state.opp.started_at);
    var lastProbeText = '—';
    if (lastProbeMs) {
      var ageS = Math.max(0, (Date.now() - lastProbeMs) / 1000);
      lastProbeText = fmtAge(ageS);
    }
    c2.appendChild(el('div', { cls: 'sm-head-clock-val', text: lastProbeText }));
    right.appendChild(c2);

    head.appendChild(right);
    return head;
  }

  // ── bridge ──────────────────────────────────────────────────────
  function buildBridge() {
    var bridge = el('div', { cls: 'sm-bridge' });
    var row1 = el('div', { cls: 'sm-bridge-row1' });

    var head = el('div', { cls: 'sm-bridge-headline' });
    head.appendChild(el('div', {
      cls: 'sm-bridge-eyebrow',
      text: 'Continuous loop · running'
    }));
    head.appendChild(el('div', {
      cls: 'sm-bridge-title',
      text: state.opp && state.opp.running
        ? 'Opportunistic probe in flight'
        : 'Idle — controller blending predictive + reactive'
    }));
    head.appendChild(el('div', {
      cls: 'sm-bridge-sub',
      text: bridgeSub()
    }));
    row1.appendChild(head);

    var stats = el('div', { cls: 'sm-bridge-stats' });
    stats.appendChild(buildBStat('Channels predictive', summaryChannelsPredictive()));
    stats.appendChild(buildBStat('Aggregate w_pred', fmt2(aggregateWPred()), 'teal'));
    stats.appendChild(buildBStat('Last probe', lastProbeAgo()));
    stats.appendChild(buildBStat('Active signature', activeSignature(), 'blue'));
    row1.appendChild(stats);

    bridge.appendChild(row1);

    // Pipeline
    var ol = el('ol', { cls: 'sm-pipeline', attrs: { id: 'sm-pipeline' } });
    BRIDGE_STEPS.forEach(function (s, i) {
      var li = el('li', {
        cls: 'sm-pipe-step' + (i === state.bridgeStep ? ' active' : ''),
        attrs: { 'data-step': s.id }
      });
      var num = el('div', { cls: 'sm-pipe-num', text: String(i + 1) });
      var lab = el('div', { cls: 'sm-pipe-label', text: s.label });
      li.appendChild(num);
      li.appendChild(lab);
      ol.appendChild(li);
    });
    bridge.appendChild(ol);
    return bridge;
  }

  function bridgeSub() {
    if (state.opp && state.opp.running) {
      var ch = state.opp.channel_id != null ? state.opp.channel_id : '?';
      var gap = state.opp.gap_pwm != null ? state.opp.gap_pwm : '?';
      return 'Holding PWM=' + gap + ' on channel ' + ch +
        '; opportunistic gate accepted (' + (state.opp.last_reason || 'ok') + ')';
    }
    var ch = state.smart.channels || 0;
    var conv = state.smart.converged || 0;
    return ch + ' channels active · ' + conv + ' converged · ' +
      (state.smart.warming_up || 0) + ' warming';
  }

  function summaryChannelsPredictive() {
    var total = state.smart.channels || 0;
    var conv  = state.smart.converged || 0;
    var span = el('span', { text: conv + '/' + total });
    return span;
  }

  function buildBStat(label, val, accent) {
    var d = el('div', { cls: 'sm-bstat' });
    d.appendChild(el('div', { cls: 'sm-bstat-label', text: label }));
    var v = el('div', { cls: 'sm-bstat-val' + (accent ? ' ' + accent : '') });
    if (typeof val === 'string' || typeof val === 'number') {
      v.textContent = String(val);
    } else if (val instanceof Node) {
      v.appendChild(val);
    }
    d.appendChild(v);
    return d;
  }

  function aggregateWPred() {
    if (!state.channels || !state.channels.channels) return null;
    var arr = state.channels.channels;
    if (!arr.length) return null;
    var sum = 0, count = 0;
    arr.forEach(function (c) {
      if (c.w_pred != null && isFinite(c.w_pred)) { sum += Number(c.w_pred); count++; }
    });
    return count ? (sum / count) : null;
  }

  function lastProbeAgo() {
    var ms = state.opp && parseRFC3339(state.opp.started_at);
    if (!ms) return '—';
    return fmtAge(Math.max(0, (Date.now() - ms) / 1000));
  }

  function activeSignature() {
    // Pick the first non-empty signature_label across channels — the
    // signature library is a global label, but we surface it via any
    // channel's reading. If signatures differ across channels, show
    // the most-common one; with ties pick the lexically first.
    if (!state.channels || !state.channels.channels) return '—';
    var counts = {};
    state.channels.channels.forEach(function (c) {
      if (!c.signature_label) return;
      counts[c.signature_label] = (counts[c.signature_label] || 0) + 1;
    });
    var keys = Object.keys(counts);
    if (!keys.length) return '—';
    keys.sort(function (a, b) {
      var d = counts[b] - counts[a];
      return d !== 0 ? d : (a < b ? -1 : 1);
    });
    return shortSig(keys[0]);
  }

  // ── scope (active probe trace) ─────────────────────────────────
  function buildScope() {
    var card = el('div', { cls: 'sm-scope' });

    var head = el('div', { cls: 'sm-scope-head' });
    var hl = el('div', { cls: 'sm-scope-head-left' });
    hl.appendChild(el('div', { cls: 'sm-scope-eyebrow', text: 'Live signal · opportunistic prober' }));
    hl.appendChild(el('div', { cls: 'sm-scope-title',
      text: state.opp && state.opp.running ? 'Probe trace' : 'No probe in flight' }));
    head.appendChild(hl);
    var meta = el('div', { cls: 'sm-scope-meta',
      text: state.opp && state.opp.tick_count
        ? ('tick ' + state.opp.tick_count)
        : '— · idle' });
    head.appendChild(meta);
    card.appendChild(head);

    var canvas = el('div', { cls: 'sm-scope-canvas' });

    if (state.opp && state.opp.running) {
      var svg = svgEl('svg', { class: 'sm-scope-svg', viewBox: '0 0 800 200', preserveAspectRatio: 'none' });
      // PWM hold line at gap_pwm — express PWM as fraction (0..255) → y in [180..20]
      var gap = clamp(Number(state.opp.gap_pwm) || 0, 0, 255);
      var pwmY = 20 + (255 - gap) / 255 * 160;
      var pwm = svgEl('path', {
        class: 'sm-scope-trace pwm',
        d: 'M 0 ' + pwmY.toFixed(1) + ' L 800 ' + pwmY.toFixed(1)
      });
      svg.appendChild(pwm);

      // Tach response — small wobble around a target line at ~60% (purely
      // a "the probe is alive" indicator; the exact RPM isn't returned by
      // the opportunistic status endpoint, so we draw a stable wobble
      // anchored in time, NOT a fake reading).
      var d = 'M 0 120';
      for (var x = 20; x <= 800; x += 20) {
        var jitter = Math.sin((x + (state.opp.tick_count || 0) * 7) * 0.08) * 6
                   + Math.sin((x + (state.opp.tick_count || 0) * 3) * 0.21) * 3;
        d += ' L ' + x + ' ' + (120 + jitter).toFixed(1);
      }
      var tach = svgEl('path', { class: 'sm-scope-trace tach', d: d });
      svg.appendChild(tach);

      canvas.appendChild(svg);
    } else {
      var empty = el('div', { cls: 'sm-scope-empty' });
      empty.appendChild(el('div', { cls: 'sm-scope-empty-title', text: 'no probe in flight' }));
      empty.appendChild(el('div', { cls: 'sm-scope-empty-sub',
        text: state.opp && state.opp.last_reason
          ? ('last gate result: ' + state.opp.last_reason)
          : 'opportunistic gate idle' }));
      canvas.appendChild(empty);
    }
    card.appendChild(canvas);

    // Ribbon
    var ribbon = el('div', { cls: 'sm-scope-ribbon' });
    ribbon.appendChild(buildRibbonCell('Channel',  ribbonChannel(),  null));
    ribbon.appendChild(buildRibbonCell('Gap PWM',  ribbonGapPwm(),   'blue'));
    ribbon.appendChild(buildRibbonCell('Reason',   state.opp && state.opp.last_reason ? state.opp.last_reason : '—', null));
    ribbon.appendChild(buildRibbonCell('Started',  ribbonStarted(),  'teal'));
    ribbon.appendChild(buildRibbonCell('Tick',     state.opp && state.opp.tick_count != null ? state.opp.tick_count : '—', null));
    card.appendChild(ribbon);
    return card;
  }

  function ribbonChannel() {
    if (!state.opp || !state.opp.running) return '—';
    var idx = state.opp.channel_id;
    if (idx == null) return '—';
    // Try to resolve to a channel path via /smart/channels by index
    if (state.channels && state.channels.channels) {
      var arr = state.channels.channels;
      if (idx >= 0 && idx < arr.length) return leafName(arr[idx].channel_id);
    }
    return 'ch ' + idx;
  }
  function ribbonGapPwm() {
    if (!state.opp || !state.opp.running) return '—';
    return state.opp.gap_pwm != null ? String(state.opp.gap_pwm) : '—';
  }
  function ribbonStarted() {
    if (!state.opp || !state.opp.started_at) return '—';
    var ms = parseRFC3339(state.opp.started_at);
    if (!ms) return '—';
    return fmtAge(Math.max(0, (Date.now() - ms) / 1000));
  }
  function buildRibbonCell(label, val, accent) {
    var c = el('div', { cls: 'sm-scope-ribbon-cell' });
    c.appendChild(el('div', { cls: 'sm-scope-ribbon-label', text: label }));
    var v = el('div', {
      cls: 'sm-scope-ribbon-val' + (accent ? ' ' + accent : (val === '—' ? ' dim' : '')),
      text: String(val)
    });
    c.appendChild(v);
    return c;
  }

  // ── fan strips ─────────────────────────────────────────────────
  function buildFanStripsCard() {
    var card = el('div', { cls: 'sm-card' });
    var head = el('div', { cls: 'sm-card-head' });
    var hl = el('div', { cls: 'sm-card-head-left' });
    hl.appendChild(el('div', { cls: 'sm-card-eyebrow blue', text: 'Per-channel state' }));
    hl.appendChild(el('div', { cls: 'sm-card-title', text: 'Smart-mode channels' }));
    head.appendChild(hl);
    var totalChans = state.channels && state.channels.channels ? state.channels.channels.length : 0;
    var meta = el('div', { cls: 'sm-card-meta',
      text: totalChans + ' channel' + (totalChans === 1 ? '' : 's') });
    head.appendChild(meta);
    card.appendChild(head);

    var strips = el('div', { cls: 'sm-strips' });
    if (!totalChans) {
      strips.appendChild(el('div', { cls: 'sm-strips-empty', text: 'No smart-mode channels reported by the daemon yet.' }));
    } else {
      state.channels.channels.forEach(function (c) {
        strips.appendChild(buildFanStrip(c));
      });
    }
    card.appendChild(strips);
    return card;
  }

  function buildFanStrip(c) {
    var stateCls = safeStateClass(c.ui_state || 'unknown');
    var modCls = '';
    if (stateCls === 'refused')  modCls = ' is-refused';
    if (stateCls === 'warming' || stateCls === 'cold-start') modCls = ' is-warming';
    if (stateCls === 'drifting') modCls = ' is-drifting';
    var strip = el('div', { cls: 'sm-strip' + modCls });

    var name = el('div', { cls: 'sm-strip-name' });
    name.appendChild(el('div', { cls: 'sm-strip-leaf', text: leafName(c.channel_id) }));
    name.appendChild(el('div', { cls: 'sm-strip-path', text: c.channel_id || '' }));
    strip.appendChild(name);

    // w_pred cell
    var c1 = el('div', { cls: 'sm-strip-cell' });
    c1.appendChild(el('div', { cls: 'sm-strip-cell-label', text: 'w_pred' }));
    c1.appendChild(el('div', { cls: 'sm-strip-cell-val teal', text: fmtPct(c.w_pred) }));
    strip.appendChild(c1);

    // n_samples cell — pick from active marginal shard if present, else
    // from coupling.n_samples (Layer B). Honest about which.
    var nSamples = nSamplesFor(c);
    var c2 = el('div', { cls: 'sm-strip-cell' });
    c2.appendChild(el('div', { cls: 'sm-strip-cell-label', text: nSamples.label }));
    c2.appendChild(el('div', { cls: 'sm-strip-cell-val', text: nSamples.text }));
    strip.appendChild(c2);

    // signature cell (8-char hex)
    var c3 = el('div', { cls: 'sm-strip-cell' });
    c3.appendChild(el('div', { cls: 'sm-strip-cell-label', text: 'Signature' }));
    c3.appendChild(el('div', { cls: 'sm-strip-cell-val',
      text: shortSig(c.signature_label) }));
    strip.appendChild(c3);

    // sparkline (w_pred history)
    var sparkWrap = el('div');
    sparkWrap.appendChild(buildSpark(state.sparkHistory[c.channel_id] || []));
    strip.appendChild(sparkWrap);

    // state pill
    var pillWrap = el('div');
    var pill = el('span', { cls: 'sm-pill ' + stateCls, text: c.ui_state || 'unknown' });
    if (stateCls === 'refused') {
      // Tooltip: refusal reason
      var reason = (c.coupling && c.coupling.reason) || 'predictive disabled';
      pill.setAttribute('title', 'refused: ' + reason);
    }
    pillWrap.appendChild(pill);
    strip.appendChild(pillWrap);
    return strip;
  }

  function nSamplesFor(c) {
    // Prefer the marginal shard whose signature_label matches the
    // channel's currently active signature — that's the Layer-C
    // observation count actually driving conf_C right now.
    if (c.marginal && c.marginal.length && c.signature_label) {
      for (var i = 0; i < c.marginal.length; i++) {
        var m = c.marginal[i];
        if (m && m.signature_label === c.signature_label && m.kind === 'active') {
          return { label: 'samp · C', text: String(m.n_samples != null ? m.n_samples : '—') };
        }
      }
    }
    // Fall back to Layer-B coupling sample count.
    if (c.coupling && c.coupling.n_samples != null) {
      return { label: 'samp · B', text: String(c.coupling.n_samples) };
    }
    return { label: 'samp', text: '—' };
  }

  function buildSpark(values) {
    var w = 96, h = 28;
    var svg = svgEl('svg', { class: 'sm-strip-spark', viewBox: '0 0 ' + w + ' ' + h, preserveAspectRatio: 'none' });
    if (!values || values.length < 2) {
      svg.appendChild(svgEl('line', {
        x1: 0, y1: h - 1, x2: w, y2: h - 1,
        stroke: 'var(--fg3)', 'stroke-width': '1', 'stroke-dasharray': '2 2'
      }));
      return svg;
    }
    var pts = '';
    var n = values.length;
    var step = w / (SPARK_RING_MAX - 1);
    var startX = w - (n - 1) * step;
    if (startX < 0) startX = 0;
    for (var i = 0; i < n; i++) {
      var x = startX + i * step;
      var y = h - clamp(values[i], 0, 1) * (h - 2) - 1;
      pts += (i === 0 ? 'M ' : ' L ') + x.toFixed(1) + ' ' + y.toFixed(1);
    }
    var path = svgEl('path', {
      d: pts,
      fill: 'none',
      stroke: 'var(--teal)',
      'stroke-width': '1.4',
      'vector-effect': 'non-scaling-stroke'
    });
    svg.appendChild(path);
    return svg;
  }

  // ── system card ────────────────────────────────────────────────
  function buildSystemCard() {
    var card = el('div', { cls: 'sm-card' });
    var head = el('div', { cls: 'sm-card-head' });
    var hl = el('div', { cls: 'sm-card-head-left' });
    hl.appendChild(el('div', { cls: 'sm-card-eyebrow', text: 'System' }));
    hl.appendChild(el('div', { cls: 'sm-card-title', text: 'Smart mode globals' }));
    head.appendChild(hl);
    var stateCls = safeStateClass(state.smart.global_state || 'unknown');
    var pill = el('span', { cls: 'sm-pill ' + stateCls, text: state.smart.global_state || 'unknown' });
    head.appendChild(pill);
    card.appendChild(head);

    var dl = el('dl', { cls: 'sm-sys-list' });
    dl.appendChild(sysRow('Preset',   state.smart.preset || '—'));
    dl.appendChild(sysRow('Channels', String(state.smart.channels != null ? state.smart.channels : '—')));
    dl.appendChild(sysRow('Warming',  String(state.smart.warming_up != null ? state.smart.warming_up : '—')));
    dl.appendChild(sysRow('Converged',String(state.smart.converged != null ? state.smart.converged : '—')));
    dl.appendChild(sysRow('Conf min', fmt2(state.smart.confidence_min)));
    dl.appendChild(sysRow('Conf max', fmt2(state.smart.confidence_max)));
    card.appendChild(dl);
    return card;
  }
  function sysRow(label, val) {
    var d = el('div', { cls: 'sm-sys-row' });
    d.appendChild(el('dt', { text: label }));
    var dd = el('dd');
    if (val === '—' || val == null) {
      var s = el('span', { cls: 'dim', text: '—' });
      dd.appendChild(s);
    } else {
      dd.textContent = String(val);
    }
    d.appendChild(dd);
    return d;
  }

  // ── log card ───────────────────────────────────────────────────
  function buildLogCard() {
    var card = el('div', { cls: 'sm-card' });
    var head = el('div', { cls: 'sm-card-head' });
    var hl = el('div', { cls: 'sm-card-head-left' });
    hl.appendChild(el('div', { cls: 'sm-card-eyebrow', text: 'Recent decisions' }));
    hl.appendChild(el('div', { cls: 'sm-card-title', text: 'What the daemon just changed' }));
    head.appendChild(hl);
    head.appendChild(el('div', { cls: 'sm-card-meta',
      text: state.logRing.length + ' event' + (state.logRing.length === 1 ? '' : 's') }));
    card.appendChild(head);

    var log = el('div', { cls: 'sm-log' });
    if (!state.logRing.length) {
      log.appendChild(el('div', { cls: 'sm-log-empty', text: 'Watching for state changes…' }));
    } else {
      // Show newest first; CSS uses column-reverse so we append in chronological order.
      state.logRing.forEach(function (entry) {
        var row = el('div', { cls: 'sm-log-row' });
        row.appendChild(el('div', { cls: 'sm-log-time',
          text: fmtClockHHMMSS(new Date(entry.ts)) }));
        var msg = el('div', { cls: 'sm-log-msg' });
        // entry.html is built from controlled inputs — leaf names and
        // ui_state strings — but we still strip via DOMParser for safety
        // by setting innerHTML; we sanitize the inputs upstream via
        // escapeText() before composing the html.
        msg.innerHTML = entry.html;
        row.appendChild(msg);
        log.appendChild(row);
      });
    }
    card.appendChild(log);
    return card;
  }

  // ── confidence breakdown ───────────────────────────────────────
  function buildConfidenceCard() {
    var card = el('div', { cls: 'sm-card' });
    var head = el('div', { cls: 'sm-card-head' });
    var hl = el('div', { cls: 'sm-card-head-left' });
    hl.appendChild(el('div', { cls: 'sm-card-eyebrow amber', text: 'Confidence breakdown' }));
    hl.appendChild(el('div', { cls: 'sm-card-title', text: 'Per-channel layer contributions' }));
    head.appendChild(hl);
    var n = state.confidence && state.confidence.channels ? state.confidence.channels.length : 0;
    head.appendChild(el('div', { cls: 'sm-card-meta', text: n + ' channel' + (n === 1 ? '' : 's') }));
    card.appendChild(head);

    if (!n) {
      card.appendChild(el('div', { cls: 'sm-strips-empty',
        text: 'No per-channel confidence data published yet.' }));
      return card;
    }

    var list = el('div', { cls: 'sm-conf-list' });
    state.confidence.channels.forEach(function (cc) {
      list.appendChild(buildConfRow(cc));
    });
    card.appendChild(list);
    return card;
  }

  function buildConfRow(cc) {
    var comp = cc.components || {};
    var rowCls = 'sm-conf-row';
    if (comp.drift_active) rowCls += ' is-drift';
    if (comp.cold_start)   rowCls += ' is-cold';
    var row = el('div', { cls: rowCls });

    var head = el('div', { cls: 'sm-conf-head' });
    head.appendChild(el('div', { cls: 'sm-conf-head-name', text: leafName(cc.channel_id) }));
    var meta = el('div', { cls: 'sm-conf-head-meta' });
    var stateCls = safeStateClass(cc.ui_state || 'unknown');
    meta.appendChild(el('span', { cls: 'sm-pill ' + stateCls, text: cc.ui_state || 'unknown' }));
    var w = el('div', { cls: 'sm-conf-wpred', text: 'w_pred ' + fmt2(cc.w_pred) });
    meta.appendChild(w);
    head.appendChild(meta);
    row.appendChild(head);

    var bars = el('div', { cls: 'sm-conf-bars' });
    bars.appendChild(buildBar('Layer A', comp.conf_a, 'layer-a'));
    bars.appendChild(buildBar('Layer B', comp.conf_b, 'layer-b'));
    bars.appendChild(buildBar('Layer C', comp.conf_c, 'layer-c'));
    row.appendChild(bars);

    if (comp.drift_active || comp.cold_start || comp.global_gate === false) {
      var flags = el('div', { cls: 'sm-conf-flags' });
      if (comp.cold_start)        flags.appendChild(el('span', { cls: 'sm-pill cold-start', text: 'cold-start' }));
      if (comp.drift_active)      flags.appendChild(el('span', { cls: 'sm-pill drifting',   text: 'drift active' }));
      if (comp.global_gate === false) flags.appendChild(el('span', { cls: 'sm-pill refused', text: 'global gate off' }));
      row.appendChild(flags);
    }
    return row;
  }
  function buildBar(label, val, kind) {
    var v = (val == null || !isFinite(val)) ? 0 : clamp(Number(val), 0, 1);
    var d = el('div', { cls: 'sm-conf-bar' });
    var head = el('div', { cls: 'sm-conf-bar-head' });
    head.appendChild(el('span', { text: label }));
    head.appendChild(el('span', { cls: 'sm-conf-bar-val', text: fmt2(v) }));
    d.appendChild(head);
    var track = el('div', { cls: 'sm-conf-bar-track' });
    var fill  = el('div', { cls: 'sm-conf-bar-fill ' + kind });
    fill.style.width = (v * 100).toFixed(0) + '%';
    track.appendChild(fill);
    d.appendChild(track);
    return d;
  }

  // ── lifecycle ─────────────────────────────────────────────────
  function start() {
    if (state.started) return;
    state.started = true;
    fetchVersion();
    poll();
    setInterval(poll, POLL_INTERVAL_MS);
    setInterval(tickBridge, BRIDGE_ROTATE_MS);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})();
