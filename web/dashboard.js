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

  function sparkPath(buf, w, h) {
    if (!buf || buf.length < 2) return '';
    var max = -Infinity, min = Infinity;
    for (var i = 0; i < buf.length; i++) {
      if (buf[i] > max) max = buf[i];
      if (buf[i] < min) min = buf[i];
    }
    var range = Math.max(max - min, max * 0.05, 1);
    var d = '';
    for (var j = 0; j < buf.length; j++) {
      var x = (j / (SPARK_N - 1)) * w;
      var y = (h - 2) - ((buf[j] - min) / range) * (h - 4);
      d += (j === 0 ? 'M ' : ' L ') + x.toFixed(1) + ' ' + y.toFixed(1);
    }
    return d;
  }

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
      if (path) path.setAttribute('d', sparkPath(sensorHistory[s.name], 240, 28));
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
      if (rpm != null && rpm > 60) spinning++;
      pushHistory(fanHistory, f.name, rpm == null ? null : rpm);
      var tile = existing[key];
      if (!tile) {
        tile = document.createElement('div');
        tile.className = 'dash-tile';
        tile.dataset.key = key;
        tile.innerHTML =
            '<div class="dash-tile-head">'
          +   '<span class="dash-tile-name">' + escapeHTML(f.name) + '</span>'
          +   '<span class="dash-tile-source mono">RPM</span>'
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
      if (rpm == null) valEl.textContent = '—';
      else valEl.textContent = rpm;
      var bar = tile.querySelector('.js-bar');
      if (bar) bar.style.width = (clamp(f.duty_pct || 0, 0, 100)).toFixed(1) + '%';
      var duty = tile.querySelector('.js-duty');
      if (duty) duty.textContent = Math.round(f.duty_pct || 0);
      var path = tile.querySelector('.js-spark');
      if (path) path.setAttribute('d', sparkPath(fanHistory[f.name], 240, 28));

      tile.classList.toggle('is-spinning', !!(rpm && rpm > 60));
      tile.classList.toggle('is-stalled', rpm === 0 && (f.duty_pct || 0) > 5);
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
    var cpuP  = $('hero-cpu-path'); if (cpuP) cpuP.setAttribute('d', sparkPath(heroCpuHistory, 240, 48));
    var gpuP  = $('hero-gpu-path'); if (gpuP) gpuP.setAttribute('d', sparkPath(heroGpuHistory, 240, 48));
    var cpuS = $('hero-cpu-sub'); if (cpuS) cpuS.textContent = cpu == null ? 'no source' : 'last 60s';
    var gpuS = $('hero-gpu-sub'); if (gpuS) gpuS.textContent = gpu == null ? 'no source' : 'last 60s';

    // Fan hero
    var spinning = 0, total = 0;
    if (fans) {
      total = fans.length;
      for (var k = 0; k < fans.length; k++) {
        if (fans[k].rpm != null && fans[k].rpm > 60) spinning++;
      }
    }
    var fEl = $('hero-fans-val'); if (fEl) fEl.textContent = total === 0 ? '—' : (spinning + '/' + total);
    var fSub = $('hero-fans-sub'); if (fSub) fSub.textContent = total === 0 ? 'no fans yet' : 'of ' + total + ' total';

    // Fan duty bar chart in hero
    var bars = $('hero-fans-bars');
    if (bars) {
      var html = '';
      var show = (fans || []).slice(0, 12);
      for (var b = 0; b < 12; b++) {
        var d = show[b] ? clamp(show[b].duty_pct || 0, 0, 100) : 0;
        var height = Math.max(6, (d / 100) * 30);
        html += '<span style="height:' + height.toFixed(1) + 'px"></span>';
      }
      bars.innerHTML = html;
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

  function pollOnce() {
    Promise.all([
      fetch('/api/v1/status', { credentials: 'same-origin' })
        .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); }),
      fetch('/api/v1/profile/active', { credentials: 'same-origin' })
        .then(function (r) { return r.ok ? r.json() : null; })
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

  // ── start ──────────────────────────────────────────────────────────
  pollOnce();
  pollTimer = setInterval(pollOnce, pollInterval);
  setInterval(function () { if (!inDemo) tickUptime(); }, 1000);
})();
