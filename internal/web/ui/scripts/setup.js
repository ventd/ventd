"use strict";

// setup.js — first-boot wizard UI and dashboard bootstrap.
//
// The wizard owns the /api/setup/* lifecycle: status polling, phase
// rendering, the fan table, reboot prompt, and final Apply. It renders
// into #setup-overlay and its children; that overlay is shown until
// the daemon reports `applied` on /api/setup/status.
//
// The bootstrap block at the bottom is the last script to run (setup.js
// is loaded last in index.html). It wires the Apply button, calls
// checkSetup(), and — if the wizard is not needed — kicks off the
// dashboard's initial loads plus periodic pollers.

let setupPollTimer = null;
let setupLastInstallLogLen = 0;

async function checkSetup(){
  try {
    const r = await fetch('/api/setup/status');
    const p = await r.json();
    if(p.needed && !p.applied){
      document.getElementById('setup-overlay').classList.remove('hidden');
      if(p.running){
        // Already running (e.g. page reload mid-setup) — attach poller.
        document.getElementById('setup-phase-area').classList.remove('hidden');
        renderSetupProgress(p);
        if(!setupPollTimer) setupPollTimer = setInterval(pollSetupStatus, 800);
      } else if(p.done){
        renderSetupProgress(p);
      } else {
        // Auto-start the wizard — user never needs to click anything.
        setupAutoStart();
      }
    } else {
      document.getElementById('setup-overlay').classList.add('hidden');
    }
  } catch(e){
    document.getElementById('setup-overlay').classList.add('hidden');
  }
}

async function setupAutoStart(){
  try {
    const r = await fetch('/api/setup/start',{method:'POST'});
    if(!r.ok){ return; } // already running, or error — poll will sort it out
  } catch(e){ return; }
  document.getElementById('setup-phase-area').classList.remove('hidden');
  setupPollTimer = setInterval(pollSetupStatus, 800);
}

async function pollSetupStatus(){
  try {
    const r = await fetch('/api/setup/status');
    const p = await r.json();
    renderSetupProgress(p);
    if(p.done || p.reboot_needed || (p.error && !p.running)){
      clearInterval(setupPollTimer);
      setupPollTimer = null;
    }
  } catch(e){}
}

async function doReboot(){
  const btn = document.getElementById('btn-reboot');
  const status = document.getElementById('reboot-status');
  btn.disabled = true;
  status.textContent = 'Sending reboot command…';
  try {
    await fetch('/api/system/reboot', {method:'POST'});
  } catch(e){}
  status.textContent = 'Rebooting… this page will reload automatically.';
  // Poll until the server comes back, then reload.
  const poll = setInterval(async () => {
    try {
      await fetch('/api/ping');
      clearInterval(poll);
      location.reload();
    } catch(e){}
  }, 3000);
}

function setupPhaseBadge(phase, label){
  const cls = phase === 'n/a' ? 'phase-na' : 'phase-'+phase;
  return '<span class="phase-badge '+cls+'">'+(label||phase)+'</span>';
}

function renderSetupProgress(p){
  // ── Phase status line ───────────────────────────────────────────────────
  const phaseArea = document.getElementById('setup-phase-area');
  const fanTablePhases = ['scanning_fans','detecting_rpm','calibrating','finalizing'];
  const showingFans = fanTablePhases.includes(p.phase) || (p.done && !p.error);

  if(p.running || (p.phase && !p.done)){
    phaseArea.classList.remove('hidden');
    const statusEl = document.getElementById('setup-phase-status');
    statusEl.textContent = p.phase_msg || p.phase || '';

    // Board line — shown once board is known
    const boardEl = document.getElementById('setup-board-line');
    if(p.board){
      boardEl.textContent = p.board;
      boardEl.classList.remove('hidden');
    }

    // Chip line — shown during driver install
    const chipEl = document.getElementById('setup-chip-line');
    if(p.chip_name){
      chipEl.textContent = 'Installing driver for ' + p.chip_name + '…';
      chipEl.classList.remove('hidden');
    } else {
      chipEl.classList.add('hidden');
    }

    // Install log — shown only during installing_driver phase
    const logEl = document.getElementById('setup-install-log');
    const lines = p.install_log || [];
    if(p.phase === 'installing_driver' && lines.length > 0){
      logEl.classList.remove('hidden');
      for(let i = setupLastInstallLogLen; i < lines.length; i++){
        const ln = document.createElement('div');
        ln.className = 'log-line';
        ln.textContent = lines[i];
        logEl.appendChild(ln);
      }
      setupLastInstallLogLen = lines.length;
      logEl.scrollTop = logEl.scrollHeight;
    } else if(p.phase !== 'installing_driver'){
      setupLastInstallLogLen = 0; // reset for next potential install
    }
  }

  // ── Fan table ───────────────────────────────────────────────────────────
  const progressArea = document.getElementById('setup-progress-area');
  if(showingFans){
    progressArea.classList.remove('hidden');
    const tbody = document.getElementById('setup-fan-tbody');
    tbody.innerHTML = '';
    for(const f of (p.fans||[])){
      const row = document.createElement('tr');

      let detectHtml;
      switch(f.detect_phase){
        case 'n/a':       detectHtml = setupPhaseBadge('na','n/a'); break;
        case 'pending':   detectHtml = setupPhaseBadge('pending','pending'); break;
        case 'detecting': detectHtml = setupPhaseBadge('detecting','detecting…'); break;
        case 'found':     detectHtml = setupPhaseBadge('found','detected'); break;
        case 'none':      detectHtml = setupPhaseBadge('none','none'); break;
        default:          detectHtml = setupPhaseBadge('pending', f.detect_phase||'—');
      }

      let calHtml = setupPhaseBadge(f.cal_phase||'pending', f.cal_phase||'pending');
      if(f.cal_phase==='calibrating'){
        const pct = f.cal_progress||0;
        // data-width drives a post-render style.width assignment so
        // this cell carries no inline style="..." attribute — the CSP
        // forbids it once 'unsafe-inline' is dropped from style-src.
        calHtml += '<div class="setup-prog-bar"><div class="fill" data-width="'+pct+'"></div></div>';
      }

      let result = '—';
      if(f.cal_phase==='done'){
        result = 'start '+f.start_pwm+'/255 ('+Math.round(f.start_pwm/255*100)+'%)';
        if(f.type!=='nvidia' && f.max_rpm) result += ', '+f.max_rpm+' RPM';
      } else if(f.cal_phase==='error'){
        result = '<span class="cal-err">'+esc(f.error)+'</span>';
      }

      row.innerHTML = '<td>'+esc(f.name)+'</td><td>'+esc(f.type)+'</td><td>'+detectHtml+'</td><td>'+calHtml+'</td><td>'+result+'</td>';
      tbody.appendChild(row);
    }
    // Dynamic widths (cal-prog-bar fills) are applied here rather
    // than inline so the HTML stays free of style="..." attrs.
    tbody.querySelectorAll('[data-width]').forEach(el => {
      el.style.width = el.dataset.width + '%';
    });
  } else {
    progressArea.classList.add('hidden');
  }

  // ── Reboot required ─────────────────────────────────────────────────────
  const rebootPanel = document.getElementById('setup-reboot-panel');
  if(p.reboot_needed){
    document.getElementById('setup-reboot-msg').textContent = p.reboot_message || '';
    rebootPanel.classList.remove('hidden');
    phaseArea.classList.add('hidden');
  } else {
    rebootPanel.classList.add('hidden');
  }

  // ── Error ───────────────────────────────────────────────────────────────
  const errEl = document.getElementById('setup-error');
  if(p.error){
    errEl.textContent = 'Setup failed: '+p.error;
    errEl.classList.remove('hidden');
    progressArea.classList.remove('hidden');
  } else {
    errEl.classList.add('hidden');
  }

  // ── Done — show Apply button ────────────────────────────────────────────
  if(p.done && !p.error && p.config){
    document.getElementById('setup-phase-status').textContent = 'Setup complete — review and apply your configuration.';
    document.getElementById('setup-chip-line').classList.add('hidden');
    document.getElementById('setup-done-area').classList.remove('hidden');
    renderSetupSummary(p);
  }
}


function renderSetupSummary(p){
  const cfg = p.config || {};
  const prof = p.profile || {};
  let html = '';

  // Hardware profile section
  if(prof.cpu_model || prof.gpu_model){
    html += '<div class="hw-profile">';
    if(prof.cpu_model){
      let s = '<strong>CPU:</strong> <span class="val">'+esc(prof.cpu_model)+'</span>';
      if(prof.cpu_tdp_w)     s += '  |  TDP <span class="val">'+prof.cpu_tdp_w+'W</span>';
      if(prof.cpu_thermal_c) s += '  |  TjMax <span class="val">'+prof.cpu_thermal_c+'°C</span>';
      html += '<div class="hw-profile-row">'+s+'</div>';
    }
    if(prof.gpu_model){
      let s = '<strong>GPU:</strong> <span class="val">'+esc(prof.gpu_model)+'</span>';
      if(prof.gpu_power_w)   s += '  |  Power limit <span class="val">'+prof.gpu_power_w+'W</span>';
      if(prof.gpu_thermal_c) s += '  |  Thermal limit <span class="val">'+prof.gpu_thermal_c+'°C</span>';
      html += '<div class="hw-profile-row">'+s+'</div>';
    }
    html += '</div>';
  }

  // Curve design notes
  if(prof.curve_notes && prof.curve_notes.length){
    html += '<div class="curve-notes"><span class="hdr">Curve design</span><ul>';
    for(const n of prof.curve_notes) html += '<li>'+esc(n)+'</li>';
    html += '</ul></div>';
  }

  // Sensors
  if((cfg.sensors||[]).length){
    html += '<h3>Sensors</h3><ul>';
    for(const s of cfg.sensors||[]) html += '<li>Sensor <span>'+esc(s.name)+'</span> ('+s.type+')</li>';
    html += '</ul>';
  }

  // Fans
  if((cfg.fans||[]).length){
    html += '<h3>Fans ('+cfg.fans.length+')</h3><ul>';
    for(const f of cfg.fans||[]){
      let s = '<li>Fan <span>'+esc(f.name)+'</span> ('+f.type+')';
      if(f.min_pwm) s += ' — min '+f.min_pwm+'/255 ('+Math.round(f.min_pwm/255*100)+'%)';
      html += s+'</li>';
    }
    html += '</ul>';
  }

  // Curves
  if((cfg.curves||[]).length){
    html += '<h3>Curves</h3><ul>';
    for(const c of cfg.curves||[]){
      let s = '<li>Curve <span>'+esc(c.name)+'</span> ('+c.type+')';
      if(c.sensor) s += ' → sensor <span>'+esc(c.sensor)+'</span>';
      if(c.min_temp!=null && c.max_temp!=null) s += ', <span>'+c.min_temp+'–'+c.max_temp+'°C</span>';
      if(c.min_pwm!=null && c.max_pwm!=null)   s += ', PWM <span>'+c.min_pwm+'–'+c.max_pwm+'</span>';
      html += s+'</li>';
    }
    html += '</ul>';
  }

  document.getElementById('setup-summary').innerHTML = html;
}

async function setupApply(){
  document.getElementById('btn-setup-apply').disabled = true;
  document.getElementById('setup-apply-hint').textContent = 'Saving…';
  try {
    const r = await fetch('/api/setup/apply',{method:'POST'});
    if(!r.ok){ throw new Error(await r.text()); }
  } catch(e){
    document.getElementById('btn-setup-apply').disabled = false;
    document.getElementById('setup-apply-hint').textContent = 'Error: '+e.message;
    return;
  }
  // Config saved — daemon is restarting. Show status and poll until it's back.
  document.getElementById('setup-actions').classList.add('hidden');
  document.getElementById('setup-restarting').classList.remove('hidden');
  let dots = 1;
  const dotsEl = document.getElementById('setup-restart-dots');
  const dotsTimer = setInterval(()=>{ dots=(dots%3)+1; dotsEl.textContent='.'.repeat(dots); }, 500);
  // Wait briefly for the daemon to begin its shutdown, then poll /api/ping.
  // We use /api/ping (unauthenticated) because the session cookie is lost
  // when the daemon restarts with a fresh in-memory session store.
  await new Promise(r=>setTimeout(r,1200));
  const poll = setInterval(async()=>{
    try {
      const r = await fetch('/api/ping');
      if(r.ok){
        clearInterval(poll);
        clearInterval(dotsTimer);
        // Daemon is up — reload so the browser lands on the login page.
        window.location.reload();
      }
    } catch(_){}
  }, 800);
}

// ── Init ──
//
// setup.js is the last deferred script; by now state/api/render/curve-
// editor have all run. Wire the Apply button, then either show the
// wizard or boot the dashboard pollers.
document.getElementById('btn-apply').addEventListener('click',applyConfig);
checkSetup().then(()=>{
  const overlay = document.getElementById('setup-overlay');
  if(overlay.classList.contains('hidden')){
    // Normal mode: load dashboard immediately.
    loadConfig(); loadStatus(); loadHardware(); loadCalibration(); loadHwdiag();
    if (typeof loadDiagnosticsForBanner === 'function') loadDiagnosticsForBanner();

    // Status updates: prefer SSE (/api/events) for live frames. Start
    // the 2s poll as a fallback, and let the SSE handlers swap between
    // the two — onOpen clears the poll when the stream is proven
    // working; onFallback restarts it if the browser/proxy drops SSE.
    let statusPollId = setInterval(loadStatus, 2000);
    openEventStream(
      () => { if(statusPollId){ clearInterval(statusPollId); statusPollId = 0; } },
      () => { if(!statusPollId){ statusPollId = setInterval(loadStatus, 2000); } }
    );

    setInterval(loadHardware,3000);
    setInterval(loadCalibration,5000);
    setInterval(loadHwdiag,10000);
    if (typeof loadDiagnosticsForBanner === 'function') {
      setInterval(loadDiagnosticsForBanner, 30000);
    }
  }
});
