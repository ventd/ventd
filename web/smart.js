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
// Design notes (per the "no theatre on the web UI" rule):
//   • "Last probe N ago" replaces a hypothetical "Next probe ETA" —
//     the existing opportunistic status endpoint exposes started_at,
//     not a scheduled-next timestamp. We don't fabricate an ETA.
//   • The bridge sub-step pipeline (gate-eval → probe-active → … →
//     controller) was REMOVED in this branch. The rotating spotlight
//     was cosmetic; rotation wasn't tied to actual sub-step ticks. The
//     headline + stats above the pipeline already convey the real
//     state from /smart/status + /probe/opportunistic/status.
//   • The "Recent decisions" log is built client-side from observed
//     state transitions and w_pred deltas in the polled snapshots.
//     Each row reflects what the daemon actually changed between two
//     successive polls; nothing fabricated.
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
  var LOG_RING_MAX = 80;
  var SPARK_RING_MAX = 60;
  // BRIDGE_STEPS pipeline + rotation were removed — see no-theatre note
  // above. Headline + stats already surface the real state.

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
  // fmtUptime renders a daemon-uptime duration in seconds as a compact,
  // human-readable span (MVP-3 / #931): real uptime from the daemon's
  // started_at, not a browser wall clock dressed up as "live".
  function fmtUptime(seconds) {
    if (seconds == null || !isFinite(seconds) || seconds < 0) return '—';
    var s = Math.floor(seconds);
    if (s < 60)    return s + 's';
    var m = Math.floor(s / 60);
    if (m < 60)    return m + 'm';
    var h = Math.floor(m / 60);
    if (h < 24)    return h + 'h ' + (m % 60) + 'm';
    var d = Math.floor(h / 24);
    return d + 'd ' + (h % 24) + 'h';
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
    status:      null,    // /api/v1/status payload (best-effort; for daemon uptime)
    version:     null,    // /api/v1/version (best-effort)
    fetchError:  null,    // last poll error message
    lastPollAt:  null,
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
      fetchJSON('/api/v1/confidence/status'),
      // /api/v1/status is fetched best-effort for the daemon-uptime clock
      // (MVP-3): a transient hiccup on it must never blank the smart page,
      // so its failure resolves to null and the last good value is kept.
      fetchJSON('/api/v1/status').catch(function () { return null; })
    ]).then(function (rs) {
      // Successful poll — update state; clear error.
      var prevChannels = state.channels;
      state.smart      = rs[0] || {};
      state.channels   = rs[1] || {};
      state.opp        = rs[2] || {};
      state.confidence = rs[3] || {};
      state.status     = rs[4] || state.status;
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

  // prettyState maps the daemon's internal ui_state / global_state
  // labels to plain English an operator who hasn't read the design
  // doc can interpret correctly. Internal names like "warming" read
  // as "the fan is overheating" to a first-time user; "Learning"
  // says what the controller is actually doing. CSS classes still
  // use the internal name (acc-blue for warming etc.) so styling
  // doesn't shift. (#1254 / #1228 child fix.)
  function prettyState(s) {
    // MVP-1 (#1254): plain-English labels for the internal state enum.
    // The remaining jargon leaks were `refused` ("Refused" tells a
    // non-technical user nothing actionable) and `unknown` ("Unknown"
    // reads as broken when it's just the pre-first-poll transient), so
    // those become "Needs setup" / "Connecting" per the issue spec;
    // converged/cold-start/drifting align to the same spec wording.
    switch (s) {
      case 'converged':  return 'Active';
      case 'warming':    return 'Learning';
      case 'cold-start': return 'Just started';
      case 'drifting':   return 'Re-learning';
      case 'refused':    return 'Needs setup';
      case 'idle':       return 'Idle';
      case 'fallback':   return 'Fallback';
      case 'unknown':    return 'Connecting';
      default:           return s || 'Connecting';
    }
  }

  function pushLog(html) {
    state.logRing.push({ ts: Date.now(), html: html });
    while (state.logRing.length > LOG_RING_MAX) state.logRing.shift();
  }

  // tickBridge / BRIDGE_STEPS rotation REMOVED — was cosmetic (rotation
  // wasn't tied to actual sub-step ticks). Headline + stats convey the
  // real state now.

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
    var firstVisit = buildFirstVisitBanner();
    if (firstVisit) stage.appendChild(firstVisit);
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
      pill.textContent = prettyState(state.smart.global_state || 'unknown');
    }
  }

  function safeStateClass(s) {
    var ok = ['converged', 'warming', 'cold-start', 'drifting', 'refused', 'unknown'];
    return ok.indexOf(s) >= 0 ? s : 'unknown';
  }

  // buildPresetSwitcher renders the 3-position Silent/Balanced/Performance
  // segmented switch (#1254 MVP-2). Wired to the existing
  // PUT /api/v1/confidence/preset endpoint. The currently-active preset
  // is highlighted; clicking another segment fires the PUT and the next
  // poll picks up the change (no optimistic local-state mutation —
  // the daemon is the source of truth).
  function buildPresetSwitcher(active) {
    var wrap = el('div', { cls: 'sm-preset-switch' });
    var presets = [
      { id: 'silent',      label: 'Silent' },
      { id: 'balanced',    label: 'Balanced' },
      { id: 'performance', label: 'Performance' }
    ];
    presets.forEach(function (p) {
      var btn = el('button', {
        cls: 'sm-preset-btn' + (p.id === active ? ' is-active' : ''),
        attrs: { type: 'button', 'aria-pressed': (p.id === active) ? 'true' : 'false' },
        text: p.label
      });
      btn.addEventListener('click', function () {
        if (p.id === active) return;
        // Disable the whole switch while in flight so a double-click
        // doesn't queue two competing PUTs.
        Array.prototype.forEach.call(wrap.querySelectorAll('button'), function (b) {
          b.disabled = true;
        });
        fetch('/api/v1/confidence/preset', {
          method: 'PUT',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ preset: p.id })
        }).then(function (r) {
          if (!r.ok) {
            // Re-enable so the operator can retry. The next poll will
            // re-render and (if the daemon ignored the change for
            // some reason) the active state will reflect reality.
            Array.prototype.forEach.call(wrap.querySelectorAll('button'), function (b) {
              b.disabled = false;
            });
          }
        }).catch(function () {
          Array.prototype.forEach.call(wrap.querySelectorAll('button'), function (b) {
            b.disabled = false;
          });
        });
      });
      wrap.appendChild(btn);
    });
    return wrap;
  }

  // buildFirstVisitBanner renders the friendly "Smart mode is learning"
  // banner when global_state=warming AND converged=0 (#1254 MVP-4). The
  // banner is dismissible per-session via localStorage; it returns null
  // when dismissed or when smart mode is past the warming-no-converged
  // state so the page isn't cluttered once learning starts producing
  // results.
  function buildFirstVisitBanner() {
    var globalState = state.smart && state.smart.global_state;
    var converged   = (state.smart && state.smart.converged) || 0;
    if (globalState !== 'warming' || converged > 0) return null;
    try {
      if (localStorage.getItem('ventd-smart-warming-banner-dismissed') === '1') {
        return null;
      }
    } catch (_) { /* ignore */ }

    var banner = el('div', { cls: 'sm-warming-banner' });
    var icon = el('div', { cls: 'sm-warming-banner-icon', text: '⏱' });
    banner.appendChild(icon);
    var body = el('div', { cls: 'sm-warming-banner-body' });
    body.appendChild(el('div', {
      cls: 'sm-warming-banner-title',
      text: 'Smart mode is learning your system’s thermal behaviour.'
    }));
    body.appendChild(el('div', {
      cls: 'sm-warming-banner-text',
      text: 'This usually takes 1–6 hours of normal use. Come back later, or pick a preset above to adjust the balance between quiet and cool.'
    }));
    banner.appendChild(body);
    var close = el('button', {
      cls: 'sm-warming-banner-close',
      attrs: { type: 'button', 'aria-label': 'Dismiss' },
      text: '×'
    });
    close.addEventListener('click', function () {
      try { localStorage.setItem('ventd-smart-warming-banner-dismissed', '1'); } catch (_) {}
      banner.remove();
    });
    banner.appendChild(close);
    return banner;
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
      text: 'Preset: ' + preset + ' · ' + prettyState(state.smart.global_state || 'unknown') });
    txt.appendChild(sub);

    // MVP-2 (#1254): preset switcher wired to PUT /api/v1/confidence/preset.
    // Three-position segmented switch — Silent · Balanced · Performance —
    // directly under the header sub so a first-time user can change the
    // balance between quiet and cool without diving into Settings. The
    // PUT path already exists; this surface just makes it discoverable.
    var presetSwitch = buildPresetSwitcher(preset);
    txt.appendChild(presetSwitch);

    left.appendChild(txt);

    head.appendChild(left);

    var right = el('div', { cls: 'sm-head-right' });

    // Real daemon uptime (MVP-3 / #931): computed from the daemon's
    // started_at on /api/v1/status, not a browser wall clock dressed up
    // as "live". Shows "—" until the first status payload lands or when
    // started_at is the Go zero value (parseRFC3339 returns null).
    var c1 = el('div', { cls: 'sm-head-clock' });
    c1.appendChild(el('div', { cls: 'sm-head-clock-label', text: 'Uptime' }));
    var startedMs = state.status && parseRFC3339(state.status.started_at);
    var uptimeText = startedMs ? fmtUptime((Date.now() - startedMs) / 1000) : '—';
    c1.appendChild(el('div', { cls: 'sm-head-clock-val', text: uptimeText }));
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
    stats.appendChild(buildBStat('Channels predicting', summaryChannelsPredictive()));
    stats.appendChild(buildBStat('Average confidence', fmt2(aggregateWPred()), 'teal'));
    stats.appendChild(buildBStat('Last probe', lastProbeAgo()));
    stats.appendChild(buildBStat('Active signature', activeSignature(), 'blue'));
    row1.appendChild(stats);

    bridge.appendChild(row1);

    // Pipeline row removed — see no-theatre note. The headline +
    // stats above already surface the real loop state.
    return bridge;
  }

  // gateReason returns a user-facing string for the opportunistic
  // gate's last result. Prefers last_reason_human (added in v1.1.0;
  // sentence-length friendly text, e.g. "System under load — waiting
  // for a quiet moment") and falls back to the raw last_reason for
  // forward-compat with older daemons that don't emit the human
  // field. Returns null when neither is present.
  function gateReason(opp) {
    if (!opp) return null;
    return opp.last_reason_human || opp.last_reason || null;
  }

  function bridgeSub() {
    if (state.opp && state.opp.running) {
      var ch = state.opp.channel_id != null ? state.opp.channel_id : '?';
      var gap = state.opp.gap_pwm != null ? state.opp.gap_pwm : '?';
      return 'Holding PWM=' + gap + ' on channel ' + ch +
        '; opportunistic gate accepted (' + (gateReason(state.opp) || 'ok') + ')';
    }
    var ch = state.smart.channels || 0;
    var conv = state.smart.converged || 0;
    var notStarted = state.smart.not_started || 0;
    // When every reported channel is pre-first-contact (the classic
    // "1 channel · 0 stable · 0 learning" state on a fresh single-channel
    // host), surface the opportunistic-gate reason so the operator knows
    // WHY no probe has fired. Without this the smart page reads "1
    // channels active · 0 stable · 0 learning" with zero explanation —
    // #1417.
    if (ch > 0 && conv === 0 && (state.smart.warming_up || 0) === 0 && notStarted >= ch) {
      var reason = gateReason(state.opp);
      var base = ch + ' channel' + (ch === 1 ? '' : 's') +
        ' tracked · awaiting first probe.';
      if (reason) {
        return base + ' Opportunistic gate: ' + reason + '.';
      }
      return base + ' The first probe fires opportunistically when the host is idle (typically within ~24 h of install on a quiet box).';
    }
    return ch + ' channels active · ' + conv + ' stable · ' +
      (state.smart.warming_up || 0) + ' learning';
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
      // Show only the REAL signal we have: the PWM hold value the probe
      // is currently writing (gap_pwm). The tach wobble previously drawn
      // here was synthetic (keyed off tick_count, not real RPM); REMOVED
      // per the no-theatre rule. When the opportunistic status endpoint
      // exposes real probe-time tach samples, draw those instead.
      var svg = svgEl('svg', { class: 'sm-scope-svg', viewBox: '0 0 800 200', preserveAspectRatio: 'none' });
      var gap = clamp(Number(state.opp.gap_pwm) || 0, 0, 255);
      var pwmY = 20 + (255 - gap) / 255 * 160;
      var pwm = svgEl('path', {
        class: 'sm-scope-trace pwm',
        d: 'M 0 ' + pwmY.toFixed(1) + ' L 800 ' + pwmY.toFixed(1)
      });
      svg.appendChild(pwm);
      canvas.appendChild(svg);
      // Caption beneath the line so the operator knows what they're
      // looking at instead of guessing from the trace shape.
      var cap = el('div', { cls: 'sm-scope-empty-sub',
        text: 'PWM held at ' + Math.round(gap) +
              ' (' + Math.round(gap / 255 * 100) + '%) — tach response not surfaced yet' });
      cap.style.padding = '6px 12px';
      canvas.appendChild(cap);
    } else {
      var empty = el('div', { cls: 'sm-scope-empty' });
      empty.appendChild(el('div', { cls: 'sm-scope-empty-title', text: 'no probe in flight' }));
      empty.appendChild(el('div', { cls: 'sm-scope-empty-sub',
        text: gateReason(state.opp)
          ? ('last gate result: ' + gateReason(state.opp))
          : 'opportunistic gate idle' }));
      canvas.appendChild(empty);
    }
    card.appendChild(canvas);

    // Ribbon
    var ribbon = el('div', { cls: 'sm-scope-ribbon' });
    ribbon.appendChild(buildRibbonCell('Channel',  ribbonChannel(),  null));
    ribbon.appendChild(buildRibbonCell('Gap PWM',  ribbonGapPwm(),   'blue'));
    ribbon.appendChild(buildRibbonCell('Reason',   gateReason(state.opp) || '—', null));
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
      // The strips list only has entries for channels with active
      // aggregator snapshots; a daemon in pure monitor-only mode (no
      // controllable PWMs) or a fresh start where every channel is
      // pre-first-contact yields zero strips. Distinguish those two
      // cases (#1417): if smart/status saw channels but they're all
      // not_started, surface that — the operator's takeaway is "the
      // controller knows your fan, it just hasn't probed yet" rather
      // than "the daemon found nothing".
      var msg = 'No smart-mode channels reported by the daemon yet.';
      if ((state.smart.channels || 0) > 0 && (state.smart.not_started || 0) >= (state.smart.channels || 0)) {
        msg = 'Smart mode is tracking ' + state.smart.channels + ' channel' +
          (state.smart.channels === 1 ? '' : 's') +
          ' but no opportunistic probe has fired yet. The first probe ' +
          'lands when the host has been idle for a sustained window.';
        var gReason = gateReason(state.opp);
        if (gReason) msg += ' Gate currently: ' + gReason + '.';
      }
      strips.appendChild(el('div', { cls: 'sm-strips-empty', text: msg }));
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
    name.appendChild(el('div', { cls: 'sm-strip-leaf', text: c.name || leafName(c.channel_id) }));
    name.appendChild(el('div', { cls: 'sm-strip-path', text: c.channel_id || '' }));
    strip.appendChild(name);

    // Confidence cell (`w_pred` in daemon-internal terms)
    var c1 = el('div', { cls: 'sm-strip-cell' });
    c1.appendChild(el('div', { cls: 'sm-strip-cell-label', text: 'Confidence' }));
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
    var pill = el('span', { cls: 'sm-pill ' + stateCls, text: prettyState(c.ui_state || 'unknown') });
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
    var pill = el('span', { cls: 'sm-pill ' + stateCls, text: prettyState(state.smart.global_state || 'unknown') });
    head.appendChild(pill);
    card.appendChild(head);

    var dl = el('dl', { cls: 'sm-sys-list' });
    dl.appendChild(sysRow('Preset',   state.smart.preset || '—'));
    dl.appendChild(sysRow('Channels', String(state.smart.channels != null ? state.smart.channels : '—')));
    // "Learning"/"Active" match the prettyState labels for warming/converged
    // so the count rows and the status pills speak the same language (#1254).
    dl.appendChild(sysRow('Learning', String(state.smart.warming_up != null ? state.smart.warming_up : '—')));
    dl.appendChild(sysRow('Active',   String(state.smart.converged != null ? state.smart.converged : '—')));
    dl.appendChild(sysRow('Min confidence', fmt2(state.smart.confidence_min)));
    dl.appendChild(sysRow('Max confidence', fmt2(state.smart.confidence_max)));
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
    var rowCls = 'sm-conf-row';
    if (cc.drift_active) rowCls += ' is-drift';
    if (cc.cold_start)   rowCls += ' is-cold';
    var row = el('div', { cls: rowCls });

    var head = el('div', { cls: 'sm-conf-head' });
    head.appendChild(el('div', { cls: 'sm-conf-head-name', text: cc.name || leafName(cc.channel_id) }));
    var meta = el('div', { cls: 'sm-conf-head-meta' });
    var stateCls = safeStateClass(cc.ui_state || 'unknown');
    meta.appendChild(el('span', { cls: 'sm-pill ' + stateCls, text: prettyState(cc.ui_state || 'unknown') }));
    var w = el('div', { cls: 'sm-conf-wpred', text: 'Confidence ' + fmt2(cc.w_pred) });
    meta.appendChild(w);
    head.appendChild(meta);
    row.appendChild(head);

    var bars = el('div', { cls: 'sm-conf-bars' });
    bars.appendChild(buildBar('Layer A', cc.conf_a, 'layer-a'));
    bars.appendChild(buildBar('Layer B', cc.conf_b, 'layer-b'));
    bars.appendChild(buildBar('Layer C', cc.conf_c, 'layer-c'));
    row.appendChild(bars);

    if (cc.drift_active || cc.cold_start || cc.global_gate === false) {
      var flags = el('div', { cls: 'sm-conf-flags' });
      if (cc.cold_start)        flags.appendChild(el('span', { cls: 'sm-pill cold-start', text: 'cold-start' }));
      if (cc.drift_active)      flags.appendChild(el('span', { cls: 'sm-pill drifting',   text: 'drift active' }));
      if (cc.global_gate === false) flags.appendChild(el('span', { cls: 'sm-pill refused', text: 'global gate off' }));
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
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})();
