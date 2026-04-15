"use strict";

// curve-editor.js — curve selection, per-type editors, SVG rendering
// and dragging, curve CRUD, and the settings modal (which lives here
// because Reset funnels through /api/setup/reset, a curve-adjacent
// operation, and closeSettings is invoked by the same delegator).
//
// All state comes from state.js (cfg, selIdx, dragging, G geometry).
// DOM writes target #curve-editor and #curve-svg (inserted by
// renderLinearEditor). The drag handlers are registered at module
// load time because scripts are deferred — the document element is
// always present by then.

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
//
// Visibility is driven by the .modal-backdrop / .modal-backdrop.open
// CSS pair defined in app.css. Toggling a class is preferable to
// setting element.style.display here because future modals (Apply-
// diff in Session C, panic popover later) can reuse the same rule
// set without each one replicating the display: flex; rule.

function openSettings(){
  const el = document.getElementById('settings-overlay');
  el.classList.add('open');
  document.getElementById('settings-status').textContent = '';
  document.querySelector('#settings-overlay .danger').disabled = false;
}
function closeSettings(){
  document.getElementById('settings-overlay').classList.remove('open');
}
// Close on backdrop click. The modal-card is a child of the
// modal-backdrop, so a click that reaches the backdrop itself means
// the user clicked outside the card.
(function(){
  const el = document.getElementById('settings-overlay');
  if(el){
    el.addEventListener('click', e => {
      if(e.target === el) closeSettings();
    });
  }
})();

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
