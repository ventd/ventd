// health.js — consolidated read view of every sensor/fan/voltage/
// power reading the daemon enumerated. Polls /api/v1/hardware/inventory
// + /api/v1/doctor + /api/v1/version on a 2s cadence; renders into
// per-kind groups with trend sparks. Pure DOM construction, no
// innerHTML, no external CDN per RULE-UI-01 / RULE-UI-02.

(function () {
  'use strict';

  var POLL_INTERVAL_MS = 2000;

  function $(id) { return document.getElementById(id); }

  function fetchJSON(url) {
    return fetch(url, { credentials: 'same-origin', cache: 'no-store' })
      .then(function (r) {
        if (!r.ok) throw new Error(url + ' ' + r.status);
        return r.json();
      });
  }

  // sparkPath builds a polyline 'd' attribute from history values.
  function sparkPath(history, w, h) {
    if (!history || history.length < 2) return '';
    var min = Infinity, max = -Infinity;
    for (var i = 0; i < history.length; i++) {
      var v = history[i];
      if (typeof v !== 'number' || !isFinite(v)) continue;
      if (v < min) min = v;
      if (v > max) max = v;
    }
    if (!isFinite(min) || !isFinite(max)) return '';
    var range = max - min;
    if (range < 1e-9) range = 1;
    var stepX = (history.length > 1) ? w / (history.length - 1) : 0;
    var d = '';
    for (var j = 0; j < history.length; j++) {
      var val = history[j];
      var y = h - ((val - min) / range) * h;
      var x = j * stepX;
      d += (j === 0 ? 'M' : 'L') + x.toFixed(1) + ' ' + y.toFixed(1) + ' ';
    }
    return d.trim();
  }

  // trendInfo summarises a sensor's history as (label, css class).
  function trendInfo(history, kind) {
    if (!Array.isArray(history) || history.length < 4) {
      return { label: '—', cls: 'is-flat' };
    }
    var head = history.slice(0, Math.max(2, Math.floor(history.length / 4)));
    var tail = history.slice(-Math.max(2, Math.floor(history.length / 4)));
    var avg = function (a) { var s = 0; for (var i = 0; i < a.length; i++) s += a[i]; return s / a.length; };
    var dh = avg(tail) - avg(head);
    var threshold = (kind === 'temp') ? 0.4 : (kind === 'fan' ? 30 : 0.05);
    if (Math.abs(dh) < threshold) return { label: '·', cls: 'is-flat' };
    if (dh > 0) return { label: '↑ ' + dh.toFixed(2), cls: (kind === 'temp' || kind === 'power') ? 'is-up' : 'is-down' };
    return { label: '↓ ' + Math.abs(dh).toFixed(2), cls: (kind === 'temp' || kind === 'power') ? 'is-down' : 'is-up' };
  }

  function strokeForKind(kind) {
    switch (kind) {
      case 'temp':  return 'var(--red)';
      case 'fan':   return 'var(--teal)';
      case 'volt':  return 'var(--blue)';
      case 'power': return 'var(--amber)';
      default:      return 'var(--fg3)';
    }
  }

  function renderCard(sensor, chipName) {
    var card = document.createElement('div');
    card.className = 'hl-card';

    var head = document.createElement('div');
    head.className = 'hl-card-row';
    var name = document.createElement('span');
    name.className = 'hl-card-name';
    name.textContent = sensor.name || '(unnamed)';
    head.appendChild(name);
    var val = document.createElement('span');
    val.className = 'hl-card-value mono';
    val.textContent = (typeof sensor.value === 'number' && isFinite(sensor.value))
      ? sensor.value.toFixed(sensor.kind === 'fan' ? 0 : 2)
      : '—';
    head.appendChild(val);
    var unit = document.createElement('span');
    unit.className = 'hl-card-unit';
    unit.textContent = sensor.unit || '';
    head.appendChild(unit);
    card.appendChild(head);

    var SVG_NS = 'http://www.w3.org/2000/svg';
    var spark = document.createElementNS(SVG_NS, 'svg');
    spark.setAttribute('class', 'hl-card-spark');
    spark.setAttribute('viewBox', '0 0 200 26');
    spark.setAttribute('preserveAspectRatio', 'none');
    var path = document.createElementNS(SVG_NS, 'path');
    var d = sparkPath(sensor.history, 200, 26);
    path.setAttribute('d', d);
    path.setAttribute('fill', 'none');
    path.setAttribute('stroke', strokeForKind(sensor.kind));
    path.setAttribute('stroke-width', '1.4');
    spark.appendChild(path);
    card.appendChild(spark);

    var foot = document.createElement('div');
    foot.className = 'hl-card-foot';
    var chip = document.createElement('span');
    chip.className = 'hl-card-chip';
    chip.textContent = chipName || '';
    foot.appendChild(chip);
    var t = trendInfo(sensor.history, sensor.kind);
    var trend = document.createElement('span');
    trend.className = 'hl-card-trend ' + t.cls;
    trend.textContent = t.label;
    foot.appendChild(trend);
    card.appendChild(foot);

    return card;
  }

  function paintGroup(kind, sensors, chipNamesByIndex) {
    var section = $('hl-group-' + kind + 's');
    var list    = $('hl-list-'  + kind + 's');
    var count   = $('hl-count-' + kind + 's');
    if (!section || !list || !count) return;
    list.textContent = '';
    sensors.forEach(function (s, i) {
      list.appendChild(renderCard(s, chipNamesByIndex[i]));
    });
    count.textContent = String(sensors.length);
    section.hidden = sensors.length === 0;
  }

  function renderInventory(inv) {
    var groups = { temp: [], fan: [], volt: [], power: [] };
    var chipNames = { temp: [], fan: [], volt: [], power: [] };
    var totalSensors = 0;
    var spinning = 0, fanTotal = 0;
    var hottestVal = -Infinity, hottestName = null;

    if (inv && Array.isArray(inv.chips)) {
      inv.chips.forEach(function (chip) {
        var sensors = (chip && chip.sensors) || [];
        sensors.forEach(function (s) {
          totalSensors++;
          var kind = s.kind;
          if (!groups[kind]) return;
          groups[kind].push(s);
          chipNames[kind].push(chip.name || '');
          if (kind === 'temp' && typeof s.value === 'number' && s.value > hottestVal) {
            hottestVal = s.value;
            hottestName = (chip.name || '') + ' · ' + s.name;
          }
          if (kind === 'fan' && typeof s.value === 'number') {
            fanTotal++;
            if (s.value > 60) spinning++;
          }
        });
      });
    }

    paintGroup('temp',  groups.temp,  chipNames.temp);
    paintGroup('fan',   groups.fan,   chipNames.fan);
    paintGroup('volt',  groups.volt,  chipNames.volt);
    paintGroup('power', groups.power, chipNames.power);

    var emptyEl = $('hl-empty');
    if (emptyEl) emptyEl.hidden = totalSensors > 0;

    var hot = $('hl-hot-temp');
    if (hot) hot.textContent = isFinite(hottestVal) ? hottestVal.toFixed(1) + ' °C' : '—';
    var hotName = $('hl-hot-name');
    if (hotName) hotName.textContent = hottestName || '—';

    var fanCount = $('hl-fan-count');
    if (fanCount) fanCount.textContent = fanTotal === 0 ? '—' : (spinning + ' / ' + fanTotal);
    var fanSub = $('hl-fan-sub');
    if (fanSub) fanSub.textContent = fanTotal === 0 ? 'no fans enumerated' :
      (spinning === 0 ? 'all stopped' : 'spinning at >60 RPM');

    var ssCount = $('hl-sensor-count');
    if (ssCount) ssCount.textContent = String(totalSensors);
    var ssSub = $('hl-sensor-sub');
    if (ssSub) ssSub.textContent = (inv && Array.isArray(inv.chips)) ? (inv.chips.length + ' chip' + (inv.chips.length === 1 ? '' : 's')) : '—';

    var meta = $('hl-meta');
    if (meta) {
      meta.textContent = totalSensors + ' sensor' + (totalSensors === 1 ? '' : 's') +
        ' · ' + (inv && Array.isArray(inv.chips) ? inv.chips.length : 0) + ' chip' +
        (inv && inv.chips && inv.chips.length === 1 ? '' : 's') +
        ' · last poll ' + new Date().toLocaleTimeString();
    }
  }

  function renderDoctor(report) {
    var docCount = $('hl-doc-count');
    var docSub   = $('hl-doc-sub');
    if (!docCount || !docSub) return;
    if (!report || !Array.isArray(report.facts)) {
      docCount.textContent = '—';
      docSub.textContent   = 'doctor offline';
      return;
    }
    var blockers = 0, warnings = 0;
    report.facts.forEach(function (f) {
      var s = (f.severity || '').toLowerCase();
      if (s === 'blocker') blockers++;
      else if (s === 'warning') warnings++;
    });
    if (blockers === 0 && warnings === 0) {
      docCount.textContent = '0';
      docSub.textContent = 'all clear';
    } else if (blockers > 0) {
      docCount.textContent = String(blockers);
      docSub.textContent = blockers + ' blocker' + (blockers === 1 ? '' : 's') +
        (warnings > 0 ? ' · ' + warnings + ' warning' + (warnings === 1 ? '' : 's') : '');
    } else {
      docCount.textContent = String(warnings);
      docSub.textContent = warnings + ' warning' + (warnings === 1 ? '' : 's');
    }
  }

  function renderVersion(v) {
    var el = $('hl-version');
    if (!el) return;
    if (v && v.version) {
      el.textContent = 'ventd ' + v.version;
    } else {
      el.textContent = '—';
    }
  }

  // tick polls every endpoint, swallowing per-endpoint failures into
  // a null result so one broken endpoint doesn't blank the whole page.
  // BUT the operator MUST be told when an endpoint failed: bug-hunt
  // finding (Agent 1 #1) was that the silent-null fallback made
  // "Hottest: —" indistinguishable from "system has zero sensors"
  // versus "the inventory endpoint 500'd". The mode pill turns
  // 'warn' on any partial failure + a one-line cause lands in the
  // sub-line so the operator knows what they're looking at.
  function tick() {
    Promise.all([
      fetchJSON('/api/v1/hardware/inventory').catch(function (e) { return { __err: e }; }),
      fetchJSON('/api/v1/doctor').catch(function (e) { return { __err: e }; }),
      fetchJSON('/api/v1/version').catch(function (e) { return { __err: e }; })
    ]).then(function (rs) {
      var failures = [];
      rs.forEach(function (r, i) {
        if (r && r.__err) {
          failures.push(['inventory', 'doctor', 'version'][i] + ': ' + (r.__err.message || 'fetch failed'));
        }
      });
      renderInventory((rs[0] && !rs[0].__err) ? rs[0] : null);
      renderDoctor((rs[1] && !rs[1].__err) ? rs[1] : null);
      renderVersion((rs[2] && !rs[2].__err) ? rs[2] : null);
      paintErrorState(failures);
    });
  }

  // paintErrorState toggles the topbar mode pill + writes the per-
  // endpoint failure list into the meta footer. Empty failures →
  // pill back to 'live' green.
  function paintErrorState(failures) {
    var pill = $('hl-mode-pill');
    if (!pill) return;
    pill.classList.remove('ok', 'warn');
    if (failures.length === 0) {
      pill.classList.add('ok');
      pill.textContent = 'live';
      return;
    }
    pill.classList.add('warn');
    pill.textContent = 'partial · ' + failures.length + ' endpoint' +
      (failures.length === 1 ? '' : 's') + ' failed';
    var meta = $('hl-meta');
    if (meta) meta.textContent = 'errors: ' + failures.join(' · ');
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () {
      tick();
      setInterval(tick, POLL_INTERVAL_MS);
    });
  } else {
    tick();
    setInterval(tick, POLL_INTERVAL_MS);
  }
})();
