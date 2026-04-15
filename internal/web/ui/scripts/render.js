"use strict";

// render.js — dashboard rendering, event delegation, theme/sidebar,
// and hwdiag UI.
//
// This module owns every DOM write that happens *outside* the curve
// editor and the setup wizard. It consumes the globals populated by
// api.js (cfg/sts/hw/calStatuses/calResults) and invokes the rendering
// helpers that curve-editor.js and setup.js expose. Event delegators
// at the bottom route every user interaction through named handlers
// declared across the four sibling scripts — because all scripts load
// with `defer` in a fixed order, each handler is in scope by the time
// the first click can fire.

function render(){
  renderSensorBar(); renderSensorCards(); renderFanCards(); renderCurveCards(); renderEditor(); renderHardware();
}

function renderSensorBar(){
  if(!sts) return;
  const parts = sts.sensors.map(s => {
    // Temperatures get the sensor-val class plus the tempClass
    // heat-colour modifier (tc-cool/warm/hot/crit); non-temp readings
    // carry only sensor-val (which leaves them at the default teal
    // palette). The sensor-name and dot separator classes live in
    // app.css under .sys-status so they only apply here.
    const cls = s.unit==='°C' ? tempClass(s.value) : '';
    const valCls = cls ? 'sensor-val '+cls : 'sensor-val';
    const val = '<span class="'+valCls+'">'+fmtSensorVal(s.value, s.unit)+'</span>';
    return '<span class="sensor-name">'+esc(s.name)+'</span> '+val;
  });
  const dot = '<span class="dot"> · </span>';
  document.getElementById('sys-status').innerHTML = parts.join(dot);
}

function renderHardware(){
  if(!hw || !hw.length){
    // The hw-empty class on the container already provides the
    // fg3 colour + 0.75rem sizing; replace the child span with a
    // plain-text message.
    document.getElementById('hw-devices').innerHTML='No devices found';
    return;
  }
  // Build set of already-added sensor paths+metrics for dedup
  const added = new Set();
  if(cfg && cfg.sensors){
    cfg.sensors.forEach(s => added.add(s.type+'|'+(s.path||'')+'|'+(s.metric||'')));
  }

  const valClass = r => {
    if(r.unit==='°C') return 'temp';
    if(r.unit==='RPM') return 'fan';
    if(r.unit==='V') return 'volt';
    if(r.unit==='W') return 'power';
    if(r.unit==='%') return 'pct';
    if(r.unit==='MHz') return 'clock';
    return '';
  };
  const fmtVal = r => {
    if(r.unit==='°C') return r.value.toFixed(1)+'°C';
    if(r.unit==='RPM') return Math.round(r.value)+' RPM';
    if(r.unit==='V') return r.value.toFixed(3)+' V';
    if(r.unit==='W') return r.value.toFixed(1)+' W';
    if(r.unit==='%') return Math.round(r.value)+'%';
    if(r.unit==='MHz') return Math.round(r.value)+' MHz';
    return r.value+' '+r.unit;
  };
  const renderReading = r => {
    const akey = r.sensor_type+'|'+r.sensor_path+'|'+(r.metric||'');
    const isAdded = added.has(akey);
    const btnData = 'data-st="'+esc(r.sensor_type)+'" data-sp="'+esc(r.sensor_path)+'" '+
      'data-mt="'+esc(r.metric||'')+'" data-lbl="'+esc(r.label)+'"';
    const btn = '<button class="add-sensor-btn'+(isAdded?' added':'')+'" '+
      (isAdded?'disabled ':'')+btnData+
      (isAdded?'' : ' data-action="add-sensor"')+
      '>'+(isAdded
        ? '<svg class="icon" aria-hidden="true"><use href="/ui/icons/sprite.svg#check"/></svg>'
        : '<svg class="icon" aria-hidden="true"><use href="/ui/icons/sprite.svg#plus"/></svg>')+
      '</button>';
    const vc = r.unit==='°C' ? tempClass(r.value) : valClass(r);
    return '<div class="hw-reading">'+
      '<span class="lbl">'+esc(r.label)+'</span>'+
      '<span class="val '+vc+'">'+fmtVal(r)+'</span>'+
      btn+
    '</div>';
  };

  // Reading groups shown in display order; only non-empty groups get a subheading.
  const readingGroups = [
    { label: 'Temperatures', test: r => r.unit === '\u00b0C' },
    { label: 'Fan Speeds',   test: r => r.unit === 'RPM' },
    { label: 'Voltages',     test: r => r.unit === 'V' },
    { label: 'Power',        test: r => r.unit === 'W' },
    { label: 'Utilization',  test: r => r.unit === '%' },
    { label: 'Clocks',       test: r => r.unit === 'MHz' },
  ];

  document.getElementById('hw-devices').innerHTML = hw.map(dev => {
    const key = dev.path;
    const collapsed = hwCollapsed[key];

    let readingsHtml = '';
    const needSubhdrs = readingGroups.filter(g => dev.readings.some(g.test)).length > 1;
    readingGroups.forEach(grp => {
      const group = dev.readings.filter(grp.test);
      if(!group.length) return;
      if(needSubhdrs){
        readingsHtml += '<div class="hw-subhdr">'+grp.label+'</div>';
      }
      readingsHtml += group.map(renderReading).join('');
    });

    return '<div class="hw-device">'+
      '<div class="hw-device-name" data-action="toggle-hw" data-key="'+esc(key)+'">'+
        '<span>'+esc(dev.name)+'</span>'+
        '<span class="toggle"><svg class="icon" aria-hidden="true"><use href="/ui/icons/sprite.svg#'+(collapsed?'chevron-right':'chevron-down')+'"/></svg></span>'+
      '</div>'+
      '<div class="hw-readings'+(collapsed?' collapsed':'')+'">'+readingsHtml+'</div>'+
    '</div>';
  }).join('');
}

function toggleHw(key){
  hwCollapsed[key]=!hwCollapsed[key];
  renderHardware();
}

function addSensorFromReading(btn){
  if(!cfg) return;
  const st = btn.dataset.st;
  const sp = btn.dataset.sp;
  const mt = btn.dataset.mt;
  const lbl = btn.dataset.lbl;
  // Generate unique name
  let nm = lbl.toLowerCase().replace(/[^a-z0-9]+/g,'_').replace(/^_|_$/g,'');
  if(!nm) nm = 'sensor';
  let base = nm, n = 1;
  while(cfg.sensors.some(s=>s.name===nm)) nm = base+'_'+n++;
  const s = {name:nm, type:st, path:sp};
  if(mt) s.metric = mt;
  cfg.sensors.push(s);
  markDirty(); renderSensorCards(); renderHardware();
  notify('Added sensor "'+nm+'" — rename it in the Sensors section','ok');
}

function renderSensorCards(){
  if(!cfg) return;
  document.getElementById('sensor-cards').innerHTML = cfg.sensors.map((s,i) => {
    const st = sts ? sts.sensors.find(x=>x.name===s.name) : null;
    const unit = st ? st.unit : sensorUnit(s);
    const val = st ? fmtSensorVal(st.value, st.unit) : '—';
    const valCls = (st && st.unit==='°C') ? 'sensor-val '+tempClass(st.value) : 'sensor-val';
    const pathDisplay = s.type==='nvidia'
      ? 'GPU '+(s.path||'0')+(s.metric?' · '+s.metric:'')
      : (s.path||'').replace('/sys/class/hwmon/','');
    return '<div class="card">'+
      '<div class="card-name-edit sensor-name-edit">'+
        '<input type="text" value="'+esc(s.name)+'" '+
          'data-orig="'+esc(s.name)+'" '+
          'data-action="rename-sensor" data-idx="'+i+'" '+
          'title="Click to rename">'+
        '<span class="edit-icon"><svg class="icon" aria-hidden="true"><use href="/ui/icons/sprite.svg#pencil"/></svg></span>'+
      '</div>'+
      '<div class="sensor-path">'+esc(pathDisplay)+'</div>'+
      '<div class="'+valCls+'">'+val+'</div>'+
      '<div class="sensor-actions">'+
        '<button class="danger" data-action="delete-sensor" data-idx="'+i+'" title="Remove sensor"><svg class="icon" aria-hidden="true"><use href="/ui/icons/sprite.svg#trash-2"/></svg></button>'+
      '</div>'+
    '</div>';
  }).join('');
}

function renderFanCards(){
  if(!cfg) return;
  document.getElementById('fan-cards').innerHTML = cfg.controls.map((ctrl,i) => {
    const st = sts ? sts.fans.find(f=>f.name===ctrl.fan) : null;
    const rpm = st && st.rpm!=null ? st.rpm+' RPM' : '\u2014';
    const duty = st ? Math.round(st.duty_pct) : 0;
    const isManual = ctrl.manual_pwm != null;
    const manualPct = isManual ? Math.round(ctrl.manual_pwm/255*100) : 50;

    // find fan config for pwm_path (needed for calibrate)
    const fanCfg = cfg.fans ? cfg.fans.find(f=>f.name===ctrl.fan) : null;
    const pwmPath = fanCfg ? fanCfg.pwm_path : '';

    // calibration state
    const calSt = pwmPath ? calStatuses[pwmPath] : null;
    const calRes = pwmPath ? calResults[pwmPath] : null;
    const isCalibrating = calSt && calSt.running;

    let controlRow = '';
    if(isManual){
      controlRow = '<div class="manual-slider">'+
        '<input type="range" min="0" max="100" step="1" value="'+manualPct+'" '+
          'data-action="manual-pwm" data-idx="'+i+'">'+
        '<span class="manual-pct">'+manualPct+'%</span>'+
      '</div>';
    } else {
      const opts = cfg.curves.map(c =>
        '<option value="'+esc(c.name)+'"'+(c.name===ctrl.curve?' selected':'')+'>'+esc(c.name)+'</option>'
      ).join('');
      controlRow = '<select data-action="ctrl-curve" data-idx="'+i+'">'+opts+'</select>';
    }

    let calSection = '';
    if(isCalibrating){
      // Dynamic width (0–100%) is applied via el.style.width after
      // innerHTML so nothing inline lands in the markup.
      calSection = '<div class="cal-running"><svg class="icon icon-spin" aria-hidden="true"><use href="/ui/icons/sprite.svg#loader"/></svg> Calibrating\u2026 PWM '+calSt.current_pwm+'</div>'+
        '<div class="cal-prog-bar"><div class="fill" data-width="'+calSt.progress+'"></div></div>';
    } else {
      const calBtnDisabled = isCalibrating || !pwmPath;
      calSection = '<button class="cal-btn" '+(calBtnDisabled?'disabled ':'')+
        'data-action="calibrate" data-pwm="'+esc(pwmPath)+'" title="Measure start PWM and max RPM"><svg class="icon" aria-hidden="true"><use href="/ui/icons/sprite.svg#play"/></svg> Calibrate</button>';
    }
    let calResultRow = '';
    if(calRes && !isCalibrating){
      const sp = calRes.start_pwm ? Math.round(calRes.start_pwm/255*100)+'%' : '\u2014';
      const isNvidiaFan = fanCfg && fanCfg.type === 'nvidia';
      const mr = calRes.max_rpm
        ? (isNvidiaFan ? Math.round(calRes.max_rpm/255*100)+'%' : calRes.max_rpm+' RPM')
        : '\u2014';
      calResultRow = '<div class="cal-result">Start: '+sp+' \u00b7 Max: '+mr+'</div>';
    }

    const hasRPM = fanCfg && (fanCfg.rpm_path || fanCfg.type !== 'nvidia');
    const detectDisabled = !pwmPath || (fanCfg && fanCfg.type==='nvidia') || isCalibrating;
    const detectBtn = '<button class="detect-btn" '+
      (detectDisabled?'disabled ':'')+
      'data-action="detect-rpm" data-pwm="'+esc(pwmPath)+'" data-idx="'+i+'" '+
      'title="Auto-detect RPM sensor"><svg class="icon" aria-hidden="true"><use href="/ui/icons/sprite.svg#search"/></svg></button>';

    const dCls = dutyClass(duty);
    return '<div class="card">'+
      '<div class="card-name-edit">'+
        '<input type="text" value="'+esc(ctrl.fan)+'" '+
          'data-orig="'+esc(ctrl.fan)+'" '+
          'data-action="rename-fan" data-idx="'+i+'" '+
          'title="Click to rename">'+
        '<span class="edit-icon"><svg class="icon" aria-hidden="true"><use href="/ui/icons/sprite.svg#pencil"/></svg></span>'+
      '</div>'+
      '<div class="fan-meta">'+
        '<div class="fan-rpm">'+rpm+'</div>'+
        detectBtn+
      '</div>'+
      '<div class="fan-duty">'+
        '<div class="duty-bar"><div class="fill '+dCls+'" data-width="'+duty+'"></div></div>'+
        '<span class="'+dCls+'">'+duty+'%</span>'+
      '</div>'+
      '<div class="mode-toggle">'+
        '<button class="mode-btn'+(isManual?'':' active')+'" data-action="set-mode" data-idx="'+i+'" data-manual="0">Curve</button>'+
        '<button class="mode-btn'+(isManual?' active':'')+'" data-action="set-mode" data-idx="'+i+'" data-manual="1">Manual</button>'+
      '</div>'+
      controlRow+
      calSection+
      calResultRow+
    '</div>';
  }).join('');

  // Apply dynamic widths — anything that varies per render goes via
  // element.style assignment rather than an inline style= attribute,
  // which would keep style-src 'unsafe-inline' pinned. Element-level
  // style assignment is not affected by CSP.
  document.querySelectorAll('#fan-cards [data-width]').forEach(el => {
    el.style.width = el.dataset.width + '%';
  });
}

function renderCurveCards(){
  if(!cfg) return;
  document.getElementById('curve-cards').innerHTML = cfg.curves.map((c,i) => {
    let out = '';
    if(c.type==='linear' && sts){
      const sd=sts.sensors.find(s=>s.name===c.sensor);
      if(sd){
        let v;
        if(sd.value<=c.min_temp) v=c.min_pwm;
        else if(sd.value>=c.max_temp) v=c.max_pwm;
        else { const r=(sd.value-c.min_temp)/(c.max_temp-c.min_temp); v=Math.round(c.min_pwm+r*(c.max_pwm-c.min_pwm)); }
        out=fmtSensorVal(sd.value,sd.unit)+' \u2192 '+p2pct(v)+'%';
      }
    } else if(c.type==='fixed'){
      out=p2pct(c.value)+'%';
    } else if(c.type==='mix'){
      out=c.function+'('+(c.sources||[]).join(', ')+')';
    }
    return '<div class="card curve-card'+(i===selIdx?' active':'')+'" data-action="select-curve" data-idx="'+i+'">'+
      '<div class="card-header"><span class="card-name">'+esc(c.name)+'</span>'+
        '<span class="type-badge type-'+c.type+'">'+c.type.toUpperCase()+'</span></div>'+
      miniSVG(c)+
      '<div class="card-output">'+out+'</div>'+
    '</div>';
  }).join('');
}

function miniSVG(c){
  if(c.type==='linear'){
    const y1=38-(c.min_pwm/255)*33, y2=38-(c.max_pwm/255)*33;
    return '<svg viewBox="0 0 100 42" class="mini-graph">'+
      '<polyline points="0,'+y1+' '+c.min_temp+','+y1+' '+c.max_temp+','+y2+' 100,'+y2+
      '" class="svg-stroke-teal" fill="none" stroke-width="2" stroke-linecap="round"/></svg>';
  }
  if(c.type==='fixed'){
    const y=38-(c.value/255)*33;
    return '<svg viewBox="0 0 100 42" class="mini-graph">'+
      '<line x1="0" y1="'+y+'" x2="100" y2="'+y+'" class="svg-stroke-blue" stroke-width="2"/></svg>';
  }
  return '';
}

// ── Sensor rename / delete ──

function renameSensor(idx, el){
  if(!cfg) return;
  const newName = el.value.trim();
  const orig = el.dataset.orig;
  if(!newName){ el.value=orig; return; }
  if(orig===newName) return;
  if(cfg.sensors.some(s=>s.name===newName && s.name!==orig)){
    notify('Sensor name "'+newName+'" already exists','error'); el.value=orig; return;
  }
  cfg.sensors[idx].name = newName;
  el.dataset.orig = newName;
  // Update curve references
  cfg.curves.forEach(c=>{ if(c.sensor===orig) c.sensor=newName; });
  markDirty(); renderCurveCards(); renderHardware();
}

function deleteSensor(idx){
  if(!cfg) return;
  const nm = cfg.sensors[idx].name;
  const used = cfg.curves.find(c=>c.sensor===nm);
  if(used){ notify('"'+nm+'" is used by curve "'+used.name+'"','error'); return; }
  cfg.sensors.splice(idx,1);
  markDirty(); renderSensorCards(); renderHardware(); renderCurveCards(); renderEditor();
}

// ── Fan rename ──

function renameFan(ctrlIdx, el){
  if(!cfg) return;
  const newName = el.value.trim();
  const orig = el.dataset.orig;
  if(!newName){ el.value=orig; return; }
  if(orig===newName) return;
  if(cfg.fans.some(f=>f.name===newName && f.name!==orig)){
    notify('Fan name "'+newName+'" already exists','error'); el.value=orig; return;
  }
  const fan = cfg.fans.find(f=>f.name===orig);
  if(!fan){ el.value=orig; return; }
  fan.name = newName;
  cfg.controls[ctrlIdx].fan = newName;
  el.dataset.orig = newName;
  markDirty();
}

// ── Manual PWM ──

function setManualMode(i, isManual){
  if(!cfg) return;
  if(isManual){
    cfg.controls[i].manual_pwm = 128; // default 50%
  } else {
    delete cfg.controls[i].manual_pwm;
  }
  markDirty(); renderFanCards();
}

function updManualPWM(i, pct, sliderEl){
  if(!cfg) return;
  cfg.controls[i].manual_pwm = Math.round(pct/100*255);
  // Update the sibling span without re-rendering
  if(sliderEl){
    const span = sliderEl.nextElementSibling;
    if(span) span.textContent = pct+'%';
  }
  markDirty();
}

// ── Calibration ──

async function startCalibration(pwmPath){
  if(!pwmPath) return;
  try {
    const r = await fetch('/api/calibrate/start?fan='+encodeURIComponent(pwmPath),{method:'POST'});
    if(r.ok){
      notify('Calibration started — fan will ramp up over ~40s','ok');
      loadCalibration();
      // poll more frequently while calibrating
      const poll = setInterval(async()=>{
        await loadCalibration();
        const st = calStatuses[pwmPath];
        if(!st || !st.running) clearInterval(poll);
      }, 3000);
    } else { notify(await r.text(),'error'); }
  } catch(e){ notify('Calibrate: '+e.message,'error'); }
}

async function detectRPM(pwmPath, ctrlIdx, btn){
  if(!pwmPath || !cfg) return;
  // Capture/restore innerHTML rather than textContent because the button
  // now holds an inline <svg><use …></svg> icon, not bare text.
  const orig = btn.innerHTML;
  btn.disabled = true;
  btn.innerHTML = '<svg class="icon icon-spin" aria-hidden="true"><use href="/ui/icons/sprite.svg#loader"/></svg>';
  notify('Detecting RPM sensor\u2026 (~5s)', 'ok');
  try {
    const r = await fetch('/api/detect-rpm?fan='+encodeURIComponent(pwmPath), {method:'POST'});
    if(!r.ok){ notify(await r.text(),'error'); return; }
    const res = await r.json();
    if(!res.rpm_path){
      notify('No RPM sensor correlated with this fan \u2014 check wiring or fan type','error');
      return;
    }
    // Update the fan config with the detected rpm_path.
    const fan = cfg.fans.find(f=>f.name===cfg.controls[ctrlIdx].fan);
    if(fan){
      fan.rpm_path = res.rpm_path;
      // Show a short human-readable name: last two path segments
      const parts = res.rpm_path.split('/');
      const label = parts.slice(-2).join('/');
      notify('Detected: '+label+' (\u0394'+res.delta+' RPM) \u2014 hit Apply to save','ok');
      markDirty(); renderFanCards();
    }
  } catch(e){ notify('Detect: '+e.message,'error'); }
  finally { btn.disabled=false; btn.innerHTML=orig; }
}

// ── Hardware diagnostics render ──

function renderHwdiag(entries){
  const section = document.getElementById('hwdiag-section');
  const panel = document.getElementById('hwdiag-panel');
  if(!section || !panel) return;
  if(!entries.length){
    section.classList.add('hidden');
    panel.innerHTML = '';
    return;
  }
  section.classList.remove('hidden');
  // Group by component.
  const groups = {};
  entries.forEach(e => { (groups[e.component] = groups[e.component] || []).push(e); });
  const compOrder = Object.keys(groups).sort();
  panel.innerHTML = compOrder.map(c => {
    const label = COMPONENT_LABELS[c] || c;
    const items = groups[c].map(hwdiagItemHTML).join('');
    return '<div class="hwdiag-group"><div class="hwdiag-group-hdr">'+esc(label)+'</div>'+items+'</div>';
  }).join('');
  panel.querySelectorAll('[data-hwdiag-fix]').forEach(btn => {
    btn.addEventListener('click', () => hwdiagRunRemediation(btn.dataset.hwdiagEndpoint, btn.dataset.hwdiagFix, btn));
  });
}

function hwdiagItemHTML(e){
  const sev = e.severity || 'info';
  const rem = e.remediation;
  let btn = '';
  if(rem && rem.label){
    const disabled = rem.endpoint ? '' : ' disabled title="Remediation endpoint not wired yet (TODO)"';
    btn = '<button class="hwdiag-fix" data-hwdiag-fix="'+esc(rem.auto_fix_id||'')+'" data-hwdiag-endpoint="'+esc(rem.endpoint||'')+'"'+disabled+'>'+esc(rem.label)+'</button>';
  }
  const detail = e.detail ? '<div class="hwdiag-detail">'+esc(e.detail)+'</div>' : '';
  const affected = (e.affected && e.affected.length)
    ? '<div class="hwdiag-affected">'+e.affected.map(esc).join(', ')+'</div>' : '';
  return '<div class="hwdiag-item hwdiag-'+esc(sev)+'">'
       +   '<div class="hwdiag-row">'
       +     '<span class="hwdiag-sev">'+esc(sev.toUpperCase())+'</span>'
       +     '<span class="hwdiag-summary">'+esc(e.summary||e.id)+'</span>'
       +     btn
       +   '</div>'
       +   detail + affected
       + '</div>';
}

// ── Event delegation ──
//
// Every rendered template uses `data-action="..."` instead of an inline
// `on*=` attribute so the CSP can drop `script-src 'unsafe-inline'`.
// Listeners are installed once on document and dispatch by action name
// + element type. Handler bodies live across render.js, curve-editor.js,
// and setup.js; because all four scripts load with `defer` in a fixed
// order, each function is in scope by the time a click can fire.

// click: buttons, card bodies, icons, anchors.
document.addEventListener('click', (e) => {
  const el = e.target.closest('[data-action]');
  if (!el) return;
  const action = el.dataset.action;
  switch (action) {
    // ── sensor group ──
    case 'delete-sensor':
      deleteSensor(+el.dataset.idx);
      break;
    case 'add-sensor':
      addSensorFromReading(el);
      break;
    // ── fan group ──
    case 'calibrate':
      startCalibration(el.dataset.pwm);
      break;
    case 'detect-rpm':
      detectRPM(el.dataset.pwm, +el.dataset.idx, el);
      break;
    case 'set-mode':
      setManualMode(+el.dataset.idx, el.dataset.manual === '1');
      break;
    // ── curve card group ──
    case 'select-curve':
      selectCurve(+el.dataset.idx);
      break;
    // ── curve editor group ──
    case 'delete-curve':
      deleteCurve();
      break;
    // ── header / sidebar group ──
    case 'toggle-theme':
      toggleTheme();
      break;
    case 'toggle-sidebar':
      toggleSidebar();
      break;
    case 'toggle-hw':
      toggleHw(el.dataset.key);
      break;
    case 'add-curve':
      addCurve(el.dataset.type);
      break;
    case 'auto-curve':
      autoCurve();
      break;
    // ── modals + setup wizard ──
    case 'open-settings':
      openSettings();
      break;
    case 'close-settings':
      closeSettings();
      break;
    case 'confirm-reset':
      confirmReset();
      break;
    case 'reboot':
      doReboot();
      break;
    case 'setup-apply':
      setupApply();
      break;
  }
});

// input: range sliders and any other live-commit inputs.
document.addEventListener('input', (e) => {
  const el = e.target;
  if (!(el instanceof HTMLElement) || !el.dataset || !el.dataset.action) return;
  const action = el.dataset.action;
  switch (action) {
    // ── fan group ──
    case 'manual-pwm':
      updManualPWM(+el.dataset.idx, +el.value, el);
      break;
    // ── curve editor group ──
    case 'fixed-pct':
      // range slider live-updates through input; the number input's
      // commit also fires change (below) and both routes call the
      // same updFixedPct — re-rendering is idempotent.
      updFixedPct(+el.value);
      break;
  }
});

// change: selects and number inputs that commit on blur/submit.
document.addEventListener('change', (e) => {
  const el = e.target;
  if (!(el instanceof HTMLElement) || !el.dataset || !el.dataset.action) return;
  const action = el.dataset.action;
  switch (action) {
    // ── fan group ──
    case 'ctrl-curve':
      updCtrl(+el.dataset.idx, el.value);
      break;
    // ── curve editor group ──
    case 'rename-curve':
      renameCurve(el.value);
      break;
    case 'upd-field':
      updField(el.dataset.field, el.value);
      break;
    case 'upd-field-num':
      updField(el.dataset.field, +el.value);
      break;
    case 'upd-pct-field':
      updPctField(el.dataset.field, +el.value);
      break;
    case 'fixed-pct':
      updFixedPct(+el.value);
      break;
    case 'mix-sources':
      updMixSources();
      break;
  }
});

// blur: rename-on-blur for card name inputs. Blur does not bubble, so
// capture phase is mandatory to catch it at the document level.
document.addEventListener('blur', (e) => {
  const el = e.target;
  if (!(el instanceof HTMLElement) || !el.dataset || !el.dataset.action) return;
  const action = el.dataset.action;
  switch (action) {
    // ── sensor group ──
    case 'rename-sensor':
      renameSensor(+el.dataset.idx, el);
      break;
    // ── fan group ──
    case 'rename-fan':
      renameFan(+el.dataset.idx, el);
      break;
  }
}, true);

// keydown: Enter-to-commit for rename inputs. Any input that carries a
// data-action triggers a blur on Enter so the action fires through the
// blur handler above.
document.addEventListener('keydown', (e) => {
  if (e.key !== 'Enter') return;
  const el = e.target;
  if (!(el instanceof HTMLElement) || el.tagName !== 'INPUT') return;
  if (!el.dataset || !el.dataset.action) return;
  el.blur();
});

// ── Sidebar toggle ──
function toggleSidebar(){
  const sb = document.getElementById('sidebar');
  const collapsed = sb.classList.toggle('collapsed');
  try { localStorage.setItem('ventd-sidebar', collapsed ? '0' : '1'); } catch(_){}
}
(function(){
  try {
    if(localStorage.getItem('ventd-sidebar') === '0'){
      const sb = document.getElementById('sidebar');
      if(sb) sb.classList.add('collapsed');
    }
  } catch(_){}
})();

// ── Theme toggle ──
function applyTheme(theme){
  document.documentElement.setAttribute('data-theme', theme);
  // Show the icon of the *opposite* theme the user can switch to: dark
  // theme offers the sun, light theme offers the moon. Selector targets
  // all toggle buttons (header, setup wizard, any future addition) via
  // data-action so no button-specific class is required.
  const iconName = theme === 'light' ? 'moon' : 'sun';
  const svg = '<svg class="icon" aria-hidden="true"><use href="/ui/icons/sprite.svg#'+iconName+'"/></svg>';
  document.querySelectorAll('[data-action="toggle-theme"]').forEach(b => { b.innerHTML = svg; });
}
function toggleTheme(){
  const next = document.documentElement.getAttribute('data-theme') === 'light' ? 'dark' : 'light';
  applyTheme(next);
  try { localStorage.setItem('ventd-theme', next); } catch(_){}
}
// Apply saved theme immediately (scripts are deferred, so DOM is ready).
(function(){
  let saved = 'dark';
  try { saved = localStorage.getItem('ventd-theme') || 'dark'; } catch(_){}
  applyTheme(saved);
})();
