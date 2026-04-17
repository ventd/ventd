"use strict";

// api.js — fetch wrappers against the daemon's HTTP API.
//
// Every network call lives here so the rest of the codebase stays
// fetch-free. Each loader hydrates its corresponding global from
// state.js (cfg / sts / hw / calStatuses / calResults) and then asks
// render.js to redraw.
//
// Convention: a successful load triggers render(); a failed load
// surfaces a notify('… : '+err.message, 'error') so the operator sees
// what's broken instead of a silent stale UI. Hardware diagnostics
// are intentionally silent on failure — they're best-effort.

async function loadConfig(){
  try {
    const r = await fetch('/api/config');
    cfg = await r.json();
    if(selIdx<0 && cfg.curves && cfg.curves.length>0) selIdx=0;
    render();
  } catch(e){ notify('Load config: '+e.message,'error'); }
}

// applyStatus repaints the dashboard from a fresh status snapshot.
// Called from both loadStatus (polling) and openEventStream (SSE) so
// the focus-preservation guards below live in exactly one place. A
// re-render mid-edit would steal focus from the operator's name
// input or close an open <select>; the activeElement checks skip the
// repaint of any card whose input is currently focused.
function applyStatus(snap){
  const dot = document.getElementById('live-dot');
  sts = snap;
  // Append new readings to the per-metric history buffer BEFORE the
  // cards re-render, so the sparkline path inside each card template
  // reflects the freshest tick the same repaint cycle that updates
  // the numeric value.
  if(typeof updateHistoryFromStatus === 'function') updateHistoryFromStatus(snap);
  if(dot){ dot.className='live-dot on'; clearTimeout(dot._t); dot._t=setTimeout(()=>{ dot.className='live-dot'; },2000); }
  const editingFan = document.activeElement && document.activeElement.closest('.card-name-edit');
  const editingSensor = document.activeElement && document.activeElement.closest('.sensor-name-edit');
  const fanSelectOpen = document.activeElement && document.activeElement.tagName === 'SELECT'
                        && document.activeElement.closest('#fan-cards');
  const anyCalRunning = Object.values(calStatuses).some(s=>s.running);
  renderSensorBar();
  if(!editingFan && !anyCalRunning && !fanSelectOpen) renderFanCards();
  if(!editingSensor) renderSensorCards();
  renderCurveCards();
  if(!dragging && selIdx>=0 && cfg && cfg.curves[selIdx] && cfg.curves[selIdx].type==='linear'){
    drawSVG(cfg.curves[selIdx]);
  }
}

async function loadStatus(){
  const dot = document.getElementById('live-dot');
  try {
    const r = await fetch('/api/status');
    applyStatus(await r.json());
  } catch(e){ if(dot) dot.className='live-dot err'; }
}

// openEventStream subscribes to /api/events (Server-Sent Events). On
// the first successful `status` frame it tells the caller to stop its
// polling timer via onOpen; on any error (network drop, proxy cutting
// the connection, browser without EventSource support) it calls
// onFallback so the caller can restart polling. The browser's
// EventSource also auto-reconnects internally — the fallback is only
// invoked when the connection is definitively gone.
//
// Returns the EventSource so callers can close() it on page teardown,
// or null when the browser has no EventSource (IE-class fallback).
function openEventStream(onOpen, onFallback){
  if(typeof EventSource === 'undefined'){
    onFallback();
    return null;
  }
  let es;
  try {
    es = new EventSource('/api/events');
  } catch(e){
    onFallback();
    return null;
  }
  let opened = false;
  es.addEventListener('status', ev => {
    try {
      applyStatus(JSON.parse(ev.data));
    } catch(_){ /* malformed frame — skip, next tick is already queued */ }
    if(!opened){
      opened = true;
      onOpen();
    }
  });
  es.addEventListener('error', () => {
    // EventSource fires 'error' both on transient disconnects (it will
    // auto-reconnect) and on final failures. Once the readyState is
    // CLOSED we know it won't come back — flip to polling. Otherwise
    // leave it alone and let the browser's reconnect handle it.
    if(es.readyState === EventSource.CLOSED){
      onFallback();
    }
  });
  return es;
}

async function loadHardware(){
  try {
    const r = await fetch('/api/hardware');
    hw = await r.json();
    renderHardware();
  } catch(e){}
}

// rescanHardware POSTs /api/hardware/rescan, shows a per-outcome toast,
// and re-fetches the sidebar view. Separate from the periodic poll
// because the operator is explicitly asking "did anything change?" and
// a silent re-render would look like a no-op.
async function rescanHardware(){
  const btn = document.getElementById('btn-rescan');
  if(btn){ btn.disabled = true; btn.querySelector('.icon').classList.add('icon-spin'); }
  try {
    const r = await fetch('/api/hardware/rescan', {method:'POST'});
    if(!r.ok) throw new Error('rescan failed: HTTP '+r.status);
    const j = await r.json();
    const added = (j.new_devices || []);
    const removed = (j.removed_devices || []);
    if(added.length === 0 && removed.length === 0){
      notify('No hardware changes detected', 'ok');
    } else {
      const parts = [];
      if(added.length)   parts.push('Detected new fan: '+added.join(', '));
      if(removed.length) parts.push('Removed: '+removed.join(', ')+' (no longer present)');
      notify(parts.join(' — '), 'ok');
    }
    // Refresh the sidebar from the authoritative /api/hardware endpoint
    // so values and friendly names stay consistent with the rest of the
    // dashboard's polling loop.
    await loadHardware();
  } catch(e){
    notify('Rescan failed: '+e.message, 'error');
  } finally {
    if(btn){ btn.disabled = false; btn.querySelector('.icon').classList.remove('icon-spin'); }
  }
}

async function loadCalibration(){
  try {
    const [sr, rr] = await Promise.all([
      fetch('/api/calibrate/status').then(r=>r.json()),
      fetch('/api/calibrate/results').then(r=>r.json()),
    ]);
    calStatuses = {};
    (sr||[]).forEach(s => { calStatuses[s.pwm_path] = s; });
    calResults = rr || {};
    renderFanCards();
  } catch(e){}
}

// applyConfig is the Apply-button entry point. It calls the daemon's
// dryrun endpoint to fetch a semantic diff of the in-memory config
// against what the daemon is currently running, then shows the diff
// in a modal so the user can confirm before committing. On empty
// diff the modal is skipped and a toast explains there were no
// changes to apply.
async function applyConfig(){
  try {
    const r = await fetch('/api/config/dryrun', {
      method: 'POST', headers: {'Content-Type':'application/json'},
      body: JSON.stringify(cfg)
    });
    if(!r.ok){
      notify('Dryrun failed: '+await r.text(), 'error');
      return;
    }
    const diff = await r.json();
    if(!diff.changed){
      notify('No changes to apply', 'ok');
      dirty=false;
      document.getElementById('dirty').hidden=true;
      document.getElementById('btn-apply').disabled=true;
      return;
    }
    openApplyModal(diff);
  } catch(e){ notify('Dryrun failed: '+e.message, 'error'); }
}

// commitConfigApply is the second half of the Apply flow — called
// from the modal's Confirm button after the user has reviewed the
// diff. Same PUT semantics as the previous one-step applyConfig:
// on 200 the dirty flag clears, on non-200 the error surface goes
// through the modal's status line so the user can retry without
// dismissing.
async function commitConfigApply(){
  const statusEl = document.getElementById('apply-status');
  const confirmBtn = document.getElementById('btn-apply-confirm');
  if(confirmBtn) confirmBtn.disabled = true;
  if(statusEl){ statusEl.textContent = 'Applying\u2026'; statusEl.className = 'apply-status'; }
  try {
    const r = await fetch('/api/config', {
      method:'PUT', headers:{'Content-Type':'application/json'},
      body:JSON.stringify(cfg)
    });
    if(r.ok){
      dirty=false;
      document.getElementById('dirty').hidden=true;
      document.getElementById('btn-apply').disabled=true;
      notify('Configuration applied', 'ok');
      closeApplyModal();
      loadConfig();
    } else {
      const msg = await r.text();
      if(statusEl){ statusEl.textContent = msg; statusEl.className = 'apply-status apply-status-err'; }
    }
  } catch(e){
    if(statusEl){ statusEl.textContent = 'Apply failed: '+e.message; statusEl.className = 'apply-status apply-status-err'; }
  } finally {
    if(confirmBtn) confirmBtn.disabled = false;
  }
}

// ── Hardware diagnostics (hwdiag) ──
//
// Polls /api/hwdiag every 10s; the snapshot's revision counter lets
// us skip a re-render when nothing changed. Entries group by
// component, colour by severity, and remediation renders as a
// button posting to entry.remediation.endpoint. The endpoint is
// daemon-side and may be omitted (TODO) — in that case the button
// renders disabled with an explanatory tooltip.
const COMPONENT_LABELS = {
  calibration: 'Calibration', hwmon: 'Sensors', oot: 'Kernel modules',
  dmi: 'Motherboard', gpu: 'GPU', boot: 'Bootloader', secureboot: 'Secure Boot',
  nixos: 'NixOS', arm: 'ARM board', ipmi: 'BMC / IPMI', bios: 'BIOS',
  nvidia: 'NVIDIA', hardware: 'Hardware'
};
let hwdiagRevision = -1;
async function loadHwdiag(){
  try {
    const r = await fetch('/api/hwdiag');
    if(!r.ok) return;
    const snap = await r.json();
    if(snap.revision === hwdiagRevision) return;
    hwdiagRevision = snap.revision;
    renderHwdiag(snap.entries || []);
  } catch(_){ /* silent; diagnostics are non-critical */ }
}
// hwdiagRunRemediation posts to the remediation endpoint. When the hwdiag
// entry carries a `context` map (e.g. {module:"coretemp"}), the button's
// data-hwdiag-context attr holds it as JSON and we forward it as the POST
// body. Endpoints that don't accept a body ignore it; the existing install-*
// handlers don't read r.Body so the extra payload is harmless.
//
// UX per usability.md:
//   - button shows a spinner while the request is in flight so the operator
//     sees that the click landed (modprobe can take a second or two)
//   - failures render inline beneath the button rather than as a toast so
//     the error stays visible next to the card that produced it, not off in
//     the top-right corner
async function hwdiagRunRemediation(endpoint, fix, btn, payload){
  if(!endpoint) return;
  const item = btn.closest('.hwdiag-item');
  hwdiagClearInlineError(item);
  const origLabel = btn.innerHTML;
  btn.disabled = true;
  btn.innerHTML = '<span class="hwdiag-spinner" aria-hidden="true"></span> Working…';
  try {
    const init = {method:'POST'};
    if(payload){
      init.headers = {'Content-Type':'application/json'};
      init.body = JSON.stringify(payload);
    }
    const r = await fetch(endpoint, init);
    let body = null;
    try { body = await r.json(); } catch(_){ /* not JSON — fine */ }
    if(!r.ok){
      hwdiagShowInlineError(item, 'Remediation failed (HTTP '+r.status+').');
    } else if(body && body.kind === 'install_log' && body.success === false){
      hwdiagShowInlineError(item, body.error || 'Remediation reported failure.');
    } else {
      notify('Remediation started: '+fix, 'ok');
    }
  } catch(e){ hwdiagShowInlineError(item, 'Remediation failed: '+e.message); }
  finally {
    btn.disabled = false;
    btn.innerHTML = origLabel;
    loadHwdiag();
  }
}

// hwdiagShowInlineError appends an error row to the card currently running a
// remediation. Scoped to the card (not a toast) so the operator sees the
// failure next to the thing they clicked.
function hwdiagShowInlineError(item, msg){
  if(!item) return;
  hwdiagClearInlineError(item);
  const err = document.createElement('div');
  err.className = 'hwdiag-inline-error';
  err.textContent = msg;
  item.appendChild(err);
}

function hwdiagClearInlineError(item){
  if(!item) return;
  const prev = item.querySelector(':scope > .hwdiag-inline-error');
  if(prev) prev.remove();
}

// ── System status ─────────────────────────────────────────────────
//
// The Settings modal's "System Status" section aggregates four
// read-only daemon endpoints into a single row-list. Each row is a
// one-liner — icon + label + status text — because the section sits
// inside a modal and must read at a glance. The dashboard's banner
// builds on the same `/api/system/diagnostics` response so both
// surfaces stay in sync (banner counts + modal body).

async function loadSystemStatus(){
  const body = document.getElementById('system-status-body');
  if(!body) return;
  body.innerHTML = '<div class="sys-row">Loading…</div>';
  try {
    const [wd, rec, sec, diag] = await Promise.all([
      fetch('/api/system/watchdog').then(r=>r.ok?r.json():null),
      fetch('/api/system/recovery').then(r=>r.ok?r.json():null),
      fetch('/api/system/security').then(r=>r.ok?r.json():null),
      fetch('/api/system/diagnostics').then(r=>r.ok?r.json():null),
    ]);
    body.innerHTML = renderSystemStatus(wd, rec, sec, diag);
  } catch(e){
    body.innerHTML = '<div class="sys-row sys-row-err">Could not load system status: '+esc(e.message)+'</div>';
  }
}

function renderSystemStatus(wd, rec, sec, diag){
  let out = '';
  // Watchdog
  if(wd){
    if(wd.enabled){
      out += renderSysRow('Watchdog',
        'healthy (' + (wd.interval_ms/1000).toFixed(1) + 's interval)', 'ok');
    } else {
      out += renderSysRow('Watchdog',
        'not active (running outside systemd)', 'muted');
    }
  }
  // Crash recovery
  if(rec){
    if(rec.installed){
      out += renderSysRow('Crash recovery',
        rec.service_active ? 'armed' : 'installed (service inactive)',
        rec.service_active ? 'ok' : 'warn');
    } else {
      out += renderSysRow('Crash recovery', 'not installed', 'muted');
    }
  }
  // MAC policies — one row per LSM, skipping "unsupported" so a vanilla
  // desktop isn't peppered with non-actionable rows.
  if(sec){
    if(sec.selinux_module && sec.selinux_module !== 'unsupported'){
      out += renderSysRow('SELinux module',
        sec.selinux_module, sec.selinux_module === 'loaded' ? 'ok' : 'warn');
    }
    if(sec.apparmor_profile && sec.apparmor_profile !== 'unsupported'){
      out += renderSysRow('AppArmor profile',
        sec.apparmor_profile, sec.apparmor_profile === 'loaded' ? 'ok' : 'warn');
    }
  }
  // Diagnostics roll-up counts; the full list lives in the wizard's
  // /api/hwdiag surface, this modal shows the summary.
  if(diag){
    const c = diag.counts || {};
    const warn = c.warn || 0;
    const err = c.error || 0;
    const info = c.info || 0;
    let text = 'no issues';
    let kind = 'ok';
    if(err > 0){ text = err + ' error'+(err===1?'':'s'); kind = 'err'; }
    else if(warn > 0){ text = warn + ' warning'+(warn===1?'':'s'); kind = 'warn'; }
    else if(info > 0){ text = info + ' info note'+(info===1?'':'s'); kind = 'muted'; }
    out += renderSysRow('Diagnostics', text, kind);
  }
  return out || '<div class="sys-row sys-row-muted">No system status available.</div>';
}

function renderSysRow(label, value, cls){
  return '<div class="sys-row sys-row-'+cls+'">'+
    '<span class="sys-label">'+esc(label)+'</span>'+
    '<span class="sys-value">'+esc(value)+'</span>'+
  '</div>';
}

async function loadAboutInfo(){
  const body = document.getElementById('about-body');
  if(!body) return;
  body.textContent = 'Loading…';
  try {
    const r = await fetch('/api/version');
    if(!r.ok){ body.textContent = 'Could not load version info.'; return; }
    const v = await r.json();
    body.innerHTML =
      '<div class="about-row"><span class="about-lbl">Version</span><span class="about-val">'+esc(v.version||'unknown')+'</span></div>'+
      '<div class="about-row"><span class="about-lbl">Commit</span><span class="about-val about-mono">'+esc((v.commit||'').slice(0,12))+'</span></div>'+
      '<div class="about-row"><span class="about-lbl">Built</span><span class="about-val about-mono">'+esc(v.build_date||'')+'</span></div>'+
      '<div class="about-row"><span class="about-lbl">License</span><span class="about-val">GPL-3.0</span></div>'+
      '<div class="about-row"><a class="about-link" href="https://github.com/ventd/ventd/releases" target="_blank" rel="noopener">GitHub releases →</a></div>';
  } catch(e){
    body.textContent = 'Could not load version info: ' + e.message;
  }
}

// ── Diagnostics banner ────────────────────────────────────────────
//
// The banner sits at the top of the dashboard and surfaces
// WARN/ERROR-severity diagnostic entries so they can't be missed.
// Polled alongside hwdiag so the daemon's 10s rescan cadence
// propagates without a second poll loop. sessionStorage suppresses
// the banner for the rest of the tab's lifetime once dismissed —
// a refresh re-shows it so an operator who closes it then comes
// back later doesn't miss a newly-risen warning.

let diagBannerSeverity = '';
async function loadDiagnosticsForBanner(){
  const banner = document.getElementById('diag-banner');
  const text = document.getElementById('diag-banner-text');
  if(!banner || !text) return;
  try {
    if(sessionStorage.getItem('ventd.diag-banner.dismissed') === '1'){
      banner.classList.add('hidden');
      return;
    }
  } catch(_){}
  try {
    const r = await fetch('/api/system/diagnostics');
    if(!r.ok) return;
    const d = await r.json();
    const c = d.counts || {};
    if((c.error||0) > 0){
      banner.classList.remove('hidden');
      banner.classList.add('diag-banner-err');
      banner.classList.remove('diag-banner-warn');
      text.textContent = 'Hardware diagnostics: ' + c.error + ' error'+(c.error===1?'':'s')+'.';
      diagBannerSeverity = 'error';
    } else if((c.warn||0) > 0){
      banner.classList.remove('hidden');
      banner.classList.add('diag-banner-warn');
      banner.classList.remove('diag-banner-err');
      text.textContent = 'Hardware diagnostics: ' + c.warn + ' warning'+(c.warn===1?'':'s')+'.';
      diagBannerSeverity = 'warn';
    } else {
      banner.classList.add('hidden');
      diagBannerSeverity = '';
    }
  } catch(_){}
}
function dismissDiagBanner(){
  const banner = document.getElementById('diag-banner');
  if(banner) banner.classList.add('hidden');
  try { sessionStorage.setItem('ventd.diag-banner.dismissed', '1'); } catch(_){}
}

// ── Panic button (Session C 2e) ──
//
// startPanic POSTs /api/panic and switches the header UI into active
// mode. The countdown is driven by polling /api/panic/state every
// second — cheap, and lets the UI stay in sync even if a second tab
// started or cancelled the panic. Server owns the timer so the UI
// never decides on its own when to stop.

let panicPollTimer = null;

async function startPanic(durationS){
  closePanicPopover();
  try {
    const r = await fetch('/api/panic', {method:'POST',
      headers:{'Content-Type':'application/json'},
      body: JSON.stringify({duration_s: durationS})});
    if(!r.ok) throw new Error('HTTP '+r.status);
    const j = await r.json();
    setPanicUI(j);
    startPanicPoll();
  } catch(e){
    notify('Panic failed: '+e.message, 'error');
  }
}

async function cancelPanic(){
  try {
    const r = await fetch('/api/panic/cancel', {method:'POST'});
    if(!r.ok) throw new Error('HTTP '+r.status);
    const j = await r.json();
    setPanicUI(j);
    stopPanicPoll();
  } catch(e){
    notify('Cancel panic failed: '+e.message, 'error');
  }
}

async function pollPanicState(){
  try {
    const r = await fetch('/api/panic/state');
    if(!r.ok) return;
    const j = await r.json();
    setPanicUI(j);
    if(!j.active) stopPanicPoll();
  } catch(_){}
}

function startPanicPoll(){
  stopPanicPoll();
  panicPollTimer = setInterval(pollPanicState, 1000);
}
function stopPanicPoll(){
  if(panicPollTimer){ clearInterval(panicPollTimer); panicPollTimer = null; }
}

function setPanicUI(state){
  const btn = document.getElementById('btn-panic');
  const active = document.getElementById('panic-active');
  const cd = document.getElementById('panic-countdown');
  if(!btn || !active || !cd) return;
  if(state && state.active){
    btn.classList.add('hidden');
    active.classList.remove('hidden');
    cd.textContent = state.end_at ? (state.remaining_s + 's') : 'until cancelled';
  } else {
    btn.classList.remove('hidden');
    active.classList.add('hidden');
  }
}

function togglePanicPopover(){
  const pop = document.getElementById('panic-popover');
  if(pop) pop.classList.toggle('hidden');
}
function closePanicPopover(){
  const pop = document.getElementById('panic-popover');
  if(pop) pop.classList.add('hidden');
}

// ── Profiles (Session C 2e) ──

async function loadProfiles(){
  try {
    const r = await fetch('/api/profile');
    if(!r.ok) return;
    const j = await r.json();
    renderProfileSelect(j);
  } catch(_){}
}

function renderProfileSelect(state){
  const sel = document.getElementById('profile-select');
  if(!sel) return;
  const names = Object.keys(state.profiles || {});
  if(names.length === 0){
    sel.classList.add('hidden');
    sel.innerHTML = '';
    return;
  }
  names.sort();
  sel.innerHTML = names.map(n => {
    const schedule = (state.profiles[n] && state.profiles[n].schedule) || '';
    const suffix = schedule ? ' ('+esc(schedule)+')' : ' (default)';
    return '<option value="'+esc(n)+'"'+(n===state.active?' selected':'')+'>'+esc(n)+suffix+'</option>';
  }).join('');
  sel.classList.remove('hidden');
  // Schedule source badge is refreshed by schedule.js; kick it so the
  // badge tracks the same loadProfiles cadence without a separate
  // polling loop.
  if(typeof refreshScheduleStatus === 'function') refreshScheduleStatus();
  if(typeof renderProfileScheduleEditor === 'function') renderProfileScheduleEditor(state);
}

async function switchProfile(name){
  try {
    const r = await fetch('/api/profile/active', {method:'POST',
      headers:{'Content-Type':'application/json'},
      body: JSON.stringify({name: name})});
    if(!r.ok){
      const txt = await r.text();
      throw new Error(txt || ('HTTP '+r.status));
    }
    notify('Switched to '+name, 'ok');
    // Re-fetch config so the dashboard reflects the new bindings.
    if(typeof loadConfig === 'function') loadConfig();
    // Update the source badge: the POST flipped us to manual.
    if(typeof refreshScheduleStatus === 'function') refreshScheduleStatus();
  } catch(e){
    notify('Switch profile failed: '+e.message, 'error');
  }
}
