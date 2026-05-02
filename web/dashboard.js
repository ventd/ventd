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
    var cpuP  = $('hero-cpu-path'); if (cpuP) cpuP.setAttribute('d', sparkPathTemp(heroCpuHistory, 240, 48));
    var gpuP  = $('hero-gpu-path'); if (gpuP) gpuP.setAttribute('d', sparkPathTemp(heroGpuHistory, 240, 48));
    var cpuS = $('hero-cpu-sub'); if (cpuS) cpuS.textContent = cpu == null ? 'no source' : 'last 60s';
    var gpuS = $('hero-gpu-sub'); if (gpuS) gpuS.textContent = gpu == null ? 'no source' : 'last 60s';

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
      .then(function (out) { applyStatus(out[0]); if (out[1]) applyProfile(out[1]); })
      .catch(function () {
        if (!inDemo) { inDemo = true; startDemo(); }
      });
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
      cpuTemp = clamp(cpuTemp + (Math.random() - 0.4) * 1.5, 35, 90);
      gpuTemp = clamp(gpuTemp + (Math.random() - 0.4) * 1.8, 38, 88);
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
    pollTimer = setInterval(tick, 900);
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
})();
