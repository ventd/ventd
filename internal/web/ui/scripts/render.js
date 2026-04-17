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
    // Empty-state copy: the old "No devices found" string gave no hint
    // about remediation. Users hitting this state almost always need to
    // load a kernel module; the text now says so, and the Rescan button
    // landing in PR-2h will pick up newly-loaded modules without a restart.
    document.getElementById('hw-devices').innerHTML =
      '<div class="empty-state sidebar-empty">'+
        '<p>No hardware devices detected.</p>'+
        '<p class="empty-state-hint">This usually means the kernel module isn\u2019t loaded. '+
        'Click <strong>Rescan hardware</strong> below, or run '+
        '<code>sudo ventd --probe-modules</code> from a terminal.</p>'+
      '</div>';
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

// renderSensorCardHTML renders the inner HTML for a single sensor
// card. Extracted so renderSensorCards can group cards by category
// without duplicating the card template.
function renderSensorCardHTML(s, i){
  const st = sts ? sts.sensors.find(x=>x.name===s.name) : null;
  const val = st ? fmtSensorVal(st.value, st.unit) : '\u2014';
  const heatClass = (st && st.unit==='\u00b0C') ? tempClass(st.value) : '';
  const valCls = heatClass ? 'sensor-val '+heatClass : 'sensor-val';
  const pathDisplay = s.type==='nvidia'
    ? 'GPU '+(s.path||'0')+(s.metric?' \u00b7 '+s.metric:'')
    : (s.path||'').replace('/sys/class/hwmon/','');
  // Sparkline inherits its stroke from the wrapper's colour class.
  // For temps that's the tc-* ramp (cool→crit); for everything else
  // (voltage, power, RPM) we leave the wrapper uncoloured so the
  // default .sparkline-wrap rule picks up var(--fg3).
  const spark = (typeof sparklineHTML === 'function')
    ? sparklineHTML(s.name, heatClass) : '';
  return '<div class="card sensor-card" data-sensor="'+esc(s.name)+'">'+
    '<div class="card-name-edit sensor-name-edit">'+
      '<input type="text" value="'+esc(s.name)+'" '+
        'data-orig="'+esc(s.name)+'" '+
        'data-action="rename-sensor" data-idx="'+i+'" '+
        'title="Click to rename">'+
      '<span class="edit-icon"><svg class="icon" aria-hidden="true"><use href="/ui/icons/sprite.svg#pencil"/></svg></span>'+
    '</div>'+
    '<div class="sensor-path">'+esc(pathDisplay)+'</div>'+
    '<div class="'+valCls+'">'+val+'</div>'+
    spark+
    '<div class="sensor-actions">'+
      '<button class="danger" data-action="delete-sensor" data-idx="'+i+'" title="Remove sensor"><svg class="icon" aria-hidden="true"><use href="/ui/icons/sprite.svg#trash-2"/></svg></button>'+
    '</div>'+
  '</div>';
}

// renderGroupedCards emits the <details>/<summary> accordion wrapper
// around per-category card grids. `section` is a namespaced key
// ("sensors" / "fans") used by the sessionStorage helpers; `buckets`
// maps group id → array of pre-rendered card HTML strings.
//
// Empty buckets are skipped so a machine without NVMe temps doesn't
// render a bare "Storage" header. The open attribute comes from
// groupIsOpen (session storage if set, else viewport default) so a
// full re-render on every SSE tick doesn't clobber a user toggle.
function renderGroupedCards(section, buckets){
  return DASHBOARD_GROUPS.map(g => {
    const items = buckets[g.id];
    if(!items || !items.length) return '';
    const open = groupIsOpen(section, g.id) ? ' open' : '';
    return '<details class="dashboard-group" data-group="'+g.id+'" data-section="'+section+'"'+open+'>'+
      '<summary class="dashboard-group-hdr">'+
        '<span class="dashboard-group-label">'+esc(g.label)+'</span>'+
        '<span class="dashboard-group-count">'+items.length+'</span>'+
      '</summary>'+
      '<div class="card-grid">'+items.join('')+'</div>'+
    '</details>';
  }).join('');
}

function renderSensorCards(){
  if(!cfg) return;
  const el = document.getElementById('sensor-cards');
  if(!cfg.sensors || !cfg.sensors.length){
    el.innerHTML =
      '<div class="empty-state">'+
        '<p>No sensors configured yet.</p>'+
        '<p class="empty-state-hint">A sensor reads a temperature, voltage, or fan-speed value from the kernel. '+
        'Add one from the Hardware Monitor panel.</p>'+
        '<button class="empty-state-btn" data-action="toggle-sidebar">Open Hardware Monitor</button>'+
      '</div>';
    return;
  }
  const buckets = {};
  cfg.sensors.forEach((s, i) => {
    const cat = classifyCategory(s.name, s);
    (buckets[cat] = buckets[cat] || []).push(renderSensorCardHTML(s, i));
  });
  el.innerHTML = renderGroupedCards('sensors', buckets);
}

// renderFanCardHTML renders the inner HTML for a single fan/control
// card. Extracted so renderFanCards can group cards by category
// without duplicating the card template.
function renderFanCardHTML(ctrl, i){
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
      // Config from /api/config can legitimately have Curves=null
      // (JSON marshal of a Go nil slice, fresh install before any
      // curve exists, manual-mode control with no curves defined
      // yet). The `|| []` is a boundary guard against that wire
      // shape — without it a curve-less config trips the whole
      // fan-card render.
      const opts = (cfg.curves || []).map(c =>
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
    const fanBindCurve = (!isManual && ctrl.curve) ? ctrl.curve : '';
    // Sparkline uses the same duty-colour ramp the duty bar already
    // applies — low=teal, mid=amber, high=red — so a glance at the
    // card reads both "where is it now" and "where has it been".
    const fanSpark = (typeof sparklineHTML === 'function')
      ? sparklineHTML(ctrl.fan, dCls) : '';
    return '<div class="card fan-card" data-fan="'+esc(ctrl.fan)+'"'+
      (fanBindCurve ? ' data-binds-curve="'+esc(fanBindCurve)+'"' : '')+'>'+
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
      fanSpark+
      '<div class="mode-toggle">'+
        '<button class="mode-btn'+(isManual?'':' active')+'" data-action="set-mode" data-idx="'+i+'" data-manual="0">Curve</button>'+
        '<button class="mode-btn'+(isManual?' active':'')+'" data-action="set-mode" data-idx="'+i+'" data-manual="1">Manual</button>'+
      '</div>'+
      controlRow+
      calSection+
      calResultRow+
    '</div>';
}

function renderFanCards(){
  if(!cfg) return;
  const el = document.getElementById('fan-cards');
  if(!cfg.controls || !cfg.controls.length){
    el.innerHTML =
      '<div class="empty-state">'+
        '<p>No fans configured.</p>'+
        '<p class="empty-state-hint">Run the setup wizard to auto-detect fans, or add them manually after configuring sensors.</p>'+
      '</div>';
    return;
  }
  const buckets = {};
  cfg.controls.forEach((ctrl, i) => {
    const fanCfg = cfg.fans ? cfg.fans.find(f => f.name === ctrl.fan) : null;
    const cat = classifyCategory(ctrl.fan, fanCfg);
    (buckets[cat] = buckets[cat] || []).push(renderFanCardHTML(ctrl, i));
  });
  el.innerHTML = renderGroupedCards('fans', buckets);

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
  const el = document.getElementById('curve-cards');
  // cfg.curves can legitimately be null on a fresh config (wire-shape
  // from JSON-marshalled nil slice); treat missing and empty the same.
  if(!cfg.curves || !cfg.curves.length){
    el.innerHTML =
      '<div class="empty-state">'+
        '<p>No curves yet.</p>'+
        '<p class="empty-state-hint">Curves map sensor readings to fan speed. Use the buttons above to create one and bind it to a fan.</p>'+
      '</div>';
    return;
  }
  el.innerHTML = cfg.curves.map((c,i) => {
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
    } else if(c.type==='points' && sts){
      const sd=sts.sensors.find(s=>s.name===c.sensor);
      if(sd){
        const v = evalPointsCurve(c.points, sd.value);
        if(v != null){
          out=fmtSensorVal(sd.value,sd.unit)+' \u2192 '+p2pct(v)+'%';
        }
      }
    } else if(c.type==='fixed'){
      out=p2pct(c.value)+'%';
    } else if(c.type==='mix'){
      // Each source name renders as an anchor so a click selects that
      // curve in the editor below. An unresolvable source (renamed or
      // deleted upstream) stays text-only with a muted class so the
      // user can spot the dangling reference without the click hinting
      // at a target that no longer exists.
      const sources = (c.sources||[]).map(srcName => {
        const ok = cfg.curves.some(x => x.name === srcName);
        if(!ok) return '<span class="curve-link curve-link-missing" title="Source curve not found">'+esc(srcName)+'</span>';
        return '<a class="curve-link" data-action="select-curve-by-name" '+
          'data-name="'+esc(srcName)+'">'+esc(srcName)+'</a>';
      }).join(', ');
      out=esc(c.function)+'('+sources+')';
    }
    // Curve card carries its binding data — data-curve is the name, and
    // data-reads-sensor / data-reads-curves expose the inputs so the
    // hover-binding helper in collectBindings() can walk the dependency
    // graph without re-grepping cfg on every mouseenter.
    let bindAttrs = 'data-curve="'+esc(c.name)+'"';
    if((c.type==='linear' || c.type==='points') && c.sensor) bindAttrs += ' data-reads-sensor="'+esc(c.sensor)+'"';
    if(c.type==='mix' && c.sources && c.sources.length) bindAttrs += ' data-reads-curves="'+esc(c.sources.join(','))+'"';
    return '<div class="card curve-card'+(i===selIdx?' active':'')+'" '+bindAttrs+' data-action="select-curve" data-idx="'+i+'">'+
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
  if(c.type==='points' && c.points && c.points.length >= 2){
    // Mini-graph polyline walks every anchor. Clamp segments before
    // the first and after the last to flat "rails" (dashed) matching
    // the linear card so both curve types read visually similar.
    const pts = (c.points||[]).slice().sort((a,b)=>a.temp-b.temp);
    const first = pts[0], last = pts[pts.length-1];
    const y0 = 38-(first.pwm/255)*33;
    const yN = 38-(last.pwm/255)*33;
    let path = '0,'+y0+' ';
    pts.forEach(p => {
      const x = Math.max(0, Math.min(100, p.temp));
      const y = 38-(p.pwm/255)*33;
      path += x+','+y+' ';
    });
    path += '100,'+yN;
    return '<svg viewBox="0 0 100 42" class="mini-graph">'+
      '<polyline points="'+path+
      '" class="svg-stroke-teal" fill="none" stroke-width="2" stroke-linecap="round"/></svg>';
  }
  if(c.type==='fixed'){
    const y=38-(c.value/255)*33;
    return '<svg viewBox="0 0 100 42" class="mini-graph">'+
      '<line x1="0" y1="'+y+'" x2="100" y2="'+y+'" class="svg-stroke-blue" stroke-width="2"/></svg>';
  }
  return '';
}

// evalPointsCurve mirrors the Go Points.Evaluate contract in JS so the
// card output and editor preview don't round-trip through the daemon
// on every sensor update. Returns a uint8-equivalent number or null if
// the curve has fewer than 2 anchors.
function evalPointsCurve(points, tempC){
  if(!points || points.length === 0) return null;
  const pts = points.slice().sort((a,b)=>a.temp-b.temp);
  if(pts.length === 1) return pts[0].pwm;
  const first = pts[0], last = pts[pts.length-1];
  if(tempC <= first.temp) return first.pwm;
  if(tempC >= last.temp) return last.pwm;
  for(let i=0; i<pts.length-1; i++){
    const lo = pts[i], hi = pts[i+1];
    if(tempC < lo.temp || tempC > hi.temp) continue;
    const r = (tempC - lo.temp) / (hi.temp - lo.temp);
    return Math.round(lo.pwm + r*(hi.pwm - lo.pwm));
  }
  return last.pwm;
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
    btn.addEventListener('click', () => {
      let payload = null;
      if(btn.dataset.hwdiagContext){
        try { payload = JSON.parse(btn.dataset.hwdiagContext); } catch(_){}
      }
      hwdiagRunRemediation(btn.dataset.hwdiagEndpoint, btn.dataset.hwdiagFix, btn, payload);
    });
  });
}

// hwdiagRemediationPayload extracts the subset of entry.context the client
// forwards to the remediation endpoint. Currently only `module` is used
// (for /api/setup/load-module); filtering here keeps us from echoing
// board-identifier metadata we don't want to pin into the request shape.
function hwdiagRemediationPayload(e){
  if(!e.context) return null;
  if(typeof e.context.module === 'string' && e.context.module){
    return {module: e.context.module};
  }
  return null;
}

function hwdiagItemHTML(e){
  const sev = e.severity || 'info';
  const rem = e.remediation;
  let btn = '';
  if(rem && rem.label){
    const disabled = rem.endpoint ? '' : ' disabled title="Remediation endpoint not wired yet (TODO)"';
    const payload = hwdiagRemediationPayload(e);
    const ctxAttr = payload ? ' data-hwdiag-context="'+esc(JSON.stringify(payload))+'"' : '';
    btn = '<button class="hwdiag-fix" data-hwdiag-fix="'+esc(rem.auto_fix_id||'')+'" data-hwdiag-endpoint="'+esc(rem.endpoint||'')+'"'+ctxAttr+disabled+'>'+esc(rem.label)+'</button>';
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
    case 'select-curve-by-name':
      selectCurveByName(el.dataset.name);
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
    case 'close-sidebar':
      closeSidebarDrawer();
      break;
    case 'toggle-hw':
      toggleHw(el.dataset.key);
      break;
    case 'rescan-hardware':
      rescanHardware();
      break;
    case 'toggle-panic-popover':
      togglePanicPopover();
      break;
    case 'start-panic':
      startPanic(parseInt(el.dataset.duration || '30', 10));
      break;
    case 'cancel-panic':
      cancelPanic();
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
    case 'open-diagnostics':
      openSettings();
      setTimeout(() => {
        const body = document.getElementById('system-status-body');
        if(body) body.scrollIntoView({behavior: 'smooth', block: 'start'});
      }, 100);
      break;
    case 'dismiss-diag-banner':
      dismissDiagBanner();
      break;
    case 'close-settings':
      closeSettings();
      break;
    case 'close-apply':
      closeApplyModal();
      break;
    case 'confirm-apply':
      commitConfigApply();
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
    case 'upd-duration-sec':
      updDurationSec(el.dataset.field, +el.value);
      break;
    case 'change-type':
      changeType(el.value);
      break;
    case 'fixed-pct':
      updFixedPct(+el.value);
      break;
    case 'mix-sources':
      updMixSources();
      break;
    // ── Settings modal: display preferences ──
    // Theme select covers the same three states the header toggle
    // cycles through, but exposed as an explicit select so the user
    // can land on "auto" (prefers-color-scheme) without clicking
    // through two steps. The unit select is a stub until the unit
    // preference plumbs into render paths (Session D work).
    case 'setting-theme':
      try { localStorage.setItem('ventd-theme', el.value); } catch(_){}
      if(el.value === 'auto'){
        // Clear the pin so applyTheme falls back to prefers-color-scheme
        // on next reload. Immediate effect: pick a theme matching the
        // OS to avoid a flash of wrong mode.
        const prefersLight = window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches;
        applyTheme(prefersLight ? 'light' : 'dark');
      } else {
        applyTheme(el.value);
      }
      break;
    case 'switch-profile':
      // <select> elements fire `change`, not `click`. Before #212
      // landed this case was misplaced under the click listener and
      // the dropdown was inert.
      switchProfile(el.value);
      break;
    case 'setting-temp-unit':
      try { localStorage.setItem('ventd-temp-unit', el.value); } catch(_){}
      // Re-render sensor cards + HW sidebar so the unit swap picks up
      // immediately. The actual °C→°F conversion lives in state.js's
      // fmtSensorVal helper (Session D work).
      if (typeof renderSensorBar === 'function') renderSensorBar();
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

// ── Visual binding (fan ↔ curve ↔ sensor) ──
//
// On hover, compute the transitive dependency set for the hovered card
// and mark every related card with `.binding-highlight`. A fan card
// lights up the curve it binds to, that curve's sensor, and — if the
// curve is a mix — every upstream curve and their sensors. A curve
// card lights up every fan using it and every sensor it (transitively)
// reads. A sensor card lights up every curve that reaches it and every
// fan bound to those curves.
//
// A mouseenter/mouseleave pair per card would work but re-registering
// after each renderFanCards re-render would leak listeners. Delegate
// to the document with a cheap closest() walk instead; on mouseleave
// every highlight is cleared, so a fast cursor move across two cards
// never paints two overlapping sets.
function collectBindings(kind, name){
  const out = { curves: new Set(), sensors: new Set(), fans: new Set() };
  if(!cfg || !name) return out;
  const curves = cfg.curves || [];
  const controls = cfg.controls || [];

  const visitCurve = (cname, depth) => {
    if(depth > 16 || out.curves.has(cname)) return;
    out.curves.add(cname);
    const c = curves.find(x => x.name === cname);
    if(!c) return;
    if(c.type === 'linear' && c.sensor) out.sensors.add(c.sensor);
    if(c.type === 'mix' && c.sources) c.sources.forEach(s => visitCurve(s, depth+1));
  };
  const visitFan = (fname) => {
    if(out.fans.has(fname)) return;
    out.fans.add(fname);
    const ctrl = controls.find(c => c.fan === fname);
    if(ctrl && ctrl.curve) visitCurve(ctrl.curve, 0);
  };
  const readsSensor = (cname, target, chain, depth) => {
    if(depth > 16 || chain.has(cname)) return false;
    chain.add(cname);
    const c = curves.find(x => x.name === cname);
    if(!c) return false;
    if(c.type === 'linear' && c.sensor === target) return true;
    if(c.type === 'mix' && c.sources) return c.sources.some(s => readsSensor(s, target, chain, depth+1));
    return false;
  };

  if(kind === 'fan') visitFan(name);
  else if(kind === 'curve'){
    visitCurve(name, 0);
    controls.forEach(c => { if(c.curve === name) visitFan(c.fan); });
  } else if(kind === 'sensor'){
    out.sensors.add(name);
    curves.forEach(c => {
      if(readsSensor(c.name, name, new Set(), 0)) visitCurve(c.name, 0);
    });
    controls.forEach(ctrl => { if(out.curves.has(ctrl.curve)) out.fans.add(ctrl.fan); });
  }
  return out;
}

function clearBindingHighlights(){
  document.querySelectorAll('.binding-highlight').forEach(el => el.classList.remove('binding-highlight'));
  document.body.classList.remove('binding-dim');
}
function applyBindingHighlights(kind, name){
  const b = collectBindings(kind, name);
  if(!b.curves.size && !b.sensors.size && !b.fans.size) return;
  document.body.classList.add('binding-dim');
  const mark = (sel) => { const el = document.querySelector(sel); if(el) el.classList.add('binding-highlight'); };
  b.curves.forEach(n => mark('.curve-card[data-curve="'+CSS.escape(n)+'"]'));
  b.sensors.forEach(n => mark('.sensor-card[data-sensor="'+CSS.escape(n)+'"]'));
  b.fans.forEach(n => mark('.fan-card[data-fan="'+CSS.escape(n)+'"]'));
}

document.addEventListener('mouseover', (e) => {
  if(!(e.target instanceof Element)) return;
  const card = e.target.closest('.fan-card, .curve-card, .sensor-card');
  if(!card){ clearBindingHighlights(); return; }
  // Skip if we're still inside the same card — avoid churn during a
  // cursor wiggle inside the card's padding.
  if(card.classList.contains('binding-highlight')) return;
  clearBindingHighlights();
  let kind = '', name = '';
  if(card.classList.contains('fan-card')){ kind = 'fan'; name = card.dataset.fan; }
  else if(card.classList.contains('curve-card')){ kind = 'curve'; name = card.dataset.curve; }
  else if(card.classList.contains('sensor-card')){ kind = 'sensor'; name = card.dataset.sensor; }
  if(name) applyBindingHighlights(kind, name);
});
document.addEventListener('mouseleave', (e) => {
  // Main element leave — only clear when the cursor exits the whole
  // dashboard body so a mouseleave inside a card's subtree doesn't
  // strip the highlight mid-render.
  if(e.target === document.documentElement) clearBindingHighlights();
});
// Mirror the hover behaviour for touch: tapping a card briefly
// highlights the bindings without selecting the curve. Touchend
// removes the highlight after a short settle so a user who taps then
// scrolls sees the relationship before it fades.
let touchHighlightTimer = null;
document.addEventListener('touchstart', (e) => {
  if(!(e.target instanceof Element)) return;
  const card = e.target.closest('.fan-card, .curve-card, .sensor-card');
  if(!card) return;
  clearBindingHighlights();
  if(card.classList.contains('fan-card') && card.dataset.fan) applyBindingHighlights('fan', card.dataset.fan);
  else if(card.classList.contains('curve-card') && card.dataset.curve) applyBindingHighlights('curve', card.dataset.curve);
  else if(card.classList.contains('sensor-card') && card.dataset.sensor) applyBindingHighlights('sensor', card.dataset.sensor);
  clearTimeout(touchHighlightTimer);
  touchHighlightTimer = setTimeout(clearBindingHighlights, 1200);
}, { passive: true });

// selectCurveByName is called from the mix-source anchor click path.
// Scrolls the editor into view after selecting so the user sees the
// target curve rather than just having to hunt for it below the fold.
function selectCurveByName(name){
  if(!cfg || !name) return;
  const idx = (cfg.curves || []).findIndex(c => c.name === name);
  if(idx < 0){
    if(typeof notify === 'function') notify('Curve "'+name+'" not found', 'error');
    return;
  }
  selectCurve(idx);
  const editor = document.getElementById('curve-editor');
  if(editor) editor.scrollIntoView({behavior: 'smooth', block: 'start'});
}

// toggle: <details> open/close inside the grouped dashboard sections.
// The event does not bubble, so capture-phase listening at the
// document level is required to catch it without per-element wiring
// (which would reset every time renderSensorCards / renderFanCards
// regenerates innerHTML). Writing to sessionStorage on each toggle
// is what survives the SSE-driven re-renders: the next render reads
// the stored value back through groupIsOpen.
document.addEventListener('toggle', (e) => {
  const el = e.target;
  if (!(el instanceof HTMLElement) || el.tagName !== 'DETAILS') return;
  if (!el.classList.contains('dashboard-group')) return;
  const section = el.dataset.section;
  const group = el.dataset.group;
  if (!section || !group) return;
  setGroupState(section, group, el.open);
}, true);

// narrowViewportMQ is declared in state.js. Re-render the grouped
// sections when the viewport crosses the tablet boundary so a
// rotation or window-resize picks up the new viewport-default state
// for any group that hasn't been explicitly toggled this session.
// Session-storage values still win over the new default — this only
// changes the unset-group baseline.
narrowViewportMQ.addEventListener('change', () => {
  if (typeof cfg === 'undefined' || !cfg) return;
  renderSensorCards();
  renderFanCards();
});

// ── Sidebar toggle ──
//
// Two behaviours share one hamburger button depending on viewport:
//
//   desktop (≥900px) — toggle the `.collapsed` class; persist the
//   user's choice in localStorage so the preference survives a
//   reload. This matches the legacy behaviour.
//
//   mobile (<900px)  — toggle the `.open` class, which slides the
//   drawer in and reveals the sibling backdrop via CSS. No
//   localStorage: on mobile the drawer always starts closed, and
//   closes on backdrop tap or a second hamburger press.
//
// matchMedia keeps the boundary in one place; the `change` listener
// below scrubs stale classes when the viewport crosses the breakpoint
// (e.g. a phone rotating to landscape that clears the tablet bracket,
// or a desktop window narrowed below 900px).
const sidebarMQ = window.matchMedia('(max-width: 899px)');

function toggleSidebar(){
  const sb = document.getElementById('sidebar');
  if(!sb) return;
  if(sidebarMQ.matches){
    sb.classList.toggle('open');
    return;
  }
  const collapsed = sb.classList.toggle('collapsed');
  try { localStorage.setItem('ventd-sidebar', collapsed ? '0' : '1'); } catch(_){}
}

function closeSidebarDrawer(){
  const sb = document.getElementById('sidebar');
  if(sb) sb.classList.remove('open');
}

sidebarMQ.addEventListener('change', (e) => {
  const sb = document.getElementById('sidebar');
  if(!sb) return;
  if(e.matches){
    // Entering mobile: drawer starts closed regardless of prior state.
    sb.classList.remove('collapsed');
    sb.classList.remove('open');
  } else {
    // Entering desktop: drop any lingering drawer-open class and
    // replay the saved collapse preference.
    sb.classList.remove('open');
    try {
      if(localStorage.getItem('ventd-sidebar') === '0'){
        sb.classList.add('collapsed');
      } else {
        sb.classList.remove('collapsed');
      }
    } catch(_){}
  }
});

(function(){
  try {
    // Only apply the saved desktop collapse state if we're booting on
    // a desktop viewport — on mobile the drawer always starts closed.
    if(!sidebarMQ.matches && localStorage.getItem('ventd-sidebar') === '0'){
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
// A stored value of "auto" means the user explicitly picked "follow OS"
// in the Settings > Display select; on reload we must resolve that to a
// concrete light/dark so the data-theme attribute actually matches a
// CSS rule. Passing "auto" through to applyTheme would set
// `data-theme="auto"` which no tokens.css rule catches, so the page
// would paint dark regardless of the OS preference. Issue #199.
(function(){
  let saved = 'dark';
  try { saved = localStorage.getItem('ventd-theme') || 'dark'; } catch(_){}
  if(saved === 'auto'){
    const prefersLight = window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches;
    saved = prefersLight ? 'light' : 'dark';
  }
  applyTheme(saved);
})();
