// Profile scheduling UI — Session D 3e.
//
// Three responsibilities:
//
//   1. refreshScheduleStatus: poll /api/schedule/status and paint a
//      small source badge next to the profile dropdown ("scheduled" /
//      "manual override").
//
//   2. renderProfileScheduleEditor: build a list of profiles inside
//      the Settings modal with per-profile schedule editors (time
//      inputs + day checkboxes). Shown automatically whenever
//      loadProfiles fires — no separate polling loop.
//
//   3. saveProfileSchedule: PUT the edited schedule back via
//      /api/profile/schedule. Validation errors surface inline.
//
// All DOM lookups are defensive (`if(!el) return`) so this file is a
// no-op on pages that don't include the profile-schedules markup
// (e.g. the login screen).

(function(){
  'use strict';

  const WEEKDAY_ORDER = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'];
  const WEEKDAY_LABEL = {
    mon: 'Mon', tue: 'Tue', wed: 'Wed', thu: 'Thu',
    fri: 'Fri', sat: 'Sat', sun: 'Sun',
  };

  // escapeHTML mirrors the esc() helper from api.js but is scoped
  // here so schedule.js doesn't depend on script ordering.
  function escapeHTML(s){
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }

  // formatLocalTime renders an ISO timestamp as HH:MM in the user's
  // timezone so the "next transition" hint reads naturally.
  function formatLocalTime(iso){
    if(!iso) return '';
    const d = new Date(iso);
    if(isNaN(d.getTime())) return '';
    const hh = String(d.getHours()).padStart(2, '0');
    const mm = String(d.getMinutes()).padStart(2, '0');
    return hh+':'+mm;
  }

  // parseScheduleString breaks a "HH:MM-HH:MM DAYSPEC" grammar into
  // editor-friendly fields. Returns null on malformed input rather
  // than throwing — the editor renders empty inputs in that case
  // and the server-side validator is the ultimate authority.
  function parseScheduleString(s){
    if(!s) return {start: '', end: '', days: []};
    const parts = s.trim().split(/\s+/);
    if(parts.length !== 2) return null;
    const [timeRange, daySpec] = parts;
    const dash = timeRange.indexOf('-');
    if(dash < 0) return null;
    const start = timeRange.slice(0, dash);
    const end = timeRange.slice(dash+1);
    let days = [];
    if(daySpec === '*'){
      days = WEEKDAY_ORDER.slice();
    } else if(daySpec.indexOf(',') >= 0){
      days = daySpec.split(',').map(s => s.trim().toLowerCase());
    } else if(daySpec.indexOf('-') >= 0){
      const [lo, hi] = daySpec.split('-').map(s => s.trim().toLowerCase());
      const loIdx = WEEKDAY_ORDER.indexOf(lo);
      const hiIdx = WEEKDAY_ORDER.indexOf(hi);
      if(loIdx < 0 || hiIdx < 0) return null;
      if(loIdx <= hiIdx){
        days = WEEKDAY_ORDER.slice(loIdx, hiIdx+1);
      } else {
        days = WEEKDAY_ORDER.slice(loIdx).concat(WEEKDAY_ORDER.slice(0, hiIdx+1));
      }
    } else {
      days = [daySpec.trim().toLowerCase()];
    }
    return {start: start, end: end, days: days};
  }

  // buildScheduleString reconstitutes the grammar from editor state.
  // Empty start/end/days signals "clear the schedule" — the caller
  // sends an empty string to the server.
  function buildScheduleString(start, end, days){
    if(!start && !end && days.length === 0) return '';
    if(!start || !end) return null;
    let daySpec;
    if(days.length === 7){
      daySpec = '*';
    } else if(days.length === 0){
      return null;
    } else {
      // Canonicalise: emit in weekday order so round-trips are stable.
      const ordered = WEEKDAY_ORDER.filter(d => days.indexOf(d) >= 0);
      daySpec = ordered.join(',');
    }
    return start+'-'+end+' '+daySpec;
  }

  window.refreshScheduleStatus = async function(){
    const badge = document.getElementById('schedule-source');
    if(!badge) return;
    try {
      const r = await fetch('/api/schedule/status');
      if(!r.ok){
        badge.classList.add('hidden');
        return;
      }
      const j = await r.json();
      // Only show the badge when there's a profile actually running;
      // an empty active_profile means no profile dropdown → nothing
      // to annotate.
      if(!j.active_profile){
        badge.classList.add('hidden');
        badge.textContent = '';
        return;
      }
      let label;
      if(j.source === 'manual'){
        label = 'manual';
      } else {
        label = 'scheduled';
      }
      let title = j.source === 'manual'
        ? 'Manual override — will clear at next schedule transition'
        : 'Running on schedule';
      if(j.next_transition && j.next_profile){
        title += '. Next: '+j.next_profile+' at '+formatLocalTime(j.next_transition);
      }
      badge.textContent = label;
      badge.title = title;
      badge.classList.toggle('manual', j.source === 'manual');
      badge.classList.toggle('scheduled', j.source !== 'manual');
      badge.classList.remove('hidden');
    } catch(_){
      badge.classList.add('hidden');
    }
  };

  // renderProfileScheduleEditor rebuilds the Settings-modal panel. We
  // rebuild rather than diff because the profile set is tiny (3-6
  // rows typically) and sticky DOM state is limited to an in-flight
  // status line.
  window.renderProfileScheduleEditor = function(state){
    const section = document.getElementById('profile-schedules-section');
    const body = document.getElementById('profile-schedules-body');
    if(!section || !body) return;
    const profiles = (state && state.profiles) || {};
    const names = Object.keys(profiles);
    if(names.length === 0){
      section.hidden = true;
      body.innerHTML = '';
      return;
    }
    section.hidden = false;
    names.sort();
    body.innerHTML = names.map(n => {
      const p = profiles[n] || {};
      const parsed = parseScheduleString(p.schedule || '') || {start:'', end:'', days:[]};
      const dayCheckboxes = WEEKDAY_ORDER.map(d => {
        const checked = parsed.days.indexOf(d) >= 0 ? ' checked' : '';
        return '<label class="sched-day"><input type="checkbox" data-sched-day="'+d+'"'+checked+'>'+WEEKDAY_LABEL[d]+'</label>';
      }).join('');
      return ''+
        '<div class="sched-row" data-profile="'+escapeHTML(n)+'">'+
          '<div class="sched-row-hdr">'+
            '<div class="row-title">'+escapeHTML(n)+'</div>'+
            '<div class="row-subtitle sched-preview">'+escapeHTML(schedulePreview(parsed))+'</div>'+
          '</div>'+
          '<div class="sched-inputs">'+
            '<input type="time" class="sched-start" value="'+escapeHTML(parsed.start)+'">'+
            '<span class="sched-sep">to</span>'+
            '<input type="time" class="sched-end" value="'+escapeHTML(parsed.end)+'">'+
          '</div>'+
          '<div class="sched-days">'+dayCheckboxes+'</div>'+
          '<div class="sched-actions">'+
            '<button class="sched-clear" data-action="clear-profile-schedule">Clear</button>'+
            '<button class="primary sched-save" data-action="save-profile-schedule">Save</button>'+
          '</div>'+
        '</div>';
    }).join('');
  };

  function schedulePreview(parsed){
    if(!parsed.start || !parsed.end || parsed.days.length === 0){
      return 'No schedule — manual only';
    }
    let dayLabel;
    if(parsed.days.length === 7){
      dayLabel = 'every day';
    } else if(parsed.days.length === 5
      && ['mon','tue','wed','thu','fri'].every(d => parsed.days.indexOf(d) >= 0)){
      dayLabel = 'weekdays';
    } else if(parsed.days.length === 2
      && parsed.days.indexOf('sat') >= 0 && parsed.days.indexOf('sun') >= 0){
      dayLabel = 'weekends';
    } else {
      dayLabel = parsed.days.map(d => WEEKDAY_LABEL[d]).join(', ');
    }
    return 'Active '+parsed.start+'–'+parsed.end+' '+dayLabel;
  }

  // saveProfileSchedule collects the row's editor state, rebuilds the
  // grammar, and PUTs it. The server is the canonical validator.
  async function saveProfileSchedule(row){
    const name = row.getAttribute('data-profile');
    if(!name) return;
    const start = row.querySelector('.sched-start').value.trim();
    const end = row.querySelector('.sched-end').value.trim();
    const days = Array.from(row.querySelectorAll('[data-sched-day]'))
      .filter(cb => cb.checked)
      .map(cb => cb.getAttribute('data-sched-day'));
    const schedule = buildScheduleString(start, end, days);
    if(schedule === null){
      scheduleStatus(name, 'Start, end, and at least one day are required', 'error');
      return;
    }
    try {
      const r = await fetch('/api/profile/schedule', {method:'PUT',
        headers:{'Content-Type':'application/json'},
        body: JSON.stringify({name: name, schedule: schedule})});
      if(!r.ok){
        const txt = await r.text();
        throw new Error(txt || ('HTTP '+r.status));
      }
      scheduleStatus(name, schedule ? 'Schedule saved' : 'Schedule cleared', 'ok');
      if(typeof loadProfiles === 'function') loadProfiles();
    } catch(e){
      scheduleStatus(name, 'Save failed: '+e.message, 'error');
    }
  }

  async function clearProfileSchedule(row){
    const name = row.getAttribute('data-profile');
    if(!name) return;
    try {
      const r = await fetch('/api/profile/schedule', {method:'PUT',
        headers:{'Content-Type':'application/json'},
        body: JSON.stringify({name: name, schedule: ''})});
      if(!r.ok){
        const txt = await r.text();
        throw new Error(txt || ('HTTP '+r.status));
      }
      scheduleStatus(name, 'Schedule cleared', 'ok');
      if(typeof loadProfiles === 'function') loadProfiles();
    } catch(e){
      scheduleStatus(name, 'Clear failed: '+e.message, 'error');
    }
  }

  function scheduleStatus(name, msg, kind){
    const el = document.getElementById('schedule-editor-status');
    if(!el) return;
    el.textContent = '['+name+'] '+msg;
    el.className = 'schedule-editor-status '+(kind || '');
    // Also mirror via notify() when available so the message survives
    // a modal close.
    if(typeof notify === 'function') notify(msg, kind === 'ok' ? 'ok' : 'error');
  }

  // Delegate actions on the editor rows. Uses bubbling so newly-
  // rendered rows don't need individual listeners.
  document.addEventListener('click', function(ev){
    const btn = ev.target.closest('[data-action]');
    if(!btn) return;
    const row = btn.closest('.sched-row');
    if(!row) return;
    const action = btn.getAttribute('data-action');
    if(action === 'save-profile-schedule'){
      ev.preventDefault();
      saveProfileSchedule(row);
    } else if(action === 'clear-profile-schedule'){
      ev.preventDefault();
      clearProfileSchedule(row);
    }
  });

  // Kick the badge once on load and on an interval. 30s cadence is
  // generous: the badge changes only on transitions, which the
  // scheduler evaluates once per minute server-side.
  if(document.readyState === 'loading'){
    document.addEventListener('DOMContentLoaded', function(){
      refreshScheduleStatus();
    });
  } else {
    refreshScheduleStatus();
  }
  setInterval(function(){ refreshScheduleStatus(); }, 30000);
})();
