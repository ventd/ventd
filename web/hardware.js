// hardware.js — combined Devices + Sensors page, vanilla DOM port.
//
// Polls /api/v1/hardware/inventory every 1500ms (matches devices.js
// cadence) and renders three views: Inventory / Topology / Heatmap.
// Rendering is plain DOM + SVG construction inside an IIFE; no
// frameworks, no transpilation, no external CDN. RULE-UI-01 +
// RULE-UI-02.
//
// Backend contract — see comment at top of /api/v1/hardware/inventory:
//   { chips: [{id,bus,name,path,model,status, sensors: [...]}], curves: [...] }
//
// Affordances intentionally OMITTED from the port (design-prototype
// only, no backend signal):
//   • workload simulation selector
//   • "simulate offline chip" toggle
//   • tweaks side panel

(function () {
  'use strict';

  // ── theme toggle (matches devices.js / calibration.js) ──────────
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

  // ── helpers ────────────────────────────────────────────────────
  var SVG_NS = 'http://www.w3.org/2000/svg';
  function $(id) { return document.getElementById(id); }
  function escapeHTML(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }
  function el(tag, opts) {
    var n = document.createElement(tag);
    if (!opts) return n;
    if (opts.cls) n.className = opts.cls;
    if (opts.text != null) n.textContent = String(opts.text);
    if (opts.html != null) n.innerHTML = opts.html;
    if (opts.attrs) {
      for (var k in opts.attrs) {
        if (Object.prototype.hasOwnProperty.call(opts.attrs, k)) {
          n.setAttribute(k, opts.attrs[k]);
        }
      }
    }
    if (opts.style) {
      for (var s in opts.style) {
        if (Object.prototype.hasOwnProperty.call(opts.style, s)) {
          n.style[s] = opts.style[s];
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

  function unitFor(s) {
    if (s.unit) return s.unit;
    if (s.kind === 'temp')  return '°C';
    if (s.kind === 'volt')  return 'V';
    if (s.kind === 'power') return 'W';
    return '';
  }
  function fmt(v, kind) {
    if (v == null || isNaN(v)) return '—';
    if (kind === 'temp')  return Number(v).toFixed(1);
    if (kind === 'fan')   return String(Math.round(v));
    if (kind === 'volt')  return Number(v).toFixed(2);
    if (kind === 'power') return String(Math.round(v));
    return Number(v).toFixed(1);
  }

  // ── state ───────────────────────────────────────────────────────
  var state = {
    inventory: null,            // last successful inventory payload
    error: null,                // string message for last poll failure
    view: 'inventory',
    query: '',
    kindFilter: 'all',
    openIds: null,              // Set of expanded chip ids
    selectedId: null,
    pulseTracker: {},           // sensorID -> last value snapshot
    pulseAt: {},                // sensorID -> timestamp of last pulse
  };

  function ensureOpenIdsSeeded() {
    if (state.openIds) return;
    state.openIds = {};
    if (!state.inventory) return;
    var chips = state.inventory.chips || [];
    // Default-open the first 3 chips so the UI feels alive on first paint.
    for (var i = 0; i < Math.min(3, chips.length); i++) {
      state.openIds[chips[i].id] = true;
    }
  }
  function ensureSelectedSeeded() {
    if (state.selectedId) return;
    if (!state.inventory) return;
    var chips = state.inventory.chips || [];
    for (var i = 0; i < chips.length; i++) {
      var sensors = chips[i].sensors || [];
      for (var j = 0; j < sensors.length; j++) {
        state.selectedId = sensors[j].id;
        return;
      }
    }
  }

  function findSensor(id) {
    if (!state.inventory) return null;
    var chips = state.inventory.chips || [];
    for (var i = 0; i < chips.length; i++) {
      var sensors = chips[i].sensors || [];
      for (var j = 0; j < sensors.length; j++) {
        if (sensors[j].id === id) return { chip: chips[i], sensor: sensors[j] };
      }
    }
    return null;
  }

  // pulseAt is a "value moved noticeably" indicator that drives the
  // .is-pulsing flash. Threshold is roughly 5% of the last value to
  // avoid flooding the UI on every poll.
  function trackPulses() {
    if (!state.inventory) return;
    var chips = state.inventory.chips || [];
    var now = Date.now();
    chips.forEach(function (c) {
      (c.sensors || []).forEach(function (s) {
        var prev = state.pulseTracker[s.id];
        if (prev == null) { state.pulseTracker[s.id] = s.value; return; }
        if (Math.abs(s.value - prev) > Math.max(0.5, Math.abs(prev) * 0.05)) {
          state.pulseAt[s.id] = now;
        }
        state.pulseTracker[s.id] = s.value;
      });
    });
  }

  // ── view-switcher (mounted into the topbar slot) ────────────────
  var VIEWS = [
    { id: 'inventory', label: 'Inventory', icon: function () {
        var g = svgEl('g');
        g.appendChild(svgEl('path', { d: 'M4 6h16M4 12h16M4 18h16' }));
        return g;
      }
    },
    { id: 'topology', label: 'Topology', icon: function () {
        var g = svgEl('g');
        g.appendChild(svgEl('circle', { cx: '6',  cy: '12', r: '2' }));
        g.appendChild(svgEl('circle', { cx: '18', cy: '6',  r: '2' }));
        g.appendChild(svgEl('circle', { cx: '18', cy: '18', r: '2' }));
        g.appendChild(svgEl('path',   { d: 'M8 12l8-6M8 12l8 6' }));
        return g;
      }
    },
    { id: 'heatmap', label: 'Heatmap', icon: function () {
        var g = svgEl('g');
        g.appendChild(svgEl('rect',   { x: '3', y: '3', width: '18', height: '18', rx: '2' }));
        g.appendChild(svgEl('circle', { cx: '9',  cy: '10', r: '2' }));
        g.appendChild(svgEl('circle', { cx: '15', cy: '14', r: '2' }));
        return g;
      }
    }
  ];
  function renderViewSwitcher() {
    var slot = $('hw-views-mount');
    if (!slot) return;
    clearChildren(slot);
    var bar = el('div', { cls: 'hw-views', attrs: { role: 'tablist' } });
    VIEWS.forEach(function (v) {
      var btn = el('button', {
        cls: 'hw-view-btn' + (state.view === v.id ? ' active' : ''),
        attrs: { type: 'button', role: 'tab', 'aria-selected': state.view === v.id ? 'true' : 'false' }
      });
      var svg = svgEl('svg', { viewBox: '0 0 24 24', 'aria-hidden': 'true' });
      svg.appendChild(v.icon());
      btn.appendChild(svg);
      btn.appendChild(el('span', { text: v.label }));
      btn.addEventListener('click', function () {
        if (state.view === v.id) return;
        state.view = v.id;
        var cr = $('hw-crumb-current');
        if (cr) cr.textContent = v.label;
        renderViewSwitcher();
        renderBody();
      });
      bar.appendChild(btn);
    });
    slot.appendChild(bar);
  }

  // ── summary cards strip ─────────────────────────────────────────
  function renderSummaryCards(inv, container) {
    var allSensors = [];
    (inv.chips || []).forEach(function (c) {
      (c.sensors || []).forEach(function (s) { allSensors.push(s); });
    });
    var fans = allSensors.filter(function (s) { return s.kind === 'fan'; });
    var temps = allSensors.filter(function (s) { return s.kind === 'temp'; });
    var volts = allSensors.filter(function (s) { return s.kind === 'volt'; });
    var spinning = fans.filter(function (s) { return (s.value || 0) > 200; }).length;
    var aliased  = allSensors.filter(function (s) { return s.alias; }).length;
    var inCurves = allSensors.filter(function (s) { return s.used_by && s.used_by.length; }).length;

    var hottest = null;
    for (var i = 0; i < temps.length; i++) {
      if (!hottest || (temps[i].value || -Infinity) > (hottest.value || -Infinity)) hottest = temps[i];
    }

    var avgFanRpm = 0;
    if (fans.length > 0) {
      var sum = 0;
      fans.forEach(function (s) { sum += (s.value || 0); });
      avgFanRpm = Math.round(sum / fans.length);
    }

    var totalChips = (inv.chips || []).length;
    var offlineChips = (inv.chips || []).filter(function (c) { return c.status === 'offline'; }).length;
    var liveChips = totalChips - offlineChips;

    var grid = el('div', { cls: 'hw-summary' });

    // Card 1 — Chips (with live heartbeat)
    var c1 = el('div', { cls: 'hw-summary-card' });
    var hb = el('div', { cls: 'heartbeat' });
    hb.appendChild(el('span', { cls: 'heartbeat-dot' }));
    hb.appendChild(el('span', { text: 'live' }));
    c1.appendChild(hb);
    c1.appendChild(el('div', { cls: 'hw-summary-eyebrow', text: 'Chips' }));
    c1.appendChild(el('div', { cls: 'hw-summary-value', text: String(liveChips) }));
    var c1sub = totalChips + ' discovered' + (offlineChips ? ' · ' + offlineChips + ' offline' : '');
    c1.appendChild(el('div', { cls: 'hw-summary-sub', text: c1sub }));
    grid.appendChild(c1);

    // Card 2 — Sensors
    var c2 = el('div', { cls: 'hw-summary-card' });
    c2.appendChild(el('div', { cls: 'hw-summary-eyebrow', text: 'Sensors' }));
    c2.appendChild(el('div', { cls: 'hw-summary-value', text: String(allSensors.length) }));
    c2.appendChild(el('div', { cls: 'hw-summary-sub', text: aliased + ' aliased · ' + inCurves + ' in curves' }));
    grid.appendChild(c2);

    // Card 3 — Fans spinning
    var c3 = el('div', { cls: 'hw-summary-card' });
    c3.appendChild(el('div', { cls: 'hw-summary-eyebrow', text: 'Fans spinning' }));
    var c3val = el('div', { cls: 'hw-summary-value', text: String(spinning) });
    var c3suf = el('span', { cls: 'unit-suffix', text: ' / ' + fans.length });
    c3val.appendChild(c3suf);
    c3.appendChild(c3val);
    c3.appendChild(el('div', { cls: 'hw-summary-sub', text: fans.length ? 'avg ' + avgFanRpm + ' rpm' : 'no fans detected' }));
    grid.appendChild(c3);

    // Card 4 — Hottest
    var c4 = el('div', { cls: 'hw-summary-card' });
    c4.appendChild(el('div', { cls: 'hw-summary-eyebrow', text: 'Hottest' }));
    if (hottest && hottest.value != null) {
      var c4val = el('div', { cls: 'hw-summary-value is-hot', text: Math.round(hottest.value) + '°' });
      var c4suf = el('span', { cls: 'unit-suffix', text: ' C' });
      c4val.appendChild(c4suf);
      c4.appendChild(c4val);
      c4.appendChild(el('div', { cls: 'hw-summary-sub mono', text: hottest.alias || hottest.id }));
    } else {
      c4.appendChild(el('div', { cls: 'hw-summary-value', text: '—' }));
      c4.appendChild(el('div', { cls: 'hw-summary-sub', text: 'no temperature sensors' }));
    }
    grid.appendChild(c4);

    // Card 5 — Voltages
    var c5 = el('div', { cls: 'hw-summary-card' });
    c5.appendChild(el('div', { cls: 'hw-summary-eyebrow', text: 'Voltages' }));
    var c5val = el('div', { cls: 'hw-summary-value', text: String(volts.length) });
    var c5suf = el('span', { cls: 'unit-suffix', text: ' rails' });
    c5val.appendChild(c5suf);
    c5.appendChild(c5val);
    c5.appendChild(el('div', { cls: 'hw-summary-sub', text: volts.length ? 'monitored' : 'none reported' }));
    grid.appendChild(c5);

    container.appendChild(grid);
  }

  // ── sparkline (SVG) ─────────────────────────────────────────────
  function makeSparkline(history) {
    var W = 220, H = 24;
    var svg = svgEl('svg', {
      'class': 'hw-sensor-spark',
      viewBox: '0 0 ' + W + ' ' + H,
      preserveAspectRatio: 'none',
      'aria-hidden': 'true'
    });
    if (!history || history.length === 0) return svg;
    var min = Infinity, max = -Infinity;
    for (var i = 0; i < history.length; i++) {
      if (history[i] < min) min = history[i];
      if (history[i] > max) max = history[i];
    }
    min -= 0.5; max += 0.5;
    var range = Math.max(0.01, max - min);
    var d = '';
    for (var k = 0; k < history.length; k++) {
      var x = (k / Math.max(1, history.length - 1)) * W;
      var y = H - ((history[k] - min) / range) * (H - 4) - 2;
      d += (k ? 'L' : 'M') + x.toFixed(1) + ',' + y.toFixed(1) + ' ';
    }
    svg.appendChild(svgEl('path', { d: d.trim() }));
    var lastY = H - ((history[history.length - 1] - min) / range) * (H - 4) - 2;
    svg.appendChild(svgEl('circle', { 'class': 'now', cx: String(W), cy: lastY.toFixed(1), r: '2' }));
    return svg;
  }

  // ── sensor-kind icon ───────────────────────────────────────────
  function makeSensorIcon(kind) {
    var svg = svgEl('svg', { 'class': 'hw-sensor-icon', viewBox: '0 0 24 24', 'aria-hidden': 'true' });
    if (kind === 'fan') {
      svg.appendChild(svgEl('circle', { cx: '12', cy: '12', r: '2' }));
      svg.appendChild(svgEl('path', { d: 'M12 2C9 6 9 9 12 12M12 22c3-4 3-7 0-10M2 12c4-3 7-3 10 0M22 12c-4 3-7 3-10 0' }));
    } else if (kind === 'volt') {
      svg.appendChild(svgEl('path', { d: 'M13 2L3 14h8l-2 8 10-12h-8l2-8z' }));
    } else if (kind === 'power') {
      svg.appendChild(svgEl('circle', { cx: '12', cy: '12', r: '9' }));
      svg.appendChild(svgEl('path', { d: 'M12 7v5l3 2' }));
    } else { // temp default
      svg.appendChild(svgEl('path', { d: 'M14 4v10a4 4 0 11-4 0V4a2 2 0 014 0z' }));
      svg.appendChild(svgEl('circle', { cx: '12', cy: '16', r: '1.5', fill: 'currentColor' }));
    }
    return svg;
  }

  // ── chip node (collapsible) ─────────────────────────────────────
  function renderChipNode(chip, container) {
    var sensors = chip.sensors || [];
    var fans  = sensors.filter(function (s) { return s.kind === 'fan'; }).length;
    var temps = sensors.filter(function (s) { return s.kind === 'temp'; }).length;
    var others = sensors.length - fans - temps;
    var query = state.query.toLowerCase();
    var filtered = sensors.filter(function (s) {
      if (state.kindFilter !== 'all' && s.kind !== state.kindFilter) return false;
      if (!query) return true;
      return ((s.alias || '').toLowerCase().indexOf(query) >= 0)
          || ((s.label || '').toLowerCase().indexOf(query) >= 0)
          || ((s.id || '').toLowerCase().indexOf(query) >= 0)
          || ((chip.name || '').toLowerCase().indexOf(query) >= 0);
    });
    if (query && filtered.length === 0) return;

    var open = !!state.openIds[chip.id];
    var isDown = chip.status === 'offline';
    var peakTemp = sensors
      .filter(function (s) { return s.kind === 'temp'; })
      .reduce(function (m, s) { return Math.max(m, s.value || 0); }, 0);
    var isHot = peakTemp > 80;

    var card = el('div', {
      cls: 'hw-chip' + (open ? ' is-open' : '') + (isHot ? ' is-hot' : '') + (isDown ? ' is-down' : '')
    });

    var head = el('div', { cls: 'hw-chip-head' });
    var toggleSvg = svgEl('svg', { 'class': 'hw-chip-toggle-icon', viewBox: '0 0 24 24', 'aria-hidden': 'true' });
    toggleSvg.appendChild(svgEl('path', { d: 'M9 6l6 6-6 6' }));
    head.appendChild(toggleSvg);

    var meta = el('div', { cls: 'hw-chip-meta' });
    var title = el('div', { cls: 'hw-chip-title' });
    title.appendChild(el('span', { cls: 'hw-chip-name', text: chip.name || chip.id }));
    title.appendChild(el('span', { cls: 'hw-chip-bus ' + (chip.bus || ''), text: chip.bus || '' }));
    if (isDown) title.appendChild(el('span', { cls: 'status-pill err', text: 'offline' }));
    meta.appendChild(title);
    meta.appendChild(el('div', {
      cls: 'hw-chip-path',
      text: (chip.path || '') + (chip.model ? ' · ' + chip.model : '')
    }));
    head.appendChild(meta);

    var counts = el('div', { cls: 'hw-chip-counts' });
    function addCount(kind, n, label) {
      var c = el('div', { cls: 'hw-chip-count ' + kind });
      c.appendChild(el('span', { cls: 'hw-chip-count-val' + (n ? ' has' : ''), text: String(n) }));
      c.appendChild(el('span', { cls: 'hw-chip-count-lab', text: label }));
      return c;
    }
    counts.appendChild(addCount('fans',  fans,   'fans'));
    counts.appendChild(addCount('temps', temps,  'temps'));
    counts.appendChild(addCount('other', others, 'other'));
    head.appendChild(counts);

    var ecg = svgEl('svg', { 'class': 'hw-chip-heartbeat', viewBox: '0 0 56 22', 'aria-hidden': 'true' });
    ecg.appendChild(svgEl('path', { d: 'M2,11 L12,11 L16,4 L20,18 L24,11 L34,11 L38,7 L42,15 L46,11 L54,11' }));
    head.appendChild(ecg);

    head.addEventListener('click', function () {
      if (state.openIds[chip.id]) delete state.openIds[chip.id];
      else state.openIds[chip.id] = true;
      renderBody();
    });
    card.appendChild(head);

    var body = el('div', { cls: 'hw-chip-body' });
    filtered.forEach(function (s) {
      var pulse = state.pulseAt[s.id] && (Date.now() - state.pulseAt[s.id] < 800);
      var isStale = isDown;
      var isZero  = s.kind === 'fan' && (s.value || 0) < 200;
      var row = el('div', {
        cls: 'hw-sensor kind-' + s.kind
          + (state.selectedId === s.id ? ' is-selected' : '')
          + (isStale ? ' is-stale' : '')
          + (isZero ? ' is-zero' : '')
      });
      row.appendChild(makeSensorIcon(s.kind));

      var name = el('div', { cls: 'hw-sensor-name' });
      name.appendChild(document.createTextNode(s.label || s.id));
      if (s.alias) {
        var alias = el('span', { cls: 'alias', text: '→ ' + s.alias });
        name.appendChild(alias);
      }
      row.appendChild(name);

      row.appendChild(makeSparkline(s.history || []));

      // Spacer column to balance the grid.
      row.appendChild(el('div'));

      var val = el('div', { cls: 'hw-sensor-value' + (pulse ? ' is-pulsing' : '') });
      val.appendChild(document.createTextNode(fmt(s.value, s.kind)));
      val.appendChild(el('span', { cls: 'unit', text: unitFor(s) }));
      row.appendChild(val);

      var uses = el('div', { cls: 'hw-sensor-uses' });
      if (s.used_by && s.used_by.length) {
        s.used_by.forEach(function (cid) {
          uses.appendChild(el('span', { cls: 'pill', text: String(cid).replace('_curve', '') }));
        });
      } else {
        uses.appendChild(el('span', { cls: 'pill muted', text: 'unused' }));
      }
      row.appendChild(uses);

      row.appendChild(el('div', { cls: 'hw-sensor-status' }));

      row.addEventListener('click', function (e) {
        e.stopPropagation();
        state.selectedId = s.id;
        renderBody();
      });
      body.appendChild(row);
    });
    card.appendChild(body);

    container.appendChild(card);
  }

  // ── inventory side-rail (selected sensor + coupling mini) ───────
  function renderInventoryRail(container) {
    var rail = el('div', { cls: 'hw-inv-rail' });

    if (!state.selectedId) {
      var empty = el('div', { cls: 'hw-detail' });
      var inner = el('div', { cls: 'hw-detail-empty' });
      var s = svgEl('svg', { viewBox: '0 0 24 24', 'aria-hidden': 'true' });
      s.appendChild(svgEl('circle', { cx: '12', cy: '12', r: '9' }));
      s.appendChild(svgEl('path', { d: 'M12 8v4M12 16h0' }));
      inner.appendChild(s);
      inner.appendChild(el('div', { text: "Select a sensor to see how it's wired into curves and fans." }));
      empty.appendChild(inner);
      rail.appendChild(empty);
      container.appendChild(rail);
      return;
    }

    var sel = findSensor(state.selectedId);
    if (!sel) {
      // Selected sensor disappeared between polls; clear and re-render.
      state.selectedId = null;
      ensureSelectedSeeded();
      container.appendChild(rail);
      return;
    }
    var chip = sel.chip;
    var sensor = sel.sensor;
    var history = sensor.history || [];
    var minHistory = history.length ? Math.min.apply(null, history) : sensor.value;
    var maxHistory = history.length ? Math.max.apply(null, history) : sensor.value;

    // First card — selected-sensor details
    var detail = el('div', { cls: 'hw-detail' });
    detail.appendChild(el('div', { cls: 'hw-detail-eyebrow', text: 'Selected sensor' }));
    detail.appendChild(el('div', { cls: 'hw-detail-title', text: sensor.alias || sensor.label || sensor.id }));
    detail.appendChild(el('div', { cls: 'hw-detail-sub', text: (chip.name || chip.id) + ' · ' + (sensor.label || sensor.id) }));

    function row(key, val, isPath) {
      var r = el('div', { cls: 'hw-detail-row' });
      r.appendChild(el('span', { cls: 'hw-detail-key', text: key }));
      r.appendChild(el('span', { cls: 'hw-detail-val' + (isPath ? ' path' : ''), text: val }));
      return r;
    }
    detail.appendChild(row('Now', fmt(sensor.value, sensor.kind) + ' ' + unitFor(sensor)));
    if (history.length) {
      detail.appendChild(row('Min · Max',
        Number(minHistory).toFixed(1) + ' · ' + Number(maxHistory).toFixed(1)));
    }
    detail.appendChild(row('Kind', sensor.kind));
    detail.appendChild(row('Bus', chip.bus || ''));
    detail.appendChild(row('Path', (chip.path || '') + '/' + (sensor.label || ''), true));
    rail.appendChild(detail);

    // Second card — coupling
    var coupling = el('div', { cls: 'hw-detail' });
    coupling.appendChild(el('div', { cls: 'hw-detail-eyebrow', text: 'Coupling' }));

    var allCurves = (state.inventory && state.inventory.curves) || [];
    var consuming = allCurves.filter(function (c) {
      return (sensor.used_by || []).indexOf(c.id) >= 0;
    });
    var drivenFans = [];
    consuming.forEach(function (c) {
      (c.drives || []).forEach(function (f) { if (drivenFans.indexOf(f) < 0) drivenFans.push(f); });
    });
    if (consuming.length === 0) {
      coupling.appendChild(el('div', {
        cls: 'hw-detail-sub',
        text: 'Not consumed by any curve. This sensor is reported but not used for control.',
        style: { marginBottom: '0' }
      }));
    } else {
      var sub = el('div', { cls: 'hw-detail-sub' });
      sub.innerHTML = 'Feeds <strong>' + consuming.length + '</strong> curve · drives <strong>'
        + drivenFans.length + '</strong> fans';
      coupling.appendChild(sub);
      coupling.appendChild(makeCouplingMini(sensor, consuming));
    }
    rail.appendChild(coupling);

    container.appendChild(rail);
  }

  // Mini SVG sensor → curve → fan diagram with packet flows.
  function makeCouplingMini(sensor, curves) {
    var W = 280, H = 90;
    var sx = 24, cx = W / 2, fx = W - 24;
    var fans = [];
    curves.forEach(function (c) {
      (c.drives || []).forEach(function (f) { if (fans.indexOf(f) < 0) fans.push(f); });
    });

    var svg = svgEl('svg', {
      'class': 'hw-coupling-mini',
      viewBox: '0 0 ' + W + ' ' + H,
      preserveAspectRatio: 'xMidYMid meet'
    });

    // Sensor → curve edges
    curves.forEach(function (c, i) {
      var cy = H / 2 + (i - (curves.length - 1) / 2) * 22;
      var d = 'M' + (sx + 8) + ',' + (H/2)
            + ' C' + ((sx + cx)/2) + ',' + (H/2)
            + ' ' + ((sx + cx)/2) + ',' + cy
            + ' ' + (cx - 8) + ',' + cy;
      var g = svgEl('g');
      g.appendChild(svgEl('path', { 'class': 'edge', d: d }));
      var p = svgEl('circle', { 'class': 'pulse', r: '2' });
      var anim = svgEl('animateMotion', { dur: '2.4s', repeatCount: 'indefinite', path: d });
      p.appendChild(anim);
      g.appendChild(p);
      svg.appendChild(g);
    });

    // Curve → fan edges
    curves.forEach(function (c, i) {
      var cy = H / 2 + (i - (curves.length - 1) / 2) * 22;
      (c.drives || []).forEach(function (fid, j) {
        var idx = fans.indexOf(fid);
        var fyy = (idx + 0.5) / Math.max(1, fans.length) * H;
        var d = 'M' + (cx + 8) + ',' + cy
              + ' C' + ((cx + fx)/2) + ',' + cy
              + ' ' + ((cx + fx)/2) + ',' + fyy
              + ' ' + (fx - 8) + ',' + fyy;
        var g = svgEl('g');
        g.appendChild(svgEl('path', { 'class': 'edge', d: d }));
        var p = svgEl('circle', { 'class': 'pulse', r: '2' });
        var anim = svgEl('animateMotion', {
          dur: '2.8s', begin: (j * 0.2) + 's',
          repeatCount: 'indefinite', path: d
        });
        p.appendChild(anim);
        g.appendChild(p);
        svg.appendChild(g);
      });
    });

    // Sensor node
    var sg = svgEl('g');
    sg.appendChild(svgEl('rect', { 'class': 'node sensor', x: String(sx-16), y: String(H/2-9), width: '32', height: '18', rx: '4' }));
    sg.appendChild(svgEl('text', { 'class': 'label', x: String(sx), y: String(H/2+3) })).textContent =
      String(sensor.alias || sensor.label || sensor.id).slice(0, 7);
    svg.appendChild(sg);

    // Curve nodes
    curves.forEach(function (c, i) {
      var cy = H / 2 + (i - (curves.length - 1) / 2) * 22;
      var g = svgEl('g');
      g.appendChild(svgEl('rect', { 'class': 'node curve', x: String(cx-22), y: String(cy-9), width: '44', height: '18', rx: '4' }));
      var t = svgEl('text', { 'class': 'label', x: String(cx), y: String(cy+3) });
      t.textContent = String(c.name || c.id).replace(' curve', '');
      g.appendChild(t);
      svg.appendChild(g);
    });

    // Fan nodes
    fans.forEach(function (fid, j) {
      var fy = (j + 0.5) / Math.max(1, fans.length) * H;
      var g = svgEl('g');
      g.appendChild(svgEl('rect', { 'class': 'node fan', x: String(fx-22), y: String(fy-7), width: '44', height: '14', rx: '3' }));
      var t = svgEl('text', { 'class': 'label', x: String(fx), y: String(fy+3) });
      t.textContent = String(fid).replace('fan_', '').replace('gpu_', '');
      g.appendChild(t);
      svg.appendChild(g);
    });
    return svg;
  }

  // ── inventory view ──────────────────────────────────────────────
  function renderInventoryView(inv, container) {
    // Filter row: search + kind toggles
    var fr = el('div', { cls: 'hw-filter-row' });
    var search = el('div', { cls: 'hw-search' });
    var sIcon = svgEl('svg', { viewBox: '0 0 24 24', 'aria-hidden': 'true' });
    sIcon.appendChild(svgEl('circle', { cx: '11', cy: '11', r: '7' }));
    sIcon.appendChild(svgEl('path', { d: 'M21 21l-4.3-4.3' }));
    search.appendChild(sIcon);
    var input = el('input', {
      attrs: {
        type: 'text',
        placeholder: 'Filter chips, sensors, paths…',
        autocomplete: 'off',
        spellcheck: 'false',
        'aria-label': 'Filter sensors'
      }
    });
    input.value = state.query;
    input.addEventListener('input', function () {
      state.query = input.value;
      // Re-render only the chip list area to preserve focus on the input.
      var chipsBox = $('hw-chips-box');
      if (chipsBox) renderChipList(inv, chipsBox);
    });
    search.appendChild(input);
    fr.appendChild(search);

    var toggles = el('div', { cls: 'hw-chip-toggles' });
    ['all', 'temp', 'fan', 'volt', 'power'].forEach(function (k) {
      var b = el('button', {
        cls: 'hw-chip-toggle' + (state.kindFilter === k ? ' active' : ''),
        attrs: { type: 'button' }, text: k
      });
      b.addEventListener('click', function () {
        state.kindFilter = k;
        renderBody();
      });
      toggles.appendChild(b);
    });
    fr.appendChild(toggles);
    container.appendChild(fr);

    // Two-column body: chip list + side rail
    var inv2 = el('div', { cls: 'hw-inv' });
    var chipsBox = el('div', { cls: 'hw-chips', attrs: { id: 'hw-chips-box' } });
    renderChipList(inv, chipsBox);
    inv2.appendChild(chipsBox);
    renderInventoryRail(inv2);
    container.appendChild(inv2);
  }

  function renderChipList(inv, container) {
    clearChildren(container);
    var chips = inv.chips || [];
    if (chips.length === 0) {
      var emp = el('div', { cls: 'hw-empty', text: 'No chips detected — try Rescan.' });
      container.appendChild(emp);
      return;
    }
    chips.forEach(function (c) { renderChipNode(c, container); });
  }

  // ── topology view ──────────────────────────────────────────────
  function renderTopologyView(inv, container) {
    var chips = inv.chips || [];
    var wrap = el('div', { cls: 'hw-topo' });
    var W = 1080, H = 600;
    var svg = svgEl('svg', {
      'class': 'hw-topo-svg',
      viewBox: '0 0 ' + W + ' ' + H,
      preserveAspectRatio: 'xMidYMid meet'
    });

    var daemonX = W / 2, daemonY = 80, chipY = 280;
    var chipXs = [];
    if (chips.length === 1) {
      chipXs.push(W / 2);
    } else {
      for (var i = 0; i < chips.length; i++) {
        chipXs.push(90 + i * ((W - 180) / Math.max(1, chips.length - 1)));
      }
    }

    // daemon→chip lines + packets
    chips.forEach(function (chip, i) {
      var cxn = chipXs[i];
      var d = 'M' + daemonX + ',' + (daemonY + 26)
            + ' C' + daemonX + ',' + (daemonY + 100)
            + ' ' + cxn + ',' + (chipY - 80)
            + ' ' + cxn + ',' + (chipY - 22);
      var isDown = chip.status === 'offline';
      var g = svgEl('g');
      g.appendChild(svgEl('path', { 'class': 'bus-line' + (!isDown ? ' active' : ''), d: d }));
      if (!isDown) {
        var pktCls = 'packet' + (chip.bus === 'nvml' ? ' gpu' : '');
        var p = svgEl('circle', { 'class': pktCls, r: '2.5' });
        var a = svgEl('animateMotion', {
          dur: (1.4 + (i % 3) * 0.3) + 's',
          repeatCount: 'indefinite',
          path: d, keyPoints: '1;0', keyTimes: '0;1', calcMode: 'linear'
        });
        p.appendChild(a);
        g.appendChild(p);
      }
      svg.appendChild(g);
    });

    // Daemon glow + box
    var glow = svgEl('circle', { 'class': 'daemon-glow', cx: String(daemonX), cy: String(daemonY), r: '50' });
    var glowAnim = svgEl('animate', { attributeName: 'r', values: '50;58;50', dur: '2.4s', repeatCount: 'indefinite' });
    glow.appendChild(glowAnim);
    svg.appendChild(glow);
    svg.appendChild(svgEl('rect', {
      'class': 'daemon',
      x: String(daemonX - 90), y: String(daemonY - 26),
      width: '180', height: '52', rx: '10'
    }));
    var dn = svgEl('text', { 'class': 'label bold', x: String(daemonX), y: String(daemonY - 4), 'text-anchor': 'middle' });
    dn.textContent = 'ventd · daemon';
    svg.appendChild(dn);
    var dt = svgEl('text', { 'class': 'label tag', x: String(daemonX), y: String(daemonY + 14), 'text-anchor': 'middle' });
    dt.textContent = '/var/run/ventd.sock';
    svg.appendChild(dt);

    // Chip + sensor lollipops
    chips.forEach(function (chip, i) {
      var cxn = chipXs[i];
      var isDown = chip.status === 'offline';
      var sensors = (chip.sensors || []).slice(0, 5);
      var g = svgEl('g');
      g.appendChild(svgEl('rect', {
        'class': 'chip-node ' + (chip.bus || ''),
        x: String(cxn - 70), y: String(chipY - 22),
        width: '140', height: '44', rx: '8'
      }));
      var nm = svgEl('text', { 'class': 'label bold', x: String(cxn), y: String(chipY - 5), 'text-anchor': 'middle' });
      nm.textContent = chip.name || chip.id;
      g.appendChild(nm);
      var tg = svgEl('text', { 'class': 'label tag', x: String(cxn), y: String(chipY + 11), 'text-anchor': 'middle' });
      tg.textContent = (chip.bus || '') + ' · ' + chip.id;
      g.appendChild(tg);
      if (isDown) {
        var off = svgEl('text', {
          'class': 'label offline',
          x: String(cxn), y: String(chipY + 38), 'text-anchor': 'middle'
        });
        off.textContent = '· offline ·';
        g.appendChild(off);
      } else {
        sensors.forEach(function (s, j) {
          var sxn = cxn + (j - (sensors.length - 1) / 2) * 60;
          var syn = chipY + 130;
          var d = 'M' + cxn + ',' + (chipY + 22)
                + ' L' + cxn + ',' + (chipY + 60)
                + ' L' + sxn + ',' + (chipY + 80)
                + ' L' + sxn + ',' + (syn - 10);
          var sg = svgEl('g');
          sg.appendChild(svgEl('path', { 'class': 'bus-line active', d: d }));
          sg.appendChild(svgEl('circle', { 'class': 'sensor-node ' + s.kind, cx: String(sxn), cy: String(syn), r: '14' }));
          var v = svgEl('text', { 'class': 'label small', x: String(sxn), y: String(syn + 3), 'text-anchor': 'middle' });
          v.textContent = fmt(s.value, s.kind) + unitFor(s);
          sg.appendChild(v);
          var t = svgEl('text', { 'class': 'label tag', x: String(sxn), y: String(syn + 28), 'text-anchor': 'middle' });
          t.textContent = String(s.alias || s.label || s.id).slice(0, 9);
          sg.appendChild(t);
          var pktCls = 'packet' + (s.kind === 'temp' ? ' amber' : '');
          var p = svgEl('circle', { 'class': pktCls, r: '1.6' });
          var a = svgEl('animateMotion', {
            dur: (1.2 + (j % 4) * 0.25) + 's',
            begin: (j * 0.15) + 's',
            repeatCount: 'indefinite',
            path: d, keyPoints: '1;0', keyTimes: '0;1'
          });
          p.appendChild(a);
          sg.appendChild(p);
          g.appendChild(sg);
        });
      }
      svg.appendChild(g);
    });

    wrap.appendChild(svg);

    // Legend
    var legend = el('div', { cls: 'hw-topo-legend' });
    [
      { cls: 'teal',   txt: 'hwmon' },
      { cls: 'blue',   txt: 'nvml' },
      { cls: 'purple', txt: 'acpi' },
      { cls: 'amber',  txt: 'temp packets' }
    ].forEach(function (item) {
      var li = el('div', { cls: 'hw-topo-legend-item' });
      li.appendChild(el('span', { cls: 'hw-topo-legend-dot ' + item.cls }));
      li.appendChild(document.createTextNode(item.txt));
      legend.appendChild(li);
    });
    wrap.appendChild(legend);
    container.appendChild(wrap);
  }

  // ── heatmap view ───────────────────────────────────────────────
  // v1 ships the empty state only — no sensor in the live API has a
  // `position` field set yet, so we render the docs-pointer instead
  // of fabricated placement.
  function renderHeatmapView(inv, container) {
    var hasPositions = false;
    (inv.chips || []).forEach(function (c) {
      (c.sensors || []).forEach(function (s) {
        if (s.position && typeof s.position.x === 'number' && typeof s.position.y === 'number') {
          hasPositions = true;
        }
      });
    });
    if (!hasPositions) {
      var emp = el('div', { cls: 'hw-heat-empty' });
      var s = svgEl('svg', { viewBox: '0 0 24 24', 'aria-hidden': 'true' });
      s.appendChild(svgEl('rect', { x: '3', y: '3', width: '18', height: '18', rx: '2' }));
      s.appendChild(svgEl('circle', { cx: '9',  cy: '10', r: '2' }));
      s.appendChild(svgEl('circle', { cx: '15', cy: '14', r: '2' }));
      emp.appendChild(s);
      emp.appendChild(el('h3', { text: 'Heatmap not configured' }));
      var p = el('p');
      p.innerHTML = 'Add <code>position: {x, y}</code> (normalised 0..1) to your sensors in '
        + '<code>/etc/ventd/config.yaml</code> to enable a case-shaped layout '
        + 'with sensors placed at their physical location and coloured by temperature.';
      emp.appendChild(p);
      container.appendChild(emp);
      return;
    }
    // Forward-compat path: positions exist. Render a basic case-shaped
    // layout with heat blobs sized + coloured by temperature, fan
    // markers at their positions. Kept minimal — the rich heatmap can
    // expand once real positional data lands.
    container.appendChild(renderHeatmapPlaced(inv));
  }

  function tempToTokenRgb(t) {
    // Map 25→95°C onto blue→teal→amber→red bands; emit "rgb(...)" using
    // the token RGB tuples so light/dark themes both make sense without
    // hardcoding hex values into the JS file (which the rule-lint would
    // not catch — JS is exempt — but conceptual consistency wins).
    // We read the computed style of the body to pull --rgb-* tuples.
    var cs = getComputedStyle(document.documentElement);
    function tup(name, fallback) {
      var v = (cs.getPropertyValue(name) || '').trim();
      return v || fallback;
    }
    var stops = [
      { at: 0.00, rgb: tup('--rgb-blue',  '88, 166, 255') },
      { at: 0.45, rgb: tup('--rgb-teal',  '79, 195, 161') },
      { at: 0.75, rgb: tup('--rgb-amber', '230, 162, 60') },
      { at: 1.00, rgb: tup('--rgb-red',   '248, 81, 73') }
    ];
    var x = clamp((t - 25) / (95 - 25), 0, 1);
    for (var i = 0; i < stops.length - 1; i++) {
      if (x <= stops[i+1].at) {
        var a = stops[i], b = stops[i+1];
        var f = (x - a.at) / (b.at - a.at || 1);
        var ra = a.rgb.split(',').map(function (n) { return parseFloat(n); });
        var rb = b.rgb.split(',').map(function (n) { return parseFloat(n); });
        var c = ra.map(function (v, k) { return Math.round(v + (rb[k] - v) * f); });
        return 'rgb(' + c[0] + ',' + c[1] + ',' + c[2] + ')';
      }
    }
    return 'rgb(' + stops[stops.length - 1].rgb + ')';
  }

  function renderHeatmapPlaced(inv) {
    var W = 760, H = 520, padX = 30, padY = 30;
    var innerW = W - padX * 2, innerH = H - padY * 2;
    var wrap = el('div', { cls: 'hw-topo' });        // reuse card chrome
    var svg = svgEl('svg', {
      'class': 'hw-topo-svg',
      viewBox: '0 0 ' + W + ' ' + H,
      preserveAspectRatio: 'xMidYMid meet'
    });
    svg.appendChild(svgEl('rect', {
      x: String(padX), y: String(padY),
      width: String(innerW), height: String(innerH),
      rx: '10', fill: 'var(--bg)', stroke: 'var(--border)', 'stroke-width': '1.5'
    }));
    (inv.chips || []).forEach(function (c) {
      (c.sensors || []).forEach(function (s) {
        if (!s.position) return;
        var x = padX + clamp(s.position.x, 0, 1) * innerW;
        var y = padY + clamp(s.position.y, 0, 1) * innerH;
        if (s.kind === 'temp') {
          var v = s.value || 30;
          var color = tempToTokenRgb(v);
          var r = 22 + clamp((v - 30) / 60, 0, 1) * 30;
          svg.appendChild(svgEl('circle', { cx: String(x), cy: String(y), r: String(r), fill: color, opacity: '0.7' }));
          svg.appendChild(svgEl('circle', { cx: String(x), cy: String(y), r: '4', fill: color, stroke: 'var(--bg)', 'stroke-width': '1.5' }));
          var label = svgEl('text', { 'class': 'label small', x: String(x + 8), y: String(y - 4) });
          label.textContent = Math.round(v) + '°';
          svg.appendChild(label);
        } else if (s.kind === 'fan') {
          svg.appendChild(svgEl('circle', { cx: String(x), cy: String(y), r: '6', fill: 'var(--bg)', stroke: 'var(--teal)', 'stroke-width': '1.2' }));
          var lbl = svgEl('text', { 'class': 'label tag', x: String(x), y: String(y + 16), 'text-anchor': 'middle' });
          lbl.textContent = Math.round(s.value || 0) + ' rpm';
          svg.appendChild(lbl);
        }
      });
    });
    wrap.appendChild(svg);
    return wrap;
  }

  // ── view dispatcher ────────────────────────────────────────────
  function renderBody() {
    var host = $('hw-content');
    if (!host) return;
    clearChildren(host);

    if (state.error) {
      host.appendChild(el('div', {
        cls: 'hw-empty is-error',
        text: 'Hardware data unavailable — daemon may be down.'
      }));
      return;
    }
    if (!state.inventory) {
      host.appendChild(el('div', { cls: 'hw-empty', text: 'Loading hardware inventory…' }));
      return;
    }
    ensureOpenIdsSeeded();
    ensureSelectedSeeded();

    renderSummaryCards(state.inventory, host);
    if (state.view === 'inventory')      renderInventoryView(state.inventory, host);
    else if (state.view === 'topology')  renderTopologyView(state.inventory, host);
    else if (state.view === 'heatmap')   renderHeatmapView(state.inventory, host);
  }

  // ── live dot ────────────────────────────────────────────────────
  function setLive(ok) {
    var d = $('sb-live-dot');
    var l = $('sb-live-label');
    if (d) d.classList.toggle('is-down', !ok);
    if (l) l.textContent = ok ? 'live' : 'reconnecting…';
  }

  // ── poll loop ──────────────────────────────────────────────────
  function load() {
    return fetch('/api/v1/hardware/inventory', { credentials: 'same-origin' })
      .then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      })
      .then(function (data) {
        state.inventory = data || { chips: [], curves: [] };
        state.error = null;
        trackPulses();
        setLive(true);
        renderBody();
      })
      .catch(function (e) {
        state.error = String(e && e.message || e);
        setLive(false);
        renderBody();
      });
  }

  // ── rescan button ──────────────────────────────────────────────
  var rescanBtn = $('hw-rescan');
  if (rescanBtn) {
    rescanBtn.addEventListener('click', function () {
      rescanBtn.disabled = true;
      fetch('/api/v1/hardware/rescan', { method: 'POST', credentials: 'same-origin' })
        .catch(function () { /* swallow — load() will surface state */ })
        .then(function () { return load(); })
        .then(function () { rescanBtn.disabled = false; })
        .catch(function () { rescanBtn.disabled = false; });
    });
  }

  // ── boot ───────────────────────────────────────────────────────
  renderViewSwitcher();
  load();
  setInterval(load, 1500);
})();
