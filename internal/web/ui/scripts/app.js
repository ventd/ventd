"use strict";

let cfg = null, sts = null, hw = null, selIdx = -1, dirty = false, dragging = null;
let hwCollapsed = {}, calStatuses = {}, calResults = {};

const G = {l:40, r:490, t:15, b:230};
G.w = G.r-G.l; G.h = G.b-G.t;
// x-axis is sensor value (0-100 for temp/pct, 0-1000 for others — scaled to 0-100 display)
function v2x(v){ return G.l+(Math.min(v,100)/100)*G.w; }
function p2y(p){ return G.b-(p/255)*G.h; }
function x2v(x){ return Math.round(Math.max(0,Math.min(100,(x-G.l)/G.w*100))); }
function y2p(y){ let pct=Math.round(Math.max(0,Math.min(100,(G.b-y)/G.h*100))); return Math.round(pct/100*255); }
function esc(s){ return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }
function p2pct(p){ return isNaN(p) ? 0 : Math.round(p/255*100); }
function pct2p(pct){ return isNaN(pct) ? 0 : Math.round(pct/100*255); }

// tempClass returns a CSS class for heat-colouring a temperature value.
function tempClass(val){
  if(val < 60) return 'tc-cool';
  if(val < 75) return 'tc-warm';
  if(val < 90) return 'tc-hot';
  return 'tc-crit';
}

// dutyColor returns a CSS variable reference for a fan duty % (0-100).
// Retained for any legacy caller; prefer dutyClass() inside templates
// so the colour lands via a CSS class and the element carries no inline
// style attribute (which would re-introduce a style-src 'unsafe-inline'
// requirement).
function dutyColor(pct){
  if(pct < 50) return 'var(--teal)';
  if(pct < 75) return 'var(--amber)';
  return 'var(--red)';
}

// dutyClass maps a fan duty % to one of .dc-low / .dc-mid / .dc-high.
// The CSS file maps each class to the same teal/amber/red palette.
function dutyClass(pct){
  if(pct < 50) return 'dc-low';
  if(pct < 75) return 'dc-mid';
  return 'dc-high';
}

function sensorUnit(s){
  if(!s) return '';
  if(s.type==='nvidia'){
    const m={temp:'°C',util:'%',mem_util:'%',power:'W',clock_gpu:'MHz',clock_mem:'MHz',fan_pct:'%'};
    return m[s.metric||'temp']||'';
  }
  const b=(s.path||'').split('/').pop();
  if(b.startsWith('temp')) return '°C';
  if(b.startsWith('in')) return 'V';
  if(b.startsWith('power')) return 'W';
  if(b.startsWith('fan')) return 'RPM';
  return '';
}

function fmtSensorVal(val, unit){
  if(unit==='°C') return val.toFixed(1)+'°C';
  if(unit==='V') return val.toFixed(3)+' V';
  if(unit==='W') return val.toFixed(1)+' W';
  if(unit==='RPM') return Math.round(val)+' RPM';
  if(unit==='MHz') return Math.round(val)+' MHz';
  if(unit==='%') return Math.round(val)+'%';
  return val.toFixed(1)+(unit?' '+unit:'');
}

// ── API ──

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

function markDirty(){
  dirty=true;
  document.getElementById('dirty').hidden=false;
  document.getElementById('btn-apply').disabled=false;
}
function notify(msg,type){
  const el=document.getElementById('notification');
  el.textContent=msg; el.className='notification '+type; el.hidden=false;
  clearTimeout(el._t); el._t=setTimeout(()=>el.hidden=true,5000);
}

// ── Render ──

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
      '>'+(isAdded?'\u2713':'+')+
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
        '<span class="toggle">'+(collapsed?'\u25b6':'\u25bc')+'</span>'+
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
        '<span class="edit-icon">\u270e</span>'+
      '</div>'+
      '<div class="sensor-path">'+esc(pathDisplay)+'</div>'+
      '<div class="'+valCls+'">'+val+'</div>'+
      '<div class="sensor-actions">'+
        '<button class="danger" data-action="delete-sensor" data-idx="'+i+'" title="Remove sensor">&#x2715;</button>'+
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
      calSection = '<div class="cal-running">\u23f3 Calibrating\u2026 PWM '+calSt.current_pwm+'</div>'+
        '<div class="cal-prog-bar"><div class="fill" data-width="'+calSt.progress+'"></div></div>';
    } else {
      const calBtnDisabled = isCalibrating || !pwmPath;
      calSection = '<button class="cal-btn" '+(calBtnDisabled?'disabled ':'')+
        'data-action="calibrate" data-pwm="'+esc(pwmPath)+'" title="Measure start PWM and max RPM">\u25b6 Calibrate</button>';
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
      'title="Auto-detect RPM sensor">\u{1F50D}</button>';

    const dCls = dutyClass(duty);
    return '<div class="card">'+
      '<div class="card-name-edit">'+
        '<input type="text" value="'+esc(ctrl.fan)+'" '+
          'data-orig="'+esc(ctrl.fan)+'" '+
          'data-action="rename-fan" data-idx="'+i+'" '+
          'title="Click to rename">'+
        '<span class="edit-icon">\u270e</span>'+
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
  const orig = btn.textContent;
  btn.disabled = true;
  btn.textContent = '\u23f3';
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
  finally { btn.disabled=false; btn.textContent=orig; }
}

// ── Editor ──

function selectCurve(i){ selIdx=i; renderCurveCards(); renderEditor(); }

function renderEditor(){
  const el=document.getElementById('curve-editor');
  if(!cfg||selIdx<0||selIdx>=cfg.curves.length){ el.innerHTML=''; return; }
  const c=cfg.curves[selIdx];
  if(c.type==='linear') renderLinearEditor(el,c);
  else if(c.type==='fixed') renderFixedEditor(el,c);
  else if(c.type==='mix') renderMixEditor(el,c);
}

function curveAxisLabel(c){
  if(!cfg || !c.sensor) return 'Value';
  const s = cfg.sensors.find(x=>x.name===c.sensor);
  const u = s ? sensorUnit(s) : '';
  if(u==='°C') return '°C';
  if(u==='%') return '%';
  if(u==='W') return 'W';
  if(u==='V') return 'V';
  if(u==='RPM') return 'RPM';
  if(u==='MHz') return 'MHz';
  return 'Value';
}

function renderLinearEditor(el,c){
  const sOpts=cfg.sensors.map(s =>
    '<option value="'+esc(s.name)+'"'+(s.name===c.sensor?' selected':'')+'>'+esc(s.name)+'</option>'
  ).join('');
  const ax = curveAxisLabel(c);
  el.innerHTML = '<div class="editor">'+
    '<div class="editor-svg"><svg id="curve-svg" viewBox="0 0 510 255" xmlns="http://www.w3.org/2000/svg"></svg></div>'+
    '<div class="editor-form">'+
      '<div class="fg"><label>Name</label><input type="text" value="'+esc(c.name)+'" data-action="rename-curve"></div>'+
      '<div class="fg"><label>Sensor</label><select data-action="upd-field" data-field="sensor">'+sOpts+'</select></div>'+
      '<div class="fg"></div>'+
      '<div class="fg"><label>Min '+ax+'</label><input type="number" id="f-mint" value="'+c.min_temp+'" min="0" max="10000" step="1" data-action="upd-field-num" data-field="min_temp"></div>'+
      '<div class="fg"><label>Max '+ax+'</label><input type="number" id="f-maxt" value="'+c.max_temp+'" min="0" max="10000" step="1" data-action="upd-field-num" data-field="max_temp"></div>'+
      '<div class="fg"></div>'+
      '<div class="fg"><label>Min %</label><input type="number" id="f-minp" value="'+p2pct(c.min_pwm)+'" min="0" max="100" step="1" data-action="upd-pct-field" data-field="min_pwm"></div>'+
      '<div class="fg"><label>Max %</label><input type="number" id="f-maxp" value="'+p2pct(c.max_pwm)+'" min="0" max="100" step="1" data-action="upd-pct-field" data-field="max_pwm"></div>'+
      '<div class="fg"></div>'+
    '</div>'+
    '<div class="editor-actions"><button class="danger" data-action="delete-curve">Delete</button></div>'+
  '</div>';
  drawSVG(c);
}

function renderFixedEditor(el,c){
  const pct=p2pct(c.value||0);
  el.innerHTML = '<div class="editor">'+
    '<div class="editor-form">'+
      '<div class="fg"><label>Name</label><input type="text" value="'+esc(c.name)+'" data-action="rename-curve"></div>'+
      '<div class="fg wide"><label>Speed %</label>'+
        '<div class="fixed-slider">'+
          '<input type="range" min="0" max="100" step="1" value="'+pct+'" data-action="fixed-pct">'+
          '<input type="number" min="0" max="100" step="1" value="'+pct+'" class="num" data-action="fixed-pct">'+
          '<span class="pct">'+pct+'%</span>'+
        '</div></div>'+
    '</div>'+
    '<div class="editor-actions"><button class="danger" data-action="delete-curve">Delete</button></div>'+
  '</div>';
}

function renderMixEditor(el,c){
  const fOpts=['max','min','average'].map(f =>
    '<option value="'+f+'"'+(f===c.function?' selected':'')+'>'+f+'</option>'
  ).join('');
  const avail=cfg.curves.filter(x=>x.name!==c.name);
  const srcs=avail.map(x =>
    '<label><input type="checkbox" value="'+esc(x.name)+'" '+
    ((c.sources||[]).includes(x.name)?'checked':'')+
    ' data-action="mix-sources"> '+esc(x.name)+'</label>'
  ).join('');
  el.innerHTML = '<div class="editor">'+
    '<div class="editor-form">'+
      '<div class="fg"><label>Name</label><input type="text" value="'+esc(c.name)+'" data-action="rename-curve"></div>'+
      '<div class="fg"><label>Function</label><select data-action="upd-field" data-field="function">'+fOpts+'</select></div>'+
      '<div class="fg"><label>Sources (min 2)</label><div class="source-list" id="mix-sources">'+srcs+'</div></div>'+
    '</div>'+
    '<div class="editor-actions"><button class="danger" data-action="delete-curve">Delete</button></div>'+
  '</div>';
}

// ── SVG ──

function drawSVG(c){
  const svg=document.getElementById('curve-svg');
  if(!svg) return;
  const ax = curveAxisLabel(c);
  // Stroke / fill colours land via CSS classes (see .svg-stroke-* and
  // .svg-fill-* in app.css) so no element in this tree carries a
  // style="..." attribute — required for the CSP to drop
  // style-src 'unsafe-inline'.
  let h='<rect width="510" height="255" class="svg-fill-bg" rx="4"/>';

  for(let t=0;t<=100;t+=20){
    const x=v2x(t);
    h+='<line x1="'+x+'" y1="'+G.t+'" x2="'+x+'" y2="'+G.b+'" class="svg-stroke-border2"/>';
    h+='<text x="'+x+'" y="'+(G.b+12)+'" class="svg-fill-fg3 svg-text-mono" font-size="9" text-anchor="middle">'+t+(ax==='°C'?'\u00b0':'')+'</text>';
  }
  const pg=[0,64,128,191,255],pl=['0','25','50','75','100'];
  for(let i=0;i<pg.length;i++){
    const y=p2y(pg[i]);
    h+='<line x1="'+G.l+'" y1="'+y+'" x2="'+G.r+'" y2="'+y+'" class="svg-stroke-border2"/>';
    h+='<text x="'+(G.l-4)+'" y="'+(y+3)+'" class="svg-fill-fg3 svg-text-mono" font-size="9" text-anchor="end">'+pl[i]+'%</text>';
  }

  const x1=v2x(0),y1=p2y(c.min_pwm),x2=v2x(c.min_temp),y2=p2y(c.min_pwm);
  const x3=v2x(c.max_temp),y3=p2y(c.max_pwm),x4=v2x(100),y4=p2y(c.max_pwm);
  h+='<path d="M'+x1+','+y1+' L'+x2+','+y2+'" class="svg-stroke-border" fill="none" stroke-width="1.5" stroke-dasharray="3"/>';
  h+='<line x1="'+x2+'" y1="'+y2+'" x2="'+x3+'" y2="'+y3+'" class="svg-stroke-teal" stroke-width="2.5" stroke-linecap="round"/>';
  h+='<path d="M'+x3+','+y3+' L'+x4+','+y4+'" class="svg-stroke-border" fill="none" stroke-width="1.5" stroke-dasharray="3"/>';

  if(sts && c.sensor){
    const sd=sts.sensors.find(s=>s.name===c.sensor);
    if(sd){
      const sv=Math.min(sd.value,100);
      const sx=v2x(sv);
      h+='<line x1="'+sx+'" y1="'+G.t+'" x2="'+sx+'" y2="'+G.b+'" class="svg-stroke-amber" stroke-width="1" stroke-dasharray="4,2"/>';
      let op;
      if(sd.value<=c.min_temp) op=c.min_pwm;
      else if(sd.value>=c.max_temp) op=c.max_pwm;
      else { const r=(sd.value-c.min_temp)/(c.max_temp-c.min_temp); op=c.min_pwm+r*(c.max_pwm-c.min_pwm); }
      h+='<circle cx="'+sx+'" cy="'+p2y(op)+'" r="4" class="svg-fill-amber"/>';
      h+='<text x="'+(sx>300?sx-6:sx+6)+'" y="'+(p2y(op)-6)+'" class="svg-fill-amber svg-text-mono" font-size="9" text-anchor="'+(sx>300?'end':'start')+'">'+fmtSensorVal(sd.value,sd.unit)+'\u2192'+p2pct(Math.round(op))+'%</text>';
    }
  }

  // Control points use the .ctrl-point.min / .ctrl-point.max rules in
  // app.css for fill / stroke / cursor. The drag handlers below read
  // data-point so the attribute lives on regardless of styling.
  h+='<circle class="ctrl-point min" data-point="min" cx="'+x2+'" cy="'+y2+'" r="6"/>';
  h+='<circle class="ctrl-point max" data-point="max" cx="'+x3+'" cy="'+y3+'" r="6"/>';
  svg.innerHTML=h;
}

// ── Field updates ──

function updField(f,v){
  if(selIdx<0) return;
  cfg.curves[selIdx][f]=v; markDirty(); renderEditor(); renderCurveCards();
}
function updPctField(f,pct){
  if(selIdx<0 || isNaN(pct)) return;
  cfg.curves[selIdx][f]=pct2p(pct); markDirty(); renderEditor(); renderCurveCards();
}
function updFixedPct(pct){
  if(selIdx<0) return;
  cfg.curves[selIdx].value=pct2p(pct);
  markDirty();
  renderFixedEditor(document.getElementById('curve-editor'),cfg.curves[selIdx]);
  renderCurveCards();
}
function updMixSources(){
  if(selIdx<0) return;
  const cks=document.querySelectorAll('#mix-sources input:checked');
  cfg.curves[selIdx].sources=Array.from(cks).map(c=>c.value);
  markDirty();
}
function updCtrl(i,v){ cfg.controls[i].curve=v; markDirty(); }

function renameCurve(n){
  if(selIdx<0) return;
  const old=cfg.curves[selIdx].name;
  if(old===n) return;
  if(cfg.curves.some(c=>c.name===n)){ notify('Name "'+n+'" exists','error'); renderEditor(); return; }
  cfg.controls.forEach(c=>{ if(c.curve===old) c.curve=n; });
  cfg.curves.forEach(c=>{ if(c.type==='mix'&&c.sources) c.sources=c.sources.map(s=>s===old?n:s); });
  cfg.curves[selIdx].name=n;
  markDirty(); renderCurveCards(); renderFanCards();
}

// ── CRUD ──

function addCurve(type){
  if(!cfg) return;
  let nm='new_'+type; let n=1;
  while(cfg.curves.some(c=>c.name===nm)) nm='new_'+type+'_'+n++;
  const c={name:nm,type:type};
  if(type==='linear'){
    c.sensor=cfg.sensors.length?cfg.sensors[0].name:'';
    c.min_temp=40; c.max_temp=80; c.min_pwm=30; c.max_pwm=255;
  } else if(type==='fixed'){
    c.value=128;
  } else if(type==='mix'){
    c.function='max'; c.sources=[];
  }
  cfg.curves.push(c); selIdx=cfg.curves.length-1;
  markDirty(); render();
}

function autoCurve(){
  if(!sts||!cfg||!cfg.sensors.length){ notify('No sensor data','error'); return; }
  const s=cfg.sensors[0];
  const sd=sts.sensors.find(x=>x.name===s.name);
  const cur=sd?sd.value:40;
  const minT=Math.max(25,Math.round((cur-5)/5)*5);
  let nm=s.name+'_auto'; let n=1;
  while(cfg.curves.some(c=>c.name===nm)) nm=s.name+'_auto_'+n++;
  cfg.curves.push({name:nm,type:'linear',sensor:s.name,min_temp:minT,max_temp:85,min_pwm:30,max_pwm:255});
  selIdx=cfg.curves.length-1; markDirty(); render();
  notify('Auto: '+s.name+' idle \u2248'+fmtSensorVal(cur, sd?sd.unit:'°C'),'ok');
}

function deleteCurve(){
  if(selIdx<0) return;
  const nm=cfg.curves[selIdx].name;
  const rc=cfg.controls.find(c=>c.curve===nm);
  if(rc){ notify('In use by '+rc.fan,'error'); return; }
  const rm=cfg.curves.find(c=>c.type==='mix'&&(c.sources||[]).includes(nm));
  if(rm){ notify('Source of '+rm.name,'error'); return; }
  cfg.curves.splice(selIdx,1);
  selIdx=Math.min(selIdx,cfg.curves.length-1);
  markDirty(); render();
}

// ── Drag ──

function svgPt(e){
  const svg=document.getElementById('curve-svg');
  if(!svg) return null;
  const pt=svg.createSVGPoint();
  const ev=e.touches?e.touches[0]:e;
  pt.x=ev.clientX; pt.y=ev.clientY;
  return pt.matrixTransform(svg.getScreenCTM().inverse());
}
function onDrag(e){
  if(!dragging||selIdx<0) return;
  e.preventDefault();
  const pt=svgPt(e); if(!pt) return;
  const c=cfg.curves[selIdx];
  if(dragging==='min'){
    c.min_temp=Math.min(x2v(pt.x),c.max_temp-1);
    c.min_pwm=y2p(pt.y);
  } else {
    c.max_temp=Math.max(x2v(pt.x),c.min_temp+1);
    c.max_pwm=y2p(pt.y);
  }
  markDirty(); drawSVG(c); renderCurveCards();
  const mt=document.getElementById('f-mint'),Mt=document.getElementById('f-maxt');
  const mp=document.getElementById('f-minp'),Mp=document.getElementById('f-maxp');
  if(mt)mt.value=c.min_temp; if(Mt)Mt.value=c.max_temp;
  if(mp)mp.value=p2pct(c.min_pwm); if(Mp)Mp.value=p2pct(c.max_pwm);
}
function endDrag(){
  dragging=null;
  document.removeEventListener('mousemove',onDrag);
  document.removeEventListener('mouseup',endDrag);
  document.removeEventListener('touchmove',onDrag);
  document.removeEventListener('touchend',endDrag);
}
document.addEventListener('mousedown',e=>{
  if(!e.target.classList.contains('ctrl-point')) return;
  e.preventDefault(); dragging=e.target.dataset.point;
  document.addEventListener('mousemove',onDrag);
  document.addEventListener('mouseup',endDrag);
});
document.addEventListener('touchstart',e=>{
  if(!e.target.classList.contains('ctrl-point')) return;
  e.preventDefault(); dragging=e.target.dataset.point;
  document.addEventListener('touchmove',onDrag,{passive:false});
  document.addEventListener('touchend',endDrag);
},{passive:false});

// ── Settings ──

function openSettings(){
  const el = document.getElementById('settings-overlay');
  el.style.display = 'flex';
  document.getElementById('settings-status').textContent = '';
  document.querySelector('#settings-overlay .danger').disabled = false;
}
function closeSettings(){
  document.getElementById('settings-overlay').style.display = 'none';
}
// Close on backdrop click
document.getElementById('settings-overlay').addEventListener('click', e => {
  if(e.target === document.getElementById('settings-overlay')) closeSettings();
});

async function confirmReset(){
  if(!confirm('This will delete the current configuration and restart the daemon. Continue?')) return;
  document.getElementById('settings-status').textContent = 'Resetting…';
  document.querySelector('#settings-overlay .danger').disabled = true;
  try {
    const r = await fetch('/api/setup/reset', {method:'POST'});
    if(!r.ok) throw new Error(await r.text());
  } catch(e){
    document.getElementById('settings-status').textContent = 'Error: '+e.message;
    document.querySelector('#settings-overlay .danger').disabled = false;
    return;
  }
  document.getElementById('settings-status').textContent = 'Restarting daemon…';
  // Poll /api/ping (unauthenticated) — session is lost on restart.
  await new Promise(r=>setTimeout(r,1200));
  const poll = setInterval(async()=>{
    try {
      const r = await fetch('/api/ping');
      if(r.ok){ clearInterval(poll); window.location.reload(); }
    } catch(_){}
  }, 800);
}

// ── Setup Wizard ──

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
        document.getElementById('setup-phase-area').style.display = '';
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
  document.getElementById('setup-phase-area').style.display = '';
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
    phaseArea.style.display = '';
    const statusEl = document.getElementById('setup-phase-status');
    statusEl.textContent = p.phase_msg || p.phase || '';

    // Board line — shown once board is known
    const boardEl = document.getElementById('setup-board-line');
    if(p.board){
      boardEl.textContent = p.board;
      boardEl.style.display = '';
    }

    // Chip line — shown during driver install
    const chipEl = document.getElementById('setup-chip-line');
    if(p.chip_name){
      chipEl.textContent = 'Installing driver for ' + p.chip_name + '…';
      chipEl.style.display = '';
    } else {
      chipEl.style.display = 'none';
    }

    // Install log — shown only during installing_driver phase
    const logEl = document.getElementById('setup-install-log');
    const lines = p.install_log || [];
    if(p.phase === 'installing_driver' && lines.length > 0){
      logEl.style.display = '';
      for(let i = setupLastInstallLogLen; i < lines.length; i++){
        const ln = document.createElement('div');
        ln.style.cssText = 'margin:1px 0;white-space:pre-wrap;word-break:break-all';
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
    progressArea.style.display = '';
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
        calHtml += '<div class="setup-prog-bar"><div class="fill" style="width:'+pct+'%"></div></div>';
      }

      let result = '—';
      if(f.cal_phase==='done'){
        result = 'start '+f.start_pwm+'/255 ('+Math.round(f.start_pwm/255*100)+'%)';
        if(f.type!=='nvidia' && f.max_rpm) result += ', '+f.max_rpm+' RPM';
      } else if(f.cal_phase==='error'){
        result = '<span style="color:var(--red)">'+esc(f.error)+'</span>';
      }

      row.innerHTML = '<td>'+esc(f.name)+'</td><td>'+esc(f.type)+'</td><td>'+detectHtml+'</td><td>'+calHtml+'</td><td>'+result+'</td>';
      tbody.appendChild(row);
    }
  } else {
    progressArea.style.display = 'none';
  }

  // ── Reboot required ─────────────────────────────────────────────────────
  const rebootPanel = document.getElementById('setup-reboot-panel');
  if(p.reboot_needed){
    document.getElementById('setup-reboot-msg').textContent = p.reboot_message || '';
    rebootPanel.style.display = '';
    phaseArea.style.display = 'none';
  } else {
    rebootPanel.style.display = 'none';
  }

  // ── Error ───────────────────────────────────────────────────────────────
  const errEl = document.getElementById('setup-error');
  if(p.error){
    errEl.textContent = 'Setup failed: '+p.error;
    errEl.style.display = '';
    progressArea.style.display = '';
  } else {
    errEl.style.display = 'none';
  }

  // ── Done — show Apply button ────────────────────────────────────────────
  if(p.done && !p.error && p.config){
    document.getElementById('setup-phase-status').textContent = 'Setup complete — review and apply your configuration.';
    document.getElementById('setup-chip-line').style.display = 'none';
    document.getElementById('setup-done-area').style.display = '';
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
    html += '<div class="curve-notes"><span style="color:var(--fg);font-size:0.8rem">Curve design</span><ul>';
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
  document.getElementById('setup-actions').style.display = 'none';
  document.getElementById('setup-restarting').style.display = '';
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

// ── Hardware diagnostics (hwdiag) ──
// Polls /api/hwdiag every 10s; revision counter lets us skip rerender when
// nothing changed. Entries group by component, colour by severity, and
// remediation renders as a button posting to entry.remediation.endpoint.
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

// ── Event delegation ──
//
// Every rendered template uses `data-action="..."` instead of an inline
// `on*=` attribute so the CSP can drop `script-src 'unsafe-inline'`.
// Listeners are installed once on document and dispatch by action name
// + element type. New actions get added here as each render group is
// migrated.

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

// ── Init ──
document.getElementById('btn-apply').addEventListener('click',applyConfig);
checkSetup().then(()=>{
  const overlay = document.getElementById('setup-overlay');
  if(overlay.classList.contains('hidden')){
    // Normal mode: load dashboard immediately.
    loadConfig(); loadStatus(); loadHardware(); loadCalibration(); loadHwdiag();
    setInterval(loadStatus,2000);
    setInterval(loadHardware,3000);
    setInterval(loadCalibration,5000);
    setInterval(loadHwdiag,10000);
  }
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
  // Update both toggle buttons (header + setup wizard)
  const icon = theme === 'light' ? '\u2600' : '\u263E'; // ☀ / ☾
  document.querySelectorAll('.theme-btn').forEach(b => b.textContent = icon);
}
function toggleTheme(){
  const next = document.documentElement.getAttribute('data-theme') === 'light' ? 'dark' : 'light';
  applyTheme(next);
  try { localStorage.setItem('ventd-theme', next); } catch(_){}
}
// Apply saved theme immediately (before first paint would flicker, but we're in a script at bottom).
(function(){
  let saved = 'dark';
  try { saved = localStorage.getItem('ventd-theme') || 'dark'; } catch(_){}
  applyTheme(saved);
})();
