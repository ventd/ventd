// sensors.js — live readings table.
//
//   GET /api/v1/hardware/inventory → { chips: [...], curves: [...] }
//
// Each chip carries its own sensor list, and each sensor already has a
// `used_by` array (curve names that consume the alias). This is the
// same endpoint /health and /devices consume, so the three views
// agree on what the daemon enumerated. Previously this page fetched
// /api/v1/status which only returns sensors *bound to curves*, so a
// fresh-install host with one bound sensor saw "1 / TEMP" while the
// rest of the UI saw all 17 — #1412.

(function () {
  'use strict';

  // theme
  var root = document.documentElement;
  try { var s = localStorage.getItem('ventd-theme'); if (s) root.dataset.theme = s; } catch (_) {}
  var t = document.getElementById('theme-toggle');
  if (t) t.addEventListener('click', function () {
    var n = root.dataset.theme === 'dark' ? 'light' : 'dark';
    root.dataset.theme = n;
    try { localStorage.setItem('ventd-theme', n); } catch (_) {}
  });

  function $(id) { return document.getElementById(id); }
  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  // ── history buffer per sensor ──────────────────────────────────────
  var SPARK_N = 60;
  var hist = {};
  function pushH(name, v) {
    if (!hist[name]) hist[name] = [];
    hist[name].push(v);
    if (hist[name].length > SPARK_N) hist[name].shift();
  }
  function sparkPath(buf) {
    if (!buf || buf.length < 2) return '';
    var max = -Infinity, min = Infinity;
    for (var i = 0; i < buf.length; i++) {
      if (buf[i] == null) continue;
      if (buf[i] > max) max = buf[i];
      if (buf[i] < min) min = buf[i];
    }
    if (!isFinite(max)) return '';
    var range = Math.max(max - min, max * 0.05, 1);
    var d = '', W = 200, H = 28;
    for (var j = 0; j < buf.length; j++) {
      var v = buf[j]; if (v == null) continue;
      var x = (j / (SPARK_N - 1)) * W;
      var y = (H - 2) - ((v - min) / range) * (H - 4);
      d += (d ? ' L ' : 'M ') + x.toFixed(1) + ' ' + y.toFixed(1);
    }
    return d;
  }

  // ── filter state ───────────────────────────────────────────────────
  var filterText = '';
  var filterUnit = 'all';
  $('sn-filter-input').addEventListener('input', function (e) {
    filterText = e.target.value.trim().toLowerCase();
    render();
  });
  Array.prototype.forEach.call(document.querySelectorAll('#sn-segments .filter-segment'), function (b) {
    b.addEventListener('click', function () {
      Array.prototype.forEach.call(document.querySelectorAll('#sn-segments .filter-segment'), function (x) {
        x.classList.remove('is-active'); x.setAttribute('aria-selected', 'false');
      });
      b.classList.add('is-active'); b.setAttribute('aria-selected', 'true');
      filterUnit = b.dataset.unit;
      render();
    });
  });

  // ── data ──────────────────────────────────────────────────────────
  // sensors is the flattened inventory.chips[].sensors[] list mapped
  // to the shape this page already renders: {name, value, unit, used_by}.
  // Kept as a separate variable from the raw inventory response so the
  // pre-existing render() / renderSummary() helpers don't need to know
  // about the chip-by-chip wire shape.
  var sensors = null;

  function unitClass(unit) {
    if (unit === '°C') return 'is-temp';
    if (unit === 'V')  return 'is-volt';
    if (unit === 'W')  return 'is-power';
    return '';
  }
  function tempSeverity(v) {
    if (v == null) return '';
    if (v >= 85) return 'is-hot';
    if (v >= 75) return 'is-warn';
    return '';
  }

  // map sensor name → list of curves that use it. The inventory
  // response already carries `used_by` per sensor (#1412), so this
  // helper just lifts it out of the flat sensors slice for the
  // renderer's existing keyed lookup pattern.
  function buildUsedByMap() {
    var map = {};
    if (!sensors) return map;
    sensors.forEach(function (s) {
      if (s.used_by && s.used_by.length) map[s.name] = s.used_by.slice();
    });
    return map;
  }

  // kindToUnit translates the inventory's `kind` ("temp"|"fan"|"volt"
  // |"power") into the unit string the existing UI segment buttons
  // already filter on. /status used to ship unit verbatim; inventory
  // ships kind + unit separately, so we trust unit when present and
  // fall back to a kind-derived default.
  function kindToUnit(kind, unit) {
    if (unit) return unit;
    switch (kind) {
      case 'temp':  return '°C';
      case 'volt':  return 'V';
      case 'power': return 'W';
      case 'fan':   return 'RPM';
      default:      return '';
    }
  }

  // ── render ────────────────────────────────────────────────────────
  function render() {
    var tbody = $('sn-tbody');
    if (!tbody) return;
    if (!sensors || sensors.length === 0) {
      tbody.innerHTML = '<tr><td colspan="4" class="sn-tbody-empty">No sensors reporting.</td></tr>';
      return;
    }

    // Collect history this tick.
    sensors.forEach(function (s) {
      pushH(s.name, s.value == null ? null : Number(s.value));
    });

    var used = buildUsedByMap();

    var rows = sensors.filter(function (s) {
      if (filterUnit !== 'all' && (s.unit || '°C') !== filterUnit) return false;
      if (!filterText) return true;
      return (s.name || '').toLowerCase().indexOf(filterText) >= 0;
    });

    if (rows.length === 0) {
      tbody.innerHTML = '<tr><td colspan="4" class="sn-tbody-empty">No matches.</td></tr>';
      return;
    }

    var html = '';
    rows.forEach(function (s) {
      var unit = s.unit || '°C';
      var ucls = unitClass(unit);
      var sev  = unit === '°C' ? tempSeverity(s.value) : '';
      var val  = s.value == null ? '—' : (unit === 'RPM' ? Math.round(s.value) : Number(s.value).toFixed(1));
      var path = sparkPath(hist[s.name]);
      var usedList = used[s.name] || [];
      var usedHtml = usedList.length === 0
        ? '<span class="sn-used-empty">unused</span>'
        : '<div class="sn-used-by">' + usedList.map(function (n) {
            return '<span class="sn-used-tag">' + escapeHTML(n) + '</span>';
          }).join('') + '</div>';
      html += '<tr>'
            +   '<td><div class="sn-name-cell">'
            +     '<span class="sn-name">' + escapeHTML(s.name) + '</span>'
            +     '<span class="sn-source">' + escapeHTML(unit + ' source') + '</span>'
            +   '</div></td>'
            +   '<td><span class="sn-value ' + ucls + ' ' + sev + '">' + val + '<span class="sn-value-unit">' + escapeHTML(unit) + '</span></span></td>'
            +   '<td><svg class="sn-spark ' + ucls + '" viewBox="0 0 200 28" preserveAspectRatio="none"><path d="' + path + '"/></svg></td>'
            +   '<td>' + usedHtml + '</td>'
            + '</tr>';
    });
    tbody.innerHTML = html;
  }

  function renderSummary() {
    if (!sensors) return;
    var temps = sensors.filter(function (s) { return (s.unit || '°C') === '°C' && s.value != null; });
    var volts = sensors.filter(function (s) { return s.unit === 'V'  && s.value != null; });
    var pows  = sensors.filter(function (s) { return s.unit === 'W'  && s.value != null; });

    $('sn-temp-count').textContent = temps.length;
    $('sn-volt-count').textContent = volts.length;

    if (temps.length > 0) {
      var hottest = temps.slice().sort(function (a, b) { return Number(b.value) - Number(a.value); })[0];
      $('sn-hottest-val').textContent = Number(hottest.value).toFixed(1);
      $('sn-hottest-name').textContent = hottest.name;
    } else {
      $('sn-hottest-val').textContent = '—';
      $('sn-hottest-name').textContent = '—';
    }

    var used = buildUsedByMap();
    var usedCount = 0;
    Object.keys(used).forEach(function (k) { if (k) usedCount++; });
    $('sn-used-count').textContent = usedCount;
    var unused = sensors.filter(function (s) { return !(used[s.name] || []).length; }).length;
    $('sn-unused-sub').textContent = unused === 0 ? 'all sensors in use' : unused + ' unused';

    var meta = $('sn-meta');
    if (meta) meta.textContent = (temps.length + volts.length + pows.length) + ' active';
  }

  // ── live ──────────────────────────────────────────────────────────
  function setLive(ok) {
    var d = $('sb-live-dot'), l = $('sb-live-label');
    if (d) d.classList.toggle('is-down', !ok);
    if (l) l.textContent = ok ? 'live' : 'reconnecting…';
  }

  // flattenInventory maps the chip-grouped inventory response into the
  // flat {name, value, unit, used_by} shape the renderer consumes.
  // Sensors with no live reading (Value=0 + the rare zero-reading case
  // is indistinguishable from "no value" on this wire shape, so we use
  // the kind/unit to mark voltage/temp as readable and let the
  // sparkline reflect actual values). Aliases (config-supplied
  // friendly names) win over driver labels when present.
  function flattenInventory(inv) {
    if (!inv || !inv.chips) return [];
    var out = [];
    inv.chips.forEach(function (chip) {
      (chip.sensors || []).forEach(function (s) {
        var name = s.alias || s.label || s.id || '';
        out.push({
          name: name,
          value: s.value,
          unit: kindToUnit(s.kind, s.unit),
          used_by: s.used_by || []
        });
      });
    });
    return out;
  }

  var inDemo = false;
  function poll() {
    fetch('/api/v1/hardware/inventory', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (inv) {
        sensors = flattenInventory(inv);
        renderSummary(); render(); setLive(true);
      })
      .catch(function () { if (!inDemo) { inDemo = true; demo(); } });
  }

  function demo() {
    var t = 0;
    function tick() {
      t++;
      var jitter = function (n, scale) { return n + (Math.random() - 0.5) * scale; };
      sensors = [
        { name: 'CPU package',     value: jitter(50, 3),   unit: '°C', used_by: ['Quiet CPU'] },
        { name: 'CPU core 0',      value: jitter(48, 3),   unit: '°C', used_by: [] },
        { name: 'CPU core 4',      value: jitter(52, 3),   unit: '°C', used_by: [] },
        { name: 'GPU 0', value: jitter(64, 4),  unit: '°C', used_by: ['GPU aware', 'Mix'] },
        { name: 'AIO coolant',     value: jitter(33, 0.5), unit: '°C', used_by: ['AIO pump'] },
        { name: 'Motherboard',     value: jitter(42, 0.6), unit: '°C', used_by: ['Stealth'] },
        { name: 'NVMe 0',          value: jitter(47, 1.5), unit: '°C', used_by: [] },
        { name: 'NVMe 1',          value: jitter(45, 1.2), unit: '°C', used_by: [] },
        { name: 'sda',             value: jitter(36, 0.5), unit: '°C', used_by: [] },
        { name: 'sdb',             value: jitter(38, 0.5), unit: '°C', used_by: [] },
        { name: '+12V rail',       value: jitter(12.05, 0.04), unit: 'V', used_by: [] },
        { name: '+5V rail',        value: jitter(5.02,  0.02), unit: 'V', used_by: [] },
        { name: '+3.3V rail',      value: jitter(3.31,  0.02), unit: 'V', used_by: [] },
        { name: 'CPU power',       value: jitter(140, 12),     unit: 'W', used_by: [] },
        { name: 'GPU power',       value: jitter(180, 18),     unit: 'W', used_by: [] }
      ];
      renderSummary(); render(); setLive(false);
    }
    tick();
    setInterval(tick, 900);
  }

  poll();
  setInterval(poll, 1500);
})();
