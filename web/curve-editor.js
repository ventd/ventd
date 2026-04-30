// curve-editor.js — visual curve editor backed by /api/v1/profile.
//
// /api/v1/profile returns:
//   { active: "<name>", profiles: { name → { bindings, schedule } } }
//
// Curves themselves live on the live config: GET /api/v1/config returns
// { ..., curves: [CurveConfig], controls: [Control], fans: [Fan] }.
// We render every curve and let the user drag anchors of "points" / "linear"
// curves; on Save we PUT the modified config back.

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
  function clamp(x, lo, hi) { return Math.max(lo, Math.min(hi, x)); }

  // graph geometry
  var G_W = 800, G_H = 380;
  var T_MIN = 20, T_MAX = 100;
  function tx(temp) { return ((temp - T_MIN) / (T_MAX - T_MIN)) * G_W; }
  function ty(pct)  { return G_H - (clamp(pct, 0, 100) / 100) * G_H; }
  function inverseTx(x) { return T_MIN + (clamp(x, 0, G_W) / G_W) * (T_MAX - T_MIN); }
  function inverseTy(y) { return clamp(((G_H - y) / G_H) * 100, 0, 100); }

  // ── state ──────────────────────────────────────────────────────────
  var config = null;
  var profile = null;
  var liveStatus = null;
  var selected = null; // curve index
  var dirty = false;

  // helper: derive PWM% from a curve at a given temperature
  function evalCurve(c, temp) {
    if (!c) return 0;
    if (c.type === 'fixed') {
      var v = c.value_pct != null ? c.value_pct : Math.round((c.value || 0) / 255 * 100);
      return clamp(v, 0, 100);
    }
    if (c.type === 'linear') {
      var minP = c.min_pwm_pct != null ? c.min_pwm_pct : Math.round((c.min_pwm || 0) / 255 * 100);
      var maxP = c.max_pwm_pct != null ? c.max_pwm_pct : Math.round((c.max_pwm || 255) / 255 * 100);
      var minT = c.min_temp || 30, maxT = c.max_temp || 80;
      if (temp <= minT) return minP;
      if (temp >= maxT) return maxP;
      return minP + (temp - minT) / (maxT - minT) * (maxP - minP);
    }
    if (c.type === 'points' && c.points && c.points.length > 0) {
      var pts = c.points.slice().sort(function (a, b) { return a.temp - b.temp; });
      if (temp <= pts[0].temp) return pwmPct(pts[0]);
      if (temp >= pts[pts.length - 1].temp) return pwmPct(pts[pts.length - 1]);
      for (var i = 0; i < pts.length - 1; i++) {
        var a = pts[i], b = pts[i + 1];
        if (temp >= a.temp && temp <= b.temp) {
          var f = (temp - a.temp) / (b.temp - a.temp);
          return pwmPct(a) + f * (pwmPct(b) - pwmPct(a));
        }
      }
    }
    if (c.type === 'pi') {
      // Show setpoint as a horizontal-ish band; plot feedforward as the line.
      return c.feed_forward != null ? Math.round(c.feed_forward / 255 * 100) : 50;
    }
    if (c.type === 'mix') {
      // No deterministic preview — just show a flat 50%.
      return 50;
    }
    return 0;
  }
  function pwmPct(p) {
    if (p.pwm_pct != null) return p.pwm_pct;
    return Math.round((p.pwm || 0) / 255 * 100);
  }
  function curveAnchors(c) {
    if (c.type === 'linear') {
      var minP = c.min_pwm_pct != null ? c.min_pwm_pct : Math.round((c.min_pwm || 0) / 255 * 100);
      var maxP = c.max_pwm_pct != null ? c.max_pwm_pct : Math.round((c.max_pwm || 255) / 255 * 100);
      return [
        { temp: c.min_temp || 30, pct: minP, _idx: 0 },
        { temp: c.max_temp || 80, pct: maxP, _idx: 1 }
      ];
    }
    if (c.type === 'points' && c.points && c.points.length > 0) {
      return c.points.slice().sort(function (a, b) { return a.temp - b.temp; }).map(function (p, i) {
        return { temp: p.temp, pct: pwmPct(p), _idx: i };
      });
    }
    return [];
  }

  // ── rendering ──────────────────────────────────────────────────────
  function renderList() {
    var list = $('ce-list');
    if (!list) return;
    var curves = (config && config.curves) || [];
    if (curves.length === 0) {
      list.innerHTML = '<div class="ce-list-empty">No curves loaded.</div>';
      return;
    }
    var html = '';
    curves.forEach(function (c, idx) {
      var sel = (idx === selected) ? ' is-selected' : '';
      // mini sparkline along temp range
      var path = '';
      var nSteps = 24;
      for (var i = 0; i <= nSteps; i++) {
        var t = T_MIN + (i / nSteps) * (T_MAX - T_MIN);
        var p = evalCurve(c, t);
        var x = (i / nSteps) * 100;
        var y = 24 - (p / 100) * 22 - 1;
        path += (i === 0 ? 'M ' : ' L ') + x.toFixed(1) + ' ' + y.toFixed(1);
      }
      html += '<div class="ce-list-item' + sel + '" data-idx="' + idx + '">'
            +   '<div class="ce-list-item-name">' + escapeHTML(c.name || 'unnamed') + '</div>'
            +   '<span class="ce-list-item-tag mono">' + escapeHTML(c.type || '?') + '</span>'
            +   '<svg class="ce-list-item-mini" viewBox="0 0 100 24" preserveAspectRatio="none"><path d="' + path + '"/></svg>'
            + '</div>';
    });
    list.innerHTML = html;
    Array.prototype.forEach.call(list.querySelectorAll('.ce-list-item'), function (el) {
      el.addEventListener('click', function () {
        selected = parseInt(el.dataset.idx, 10);
        renderList();
        renderGraph();
        renderProps();
        renderBound();
      });
    });
    var counter = $('ce-list-counter');
    if (counter) counter.textContent = curves.length;
  }

  function renderGraph() {
    var c = (config && config.curves) ? config.curves[selected] : null;
    var nameEl = $('ce-curve-name'), typeEl = $('ce-curve-type');
    if (!c) { if (nameEl) nameEl.textContent = 'Select a curve'; if (typeEl) typeEl.textContent = '—'; return; }
    if (nameEl) nameEl.textContent = c.name || 'unnamed';
    if (typeEl) typeEl.textContent = c.type || '';

    // Build a fine line across the temp range using evalCurve.
    var line = '';
    var area = '';
    var nSteps = 80;
    for (var i = 0; i <= nSteps; i++) {
      var t = T_MIN + (i / nSteps) * (T_MAX - T_MIN);
      var p = evalCurve(c, t);
      var x = tx(t), y = ty(p);
      line += (i === 0 ? 'M ' : ' L ') + x.toFixed(1) + ' ' + y.toFixed(1);
    }
    area = line + ' L ' + G_W + ' ' + G_H + ' L 0 ' + G_H + ' Z';
    var lineEl = $('ce-line'), areaEl = $('ce-area');
    if (lineEl) lineEl.setAttribute('d', line);
    if (areaEl) areaEl.setAttribute('d', area);

    // handles
    var handles = $('ce-handles');
    if (handles) {
      handles.innerHTML = '';
      var anchors = curveAnchors(c);
      anchors.forEach(function (a, idx) {
        var cx = tx(a.temp), cy = ty(a.pct);
        var dot = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
        dot.setAttribute('class', 'ce-handle');
        dot.setAttribute('cx', cx); dot.setAttribute('cy', cy);
        dot.setAttribute('r', 6);
        dot.dataset.idx = idx;
        handles.appendChild(dot);
        attachDrag(dot, idx);
      });
    }
    updateCursor();
  }
  function renderProps() {
    var c = (config && config.curves) ? config.curves[selected] : null;
    var setT = function (id, v) { var el = $(id); if (el) el.textContent = v; };
    if (!c) {
      setT('ce-prop-type', '—'); setT('ce-prop-sensor', '—');
      setT('ce-prop-range', '—'); setT('ce-prop-hyst', '—');
      setT('ce-prop-smooth', '—'); setT('ce-prop-points', '—');
      return;
    }
    setT('ce-prop-type', c.type || '—');
    setT('ce-prop-sensor', c.sensor || (c.sources ? c.sources.join(' + ') : '—'));
    if (c.type === 'linear') {
      var minP = c.min_pwm_pct != null ? c.min_pwm_pct : Math.round((c.min_pwm || 0) / 255 * 100);
      var maxP = c.max_pwm_pct != null ? c.max_pwm_pct : Math.round((c.max_pwm || 255) / 255 * 100);
      setT('ce-prop-range', (c.min_temp || 30) + '°→' + (c.max_temp || 80) + '°  ·  ' + minP + '%→' + maxP + '%');
    } else if (c.type === 'fixed') {
      var v = c.value_pct != null ? c.value_pct : Math.round((c.value || 0) / 255 * 100);
      setT('ce-prop-range', v + '% fixed');
    } else if (c.type === 'points') {
      setT('ce-prop-range', (c.points && c.points.length || 0) + ' anchors');
    } else {
      setT('ce-prop-range', '—');
    }
    setT('ce-prop-hyst',  c.hysteresis ? c.hysteresis + ' °C' : '—');
    var sm = c.smoothing;
    if (sm == null || sm === 0)            setT('ce-prop-smooth', '—');
    else if (typeof sm === 'string')        setT('ce-prop-smooth', sm);
    else if (typeof sm === 'number')        setT('ce-prop-smooth', sm + ' s');
    else                                     setT('ce-prop-smooth', '—');
    setT('ce-prop-points', (c.points && c.points.length) ? c.points.length : (c.type === 'linear' ? 2 : '—'));
  }
  function renderBound() {
    var ul = $('ce-bound-list');
    if (!ul) return;
    var c = (config && config.curves) ? config.curves[selected] : null;
    if (!c) { ul.innerHTML = '<li class="ce-bound-empty">—</li>'; return; }
    var fans = (config.controls || []).filter(function (ctl) { return ctl.curve === c.name; });
    if (fans.length === 0) {
      ul.innerHTML = '<li class="ce-bound-empty">no fans bound</li>';
      return;
    }
    var html = '';
    fans.forEach(function (ctl) {
      var fan = (config.fans || []).find(function (f) { return f.name === ctl.fan; });
      html += '<li>'
            +   '<span class="ce-bound-fan">' + escapeHTML(ctl.fan) + '</span>'
            +   '<span class="ce-bound-source mono">' + escapeHTML((fan && fan.pwm_path) || '') + '</span>'
            + '</li>';
    });
    ul.innerHTML = html;
  }

  // ── drag handles ───────────────────────────────────────────────────
  function attachDrag(dot, anchorIdx) {
    var canvas = $('ce-canvas');
    if (!canvas) return;
    var dragging = false;
    function onDown(e) {
      e.preventDefault();
      dragging = true;
      dot.classList.add('is-dragging');
      window.addEventListener('mousemove', onMove);
      window.addEventListener('mouseup', onUp);
      window.addEventListener('touchmove', onMove, { passive: false });
      window.addEventListener('touchend', onUp);
    }
    function onMove(e) {
      if (!dragging) return;
      e.preventDefault();
      var pt = canvas.createSVGPoint();
      var src = e.touches ? e.touches[0] : e;
      pt.x = src.clientX; pt.y = src.clientY;
      var ctm = canvas.getScreenCTM().inverse();
      var p = pt.matrixTransform(ctm);
      var temp = inverseTx(p.x);
      var pct  = inverseTy(p.y);
      applyAnchorEdit(anchorIdx, temp, pct);
    }
    function onUp() {
      dragging = false;
      dot.classList.remove('is-dragging');
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      window.removeEventListener('touchmove', onMove);
      window.removeEventListener('touchend', onUp);
    }
    dot.addEventListener('mousedown', onDown);
    dot.addEventListener('touchstart', onDown, { passive: false });
  }

  function applyAnchorEdit(idx, temp, pct) {
    var c = config && config.curves && config.curves[selected];
    if (!c) return;
    if (c.type === 'linear') {
      if (idx === 0) {
        c.min_temp = Math.round(temp);
        c.min_pwm_pct = Math.round(pct);
      } else {
        c.max_temp = Math.round(temp);
        c.max_pwm_pct = Math.round(pct);
      }
    } else if (c.type === 'points' && c.points) {
      // map sorted index back to source index
      var sorted = c.points.slice().sort(function (a, b) { return a.temp - b.temp; });
      var anchor = sorted[idx];
      var origIdx = c.points.indexOf(anchor);
      if (origIdx >= 0) {
        c.points[origIdx].temp = Math.round(temp * 10) / 10;
        c.points[origIdx].pwm_pct = Math.round(pct);
      }
    }
    setDirty(true);
    renderGraph();
    renderProps();
    renderList();
  }

  function setDirty(v) {
    dirty = v;
    var btn = $('ce-save'); if (btn) btn.disabled = !v;
  }

  // ── live cursor (current sensor temperature) ──────────────────────
  function updateCursor() {
    var c = config && config.curves && config.curves[selected];
    var tEl = $('ce-cursor-temp'), pEl = $('ce-cursor-pwm');
    if (!c || !liveStatus) return;
    // Find sensor reading by name match
    var sensorName = c.sensor || (c.sources && c.sources[0]);
    var temp = null;
    if (sensorName && liveStatus.sensors) {
      for (var i = 0; i < liveStatus.sensors.length; i++) {
        if (liveStatus.sensors[i].name === sensorName && liveStatus.sensors[i].value != null) {
          temp = Number(liveStatus.sensors[i].value); break;
        }
      }
    }
    if (temp == null && liveStatus.sensors && liveStatus.sensors[0] && liveStatus.sensors[0].value != null) {
      temp = Number(liveStatus.sensors[0].value); // fallback first sensor
    }
    if (temp == null) {
      if (tEl) tEl.textContent = '—'; if (pEl) pEl.textContent = '—';
      return;
    }
    var pct = evalCurve(c, temp);
    if (tEl) tEl.textContent = temp.toFixed(1);
    if (pEl) pEl.textContent = Math.round(pct);
    var x = tx(temp), y = ty(pct);
    var cur = $('ce-cursor'), dot = $('ce-cursor-dot');
    if (cur) { cur.setAttribute('x1', x); cur.setAttribute('x2', x); }
    if (dot) { dot.setAttribute('cx', x); dot.setAttribute('cy', y); }
  }

  // ── save ──────────────────────────────────────────────────────────
  var saveBtn = $('ce-save');
  if (saveBtn) saveBtn.addEventListener('click', function () {
    if (!config) return;
    saveBtn.disabled = true;
    saveBtn.textContent = 'Saving…';
    fetch('/api/v1/config', {
      method: 'PUT', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(config)
    })
      .then(function (r) {
        if (r.ok) {
          setDirty(false);
          saveBtn.textContent = 'Saved';
          setTimeout(function () { saveBtn.textContent = 'Save changes'; }, 1500);
          return;
        }
        return r.text().then(function (txt) { throw new Error('Save failed: ' + (txt || r.status)); });
      })
      .catch(function (err) {
        alert(err && err.message || 'Save failed');
        saveBtn.textContent = 'Save changes';
        saveBtn.disabled = !dirty;
      });
  });

  // ── load + poll ───────────────────────────────────────────────────
  var inDemo = false;
  function loadAll() {
    Promise.all([
      fetch('/api/v1/config',  { credentials: 'same-origin' }).then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); }),
      fetch('/api/v1/profile', { credentials: 'same-origin' }).then(function (r) { return r.ok ? r.json() : null; }).catch(function () { return null; })
    ])
      .then(function (out) {
        config = out[0];
        profile = out[1];
        if (selected == null && config && config.curves && config.curves.length > 0) selected = 0;
        var pp = $('ce-active-profile');
        if (pp && profile) pp.textContent = (profile.active || 'auto');
        renderList(); renderGraph(); renderProps(); renderBound();
        var d = $('sb-live-dot'); if (d) d.classList.remove('is-down');
        var l = $('sb-live-label'); if (l) l.textContent = 'live';
      })
      .catch(function () { if (!inDemo) { inDemo = true; loadDemo(); } });
  }
  function pollStatus() {
    fetch('/api/v1/status', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (s) { if (s) { liveStatus = s; updateCursor(); } })
      .catch(function () {});
  }

  function loadDemo() {
    config = {
      curves: [
        { name: 'Quiet CPU', type: 'linear',
          sensor: 'CPU package',
          min_temp: 35, max_temp: 78,
          min_pwm_pct: 28, max_pwm_pct: 92,
          hysteresis: 2, smoothing: '5s' },
        { name: 'GPU aware',  type: 'points', sensor: 'GPU 0 (RTX 4090)',
          points: [
            { temp: 30, pwm_pct: 25 },
            { temp: 50, pwm_pct: 35 },
            { temp: 65, pwm_pct: 55 },
            { temp: 75, pwm_pct: 78 },
            { temp: 85, pwm_pct: 100 }
          ],
          hysteresis: 3 },
        { name: 'AIO pump',   type: 'fixed', value_pct: 70 },
        { name: 'Mix CPU+GPU', type: 'mix', sources: ['CPU package', 'GPU 0 (RTX 4090)'], function: 'max' },
        { name: 'Stealth',    type: 'linear', sensor: 'Motherboard',
          min_temp: 25, max_temp: 70,
          min_pwm_pct: 0, max_pwm_pct: 60 }
      ],
      controls: [
        { fan: 'CPU fan',         curve: 'Quiet CPU' },
        { fan: 'Front intake top', curve: 'Quiet CPU' },
        { fan: 'Front intake mid', curve: 'Quiet CPU' },
        { fan: 'GPU 0 fan 0',     curve: 'GPU aware' },
        { fan: 'GPU 0 fan 1',     curve: 'GPU aware' },
        { fan: 'AIO pump',        curve: 'AIO pump' }
      ],
      fans: [
        { name: 'CPU fan',         pwm_path: 'nct6798d/pwm1' },
        { name: 'Front intake top', pwm_path: 'nct6798d/pwm2' },
        { name: 'Front intake mid', pwm_path: 'nct6798d/pwm3' },
        { name: 'GPU 0 fan 0',     pwm_path: 'gpu0/fan0' },
        { name: 'GPU 0 fan 1',     pwm_path: 'gpu0/fan1' },
        { name: 'AIO pump',        pwm_path: 'corsair/pump' }
      ]
    };
    profile = { active: 'Quiet' };
    var pp = $('ce-active-profile'); if (pp) pp.textContent = 'Quiet';
    selected = 0;
    renderList(); renderGraph(); renderProps(); renderBound();
    // synthesize live status
    liveStatus = { sensors: [
      { name: 'CPU package',     value: 56,  unit: '°C' },
      { name: 'GPU 0 (RTX 4090)', value: 67, unit: '°C' },
      { name: 'Motherboard',     value: 38,  unit: '°C' }
    ]};
    updateCursor();
  }

  loadAll();
  pollStatus();
  setInterval(pollStatus, 1500);
})();
