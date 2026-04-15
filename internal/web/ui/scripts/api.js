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

async function applyConfig(){
  try {
    const r = await fetch('/api/config',{
      method:'PUT', headers:{'Content-Type':'application/json'},
      body:JSON.stringify(cfg)
    });
    if(r.ok){
      dirty=false;
      document.getElementById('dirty').hidden=true;
      document.getElementById('btn-apply').disabled=true;
      notify('Configuration applied','ok');
      loadConfig();
    } else { notify(await r.text(),'error'); }
  } catch(e){ notify('Apply failed: '+e.message,'error'); }
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
