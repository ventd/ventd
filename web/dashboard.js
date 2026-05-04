// dashboard.js — steady-state at-a-glance view.
//
// Polls /api/v1/status (1Hz) and merges into a small in-memory history
// buffer so each sensor and fan tile gets its own scrolling sparkline.
// Also pulls /api/v1/profile/active for the active-profile pill, and
// /api/v1/version for the sidebar footer.
//
// Falls back to a synthetic demo loop when the API is unreachable so the
// page is always reviewable without a live daemon.

(function () {
  'use strict';

  // ── theme ──────────────────────────────────────────────────────────
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

  // ── helpers ────────────────────────────────────────────────────────
  function $(id) { return document.getElementById(id); }
  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }
  function fmtUptime(secs) {
    if (!isFinite(secs) || secs < 0) return '—';
    var d = Math.floor(secs / 86400), rem = secs % 86400;
    var h = Math.floor(rem / 3600);
    var m = Math.floor((rem % 3600) / 60);
    if (d > 0) return d + 'd ' + h + 'h';
    if (h > 0) return h + 'h ' + m + 'm';
    return m + 'm';
  }
  function clamp(x, lo, hi) { return Math.max(lo, Math.min(hi, x)); }

  // ── history buffers per sensor / fan ───────────────────────────────
  var SPARK_N = 60;
  var sensorHistory = {}; // name → [values]
  var fanHistory    = {}; // name → [rpm]
  var heroCpuHistory = []; // for the hero strip
  var heroGpuHistory = [];

  function pushHistory(map, key, val) {
    if (!map[key]) map[key] = [];
    map[key].push(val);
    if (map[key].length > SPARK_N) map[key].shift();
  }
  function pushArr(arr, val) {
    arr.push(val);
    if (arr.length > SPARK_N) arr.shift();
  }

  // sparkPath autoscales the y-axis to the buffer's min/max — with a
  // floor of `minRange` so that a sensor wobbling within ±0.5°C doesn't
  // look like it's climbing a mountain. This was the root cause of the
  // \"GPU temp keeps climbing on every page refresh\" report (#797):
  // the dashboard polled new samples into an empty buffer; auto-fit
  // collapsed the y-axis to the noise floor of the first 2-3 readings;
  // tiny variance got drawn as a steep slope. minRange=5 for temps,
  // 200 for RPM, 5 for percent gives realistic dynamic range.
  function sparkPath(buf, w, h, minRange) {
    if (!buf || buf.length < 2) return '';
    if (minRange == null) minRange = 1;
    var max = -Infinity, min = Infinity;
    for (var i = 0; i < buf.length; i++) {
      if (buf[i] > max) max = buf[i];
      if (buf[i] < min) min = buf[i];
    }
    var range = Math.max(max - min, minRange);
    // Centre the data within the range so a flat-line sample sits
    // mid-card rather than at the bottom edge.
    var mid = (max + min) / 2;
    var lo = mid - range / 2;
    var d = '';
    for (var j = 0; j < buf.length; j++) {
      var x = (j / (SPARK_N - 1)) * w;
      var y = (h - 2) - ((buf[j] - lo) / range) * (h - 4);
      d += (j === 0 ? 'M ' : ' L ') + x.toFixed(1) + ' ' + y.toFixed(1);
    }
    return d;
  }
  // sparkPathTemp / sparkPathRPM / sparkPathPct are thin wrappers that
  // bake the right minRange for the metric class.
  function sparkPathTemp(buf, w, h) { return sparkPath(buf, w, h, 5); }
  function sparkPathRPM(buf, w, h)  { return sparkPath(buf, w, h, 200); }
  function sparkPathPct(buf, w, h)  { return sparkPath(buf, w, h, 5); }

  // ── classification heuristics ──────────────────────────────────────
  function looksLikeCPU(name) {
    return /(cpu|package|core|tctl|tdie|tjm)/i.test(name);
  }
  function looksLikeGPU(name) {
    return /gpu|amd|nvidia|radeon|intel.?arc/i.test(name);
  }
  function looksLikePump(name) {
    return /pump|coolant|aio/i.test(name);
  }

  // ── sensor / fan tiles render ──────────────────────────────────────
  function renderSensorTiles(sensors) {
    var grid = $('sensors-grid');
    if (!grid) return;
    if (!sensors || sensors.length === 0) {
      grid.innerHTML = '<div class="dash-grid-empty">No sensor data yet…</div>';
      return;
    }
    // Drop any "empty state" placeholder once we have data.
    Array.prototype.forEach.call(grid.querySelectorAll('.dash-grid-empty'), function (n) { n.remove(); });
    // Diff render — preserve nodes for smooth animations.
    var existing = {};
    Array.prototype.forEach.call(grid.querySelectorAll('.dash-tile[data-key]'), function (n) {
      existing[n.dataset.key] = n;
    });
    var seen = {};
    sensors.forEach(function (s) {
      var key = 's:' + s.name;
      seen[key] = true;
      pushHistory(sensorHistory, s.name, s.value == null ? null : Number(s.value));
      var tile = existing[key];
      if (!tile) {
        tile = document.createElement('div');
        tile.className = 'dash-tile';
        tile.dataset.key = key;
        tile.innerHTML =
            '<div class="dash-tile-head">'
          +   '<span class="dash-tile-name">' + escapeHTML(s.name) + '</span>'
          +   '<span class="dash-tile-source mono">' + escapeHTML(s.unit || '°C') + '</span>'
          + '</div>'
          + '<div class="dash-tile-value">'
          +   '<span class="js-val">—</span>'
          +   '<span class="dash-tile-unit js-unit">' + escapeHTML(s.unit || '°C') + '</span>'
          + '</div>'
          + '<svg class="dash-tile-spark" viewBox="0 0 240 28" preserveAspectRatio="none">'
          +   '<path class="js-spark" fill="none" stroke="' + (looksLikeCPU(s.name) ? 'var(--teal)' : looksLikeGPU(s.name) ? 'var(--blue)' : 'var(--cyan)') + '" stroke-width="1.5"/>'
          + '</svg>';
        grid.appendChild(tile);
      }
      var valEl = tile.querySelector('.js-val');
      if (s.value == null) valEl.textContent = '—';
      else valEl.textContent = Number(s.value).toFixed(1);
      var path = tile.querySelector('.js-spark');
      if (path) path.setAttribute('d', sparkPathTemp(sensorHistory[s.name], 240, 28));
      // ── alive overlay: intent pill on sensor head ──
      aliveAttachSensorIntent(tile, s);
    });
    // Remove tiles for sensors that disappeared.
    Object.keys(existing).forEach(function (k) {
      if (!seen[k]) existing[k].remove();
    });
    var meta = $('sensors-meta');
    if (meta) meta.textContent = sensors.length + ' source' + (sensors.length === 1 ? '' : 's');
  }

  function renderFanTiles(fans) {
    var grid = $('fans-grid');
    if (!grid) return;
    if (!fans || fans.length === 0) {
      grid.innerHTML = '<div class="dash-grid-empty">No fan data yet…</div>';
      return;
    }
    Array.prototype.forEach.call(grid.querySelectorAll('.dash-grid-empty'), function (n) { n.remove(); });
    var existing = {};
    Array.prototype.forEach.call(grid.querySelectorAll('.dash-tile[data-key]'), function (n) {
      existing[n.dataset.key] = n;
    });
    var seen = {}, spinning = 0;
    fans.forEach(function (f) {
      var key = 'f:' + f.name;
      seen[key] = true;
      var rpm = f.rpm == null ? null : Number(f.rpm);
      var dutyPct = f.duty_pct == null ? null : Number(f.duty_pct);
      // Tach-less fans (NVIDIA — NVML exposes fan-speed % only, not RPM)
      // surface the duty cycle in the primary value slot with a "%" unit
      // so the tile shows a live signal instead of "—". Generic — works
      // for any backend whose Read path doesn't return an RPM.
      var pctOnly = (rpm == null && dutyPct != null);
      var primaryVal = pctOnly ? Math.round(dutyPct) : rpm;
      var primaryUnit = pctOnly ? '%' : 'RPM';
      var spinningSignal = pctOnly ? (dutyPct > 5) : (rpm != null && rpm > 60);
      if (spinningSignal) spinning++;
      // Push primaryVal into history so the spark renders meaningful motion
      // even on pct-only fans. The dynamic-range scale will be off (sparks
      // were calibrated for RPM=200) but a 0-100 range still reads cleanly.
      pushHistory(fanHistory, f.name, primaryVal == null ? null : primaryVal);
      var tile = existing[key];
      if (!tile) {
        tile = document.createElement('div');
        tile.className = 'dash-tile';
        tile.dataset.key = key;
        tile.innerHTML =
            '<div class="dash-tile-head">'
          +   '<span class="dash-tile-name">' + escapeHTML(f.name) + '</span>'
          +   '<span class="dash-tile-source mono js-source">RPM</span>'
          + '</div>'
          + '<div class="dash-tile-value">'
          +   '<span class="js-val">—</span>'
          +   '<svg class="dash-fan-icon" viewBox="0 0 24 24" aria-hidden="true">'
          +     '<circle cx="12" cy="12" r="2" fill="currentColor"/>'
          +     '<path d="M12 4 C 14 6 14 9 12 12 C 9 12 6 13 4 11 C 6 8 9 6 12 4Z" fill="currentColor" opacity="0.85"/>'
          +     '<path d="M20 12 C 18 14 15 14 12 12 C 12 9 11 6 13 4 C 16 6 18 9 20 12Z" fill="currentColor" opacity="0.85"/>'
          +     '<path d="M12 20 C 10 18 10 15 12 12 C 15 12 18 11 20 13 C 18 16 15 18 12 20Z" fill="currentColor" opacity="0.85"/>'
          +     '<path d="M4 12 C 6 10 9 10 12 12 C 12 15 13 18 11 20 C 8 18 6 15 4 12Z" fill="currentColor" opacity="0.85"/>'
          +   '</svg>'
          + '</div>'
          + '<svg class="dash-tile-spark" viewBox="0 0 240 28" preserveAspectRatio="none">'
          +   '<path class="js-spark" fill="none" stroke="var(--teal)" stroke-width="1.5"/>'
          + '</svg>'
          + '<div class="dash-fan-foot">'
          +   '<div class="dash-fan-bar"><div class="dash-fan-bar-fill js-bar"></div></div>'
          +   '<div class="dash-fan-duty mono"><span class="js-duty">—</span>%</div>'
          + '</div>';
        grid.appendChild(tile);
      }
      var valEl = tile.querySelector('.js-val');
      if (primaryVal == null) valEl.textContent = '—';
      else valEl.textContent = primaryVal;
      var sourceEl = tile.querySelector('.js-source');
      if (sourceEl) sourceEl.textContent = primaryUnit;
      var bar = tile.querySelector('.js-bar');
      if (bar) bar.style.width = (clamp(f.duty_pct || 0, 0, 100)).toFixed(1) + '%';
      var duty = tile.querySelector('.js-duty');
      if (duty) duty.textContent = Math.round(f.duty_pct || 0);
      var path = tile.querySelector('.js-spark');
      if (path) path.setAttribute('d', sparkPathRPM(fanHistory[f.name], 240, 28));

      tile.classList.toggle('is-spinning', spinningSignal);
      // is-stalled means "we're driving duty but the tach reads 0" — only
      // meaningful for fans that actually have a tach signal. Pct-only fans
      // can't be stalled (no feedback to detect it).
      tile.classList.toggle('is-stalled', !pctOnly && rpm === 0 && (f.duty_pct || 0) > 5);

      // ── alive overlay attachments (intent pill, target marker,
      //    coupling sub-line, decision flash). All idempotent —
      //    elements are created once per tile and updated in-place.
      aliveAttachFanAffordances(tile, f);
    });
    Object.keys(existing).forEach(function (k) {
      if (!seen[k]) existing[k].remove();
    });
    var meta = $('fans-meta');
    if (meta) meta.textContent = fans.length + ' fan' + (fans.length === 1 ? '' : 's')
      + ' · ' + spinning + ' spinning';
  }

  // ── hero strip ─────────────────────────────────────────────────────
  function renderHero(sensors, fans, version) {
    // CPU & GPU hero come from the first sensor matching each pattern.
    var cpu = null, gpu = null;
    if (sensors) for (var i = 0; i < sensors.length; i++) {
      if (cpu == null && looksLikeCPU(sensors[i].name)) cpu = sensors[i].value;
      if (gpu == null && looksLikeGPU(sensors[i].name)) gpu = sensors[i].value;
    }
    if (cpu != null) pushArr(heroCpuHistory, Number(cpu));
    if (gpu != null) pushArr(heroGpuHistory, Number(gpu));

    var cpuEl = $('hero-cpu-val'); if (cpuEl) cpuEl.textContent = cpu == null ? '—' : Number(cpu).toFixed(1);
    var gpuEl = $('hero-gpu-val'); if (gpuEl) gpuEl.textContent = gpu == null ? '—' : Number(gpu).toFixed(1);
    // Hero spark path + sub-line are owned by the alive overlay
    // (aliveRenderHeroSpark) which uses the inventory ring + EMA
    // smoothing + pinned Y-axis. The OLD per-/status writers here
    // were fighting the alive renderer and producing the
    // "flat → jagged → flat" alternation Phoenix saw — disabled.
    // heroCpuHistory / heroGpuHistory still get populated above so
    // the alive renderer's fallback path has data when inventory's
    // matcher misses.

    // Fan hero
    var spinning = 0, total = 0;
    if (fans) {
      total = fans.length;
      for (var k = 0; k < fans.length; k++) {
        // Tach-less fans (NVIDIA) report duty_pct only — count them as
        // spinning when duty exceeds the dead-band where the firmware
        // typically holds the fan stopped.
        if (fans[k].rpm != null && fans[k].rpm > 60) spinning++;
        else if (fans[k].rpm == null && (fans[k].duty_pct || 0) > 5) spinning++;
      }
    }
    var fEl = $('hero-fans-val'); if (fEl) fEl.textContent = total === 0 ? '—' : (spinning + '/' + total);
    var fSub = $('hero-fans-sub'); if (fSub) fSub.textContent = total === 0 ? 'no fans yet' : 'of ' + total + ' total';

    // Fan duty bar chart in hero. CSP forbids inline style="" attributes
    // under style-src 'self', so build spans without markup and apply
    // height via CSSOM (which style-src does not block).
    var bars = $('hero-fans-bars');
    if (bars) {
      bars.innerHTML = '';
      var show = (fans || []).slice(0, 12);
      for (var b = 0; b < 12; b++) {
        var d = show[b] ? clamp(show[b].duty_pct || 0, 0, 100) : 0;
        var height = Math.max(6, (d / 100) * 30);
        var span = document.createElement('span');
        span.style.height = height.toFixed(1) + 'px';
        bars.appendChild(span);
      }
    }
  }

  // ── live dot in sidebar footer ─────────────────────────────────────
  function setLive(ok) {
    var dot = $('sb-live-dot');
    var label = $('sb-live-label');
    if (dot) dot.classList.toggle('is-down', !ok);
    if (label) label.textContent = ok ? 'live · ' + new Date().toLocaleTimeString([], {hour:'2-digit', minute:'2-digit'}) : 'reconnecting…';
  }

  // ── version + uptime in sidebar / hero ─────────────────────────────
  function applyVersion(v) {
    if (!v) return;
    var sbV = $('sb-version'); if (sbV) sbV.textContent = v.version || v;
  }
  var bootAt = Date.now();
  function tickUptime(progressUptime) {
    var uEl = $('hero-uptime');
    if (!uEl) return;
    var secs = progressUptime != null ? progressUptime : Math.floor((Date.now() - bootAt) / 1000);
    uEl.textContent = fmtUptime(secs);
  }

  // ── apply API status payload ───────────────────────────────────────
  function applyStatus(data) {
    if (!data) return;
    renderHero(data.sensors || [], data.fans || []);
    renderSensorTiles(data.sensors || []);
    renderFanTiles(data.fans || []);
    setLive(true);
  }
  function applyProfile(p) {
    if (!p) return;
    var n = $('profile-name'); if (n) n.textContent = p.name || 'auto';
    var s = $('profile-source'); if (s) s.textContent = p.source || 'live';
    var sub = $('profile-sub'); if (sub) sub.textContent = (p.curves != null ? p.curves + ' curve' + (p.curves === 1 ? '' : 's') + ' active' : '—')
                                + (p.note ? ' · ' + p.note : '');
    var modeName = $('dash-mode-name');
    if (modeName) modeName.textContent = p.name || 'auto';
  }

  // ── poll loop ──────────────────────────────────────────────────────
  var pollInterval = 1000;
  var inDemo = false;
  var pollTimer = null;
  var demoTimer = null;
  // Phoenix's HIL feedback (#820): a single 401 / network blip flipped
  // the dashboard into demo mode and never came back, so a transient
  // session expiry painted fake data over a healthy daemon. Require
  // N consecutive failures before we conclude the API is gone — and
  // keep polling the real API even after entering demo so we can
  // recover the moment it returns.
  var DEMO_ACTIVATE_AFTER_FAILS = 3;
  var consecutiveFailures = 0;

  // /api/v1/profile/active is POST-only (it switches the active profile);
  // the dashboard summary is built from the GET on /api/v1/profile.
  function shapeProfile(data) {
    if (!data) return null;
    var active = data.active || 'auto';
    var prof = (data.profiles || {})[active] || {};
    var bindings = prof.bindings || {};
    return {
      name: active,
      source: prof.schedule ? 'schedule' : 'live',
      curves: Object.keys(bindings).length,
      note: prof.schedule || ''
    };
  }

  function pollOnce() {
    Promise.all([
      fetch('/api/v1/status', { credentials: 'same-origin' })
        .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); }),
      fetch('/api/v1/profile', { credentials: 'same-origin' })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(shapeProfile)
        .catch(function () { return null; })
    ])
      .then(function (out) {
        consecutiveFailures = 0;
        // API came back — exit demo if we were in it.
        if (inDemo) leaveDemo();
        applyStatus(out[0]);
        if (out[1]) applyProfile(out[1]);
      })
      .catch(function () {
        consecutiveFailures += 1;
        if (!inDemo && consecutiveFailures >= DEMO_ACTIVATE_AFTER_FAILS) {
          enterDemo();
        }
      });
  }

  function enterDemo() {
    inDemo = true;
    var banner = document.getElementById('dash-demo-banner');
    if (banner) banner.hidden = false;
    startDemo();
  }
  function leaveDemo() {
    inDemo = false;
    if (demoTimer) { clearInterval(demoTimer); demoTimer = null; }
    var banner = document.getElementById('dash-demo-banner');
    if (banner) banner.hidden = true;
  }

  fetch('/api/v1/version', { credentials: 'same-origin' })
    .then(function (r) { return r.ok ? r.json() : null; })
    .then(applyVersion)
    .catch(function () {});

  // ── panic ──────────────────────────────────────────────────────────
  var panicBtn = $('dash-panic');
  if (panicBtn) panicBtn.addEventListener('click', function () {
    if (!confirm('Pin all fans to maximum for 60 seconds?')) return;
    fetch('/api/v1/panic?duration=60', { method: 'POST', credentials: 'same-origin' });
  });

  // ── demo mode ──────────────────────────────────────────────────────
  function startDemo() {
    var cpuTemp = 48, gpuTemp = 56, t = 0;
    var demoFans = [
      { name: 'CPU fan',         duty: 35, max: 2310 },
      { name: 'Front intake top', duty: 30, max: 1820 },
      { name: 'Front intake mid', duty: 30, max: 1820 },
      { name: 'Front intake bot', duty: 30, max: 1820 },
      { name: 'Rear exhaust',    duty: 65, max: 1900 },
      { name: 'Top exhaust 1',   duty: 30, max: 1700 },
      { name: 'Top exhaust 2',   duty: 30, max: 1700 },
      { name: 'AIO pump',        duty: 60, max: 2840 },
      { name: 'AIO fan 1',       duty: 38, max: 2400 },
      { name: 'AIO fan 2',       duty: 38, max: 2400 },
      { name: 'AIO fan 3',       duty: 38, max: 2400 },
      { name: 'GPU 0 fan 0',     duty: 28, max: 3000 },
      { name: 'GPU 0 fan 1',     duty: 28, max: 3000 },
      { name: 'PSU fan',         duty: 0,  max: 0 }
    ];

    function tick() {
      t += 1;
      // Mean-zero drift — the previous (Math.random() - 0.4) had a +0.1
      // positive bias, so demo CPU/GPU temps slowly walked up to the
      // 90°C / 88°C clamp ceiling. On Phoenix's HIL desktop a transient
      // 401 flipped the dashboard into demo mode and the climbing temps
      // looked like a thermal runaway, panicking the operator (#820).
      // (Math.random() - 0.5) is the unbiased symmetric form.
      cpuTemp = clamp(cpuTemp + (Math.random() - 0.5) * 1.5, 35, 90);
      gpuTemp = clamp(gpuTemp + (Math.random() - 0.5) * 1.8, 38, 88);
      var data = {
        sensors: [
          { name: 'CPU package',     value: cpuTemp,            unit: '°C' },
          { name: 'CPU core 0',      value: cpuTemp - 1.2,      unit: '°C' },
          { name: 'CPU core 4',      value: cpuTemp + 0.4,      unit: '°C' },
          { name: 'GPU 0 (RTX 4090)', value: gpuTemp,           unit: '°C' },
          { name: 'AIO coolant',     value: 32 + Math.sin(t/8) * 0.5, unit: '°C' },
          { name: 'Motherboard',     value: 42 + Math.sin(t/15) * 0.8, unit: '°C' },
          { name: 'NVMe 0',          value: 47 + Math.sin(t/12) * 1.5, unit: '°C' }
        ],
        fans: demoFans.map(function (f) {
          if (f.max === 0) return { name: f.name, pwm: 0, duty_pct: 0, rpm: null };
          var jitter = (Math.random() - 0.5) * 30;
          var rpm = Math.round(f.max * f.duty / 100 + jitter);
          if (f.duty > 0 && f.duty < 5) rpm = 0;
          return { name: f.name, pwm: Math.round(f.duty * 2.55), duty_pct: f.duty, rpm: rpm };
        })
      };
      applyStatus(data);
      tickUptime(t);
      // Random small profile drift so the active-profile sub-text isn't static.
      if (t === 1) applyProfile({ name: 'Quiet', source: 'schedule', curves: 4, note: 'window 22:00–08:00' });
      // Wobble the duty so bars animate
      demoFans.forEach(function (f) {
        if (f.max === 0) return;
        f.duty = clamp(f.duty + (Math.random() - 0.5) * 4, 22, 92);
      });
    }
    tick();
    if (demoTimer) clearInterval(demoTimer);
    demoTimer = setInterval(tick, 900);
    applyVersion({ version: '0.5.4' });
  }

  // ── v0.5.5: opportunistic-probe in-flight pill ───────────────────
  // Polls /api/v1/probe/opportunistic/status every 5 s. The endpoint
  // returns running=false when the scheduler is not wired (monitor-
  // only mode) or when no probe is currently in flight; the pill
  // stays hidden in either case.
  function pollOpportunisticStatus() {
    fetch('/api/v1/probe/opportunistic/status', { credentials: 'same-origin' })
      .then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      })
      .then(function (s) {
        var pill = document.getElementById('dash-opp-pill');
        var text = document.getElementById('dash-opp-pill-text');
        if (!pill || !text) return;
        if (s && s.running) {
          var pwm = s.gap_pwm != null ? s.gap_pwm : '?';
          text.textContent = 'probing PWM ' + pwm;
          pill.hidden = false;
        } else {
          pill.hidden = true;
        }
      })
      .catch(function () {
        var pill = document.getElementById('dash-opp-pill');
        if (pill) pill.hidden = true;
      });
  }

  // seedHistory pre-populates the in-memory sparkline buffers from
  // ventd's server-side ring (/api/history) so a fresh page load shows
  // the last 60s of samples instead of an empty chart that fills
  // sample-by-sample over 60s — which produced the \"GPU temp keeps
  // climbing on every refresh\" report (#797). The visible \"climb\"
  // was actually the chart filling left-to-right with a buffer that
  // contained too few samples for sparkPath's auto-fit y-axis.
  function seedHistory() {
    fetch('/api/v1/history?window_s=60', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (data) {
        if (!data || !data.metrics) return;
        Object.keys(data.metrics).forEach(function (name) {
          var samples = data.metrics[name];
          if (!Array.isArray(samples) || samples.length === 0) return;
          // Truncate to the last SPARK_N samples so the buffer matches
          // the chart's display window exactly. Server ring may hold
          // more — we only want the most recent.
          var start = Math.max(0, samples.length - SPARK_N);
          var values = samples.slice(start).map(function (s) { return s.v; });
          if (looksLikeCPU(name) || looksLikeGPU(name)) {
            // Hero chart for CPU/GPU. Seed the matching hero buffer
            // (idempotent if multiple sensors match — last wins).
            if (looksLikeCPU(name)) heroCpuHistory = values.slice();
            if (looksLikeGPU(name)) heroGpuHistory = values.slice();
          }
          if (/rpm$|fan/i.test(name)) {
            fanHistory[name] = values.slice();
          } else {
            sensorHistory[name] = values.slice();
          }
        });
      })
      .catch(function () { /* monitor-only or pre-data — pollOnce will catch up */ });
  }

  // pollSmartMode populates the topbar smart-mode pill from the live
  // config — surfaces which smart-mode subsystems are active so users
  // get visible at-a-glance evidence that ventd is being intelligent
  // rather than running a dumb curve. Updates every 10s.
  function pollSmartMode() {
    fetch('/api/v1/config', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (c) {
        if (!c) return;
        var pill = document.getElementById('dash-smart-pill');
        var text = document.getElementById('dash-smart-pill-text');
        if (!pill || !text) return;
        var states = [];
        if (c.acoustic_optimisation === undefined ||
            c.acoustic_optimisation === null ||
            c.acoustic_optimisation === true) {
          states.push('acoustic');
        }
        if (!c.signature_learning_disabled) states.push('learning');
        if (!c.never_actively_probe_after_install) states.push('probing-ok');
        if (!c.smart_marginal_benefit_disabled) states.push('marginal');
        if (states.length === 0) {
          pill.hidden = true;
          return;
        }
        text.textContent = 'smart · ' + states.length + ' active';
        pill.title = 'smart-mode subsystems active: ' + states.join(', ');
        pill.hidden = false;
      })
      .catch(function () {
        var pill = document.getElementById('dash-smart-pill');
        if (pill) pill.hidden = true;
      });
  }

  // pollConfidence drives the v0.5.9 5-state confidence pill +
  // popover. Reads /api/v1/confidence/status, collapses to the
  // worst-of-channels state for the topbar pill, emits a one-off
  // ribbon on state transitions, and rebuilds the click-popover
  // body with R12 §Q7 long-form per-layer reasons.
  //
  // Hidden when the daemon reports enabled=false (monitor-only
  // mode). Polls every 5s; underlying aggregator updates faster.
  var prevConfState = null;
  function pollConfidence() {
    fetch('/api/v1/confidence/status', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (s) {
        var pill = document.getElementById('dash-conf-pill');
        var text = document.getElementById('dash-conf-pill-text');
        if (!pill || !text) return;
        if (!s || !s.enabled || !s.global_state) {
          pill.hidden = true;
          prevConfState = null;
          return;
        }
        pill.hidden = false;

        // State transition detection — emit a one-off ribbon.
        if (prevConfState !== null && prevConfState !== s.global_state) {
          emitConfidenceRibbon(pill, s.global_state);
        }
        prevConfState = s.global_state;

        pill.dataset.state = s.global_state;
        text.textContent = s.global_state;
        var n = s.channels ? s.channels.length : 0;
        pill.title = 'smart-mode confidence — ' + s.global_state +
          ' across ' + n + ' channel' + (n === 1 ? '' : 's') +
          ' (preset: ' + (s.preset || 'balanced') + ')';

        // Refresh the popover body in case it's open.
        updateConfidencePopover(s);
      })
      .catch(function () {
        var pill = document.getElementById('dash-conf-pill');
        if (pill) pill.hidden = true;
      });
  }

  // emitConfidenceRibbon attaches a "Now: <state>" ribbon over the
  // pill that animates in, holds, then fades. Removes the previous
  // ribbon if one is in flight so back-to-back transitions don't
  // stack visually.
  function emitConfidenceRibbon(pill, state) {
    var prev = pill.querySelector('.dash-conf-ribbon');
    if (prev) prev.remove();
    var r = document.createElement('span');
    r.className = 'dash-conf-ribbon';
    r.textContent = 'now: ' + state;
    pill.appendChild(r);
    // Auto-cleanup after the CSS animation completes (2.4s).
    setTimeout(function () { if (r && r.parentNode) r.remove(); }, 2600);
  }

  // Layer-reason classifiers for the popover. Each function turns
  // a Snapshot-shaped object into { reason, mood } where mood is
  // one of "good" / "warm" / "cold" / "bad" (drives the colour
  // tint of the row).
  function reasonLayerA(ch) {
    if (!ch) return { reason: 'no Layer-A snapshot', mood: 'cold' };
    var tierName = ['rpm-tach', 'coupled-ref', 'bmc-ipmi', 'ec-stepped',
                    'thermal-invert', 'rapl-echo', 'pwm-echo', 'open-loop'][ch.tier] || 'unknown';
    if (ch.tier >= 7) return { reason: 'open-loop tier — predictive controller refused', mood: 'cold' };
    if (ch.coverage < 0.25) return { reason: tierName + ' (coverage ' + Math.round(ch.coverage * 100) + '% — needs more samples across PWM range)', mood: 'warm' };
    if (ch.coverage < 0.6) return { reason: tierName + ' — coverage ' + Math.round(ch.coverage * 100) + '%, building confidence', mood: 'warm' };
    return { reason: tierName + ' — coverage ' + Math.round(ch.coverage * 100) + '% (good)', mood: 'good' };
  }
  function reasonLayerB(ch) {
    if (!ch) return { reason: 'no Layer-B snapshot', mood: 'cold' };
    if (ch.conf_b < 0.05) return { reason: 'thermal-coupling estimator warming up or unidentifiable', mood: 'warm' };
    if (ch.conf_b < 0.4) return { reason: 'thermal-coupling estimator partial — early-life noise still high', mood: 'warm' };
    return { reason: 'thermal-coupling estimator healthy (κ in range, low residual)', mood: 'good' };
  }
  function reasonLayerC(ch) {
    if (!ch || ch.conf_c <= 0) return { reason: 'marginal-benefit shard not warm yet for this workload', mood: 'cold' };
    if (ch.conf_c < 0.4) return { reason: 'marginal-benefit shard learning current workload signature', mood: 'warm' };
    return { reason: 'marginal-benefit shard converged for current workload', mood: 'good' };
  }

  // updateConfidencePopover refreshes the popover's body with the
  // latest snapshot. The popover open/close is toggled by the click
  // handler installed below.
  function updateConfidencePopover(s) {
    var pop = document.getElementById('dash-conf-popover');
    if (!pop) return;
    if (!s || !s.channels || s.channels.length === 0) {
      pop.innerHTML = '<h4>Smart-mode confidence</h4><p>No controllable channels.</p>';
      return;
    }
    // The "global" representative channel for the per-layer
    // reasons is the one driving the worst-of-channels state.
    var worst = s.channels[0];
    var priority = { 'refused': 0, 'drifting': 1, 'cold-start': 2, 'warming': 3, 'converged': 4 };
    s.channels.forEach(function (c) {
      if (priority[c.ui_state] < priority[worst.ui_state]) worst = c;
    });

    var rA = reasonLayerA(worst);
    var rB = reasonLayerB(worst);
    var rC = reasonLayerC(worst);

    var html = '<h4>Smart-mode confidence — ' + s.global_state + '</h4>' +
      '<ul>' +
      '<li><span class="layer-name">Layer A</span><span class="layer-reason is-' + rA.mood + '">' + escapeHtml(rA.reason) + '</span></li>' +
      '<li><span class="layer-name">Layer B</span><span class="layer-reason is-' + rB.mood + '">' + escapeHtml(rB.reason) + '</span></li>' +
      '<li><span class="layer-name">Layer C</span><span class="layer-reason is-' + rC.mood + '">' + escapeHtml(rC.reason) + '</span></li>' +
      '</ul>';
    if (s.channels.length > 1) {
      html += '<div class="ch-list">';
      s.channels.forEach(function (c) {
        var label = (c.channel_id || '').split('/').pop() || c.channel_id;
        html += '<span class="ch-chip" data-state="' + escapeHtml(c.ui_state) + '">' +
          escapeHtml(label) + ' · ' + escapeHtml(c.ui_state) +
          '</span>';
      });
      html += '</div>';
    }
    pop.innerHTML = html;
  }
  function escapeHtml(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }

  // Wire the click-popover. Lazy-initialise the popover element
  // (added under the pill only when the user first clicks).
  (function setupConfidencePopover() {
    var pill = document.getElementById('dash-conf-pill');
    if (!pill) return;
    pill.addEventListener('click', function (ev) {
      ev.stopPropagation();
      var pop = document.getElementById('dash-conf-popover');
      if (!pop) {
        pop = document.createElement('div');
        pop.id = 'dash-conf-popover';
        pop.className = 'dash-conf-popover';
        pop.addEventListener('click', function (e) { e.stopPropagation(); });
        pill.appendChild(pop);
        // Repopulate now from whatever the most-recent poll cached.
        // Force a fetch so the popover doesn't render stale.
        pollConfidence();
      }
      pop.classList.toggle('open');
    });
    document.addEventListener('click', function () {
      var pop = document.getElementById('dash-conf-popover');
      if (pop) pop.classList.remove('open');
    });
  })();

  // ────────────────────────────────────────────────────────────────────
  // ALIVE OVERLAY — extends the steady-state dashboard with the
  // cal.ai handoff's "what is the AI doing" affordances. Phoenix's
  // framing: ventd IS the AI; the UI surfaces what it's actually
  // doing from real backend state. No synthetic feeds, no fake
  // forecasts, no decision theatre. Every visible number traces back
  // to a real endpoint or a real client-side computation over real
  // history.
  //
  // What this block adds, on top of the existing IIFE above:
  //
  //   • aliveState           : module-private state holder (inventory,
  //                            curves, decisions, last fan-PWM map,
  //                            smart payload).
  //   • aliveAttachSensorIntent / aliveAttachFanAffordances : per-tile
  //                            DOM attachments called from the existing
  //                            renderSensorTiles / renderFanTiles loops.
  //   • aliveTick            : the new poll, on the same 1500 ms cadence
  //                            requested by the spec. Coalesces:
  //                              GET /api/v1/hardware/inventory
  //                              GET /api/v1/smart/status
  //                              GET /api/v1/smart/channels
  //                              GET /api/v1/probe/opportunistic/status
  //                            Existing /api/v1/status polling stays at
  //                            1 s — we don't double-fetch it.
  //   • aliveDetectDecisions : diff fan PWM across polls, emit a
  //                            decision event when |Δduty| ≥ 2 pp,
  //                            cap history at 40, drive the narrator
  //                            strip + decision feed.
  //   • aliveRenderHeroForecast / aliveRenderInsightRail : DOM updates
  //                            for the new hero spark band, coupling
  //                            map, decision feed, AI brief.
  //
  // RULE-UI-01: no external CDN, vanilla extension only.
  // RULE-UI-02: every colour comes from a token via var().
  // RULE-UI-03: sidebar untouched.
  // ────────────────────────────────────────────────────────────────────

  var ALIVE_TICK_MS = 1500;
  // Decision detection: windowed-delta + per-fan rate limit. Track the
  // last ALIVE_DECISION_WINDOW polls per fan; emit a decision when the
  // delta from the OLDEST sample in the window to the NEWEST exceeds
  // ALIVE_DECISION_THRESHOLD pp. The window naturally absorbs single-poll
  // noise (a 14% spike followed by 11% steady doesn't carry across the
  // window). The per-fan ALIVE_DECISION_RATELIMIT_MS prevents a sustained
  // ramp from emitting one decision per poll.
  //
  // Replaces the v1 persistence-gate from #925 which mis-classified a
  // ramp's stable tail as "flat → reset candidate" and missed real ramps
  // (Cpu Fan 41 → 64% over 4 s went undetected on the desktop HIL).
  var ALIVE_DECISION_THRESHOLD = 10;
  var ALIVE_DECISION_WINDOW = 3;          // polls in the comparison window
  var ALIVE_DECISION_RATELIMIT_MS = 6000; // min interval between decisions per fan
  var ALIVE_DECISION_CAP = 40;
  // Hero spark Y-axis is PINNED to a stable temperature range so the
  // line shape only evolves left-to-right with new samples — never
  // rescales (Phoenix's UX feedback: the line shouldn't look completely
  // different every poll). 20-100°C covers every consumer-silicon
  // thermal envelope from idle to throttle.
  var HERO_SPARK_TEMP_MIN_C = 20;
  var HERO_SPARK_TEMP_MAX_C = 100;
  // The naive client-side linear-fit forecast was REMOVED in this
  // branch — it doesn't use the smart-mode Layer-C marginal-benefit
  // RLS estimator the daemon spent v0.5.5-v0.5.10 building. The
  // dashboard hero card now shows the past spark only; the daemon-
  // backed predicted ΔT will land via #43 (P0 followup) once the
  // confidence/status endpoint exposes per-sensor predicted_delta_t.
  var ALIVE_NARRATOR_PERIOD_MS = 6000;  // line rotation cadence
  var ALIVE_NARRATOR_IDLE_MS = 12000;   // declare "system idle" after this
  var TJ_MAX = 100;                      // matches existing TJ constant

  var aliveState = {
    inventory: null,         // /api/v1/hardware/inventory payload (chips+curves)
    smart: null,             // /api/v1/smart/status payload
    channels: null,          // /api/v1/smart/channels payload
    opp: null,               // /api/v1/probe/opportunistic/status payload
    lastFanDuty: {},         // fan-name → last seen duty_pct (for sparkline + tile intent)
    fanDutyWindow: {},       // fan-name → array of last ALIVE_DECISION_WINDOW duty_pct values
    fanLastDecisionAt: {},   // fan-name → epoch ms of last emitted decision (rate limit)
    lastFanRpm: {},          // fan-name → last seen RPM (used for cause hint)
    lastFans: {},            // fan-name → most recent fan struct (for narrator)
    decisions: [],           // most-recent first; cap ALIVE_DECISION_CAP
    sensorTrendHistory: {},  // sensor-name → recent values for trend
    narratorIdx: 0,
    narratorLastShown: 0,
    pollOk: false
  };
  var aliveDecisionFlashSet = {};

  /* aliveFetchJSON: same shape as smart.js fetchJSON — credentialed
     fetch + json parse + reject on non-2xx so Promise.all's catch
     fires when any single endpoint dies. */
  function aliveFetchJSON(url) {
    return fetch(url, { credentials: 'same-origin' }).then(function (r) {
      if (!r.ok) throw new Error(url + ' ' + r.status);
      return r.json();
    });
  }

  /* aliveTick: parallel poll of the four alive-overlay endpoints.
     Coalesces into a single render pass. Failures are downgraded
     to "feature absent" — the overlay degrades gracefully rather
     than putting a banner in the user's face (the steady-state
     dashboard already owns the demo banner for /api/v1/status). */
  function aliveTick() {
    Promise.all([
      aliveFetchJSON('/api/v1/hardware/inventory').catch(function () { return null; }),
      aliveFetchJSON('/api/v1/smart/status').catch(function () { return null; }),
      aliveFetchJSON('/api/v1/smart/channels').catch(function () { return null; }),
      aliveFetchJSON('/api/v1/probe/opportunistic/status').catch(function () { return null; })
    ]).then(function (rs) {
      aliveState.inventory = rs[0];
      aliveState.smart     = rs[1];
      aliveState.channels  = rs[2];
      aliveState.opp       = rs[3];
      aliveState.pollOk = true;
      aliveRenderHeroForecast();
      aliveRenderInsightRail();
      aliveRenderNarrator(false);
      aliveUpdateOppPill();
    }).catch(function () {
      aliveState.pollOk = false;
    });
  }

  /* aliveExtractSensorHistory: pull the 60-sample history array for
     a given sensor by alias. The inventory payload's per-chip sensor
     list carries history oldest-first per spec. Returns null when
     no match — the hero spark falls back to its own client-side
     history buffer (same source the steady-state spark draws from).
  */
  function aliveExtractSensorHistory(matcher) {
    if (!aliveState.inventory || !Array.isArray(aliveState.inventory.chips)) return null;
    for (var i = 0; i < aliveState.inventory.chips.length; i++) {
      var chip = aliveState.inventory.chips[i];
      var sensors = (chip && chip.sensors) || [];
      for (var j = 0; j < sensors.length; j++) {
        var s = sensors[j];
        if (matcher(s) && Array.isArray(s.history) && s.history.length >= 2) {
          return s.history.slice();
        }
      }
    }
    return null;
  }

  /* aliveRenderHeroForecast renders the past-only spark with a
     fixed Y-axis pinned to HERO_SPARK_TEMP_MIN_C..MAX_C. The line
     evolves left-to-right as new samples arrive; its shape is
     stable across polls because the Y mapping never changes
     (Phoenix's UX feedback: "the line should never change it just
     goes up or down with the temp changes").

     The fake client-side linear-fit forecast (and its badge,
     uncertainty band, dashed future line, "forecast pending" /
     "uncertain" text) was REMOVED in this branch — see #43. The
     daemon-backed predicted ΔT from Layer-C marginal RLS lands
     once /api/v1/confidence/status (or a sibling endpoint)
     exposes per-sensor predicted_delta_t. */
  function aliveRenderHeroForecast() {
    aliveRenderHeroSpark('hero-cpu-path', 'cpu', heroCpuHistory, looksLikeCPU);
    aliveRenderHeroSpark('hero-gpu-path', 'gpu', heroGpuHistory, looksLikeGPU);
  }
  function aliveRenderHeroSpark(pathId, kind, fallbackHistory, matcher) {
    var pathEl = document.getElementById(pathId);
    if (!pathEl) return;
    var svg = pathEl.parentNode;
    if (!svg) return;
    if (!svg.dataset.kind) svg.dataset.kind = kind;

    var inv = aliveExtractSensorHistory(function (s) {
      var name = (s && (s.alias || s.id)) || '';
      return matcher(String(name));
    });
    var history = (inv && inv.length >= 4) ? inv : fallbackHistory;
    aliveClearHeroExtras(svg);
    aliveResetForecastSub(kind);
    if (!history || history.length < 4) {
      pathEl.setAttribute('d', '');
      return;
    }

    var W = 240, H = 48;
    var lo = HERO_SPARK_TEMP_MIN_C;
    var hi = HERO_SPARK_TEMP_MAX_C;
    var range = hi - lo;
    function toY(v) {
      var clamped = Math.max(lo, Math.min(hi, v));
      return (H - 2) - ((clamped - lo) / range) * (H - 4);
    }
    function toX(i, n) { return (i / Math.max(1, n - 1)) * W; }

    // Apply EMA smoothing to the displayed line so per-poll sensor
    // jitter (real but visually noisy) doesn't render as a chaotic
    // sawtooth scribble. The big number above the spark stays the
    // raw current value — we're only smoothing the visualisation,
    // not lying about the reading. Phoenix's Tailscale screenshot
    // showed the unsmoothed line as visual chaos within a single
    // frame; alpha=0.4 keeps real trends visible while damping the
    // ±1°C single-sample swings.
    var smoothed = aliveSmoothEMA(history, 0.4);

    var n = smoothed.length;
    var d = '';
    for (var i = 0; i < n; i++) {
      d += (i === 0 ? 'M ' : ' L ') + toX(i, n).toFixed(1) + ' ' + toY(smoothed[i]).toFixed(1);
    }
    pathEl.setAttribute('d', d);

    aliveEnsureNowDotOnly(svg, toX(n - 1, n), toY(smoothed[n - 1]));
  }
  /* aliveSmoothEMA applies a single-pole exponential moving average
     to a numeric series. Output[0] = input[0]; output[i] = alpha *
     input[i] + (1 - alpha) * output[i-1]. Lower alpha = smoother
     line, more lag. The smoothed value at the latest sample tracks
     the underlying trend within a couple of polls; the per-sample
     noise is suppressed in proportion to (1 - alpha). */
  function aliveSmoothEMA(arr, alpha) {
    if (!Array.isArray(arr) || arr.length === 0) return arr || [];
    var out = new Array(arr.length);
    out[0] = arr[0];
    for (var i = 1; i < arr.length; i++) {
      out[i] = alpha * arr[i] + (1 - alpha) * out[i - 1];
    }
    return out;
  }
  /* aliveResetForecastSub clears the hero card sub-line back to the
     simple "last 60 s" label. Called every poll so any stale forecast
     DOM left over from a previous v0.5.14 deploy is purged in-place. */
  function aliveResetForecastSub(kind) {
    var subId = kind === 'cpu' ? 'hero-cpu-sub' : 'hero-gpu-sub';
    var sub = document.getElementById(subId);
    if (!sub) return;
    if (sub.dataset.aliveSimpleSub === '1') return;
    sub.textContent = 'last 60 s';
    sub.className = 'dash-hero-sub';
    sub.dataset.aliveSimpleSub = '1';
  }
  function aliveEnsureNowDotOnly(svg, nowX, nowY) {
    var SVG_NS = 'http://www.w3.org/2000/svg';
    var dot = svg.querySelector('.dash-hero-spark-now');
    if (!dot) {
      dot = document.createElementNS(SVG_NS, 'circle');
      dot.setAttribute('class', 'dash-hero-spark-now');
      dot.setAttribute('r', '2.5');
      svg.appendChild(dot);
    }
    dot.setAttribute('cx', String(nowX));
    dot.setAttribute('cy', nowY.toFixed(1));
  }
  function aliveClearHeroExtras(svg) {
    ['.dash-hero-spark-band', '.dash-hero-spark-future', '.dash-hero-spark-now'].forEach(function (sel) {
      var el = svg.querySelector(sel);
      if (el) el.remove();
    });
  }
  // aliveSetForecastBadge / aliveLinearForecast: REMOVED in this branch.
  // The dashboard no longer fabricates a forecast from a 12-sample
  // client-side linear fit (#43). Until the daemon's confidence
  // endpoint exposes per-sensor predicted ΔT from Layer-C marginal
  // RLS, the hero card shows the past spark only.

  /* aliveAttachSensorIntent: idempotent — attaches an intent pill
     to the sensor tile head when missing, and updates its label
     each tick from the most recent 5 samples in sensorHistory. */
  function aliveAttachSensorIntent(tile, sensor) {
    if (!tile) return;
    var head = tile.querySelector('.dash-tile-head');
    if (!head) return;
    var intent = tile.querySelector('.js-intent');
    if (!intent) {
      intent = document.createElement('span');
      intent.className = 'dash-tile-intent js-intent';
      var arr = document.createElement('span');
      arr.className = 'dash-tile-intent-arrow js-intent-arrow';
      arr.textContent = '·';
      var lab = document.createElement('span');
      lab.className = 'js-intent-label';
      lab.textContent = '—';
      intent.appendChild(arr);
      intent.appendChild(document.createTextNode(' '));
      intent.appendChild(lab);
      head.appendChild(intent);
    }
    var hist = sensorHistory[sensor.name] || [];
    if (hist.length < 2) {
      intent.className = 'dash-tile-intent js-intent is-hold';
      intent.querySelector('.js-intent-arrow').textContent = '·';
      intent.querySelector('.js-intent-label').textContent = '—';
      return;
    }
    var recent = hist.slice(Math.max(0, hist.length - 5));
    var trend = recent[recent.length - 1] - recent[0];
    var dir = trend > 0.6 ? 'up' : (trend < -0.6 ? 'down' : 'hold');
    var arrow = dir === 'up' ? '↑' : dir === 'down' ? '↓' : '·';
    intent.className = 'dash-tile-intent js-intent is-' + dir;
    intent.querySelector('.js-intent-arrow').textContent = arrow;
    intent.querySelector('.js-intent-label').textContent =
      dir === 'hold' ? 'steady' : ((trend > 0 ? '+' : '') + trend.toFixed(1) + '°');
  }

  /* aliveAttachFanAffordances: idempotent — attaches the intent
     pill, the duty-bar target marker, the coupling sub-line, and
     dispatches the flash-on-decision class. Safe to call every
     poll for every fan tile. */
  function aliveAttachFanAffordances(tile, fan) {
    if (!tile) return;
    var head = tile.querySelector('.dash-tile-head');
    if (head && !tile.querySelector('.js-intent')) {
      var intent = document.createElement('span');
      intent.className = 'dash-tile-intent js-intent is-hold';
      intent.innerHTML = '';
      var arr = document.createElement('span');
      arr.className = 'dash-tile-intent-arrow js-intent-arrow';
      arr.textContent = '·';
      var lab = document.createElement('span');
      lab.className = 'js-intent-label';
      lab.textContent = 'hold';
      intent.appendChild(arr);
      intent.appendChild(document.createTextNode(' '));
      intent.appendChild(lab);
      head.appendChild(intent);
    }
    var bar = tile.querySelector('.dash-fan-bar');
    if (bar && !bar.querySelector('.dash-fan-bar-target')) {
      var marker = document.createElement('span');
      marker.className = 'dash-fan-bar-target js-target';
      marker.style.left = '0%';
      bar.appendChild(marker);
    }
    if (!tile.querySelector('.dash-tile-coupling')) {
      var coupling = document.createElement('div');
      coupling.className = 'dash-tile-coupling js-coupling';
      coupling.textContent = '';
      tile.appendChild(coupling);
    }

    aliveUpdateFanIntentAndTarget(tile, fan);
    aliveUpdateFanCoupling(tile, fan);

    if (aliveDecisionFlashSet[fan.name]) {
      delete aliveDecisionFlashSet[fan.name];
      tile.classList.remove('is-just-changed');
      // Restart the keyframe by forcing a reflow.
      // eslint-disable-next-line no-unused-expressions
      void tile.offsetWidth;
      tile.classList.add('is-just-changed');
    }
  }

  /* aliveUpdateFanIntentAndTarget: derive the controller's target
     from whatever the fan struct exposes — `target_pwm` (preferred,
     when the daemon ships it) or `target_duty_pct`, fallback to
     duty_pct itself (= "hold"). The intent pill shows the target
     duty when target ≠ current; otherwise "hold". */
  function aliveUpdateFanIntentAndTarget(tile, fan) {
    var intent = tile.querySelector('.js-intent');
    if (!intent) return;
    var current = (fan && typeof fan.duty_pct === 'number') ? fan.duty_pct : null;
    var target = null;
    if (fan && typeof fan.target_duty_pct === 'number') target = fan.target_duty_pct;
    else if (fan && typeof fan.target_pwm === 'number') target = (fan.target_pwm / 255) * 100;

    var arrow = '·';
    var dir = 'hold';
    var label = 'hold';
    if (current != null && target != null && Math.abs(target - current) > 1.5) {
      if (target > current) { dir = 'up';   arrow = '↑'; }
      else                  { dir = 'down'; arrow = '↓'; }
      label = Math.round(target) + '%';
    }
    intent.className = 'dash-tile-intent js-intent is-' + dir;
    intent.querySelector('.js-intent-arrow').textContent = arrow;
    intent.querySelector('.js-intent-label').textContent = label;

    var marker = tile.querySelector('.js-target');
    if (marker) {
      var t = (target != null) ? Math.max(0, Math.min(100, target)) : (current != null ? current : 0);
      marker.style.left = t.toFixed(1) + '%';
    }
  }

  /* aliveUpdateFanCoupling: looks up the fan in the inventory's
     curves[].drives lists. The first matching curve wins; if none,
     show "—" (NEVER fabricate a coupling). */
  function aliveUpdateFanCoupling(tile, fan) {
    var el = tile.querySelector('.js-coupling');
    if (!el) return;
    var curveName = aliveFindCurveForFan(fan);
    if (!curveName) {
      el.textContent = '';
      return;
    }
    el.textContent = 'coupled to ' + curveName;
  }
  function aliveFindCurveForFan(fan) {
    if (!aliveState.inventory || !Array.isArray(aliveState.inventory.curves)) return '';
    var fanIds = aliveCandidateFanIds(fan);
    var curves = aliveState.inventory.curves;
    for (var i = 0; i < curves.length; i++) {
      var c = curves[i];
      var drives = (c && c.drives) || [];
      for (var j = 0; j < drives.length; j++) {
        if (fanIds.indexOf(String(drives[j])) >= 0) return c.name || c.id || 'curve ' + i;
      }
    }
    return '';
  }
  function aliveCandidateFanIds(fan) {
    if (!fan) return [];
    var out = [];
    ['id', 'name', 'pwm_path', 'path', 'channel'].forEach(function (k) {
      if (fan[k]) out.push(String(fan[k]));
    });
    return out;
  }

  /* aliveDetectDecisions: windowed-delta + per-fan rate limit.
     Tracks the last ALIVE_DECISION_WINDOW duty_pct samples per fan;
     emits a decision when oldest-vs-newest delta crosses threshold
     AND the per-fan rate-limit window has elapsed since last emit.
     Cause is derived from the controller-coupled sensor's recent
     trend.

     Replaces the v1 persistence-gate from #925 which mis-classified
     a ramp's stable tail as "flat → reset candidate" and missed
     real ramps. The architectural fix (#924) — controller emits
     real decisions via SSE — retires inference entirely. */
  function aliveDetectDecisions(fans, sensors) {
    if (!Array.isArray(fans)) return;
    var now = Date.now();
    var sensorTrends = aliveSensorTrendMap(sensors);
    fans.forEach(function (f) {
      var name = f.name;
      if (!name) return;
      var d = (typeof f.duty_pct === 'number') ? f.duty_pct : null;
      aliveState.lastFans[name] = f;
      if (d == null) return;

      // Maintain per-fan window of the last N polls; lastFanDuty stays
      // populated as a single-poll cache for the tile intent arrows.
      var prev = aliveState.lastFanDuty[name];
      aliveState.lastFanDuty[name] = d;
      var win = aliveState.fanDutyWindow[name];
      if (!win) {
        win = [];
        aliveState.fanDutyWindow[name] = win;
      }
      win.push(d);
      if (win.length > ALIVE_DECISION_WINDOW) win.shift();
      if (win.length < 2) return;

      var oldest = win[0];
      var newest = win[win.length - 1];
      var delta = newest - oldest;
      if (Math.abs(delta) < ALIVE_DECISION_THRESHOLD) return;

      // Per-fan rate limit: a sustained ramp that keeps moving N pp per
      // poll would otherwise emit one decision per tick. 6 s gate gives
      // the operator one event per real transition.
      var lastAt = aliveState.fanLastDecisionAt[name] || 0;
      if (now - lastAt < ALIVE_DECISION_RATELIMIT_MS) return;
      aliveState.fanLastDecisionAt[name] = now;

      var dir = delta > 0 ? 'up' : 'down';
      var curveName = aliveFindCurveForFan(f);
      var coupledSensor = aliveFindCoupledSensor(curveName);
      var trendInfo = coupledSensor ? sensorTrends[coupledSensor] : null;
      var causeText;
      if (coupledSensor && trendInfo) {
        causeText = coupledSensor + ' ' + trendInfo.label;
      } else if (curveName) {
        causeText = 'curve ' + curveName;
      } else {
        causeText = 'controller adjustment';
      }
      var entry = {
        ts: now,
        fan: name,
        from: Math.round(oldest),
        to: Math.round(newest),
        dir: dir,
        cause: causeText
      };
      aliveState.decisions.unshift(entry);
      if (aliveState.decisions.length > ALIVE_DECISION_CAP) {
        aliveState.decisions.length = ALIVE_DECISION_CAP;
      }
      aliveDecisionFlashSet[name] = true;
      aliveRenderNarrator(true, entry);
      aliveRenderInsightRail();
    });
  }
  function aliveSensorTrendMap(sensors) {
    var out = {};
    if (!Array.isArray(sensors)) return out;
    sensors.forEach(function (s) {
      var hist = sensorHistory[s.name] || [];
      if (hist.length < 2) {
        out[s.name] = { trend: 0, label: 'steady' };
        return;
      }
      var recent = hist.slice(Math.max(0, hist.length - 5));
      var trend = recent[recent.length - 1] - recent[0];
      var label = trend > 0.6 ? 'trending up' : (trend < -0.6 ? 'trending down' : 'steady');
      out[s.name] = { trend: trend, label: label };
    });
    return out;
  }
  function aliveFindCoupledSensor(curveName) {
    if (!curveName || !aliveState.inventory || !Array.isArray(aliveState.inventory.curves)) return '';
    var curves = aliveState.inventory.curves;
    for (var i = 0; i < curves.length; i++) {
      var c = curves[i];
      if ((c.name || c.id) !== curveName) continue;
      var sensorIds = (c && c.consumes) || [];
      if (!sensorIds.length) return '';
      // Walk the inventory chips to resolve sensor id → human alias.
      var chips = aliveState.inventory.chips || [];
      for (var k = 0; k < chips.length; k++) {
        var ss = chips[k].sensors || [];
        for (var j = 0; j < ss.length; j++) {
          if (sensorIds.indexOf(ss[j].id) >= 0) return ss[j].alias || ss[j].id;
        }
      }
      return String(sensorIds[0]);
    }
    return '';
  }

  /* aliveRenderNarrator shows a SINGLE stable line: the most recent
     decision the controller actually made. When a new decision
     arrives, the line updates to that. After ALIVE_NARRATOR_IDLE_MS
     without any new decision, the line transitions to "system idle".
     Per Phoenix's UX feedback (24-04 burst-frame session), the line
     used to rotate through past decisions every 6 s — operators
     read the rotating strings as new things constantly happening
     when really only one thing happened. The line now stays put
     until the next REAL transition. */
  function aliveRenderNarrator(_immediate, entry) {
    var bar = $('dash-narrator');
    var txt = $('dash-narrator-text');
    var time = $('dash-narrator-time');
    if (!bar || !txt) return;
    var now = Date.now();

    // If a fresh decision was just emitted, pin the line to it.
    if (entry) {
      txt.textContent = aliveNarratorLine(entry);
      if (time) time.textContent = aliveFormatTime(entry.ts);
      bar.hidden = false;
      aliveState.narratorLastShown = now;
      return;
    }

    // Otherwise: pin to the most recent decision in the feed (index 0),
    // upgrading to "system idle" when it's been quiet long enough.
    if (aliveState.decisions.length > 0) {
      var d = aliveState.decisions[0];
      var ageMs = now - d.ts;
      if (ageMs > ALIVE_NARRATOR_IDLE_MS) {
        txt.textContent = 'system idle — no decisions in ' + Math.round(ageMs / 1000) + ' s';
      } else {
        txt.textContent = aliveNarratorLine(d);
      }
      if (time) time.textContent = aliveFormatTime(d.ts);
      bar.hidden = false;
      return;
    }

    // No decisions yet — keep hidden until the page has been live
    // long enough to confidently say "no decisions yet".
    if (now - bootAt > ALIVE_NARRATOR_IDLE_MS) {
      txt.textContent = 'system idle — no decisions yet';
      if (time) time.textContent = '';
      bar.hidden = false;
    } else {
      bar.hidden = true;
    }
  }
  function aliveNarratorLine(d) {
    if (d.dir === 'up') {
      return 'ramped ' + d.fan + ' from ' + d.from + '% → ' + d.to + '% — ' + d.cause;
    }
    if (d.dir === 'down') {
      return 'eased ' + d.fan + ' from ' + d.from + '% → ' + d.to + '% — ' + d.cause;
    }
    return 'held ' + d.fan + ' at ' + d.to + '% — ' + d.cause;
  }
  function aliveFormatTime(ms) {
    var dt = new Date(ms);
    var pad = function (n) { return n < 10 ? '0' + n : '' + n; };
    return pad(dt.getHours()) + ':' + pad(dt.getMinutes()) + ':' + pad(dt.getSeconds());
  }

  /* aliveRotateNarrator no longer rotates indices — it just refreshes
     the single most-recent line so the relative-age suffix updates
     ("system idle — no decisions in N s") as time passes. Kept as a
     periodic tick because the strip can transition from "ramped X
     from A% → B%" to "system idle — no decisions in 12 s" without
     a new decision arriving. */
  function aliveRotateNarrator() {
    aliveRenderNarrator(false);
  }

  /* aliveRenderInsightRail: top-level orchestrator for the three-
     column rail. Reveals the section once we have a poll, and hides
     the AI brief column entirely when smart mode is disabled (the
     coupling map + decision feed are still useful in monitor-only
     mode). */
  function aliveRenderInsightRail() {
    var rail = $('dash-insight');
    if (!rail) return;
    if (!aliveState.pollOk && !aliveState.inventory) {
      // Don't show an empty rail before we've had a successful poll.
      return;
    }
    rail.hidden = false;

    aliveRenderCouplingMap();
    aliveRenderDecisionFeed();
    aliveRenderBrief();

    var meta = $('dash-insight-meta');
    if (meta) {
      var n = (aliveState.inventory && aliveState.inventory.curves || []).length;
      meta.textContent = n + ' curve' + (n === 1 ? '' : 's') + ' · ' + (aliveState.smart && aliveState.smart.preset || 'manual');
    }
  }

  function aliveRenderCouplingMap() {
    var svg = $('dash-coupling-svg');
    if (!svg) return;
    var SVG_NS = 'http://www.w3.org/2000/svg';
    // Empty out everything we previously rendered. Fast — the map
    // is small (≤ a few dozen nodes/edges).
    while (svg.firstChild) svg.removeChild(svg.firstChild);

    var curves = (aliveState.inventory && aliveState.inventory.curves) || [];
    if (curves.length === 0) {
      var fo = document.createElementNS(SVG_NS, 'foreignObject');
      fo.setAttribute('x', '0'); fo.setAttribute('y', '0');
      fo.setAttribute('width', '360'); fo.setAttribute('height', '240');
      // CSP-friendly: render the empty-state via DOM, not innerHTML
      // strings that may carry a hex-coloured token comment.
      var div = document.createElement('div');
      div.className = 'dash-coupling-empty';
      div.textContent = 'no curves bound — system in monitor-only mode';
      fo.appendChild(div);
      svg.appendChild(fo);
      return;
    }

    var meta = $('dash-coupling-meta');

    // Resolve unique sensor + fan ids referenced by curves.
    var sensorList = [];
    var sensorIdx = {};
    var fanList = [];
    var fanIdx = {};
    curves.forEach(function (c) {
      (c.consumes || []).forEach(function (s) {
        var id = String(s);
        if (sensorIdx[id] == null) {
          sensorIdx[id] = sensorList.length;
          sensorList.push(id);
        }
      });
      (c.drives || []).forEach(function (f) {
        var id = String(f);
        if (fanIdx[id] == null) {
          fanIdx[id] = fanList.length;
          fanList.push(id);
        }
      });
    });

    // Lay out: sensors on left (x=40), curves middle (x=180),
    // fans on right (x=320). y-positions evenly distributed.
    var W = 360, H = 240;
    var leftX = 40, midX = 180, rightX = 320;
    function spacedY(i, n) { return ((i + 1) / (n + 1)) * H; }

    // Edges are drawn first so nodes overlay them.
    curves.forEach(function (c, ci) {
      var cy = spacedY(ci, curves.length);
      var consumes = c.consumes || [];
      var drives = c.drives || [];
      consumes.forEach(function (sid) {
        var sIdx = sensorIdx[String(sid)];
        var sy = spacedY(sIdx, sensorList.length);
        var d = aliveCurvePath(leftX + 14, sy, midX - 14, cy);
        var p = document.createElementNS(SVG_NS, 'path');
        p.setAttribute('class', 'dash-coupling-edge');
        p.setAttribute('d', d);
        svg.appendChild(p);
      });
      drives.forEach(function (fid) {
        var fIdx = fanIdx[String(fid)];
        var fy = spacedY(fIdx, fanList.length);
        var d = aliveCurvePath(midX + 14, cy, rightX - 14, fy);
        // "Active" when the live fan duty exceeds 30% — real signal,
        // pulled from the lastFanDuty map populated each poll.
        var fanDuty = aliveLookupFanDuty(String(fid));
        var active = (fanDuty != null && fanDuty > 30);
        var p = document.createElementNS(SVG_NS, 'path');
        p.setAttribute('class', active ? 'dash-coupling-edge-active' : 'dash-coupling-edge');
        p.setAttribute('d', d);
        svg.appendChild(p);
        // animateMotion packet REMOVED per the no-theatre rule —
        // the edge's active-class colour change already communicates
        // "fan is running"; the moving packet implied a per-event
        // data-flow signal we don't actually have.
      });
    });

    // Sensor nodes (left column)
    sensorList.forEach(function (sid, i) {
      var y = spacedY(i, sensorList.length);
      var alias = aliveResolveSensorAlias(sid) || sid;
      aliveDrawNode(svg, SVG_NS, leftX, y, alias, '', 'is-sensor');
    });
    // Curve nodes (middle column)
    curves.forEach(function (c, i) {
      var y = spacedY(i, curves.length);
      aliveDrawNode(svg, SVG_NS, midX, y, c.name || c.id || ('curve ' + i), '', 'is-curve');
    });
    // Fan nodes (right column)
    fanList.forEach(function (fid, i) {
      var y = spacedY(i, fanList.length);
      var duty = aliveLookupFanDuty(String(fid));
      var sub = (duty != null) ? Math.round(duty) + '%' : '';
      var on = (duty != null && duty > 30);
      aliveDrawNode(svg, SVG_NS, rightX, y, fid, sub, 'is-fan' + (on ? ' is-on' : ''));
    });

    if (meta) meta.textContent = sensorList.length + ' sensors · ' + curves.length + ' curves · ' + fanList.length + ' fans';
  }
  function aliveCurvePath(x1, y1, x2, y2) {
    var midX = (x1 + x2) / 2;
    return 'M ' + x1 + ' ' + y1 + ' C ' + midX + ' ' + y1 + ' ' + midX + ' ' + y2 + ' ' + x2 + ' ' + y2;
  }
  function aliveDrawNode(svg, SVG_NS, x, y, label, sub, cls) {
    var c = document.createElementNS(SVG_NS, 'circle');
    c.setAttribute('class', 'dash-coupling-node ' + (cls || ''));
    c.setAttribute('cx', String(x));
    c.setAttribute('cy', y.toFixed(1));
    c.setAttribute('r', '14');
    svg.appendChild(c);
    var t = document.createElementNS(SVG_NS, 'text');
    t.setAttribute('class', 'dash-coupling-label');
    t.setAttribute('x', String(x));
    t.setAttribute('y', (y + 2).toFixed(1));
    t.setAttribute('text-anchor', 'middle');
    t.textContent = aliveTrim(label, 8);
    svg.appendChild(t);
    if (sub) {
      var s = document.createElementNS(SVG_NS, 'text');
      s.setAttribute('class', 'dash-coupling-label-sub');
      s.setAttribute('x', String(x));
      s.setAttribute('y', (y + 11).toFixed(1));
      s.setAttribute('text-anchor', 'middle');
      s.textContent = sub;
      svg.appendChild(s);
    }
  }
  function aliveTrim(s, n) {
    s = String(s || '');
    return s.length <= n ? s : s.slice(0, n - 1) + '…';
  }
  function aliveLookupFanDuty(fid) {
    // Direct hit on cached lastFanDuty by string id (fan name)
    if (aliveState.lastFanDuty[fid] != null) return aliveState.lastFanDuty[fid];
    // Fall back: walk lastFans for any candidate id match.
    var keys = Object.keys(aliveState.lastFans);
    for (var i = 0; i < keys.length; i++) {
      var f = aliveState.lastFans[keys[i]];
      var ids = aliveCandidateFanIds(f);
      if (ids.indexOf(fid) >= 0) return aliveState.lastFanDuty[keys[i]];
    }
    return null;
  }
  function aliveResolveSensorAlias(sid) {
    if (!aliveState.inventory) return '';
    var chips = aliveState.inventory.chips || [];
    for (var k = 0; k < chips.length; k++) {
      var ss = chips[k].sensors || [];
      for (var j = 0; j < ss.length; j++) {
        if (String(ss[j].id) === String(sid)) return ss[j].alias || ss[j].id;
      }
    }
    return '';
  }

  function aliveRenderDecisionFeed() {
    var list = $('dash-decisions-list');
    if (!list) return;
    var meta = $('dash-decisions-meta');
    if (aliveState.decisions.length === 0) {
      list.innerHTML = '';
      var empty = document.createElement('div');
      empty.className = 'dash-decision dash-decision-empty';
      empty.textContent = 'no recent decisions — system steady';
      list.appendChild(empty);
      if (meta) meta.textContent = '0 events';
      return;
    }
    list.innerHTML = '';
    var n = Math.min(8, aliveState.decisions.length);
    for (var i = 0; i < n; i++) {
      var d = aliveState.decisions[i];
      var row = document.createElement('div');
      row.className = 'dash-decision' + (i === 0 ? ' is-fresh' : '');
      var t = document.createElement('span');
      t.className = 'dash-decision-time';
      t.textContent = aliveFormatTime(d.ts);
      var body = document.createElement('div');
      body.className = 'dash-decision-body';
      var act = document.createElement('div');
      act.className = 'dash-decision-act';
      var tg = document.createElement('span');
      tg.className = 'dash-decision-target';
      tg.textContent = d.fan;
      var dl = document.createElement('span');
      dl.className = 'dash-decision-delta is-' + d.dir;
      dl.textContent = d.from + '% → ' + d.to + '%';
      act.appendChild(tg);
      act.appendChild(dl);
      var cause = document.createElement('div');
      cause.className = 'dash-decision-cause';
      cause.textContent = 'because ' + d.cause;
      body.appendChild(act);
      body.appendChild(cause);
      row.appendChild(t);
      row.appendChild(body);
      list.appendChild(row);
    }
    if (meta) meta.textContent = aliveState.decisions.length + ' total · 8 shown';
  }

  function aliveRenderBrief() {
    var brief = $('dash-brief');
    if (!brief) return;
    var smart = aliveState.smart;
    var smartEnabled = !!(smart && smart.enabled);
    if (!smartEnabled) {
      // Honest empty state: hide the brief column. Coupling + decisions
      // still render — they're real data even without smart mode.
      brief.hidden = true;
      return;
    }
    brief.hidden = false;

    // Workload signature — modal across channels (most common label).
    var workloadLabel = aliveModeWorkloadLabel();
    var workloadEl = $('dash-brief-workload');
    if (workloadEl) workloadEl.textContent = workloadLabel || '—';

    // Thermal headroom — TJ_MAX − max(cpu_temp, gpu_temp). We don't
    // have direct access to the live status payload here so we read
    // the most recent sample from the hero-history client buffers
    // (those are populated from /api/v1/status on every tick).
    var cpu = heroCpuHistory.length ? heroCpuHistory[heroCpuHistory.length - 1] : null;
    var gpu = heroGpuHistory.length ? heroGpuHistory[heroGpuHistory.length - 1] : null;
    var hot = (cpu == null && gpu == null) ? null : Math.max(cpu == null ? -Infinity : cpu, gpu == null ? -Infinity : gpu);
    var headroomEl = $('dash-brief-headroom');
    if (headroomEl) {
      if (hot == null || !isFinite(hot)) {
        headroomEl.textContent = '—';
        headroomEl.className = 'dash-brief-stat-val mono';
      } else {
        var headroom = TJ_MAX - hot;
        var cls = headroom > 15 ? 'is-good' : (headroom > 6 ? '' : 'is-warn');
        headroomEl.textContent = headroom.toFixed(1) + '°C';
        headroomEl.className = 'dash-brief-stat-val mono ' + cls;
      }
    }

    // Avg duty — honest computable proxy for "acoustic activity". We
    // explicitly DO NOT render an "acoustic estimate dBA" here: no
    // R33-proxy endpoint exists in the daemon yet, so any dBA number
    // would be theatre. avg duty is the closest honest signal.
    var dutyEl = $('dash-brief-avgduty');
    if (dutyEl) {
      var fans = Object.keys(aliveState.lastFans).map(function (k) { return aliveState.lastFans[k]; });
      var dutyVals = fans.map(function (f) { return (typeof f.duty_pct === 'number') ? f.duty_pct : null; })
                         .filter(function (v) { return v != null; });
      if (dutyVals.length === 0) dutyEl.textContent = '—';
      else {
        var avg = dutyVals.reduce(function (a, b) { return a + b; }, 0) / dutyVals.length;
        dutyEl.textContent = avg.toFixed(0) + '%';
      }
    }

    // Policy — preset name + " · alive" when smart is on, else
    // "manual · curves only".
    var policyEl = $('dash-brief-policy');
    if (policyEl) {
      policyEl.textContent = (smart.preset || 'balanced') + ' · alive';
      policyEl.className = 'dash-brief-stat-val mono is-good';
    }

    // Summary — based on real signals:
    //   1. soak: ANY exhaust-named fan ramped UP in the recent decisions
    //   2. cooling: more "down" decisions than "up" in the last 8
    //   3. otherwise: steady state
    var foot = $('dash-brief-foot');
    if (foot) {
      var summary = aliveSummary(smart);
      foot.innerHTML = '';
      foot.appendChild(summary);
    }

    // Header meta — opportunistic probe in flight indicator, sourced
    // from /api/v1/probe/opportunistic/status. Honest "live · probe in
    // flight" only when the daemon actually reports running=true.
    var briefMeta = $('dash-brief-meta');
    if (briefMeta) {
      var oppRunning = !!(aliveState.opp && aliveState.opp.running);
      briefMeta.textContent = oppRunning ? 'live · probe in flight' : 'live';
    }
  }
  function aliveModeWorkloadLabel() {
    var ch = aliveState.channels;
    if (!ch || !Array.isArray(ch.channels) || ch.channels.length === 0) return '';
    var counts = {};
    var max = 0, mode = '';
    ch.channels.forEach(function (c) {
      var lab = c && c.signature_label;
      if (!lab) return;
      counts[lab] = (counts[lab] || 0) + 1;
      if (counts[lab] > max) { max = counts[lab]; mode = lab; }
    });
    if (!mode) return '';
    // Truncate to 8 hex chars per spec.
    return mode.length > 8 ? mode.slice(0, 8) : mode;
  }
  function aliveSummary(smart) {
    var span = document.createElement('span');
    var recent = aliveState.decisions.slice(0, 8);
    if (recent.length === 0) {
      span.textContent = 'system in steady state · loop is in the groove.';
      return span;
    }
    // soak detection: ANY exhaust-shaped fan name moved UP recently
    var exhaustUp = recent.some(function (d) {
      return d.dir === 'up' && /exhaust|rear|top/i.test(d.fan);
    });
    if (exhaustUp) {
      var s1 = document.createElement('strong');
      s1.textContent = 'Soak detected';
      span.appendChild(s1);
      span.appendChild(document.createTextNode(' — pre-spinning exhaust fans to absorb the heat.'));
      return span;
    }
    var ups = recent.filter(function (d) { return d.dir === 'up'; }).length;
    var downs = recent.filter(function (d) { return d.dir === 'down'; }).length;
    if (downs > ups + 1) {
      span.appendChild(document.createTextNode('Cooling trend across the last '));
      var n = document.createElement('strong');
      n.textContent = recent.length + ' decisions';
      span.appendChild(n);
      span.appendChild(document.createTextNode(' — easing duty to recover acoustic budget.'));
      return span;
    }
    span.appendChild(document.createTextNode('Workload signature stable on '));
    var preset = document.createElement('strong');
    preset.textContent = smart.preset || 'balanced';
    span.appendChild(preset);
    span.appendChild(document.createTextNode('. Loop is in the groove · minor adjustments only.'));
    return span;
  }

  // Allow the existing pollOpportunisticStatus pill to share the
  // same opp payload aliveTick already fetched (avoids a second
  // /api/v1/probe/opportunistic/status request inside the alive
  // cadence). The standalone 5 s pill polls in addition — both
  // payloads agree because they hit the same endpoint.
  function aliveUpdateOppPill() {
    var pill = document.getElementById('dash-opp-pill');
    var text = document.getElementById('dash-opp-pill-text');
    if (!pill || !text) return;
    var s = aliveState.opp;
    if (s && s.running) {
      var pwm = s.gap_pwm != null ? s.gap_pwm : '?';
      text.textContent = 'probing PWM ' + pwm;
      pill.hidden = false;
    }
  }

  // Hook into the existing applyStatus path so that /api/v1/status
  // payloads (already polled at 1 Hz) feed decision detection. We
  // wrap the original applyStatus rather than re-fetching — the
  // /api/v1/status endpoint must NOT be double-polled.
  var __aliveOriginalApplyStatus = applyStatus;
  applyStatus = function (data) {
    __aliveOriginalApplyStatus(data);
    if (data) {
      aliveDetectDecisions(data.fans || [], data.sensors || []);
    }
  };

  // ── start ──────────────────────────────────────────────────────────
  seedHistory();
  pollOnce();
  pollTimer = setInterval(pollOnce, pollInterval);
  setInterval(function () { if (!inDemo) tickUptime(); }, 1000);
  pollOpportunisticStatus();
  setInterval(pollOpportunisticStatus, 5000);
  pollSmartMode();
  setInterval(pollSmartMode, 10000);
  pollConfidence();
  setInterval(pollConfidence, 5000);
  // alive overlay — coalesced 1500 ms tick (separate cadence from
  // the 1 s steady-state poll on /api/v1/status; that one is
  // hooked into applyStatus above for decision detection).
  aliveTick();
  setInterval(aliveTick, ALIVE_TICK_MS);
  setInterval(aliveRotateNarrator, ALIVE_NARRATOR_PERIOD_MS);
})();
