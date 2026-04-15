"use strict";

// state.js — application state and pure helpers.
//
// The dashboard's JS modules share a single global scope (each script
// loads with `defer` from index.html so they execute in declaration
// order before DOMContentLoaded). state.js loads first and owns the
// mutable globals (cfg, sts, hw, selIdx, dirty, dragging, hwCollapsed,
// calStatuses, calResults) plus the pure helpers that translate
// between sensor values, PWM bytes, and screen coordinates. Every
// other module reads or mutates these globals — they are the dashboard's
// single source of truth.
//
// No DOM access here. No fetch. No event listeners. Adding any of
// those puts the wrong responsibility in the wrong file.

let cfg = null, sts = null, hw = null, selIdx = -1, dirty = false, dragging = null;
let hwCollapsed = {}, calStatuses = {}, calResults = {};

// Curve-editor SVG geometry. The viewBox is 510x255; G defines the
// inner plot area (the strip framed by axis labels and tick marks).
// v2x / p2y / x2v / y2p translate between (sensor value, PWM byte)
// data coordinates and (svg x, svg y) draw coordinates so callers can
// stay in problem-domain units. x-axis is sensor value 0–100 (display-
// scaled — temps use °C, percentages use %, etc.).
const G = {l:40, r:490, t:15, b:230};
G.w = G.r-G.l; G.h = G.b-G.t;
function v2x(v){ return G.l+(Math.min(v,100)/100)*G.w; }
function p2y(p){ return G.b-(p/255)*G.h; }
function x2v(x){ return Math.round(Math.max(0,Math.min(100,(x-G.l)/G.w*100))); }
function y2p(y){ let pct=Math.round(Math.max(0,Math.min(100,(G.b-y)/G.h*100))); return Math.round(pct/100*255); }

// HTML escape — used everywhere a user-supplied string lands inside
// an innerHTML template. Cheap and complete; do not skip when adding
// new template strings.
function esc(s){ return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }

// PWM byte (0–255) ↔ percentage (0–100). NaN-safe.
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

// sensorUnit returns the display unit for a configured sensor. nvidia
// sensors carry the unit in their metric tag; hwmon sensors infer it
// from the sysfs path leaf (temp* → °C, in* → V, power* → W, fan* → RPM).
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

// fmtSensorVal renders a sensor reading with its unit. Per-unit
// formatting matches what operators expect to read on a hardware
// monitor: 1 decimal for temps and watts, 3 for volts, integer
// for RPM/MHz/%.
function fmtSensorVal(val, unit){
  if(unit==='°C') return val.toFixed(1)+'°C';
  if(unit==='V') return val.toFixed(3)+' V';
  if(unit==='W') return val.toFixed(1)+' W';
  if(unit==='RPM') return Math.round(val)+' RPM';
  if(unit==='MHz') return Math.round(val)+' MHz';
  if(unit==='%') return Math.round(val)+'%';
  return val.toFixed(1)+(unit?' '+unit:'');
}

// markDirty flips the "Unsaved" badge and enables the Apply button.
// Called from every user action that mutates cfg.
function markDirty(){
  dirty=true;
  document.getElementById('dirty').hidden=false;
  document.getElementById('btn-apply').disabled=false;
}

// notify shows a short toast in the top-right corner. Two flavours:
// 'ok' (teal) and 'error' (red). Auto-dismisses after 5 s.
function notify(msg,type){
  const el=document.getElementById('notification');
  el.textContent=msg; el.className='notification '+type; el.hidden=false;
  clearTimeout(el._t); el._t=setTimeout(()=>el.hidden=true,5000);
}
