// schedule.js — profile + schedule view.
//
//   GET /api/v1/profile          → { active, profiles: { name → { bindings, schedule } } }
//   GET /api/v1/schedule/status  → { active_profile, source, next_transition?, next_profile? }
//   PUT /api/v1/profile/schedule → { name, schedule } (changes a profile's schedule)
//
// Renders:
//   • a 4-card stats header (active, next switch, profile count, local time)
//   • a 24h "today" timeline with each profile as a coloured block
//   • a 7-day "this week" stack
//   • a profile table with edit-in-place schedule grammar

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

  // ── parse schedule grammar (minimal subset) ────────────────────────
  // Supported: "HH:MM-HH:MM *", "HH:MM-HH:MM mon-fri", "HH:MM-HH:MM sat,sun".
  // Returns { start, end, days[0..6] }, end may wrap past midnight.
  var DAY_NAMES = ['mon','tue','wed','thu','fri','sat','sun'];
  function parseSchedule(str) {
    if (!str) return null;
    var parts = str.trim().split(/\s+/);
    if (parts.length < 1) return null;
    var range = parts[0];
    var m = range.match(/^(\d{1,2}):(\d{2})-(\d{1,2}):(\d{2})$/);
    if (!m) return null;
    var sh = parseInt(m[1], 10), sm = parseInt(m[2], 10);
    var eh = parseInt(m[3], 10), em = parseInt(m[4], 10);
    var startMin = sh * 60 + sm;
    var endMin   = eh * 60 + em;
    var days = [false, false, false, false, false, false, false];
    var spec = parts[1] || '*';
    if (spec === '*') for (var i = 0; i < 7; i++) days[i] = true;
    else {
      spec.split(',').forEach(function (chunk) {
        chunk = chunk.trim();
        var rangeM = chunk.match(/^([a-z]{3})-([a-z]{3})$/);
        if (rangeM) {
          var a = DAY_NAMES.indexOf(rangeM[1]);
          var b = DAY_NAMES.indexOf(rangeM[2]);
          if (a >= 0 && b >= 0) {
            var i = a;
            while (true) { days[i] = true; if (i === b) break; i = (i + 1) % 7; }
          }
        } else {
          var idx = DAY_NAMES.indexOf(chunk);
          if (idx >= 0) days[idx] = true;
        }
      });
    }
    return { start: startMin, end: endMin, days: days, raw: str };
  }

  // emit blocks for a 24h stretch on a given dayIndex (0=Mon..6=Sun).
  // Each block is { startMin, endMin } in local-day minutes.
  function blocksForDay(parsed, dayIdx) {
    if (!parsed || !parsed.days[dayIdx]) return [];
    if (parsed.end > parsed.start) {
      // simple range
      return [{ start: parsed.start, end: parsed.end }];
    }
    // wraps midnight: applies until end on the day, and from start to 24:00 on the day
    return [{ start: parsed.start, end: 24 * 60 }];
  }

  // ── colour assignment per profile ─────────────────────────────────
  var PROFILE_COLORS = ['var(--teal)', 'var(--blue)', 'var(--cyan)', 'var(--amber)', '#a371f7', '#f66a8e', '#82c91e'];
  function colourFor(name, idx) {
    return PROFILE_COLORS[idx % PROFILE_COLORS.length];
  }

  // ── state ─────────────────────────────────────────────────────────
  var profilesData = null; // { active, profiles: { name → ... } }
  var statusData   = null; // schedule/status

  // ── render: header stats ─────────────────────────────────────────
  function renderHeader() {
    var active = (profilesData && profilesData.active) || (statusData && statusData.active_profile) || '—';
    $('sched-active-name').textContent = active;
    var src = (statusData && statusData.source) || 'manual';
    $('sched-active-sub').textContent = src + ' source';
    var pp = $('sched-source');
    if (pp) {
      pp.textContent = src;
      pp.className = 'status-pill no-dot ' + (src === 'schedule' ? 'ok' : 'info');
    }

    if (statusData && statusData.next_transition) {
      var t = new Date(statusData.next_transition);
      var diff = (t - new Date()) / 1000;
      var inS = '—';
      if (diff > 0) {
        var h = Math.floor(diff / 3600);
        var m = Math.floor((diff % 3600) / 60);
        inS = (h > 0 ? h + 'h ' : '') + m + 'm';
      }
      $('sched-next-in').textContent = inS;
      $('sched-next-name').textContent = '→ ' + (statusData.next_profile || '');
    } else {
      $('sched-next-in').textContent = '—';
      $('sched-next-name').textContent = 'no upcoming switch';
    }

    var profiles = (profilesData && profilesData.profiles) || {};
    var names = Object.keys(profiles);
    $('sched-count').textContent = names.length;
    var scheduled = names.filter(function (n) { return profiles[n].schedule; }).length;
    $('sched-count-sub').textContent = scheduled + ' scheduled · ' + (names.length - scheduled) + ' manual';

    var now = new Date();
    var hh = String(now.getHours()).padStart(2, '0');
    var mm = String(now.getMinutes()).padStart(2, '0');
    $('sched-clock').textContent = hh + ':' + mm;
    $('sched-day').textContent = now.toLocaleDateString([], { weekday: 'long' });
  }

  // ── render: 24h timeline for today ────────────────────────────────
  function renderTimeline() {
    var box = $('sched-timeline');
    var legend = $('sched-legend');
    var axis = $('sched-axis');
    if (!box || !legend || !axis) return;
    box.innerHTML = '';
    legend.innerHTML = '';
    axis.innerHTML = '';

    var profiles = (profilesData && profilesData.profiles) || {};
    var names = Object.keys(profiles);
    if (names.length === 0) {
      box.innerHTML = '<div class="sched-timeline-empty">No profiles configured yet.</div>';
      return;
    }

    var now = new Date();
    // Mon=0..Sun=6 (matching DAY_NAMES)
    var dayIdx = (now.getDay() + 6) % 7;

    // Layout each profile's blocks on the 0..1440 minute timeline
    names.forEach(function (name, i) {
      var prof = profiles[name];
      if (!prof.schedule) return;
      var parsed = parseSchedule(prof.schedule);
      if (!parsed) return;
      var blocks = blocksForDay(parsed, dayIdx);
      // also include yesterday's wrap segment from end-of-yesterday — for visualization,
      // the wrap-into-today portion is end<start so we add it separately.
      if (parsed.end <= parsed.start && parsed.days[(dayIdx + 6) % 7]) {
        blocks.push({ start: 0, end: parsed.end });
      }
      var color = colourFor(name, i);
      blocks.forEach(function (b) {
        var leftPct = (b.start / 1440) * 100;
        var widthPct = ((b.end - b.start) / 1440) * 100;
        var blk = document.createElement('div');
        blk.className = 'sched-timeline-block' + ((profilesData.active === name) ? ' is-active' : '');
        blk.style.left = leftPct.toFixed(2) + '%';
        blk.style.width = widthPct.toFixed(2) + '%';
        blk.style.background = color;
        blk.title = name + ' — ' + prof.schedule;
        blk.textContent = name;
        box.appendChild(blk);
      });
    });

    // now line
    var nowMin = now.getHours() * 60 + now.getMinutes();
    var nowPct = (nowMin / 1440) * 100;
    var nl = document.createElement('div');
    nl.className = 'sched-timeline-now';
    nl.style.left = nowPct + '%';
    box.appendChild(nl);

    // axis 0..23
    var html = '';
    for (var h = 0; h < 24; h++) html += '<span>' + String(h).padStart(2, '0') + '</span>';
    axis.innerHTML = html;

    // legend. Inline style="" attributes violate CSP style-src 'self';
    // set the swatch background via CSSOM after construction.
    names.forEach(function (name, i) {
      if (!profiles[name].schedule) return;
      var color = colourFor(name, i);
      var pill = document.createElement('span');
      pill.className = 'legend-pill';
      var swatch = document.createElement('span');
      swatch.className = 'legend-swatch';
      swatch.style.background = color;
      pill.appendChild(swatch);
      pill.appendChild(document.createTextNode(name));
      legend.appendChild(pill);
    });
  }

  // ── render: 7-day week view ───────────────────────────────────────
  function renderWeek() {
    var box = $('sched-week');
    if (!box) return;
    box.innerHTML = '';

    var profiles = (profilesData && profilesData.profiles) || {};
    var names = Object.keys(profiles);
    var now = new Date();
    var todayIdx = (now.getDay() + 6) % 7;

    var labels = ['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT', 'SUN'];
    for (var d = 0; d < 7; d++) {
      var lblEl = document.createElement('div');
      lblEl.className = 'sched-week-day-label';
      lblEl.textContent = labels[d];
      box.appendChild(lblEl);
      var dayEl = document.createElement('div');
      dayEl.className = 'sched-week-day' + (d === todayIdx ? ' is-today' : '');
      // place each profile's block in the day
      names.forEach(function (name, i) {
        var prof = profiles[name];
        if (!prof.schedule) return;
        var parsed = parseSchedule(prof.schedule);
        if (!parsed) return;
        var blocks = blocksForDay(parsed, d);
        if (parsed.end <= parsed.start && parsed.days[(d + 6) % 7]) {
          blocks.push({ start: 0, end: parsed.end });
        }
        var color = colourFor(name, i);
        blocks.forEach(function (b) {
          var leftPct = (b.start / 1440) * 100;
          var widthPct = ((b.end - b.start) / 1440) * 100;
          var blk = document.createElement('div');
          blk.className = 'sched-week-block';
          blk.style.left = leftPct + '%';
          blk.style.width = widthPct + '%';
          blk.style.background = color;
          blk.title = name + ' (' + prof.schedule + ')';
          dayEl.appendChild(blk);
        });
      });
      box.appendChild(dayEl);
    }
  }

  // ── render: profile table with edit-in-place ─────────────────────
  function renderTable() {
    var tbody = $('sched-tbody');
    if (!tbody) return;
    var profiles = (profilesData && profilesData.profiles) || {};
    var names = Object.keys(profiles).sort();
    if (names.length === 0) {
      tbody.innerHTML = '<tr><td colspan="4" class="sched-tbody-empty">No profiles configured.</td></tr>';
      return;
    }
    var html = '';
    names.forEach(function (name) {
      var p = profiles[name];
      var bindings = p.bindings || {};
      var bk = Object.keys(bindings);
      var bindHtml = bk.length === 0 ? '<span class="sched-bindings">—</span>' :
        '<div class="sched-bindings">' + bk.map(function (fan) {
          return '<span class="sched-binding-tag">' + escapeHTML(fan) + ' → ' + escapeHTML(bindings[fan]) + '</span>';
        }).join('') + '</div>';
      var schedHtml = p.schedule
        ? '<span class="sched-schedule mono">' + escapeHTML(p.schedule) + '</span>'
        : '<span class="sched-schedule mono sched-schedule--manual">manual only</span>';
      html += '<tr data-name="' + escapeHTML(name) + '">'
            +   '<td><span class="sched-name' + (profilesData.active === name ? ' is-active' : '') + '">' + escapeHTML(name) + '</span></td>'
            +   '<td>' + schedHtml + '</td>'
            +   '<td>' + bindHtml + '</td>'
            +   '<td><button type="button" class="sched-edit-btn" data-name="' + escapeHTML(name) + '">Edit</button></td>'
            + '</tr>';
    });
    tbody.innerHTML = html;
    Array.prototype.forEach.call(tbody.querySelectorAll('.sched-edit-btn'), function (btn) {
      btn.addEventListener('click', function () { editSchedule(btn.dataset.name); });
    });
  }

  function editSchedule(name) {
    var prof = profilesData && profilesData.profiles && profilesData.profiles[name];
    if (!prof) return;
    var current = prof.schedule || '';
    var next = window.prompt('Schedule for "' + name + '" (HH:MM-HH:MM days):\n\nExamples:\n  22:00-08:00 *\n  09:00-17:00 mon-fri\n  10:00-22:00 sat,sun\n\nLeave empty to make this profile manual-only.', current);
    if (next === null) return; // cancelled
    fetch('/api/v1/profile/schedule', {
      method: 'PUT',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: name, schedule: next })
    })
      .then(function (r) {
        if (r.ok) { load(); return; }
        return r.text().then(function (t) { alert('Save failed: ' + t); });
      })
      .catch(function (err) { alert('Save failed: ' + (err && err.message)); });
  }

  // ── live ────────────────────────────────────────────────────────
  function setLive(ok) {
    var d = $('sb-live-dot'), l = $('sb-live-label');
    if (d) d.classList.toggle('is-down', !ok);
    if (l) l.textContent = ok ? 'live' : 'reconnecting…';
  }

  // ── load ────────────────────────────────────────────────────────
  var inDemo = false;
  function load() {
    Promise.all([
      fetch('/api/v1/profile',         { credentials: 'same-origin' }).then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); }),
      fetch('/api/v1/schedule/status', { credentials: 'same-origin' }).then(function (r) { return r.ok ? r.json() : null; }).catch(function () { return null; })
    ])
      .then(function (out) {
        profilesData = out[0];
        statusData   = out[1];
        renderAll();
        setLive(true);
      })
      .catch(function () { if (!inDemo) { inDemo = true; loadDemo(); } });
  }
  function renderAll() {
    renderHeader();
    renderTimeline();
    renderWeek();
    renderTable();
  }

  function loadDemo() {
    profilesData = {
      active: 'Quiet',
      profiles: {
        'Quiet':       { schedule: '22:00-08:00 *',         bindings: { 'CPU fan': 'Quiet CPU', 'Front intake': 'Quiet CPU' } },
        'Day':         { schedule: '08:00-22:00 mon-fri',   bindings: { 'CPU fan': 'Standard',  'Rear exhaust': 'Standard' } },
        'Weekend':     { schedule: '08:00-22:00 sat,sun',   bindings: { 'CPU fan': 'Standard' } },
        'Encode rush': { schedule: '',                       bindings: { 'AIO pump': 'AIO pump', 'AIO fan 1': 'Aggressive' } }
      }
    };
    statusData = {
      active_profile: 'Quiet',
      source: 'schedule',
      next_transition: new Date(Date.now() + 4 * 3600 * 1000 + 12 * 60 * 1000).toISOString(),
      next_profile: 'Day'
    };
    renderAll();
    setLive(false);
  }

  load();
  setInterval(function () {
    // keep clock + remaining-time updated even without API
    renderHeader();
  }, 30000);
  setInterval(load, 60000);
})();
