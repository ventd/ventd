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

async function loadStatus(){
  const dot = document.getElementById('live-dot');
  try {
    const r = await fetch('/api/status');
    sts = await r.json();
    if(dot){ dot.className='live-dot on'; clearTimeout(dot._t); dot._t=setTimeout(()=>{ dot.className='live-dot'; },2000); }
    // Avoid clobbering a focused name input or open <select> mid-edit.
    // The status poller fires every 2s; a re-render while the operator
    // is typing into a sensor or fan name would steal focus and lose
    // their in-flight character. Same logic for the curve dropdown.
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
  } catch(e){ if(dot) dot.className='live-dot err'; }
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
async function hwdiagRunRemediation(endpoint, fix, btn){
  if(!endpoint) return;
  btn.disabled = true;
  try {
    const r = await fetch(endpoint, {method:'POST'});
    if(r.ok){ notify('Remediation started: '+fix, 'ok'); }
    else { notify('Remediation failed ('+r.status+')', 'error'); }
  } catch(e){ notify('Remediation failed: '+e.message, 'error'); }
  finally { btn.disabled = false; loadHwdiag(); }
}
