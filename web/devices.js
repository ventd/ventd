// devices.js — chip + entity tree fed by /api/v1/hardware.
//
// Each /api/v1/hardware response is an array of monitor.Device, with each
// device containing a flat list of Reading entries. We bucket readings by
// type (fan / temp / voltage / power) into a chip view.

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
  function bus(d) {
    if (d.path && d.path.indexOf('gpu') >= 0)        return 'nvml';
    if (d.readings && d.readings[0] && d.readings[0].sensor_type === 'nvidia') return 'nvml';
    return 'hwmon';
  }
  function chipIcon(name) {
    if (/nvme|drivetemp|sd[a-z]/i.test(name))     return '<svg viewBox="0 0 24 24"><rect x="3" y="6" width="18" height="12" rx="1"/><path d="M7 10h2 M7 14h2 M11 10h6 M11 14h6"/></svg>';
    if (/k10|coretemp|intel|cpu/i.test(name))     return '<svg viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="1"/><path d="M9 9h6v6H9z M2 9h2 M2 14h2 M20 9h2 M20 14h2 M9 2v2 M14 2v2 M9 20v2 M14 20v2"/></svg>';
    if (/nct|it87|f7|fintek|w83/i.test(name))     return '<svg viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="1"/><path d="M9 9h6v6H9z"/></svg>';
    if (/corsair|aio|liquid|h150|nzxt|kraken/i.test(name)) return '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 3"/></svg>';
    if (/nvidia|radeon|amdgpu|gpu/i.test(name))   return '<svg viewBox="0 0 24 24"><rect x="2" y="6" width="20" height="12" rx="1"/><path d="M6 10h2 M6 14h2 M11 10h7 M11 14h7"/></svg>';
    if (/acpi|thermal/i.test(name))               return '<svg viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="1"/></svg>';
    return '<svg viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="1"/></svg>';
  }
  function entityIcon(unit) {
    if (unit === 'RPM') return '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="3"/></svg>';
    if (unit === 'V')   return '<svg viewBox="0 0 24 24"><path d="M13 2L4 14h7l-1 8 9-12h-7z"/></svg>';
    if (unit === 'W')   return '<svg viewBox="0 0 24 24"><path d="M5 12h4l2-7 2 14 2-7h4"/></svg>';
    return '<svg viewBox="0 0 24 24"><path d="M14 4v10a4 4 0 11-4 0V4a2 2 0 014 0z"/></svg>';
  }
  function entityClass(unit) {
    if (unit === 'RPM') return 'is-fan';
    if (unit === '°C')  return 'is-temp';
    if (unit === 'V')   return 'is-volt';
    return '';
  }

  // ── render ──────────────────────────────────────────────────────────
  var data = [];          // last fetched
  var filterText = '';
  var filterBus  = 'all';

  function render() {
    var box = $('dv-chips');
    if (!box) return;
    if (!data || data.length === 0) {
      box.innerHTML = '<div class="chip-empty">No hardware detected. <span class="kbd">/devices/rescan</span> to retry.</div>';
      return;
    }

    var filtered = data.filter(function (d) {
      if (filterBus !== 'all' && bus(d) !== filterBus) return false;
      if (!filterText) return true;
      var t = filterText.toLowerCase();
      if ((d.name || '').toLowerCase().indexOf(t) >= 0) return true;
      if ((d.path || '').toLowerCase().indexOf(t) >= 0) return true;
      for (var i = 0; i < (d.readings || []).length; i++) {
        if ((d.readings[i].label || '').toLowerCase().indexOf(t) >= 0) return true;
      }
      return false;
    });

    if (filtered.length === 0) {
      box.innerHTML = '<div class="chip-empty">No matches for filter.</div>';
      return;
    }

    var html = '';
    filtered.forEach(function (d, idx) {
      var fans   = (d.readings || []).filter(function (r) { return r.unit === 'RPM'; });
      var temps  = (d.readings || []).filter(function (r) { return r.unit === '°C'; });
      var volts  = (d.readings || []).filter(function (r) { return r.unit === 'V'; });
      var powers = (d.readings || []).filter(function (r) { return r.unit === 'W'; });
      var theBus = bus(d);
      var tagCls = theBus === 'nvml' ? 'chip-tag--purple' : '';
      var expanded = idx === 0 ? ' is-expanded' : '';
      var chevAria = idx === 0 ? 'true' : 'false';

      html += '<div class="chip-row' + expanded + '" role="treeitem" tabindex="0" aria-expanded="' + chevAria + '" data-key="' + escapeHTML(d.path || d.name) + '">'
            +   '<span class="chip-row-chev"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 6l6 6-6 6"/></svg></span>'
            +   '<div class="chip-row-name">'
            +     '<div class="chip-row-name-line">'
            +       '<span class="chip-icon">' + chipIcon(d.name || '') + '</span>'
            +       '<span class="chip-name">' + escapeHTML(d.name || '') + '</span>'
            +       '<span class="chip-tag ' + tagCls + '">' + theBus + '</span>'
            +     '</div>'
            +     '<div class="chip-row-path mono">' + escapeHTML(d.path || '') + '</div>'
            +   '</div>'
            +   '<div class="chip-row-stats">'
            +     '<div class="chip-stat"><span class="chip-stat-value mono">' + fans.length  + '</span><span class="chip-stat-label">fans</span></div>'
            +     '<div class="chip-stat"><span class="chip-stat-value mono">' + temps.length + '</span><span class="chip-stat-label">temps</span></div>'
            +     '<div class="chip-stat"><span class="chip-stat-value mono">' + (volts.length + powers.length) + '</span><span class="chip-stat-label">other</span></div>'
            +   '</div>'
            +   '<div class="chip-row-status"><span class="status-pill ' + (theBus === 'nvml' ? 'info' : 'ro') + ' no-dot">' + theBus + '</span></div>'
            + '</div>'
            + '<div class="entities">';

      // sort: fans first, then temps, then volts, then powers
      var sorted = []
        .concat(fans.map(function (r) { return Object.assign({}, r, { _grp: 0 }); }))
        .concat(temps.map(function (r) { return Object.assign({}, r, { _grp: 1 }); }))
        .concat(volts.map(function (r) { return Object.assign({}, r, { _grp: 2 }); }))
        .concat(powers.map(function (r) { return Object.assign({}, r, { _grp: 3 }); }));

      sorted.forEach(function (r) {
        var val = r.value == null ? '—' : (r.unit === 'RPM' ? Math.round(r.value) : Number(r.value).toFixed(1));
        html += '<div class="entity-row ' + entityClass(r.unit) + '">'
              +   '<span class="entity-icon">' + entityIcon(r.unit) + '</span>'
              +   '<span class="entity-name">' + escapeHTML(r.label || '') + '</span>'
              +   '<span class="entity-id mono">' + escapeHTML((r.sensor_path || '').split('/').pop() || '') + '</span>'
              +   '<span class="entity-readout mono">' + val + ' ' + escapeHTML(r.unit || '') + '</span>'
              + '</div>';
      });

      html += '</div>';
    });
    box.innerHTML = html;

    // wire expand
    Array.prototype.forEach.call(box.querySelectorAll('.chip-row'), function (row) {
      row.addEventListener('click', function () {
        var open = row.classList.toggle('is-expanded');
        row.setAttribute('aria-expanded', open ? 'true' : 'false');
      });
      row.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); row.click(); }
      });
    });
  }

  // ── summary ─────────────────────────────────────────────────────────
  function renderSummary() {
    var chips = data.length;
    var fans = 0, temps = 0, volts = 0, hwmonN = 0, nvmlN = 0;
    data.forEach(function (d) {
      (d.readings || []).forEach(function (r) {
        if (r.unit === 'RPM') fans++;
        else if (r.unit === '°C') temps++;
        else if (r.unit === 'V') volts++;
      });
      if (bus(d) === 'nvml') nvmlN++; else hwmonN++;
    });
    var setT = function (id, v) { var el = $(id); if (el) el.textContent = v; };
    setT('sm-chips', chips);
    setT('sm-fans', fans);
    setT('sm-temps', temps);
    setT('sm-volts', volts);
    setT('sm-scan', new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }));
    setT('sm-chips-meta', hwmonN + ' hwmon' + (nvmlN ? ' · ' + nvmlN + ' nvml' : ''));
    setT('sg-all',   chips);
    setT('sg-hwmon', hwmonN);
    setT('sg-nvml',  nvmlN);
    var nav = $('nav-chip-count'); if (nav) { nav.textContent = chips; nav.hidden = chips === 0; }
  }

  // ── filter wiring ────────────────────────────────────────────────────
  var fInput = $('dv-filter');
  if (fInput) fInput.addEventListener('input', function () {
    filterText = fInput.value.trim();
    render();
  });
  Array.prototype.forEach.call(document.querySelectorAll('#dv-segments .filter-segment'), function (b) {
    b.addEventListener('click', function () {
      Array.prototype.forEach.call(document.querySelectorAll('#dv-segments .filter-segment'), function (x) {
        x.classList.remove('is-active');
        x.setAttribute('aria-selected', 'false');
      });
      b.classList.add('is-active');
      b.setAttribute('aria-selected', 'true');
      filterBus = b.dataset.bus;
      render();
    });
  });

  // ── rescan ──────────────────────────────────────────────────────────
  var rescanBtn = $('dv-rescan');
  if (rescanBtn) rescanBtn.addEventListener('click', function () {
    rescanBtn.disabled = true;
    fetch('/api/v1/hardware/rescan', { method: 'POST', credentials: 'same-origin' })
      .then(function () { return load(); })
      .finally(function () { rescanBtn.disabled = false; });
  });

  // ── live dot ─────────────────────────────────────────────────────────
  function setLive(ok) {
    var d = $('sb-live-dot');
    var l = $('sb-live-label');
    if (d) d.classList.toggle('is-down', !ok);
    if (l) l.textContent = ok ? 'live' : 'reconnecting…';
  }

  // ── load ────────────────────────────────────────────────────────────
  var inDemo = false;
  function load() {
    return fetch('/api/v1/hardware', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (d) { data = d || []; renderSummary(); render(); setLive(true); })
      .catch(function () { if (!inDemo) { inDemo = true; loadDemo(); } });
  }

  function loadDemo() {
    data = [
      { name: 'nct6798d', path: '/sys/class/hwmon/hwmon4', readings:
        [
          { label: 'CPU fan',         value: 820,  unit: 'RPM', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon4/fan1_input' },
          { label: 'Front intake top', value: 812, unit: 'RPM', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon4/fan2_input' },
          { label: 'Front intake mid', value: 680, unit: 'RPM', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon4/fan3_input' },
          { label: 'Front intake bot', value: 792, unit: 'RPM', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon4/fan4_input' },
          { label: 'Rear exhaust',    value: 1480, unit: 'RPM', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon4/fan5_input' },
          { label: 'Top exhaust 1',   value: 906,  unit: 'RPM', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon4/fan6_input' },
          { label: 'Top exhaust 2',   value: 920,  unit: 'RPM', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon4/fan7_input' },
          { label: 'CPU package',     value: 52,   unit: '°C',  sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon4/temp1_input' },
          { label: 'PCH',             value: 47,   unit: '°C',  sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon4/temp2_input' },
          { label: 'VRM',             value: 56,   unit: '°C',  sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon4/temp3_input' },
          { label: '+12V',            value: 12.05, unit: 'V',  sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon4/in0_input' },
          { label: '+5V',             value: 5.02,  unit: 'V',  sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon4/in1_input' }
        ]
      },
      { name: 'k10temp', path: '/sys/class/hwmon/hwmon0', readings:
        [
          { label: 'Tctl', value: 50, unit: '°C', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon0/temp1_input' },
          { label: 'Tdie', value: 49, unit: '°C', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon0/temp2_input' },
          { label: 'CCD1', value: 48, unit: '°C', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon0/temp3_input' }
        ]
      },
      { name: 'nvme', path: '/sys/class/hwmon/hwmon2', readings:
        [
          { label: 'Composite', value: 42, unit: '°C', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon2/temp1_input' },
          { label: 'Sensor 1', value: 40, unit: '°C', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon2/temp2_input' }
        ]
      },
      { name: 'drivetemp', path: '/sys/class/hwmon/hwmon5', readings:
        [
          { label: 'sda', value: 36, unit: '°C', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon5/temp1_input' },
          { label: 'sdb', value: 38, unit: '°C', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon5/temp2_input' }
        ]
      },
      { name: 'acpitz', path: '/sys/class/hwmon/hwmon1', readings:
        [
          { label: 'Thermal zone 0', value: 42, unit: '°C', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon1/temp1_input' },
          { label: 'Thermal zone 1', value: 38, unit: '°C', sensor_type: 'hwmon', sensor_path: '/sys/class/hwmon/hwmon1/temp2_input' }
        ]
      },
      { name: 'NVIDIA GPU 0', path: 'gpu0', readings:
        [
          { label: 'GPU 0 fan 0', value: 1340, unit: 'RPM', sensor_type: 'nvidia', sensor_path: 'gpu0/fan0' },
          { label: 'GPU 0 fan 1', value: 1352, unit: 'RPM', sensor_type: 'nvidia', sensor_path: 'gpu0/fan1' },
          { label: 'GPU 0 die',   value: 61,   unit: '°C',  sensor_type: 'nvidia', sensor_path: 'gpu0/temp' }
        ]
      }
    ];
    renderSummary(); render(); setLive(false);
  }

  load();
  setInterval(load, 5000);
})();
