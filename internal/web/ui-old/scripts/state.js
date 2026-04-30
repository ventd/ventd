"use strict";

// state.js — application state and pure helpers.
//
// The dashboard's JS modules share a single global scope (each script
// loads with `defer` from index.html so they execute in declaration
// order before DOMContentLoaded). state.js loads first and owns the
// mutable globals (cfg, sts, hw, selIdx, dirty, dragging, sliderDragging,
// hwCollapsed, calStatuses, calResults) plus the pure helpers that translate
// between sensor values, PWM bytes, and screen coordinates. Every
// other module reads or mutates these globals — they are the dashboard's
// single source of truth.
//
// No DOM access here. No fetch. No event listeners. Adding any of
// those puts the wrong responsibility in the wrong file.

let cfg = null, sts = null, hw = null, selIdx = -1, dirty = false, dragging = null, sliderDragging = false;
let hwCollapsed = {}, calStatuses = {}, calResults = {};
// curveDirtyPatch accumulates per-field PATCH values for the selected curve.
// Keys are PATCH field names (e.g. 'min_pwm_pct'); values are the new values
// to send. Reset on curve selection so each editor session starts clean.
let curveDirtyPatch = {};

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
// Returns '—' for null/undefined values (sentinel or unavailable reading).
function fmtSensorVal(val, unit){
  if(val == null) return '\u2014';
  if(unit==='°C') return val.toFixed(1)+'°C';
  if(unit==='V') return val.toFixed(3)+' V';
  if(unit==='W') return val.toFixed(1)+' W';
  if(unit==='RPM') return Math.round(val)+' RPM';
  if(unit==='MHz') return Math.round(val)+' MHz';
  if(unit==='%') return Math.round(val)+'%';
  return val.toFixed(1)+(unit?' '+unit:'');
}

// ── Dashboard group accordion state ──
//
// The Sensors and Controls sections on the dashboard render as
// <details>/<summary> accordions grouped by category (CPU / GPU /
// System / Storage / Other). Whether a group is open or closed is
// per-tab state: it must survive SSE re-renders (each status frame
// regenerates the card grid) but not a tab close — hence session
// storage, not local storage.
//
// Keys are namespaced as "ventd.dashboard.<section>.<group>" so
// other session-storage consumers can't collide, and so a future
// dashboard section can opt into the same mechanism without
// reshaping the helpers. Section is e.g. "sensors" / "fans";
// group is the classifyCategory return value ("cpu" / "gpu" ...).
//
// Reads return null when the key isn't set — callers translate
// that into the viewport-dependent default (wide: open, narrow:
// CPU open else collapsed). Writes are best-effort; browsers that
// reject session storage (private mode on some iOS builds) fall
// back to ephemeral in-memory state, which is still correct for
// the lifetime of the current render chain.
function groupStorageKey(section, group){
  return 'ventd.dashboard.'+section+'.'+group;
}

function getGroupState(section, group){
  try {
    const v = sessionStorage.getItem(groupStorageKey(section, group));
    if(v === '1') return true;
    if(v === '0') return false;
  } catch(_){}
  return null;
}

function setGroupState(section, group, open){
  try { sessionStorage.setItem(groupStorageKey(section, group), open ? '1' : '0'); } catch(_){}
}

// narrowViewportMQ pins the accordion-default breakpoint to the
// tablet boundary declared in tokens.css (--bp-tablet: 900px). The
// sidebar uses the same threshold for its drawer/inline flip, so
// "narrow" here means the same viewport class that already gets
// density-constrained layout.
const narrowViewportMQ = window.matchMedia('(max-width: 899px)');

// groupDefaultOpen returns the baseline open-state for a group
// when no session-storage preference exists. Wide viewports keep
// all groups open (desktop has the vertical room); narrow viewports
// open only CPU so the operator's primary interest paints without a
// tap but GPU / System / Storage / Other stay tucked away.
function groupDefaultOpen(group){
  if(!narrowViewportMQ.matches) return true;
  return group === 'cpu';
}

// groupIsOpen resolves the effective open-state for a group. Session
// storage wins if set; otherwise fall back to the viewport default.
function groupIsOpen(section, group){
  const stored = getGroupState(section, group);
  if(stored !== null) return stored;
  return groupDefaultOpen(group);
}

// ── Dashboard group classification ──
//
// classifyCategory maps a sensor or fan name + its cfg entry to one
// of 'cpu' / 'gpu' / 'system' / 'storage' / 'other'. The ladder
// exists so a future SSE payload can add an explicit Category field
// without reshaping the callers — the first rung to match wins, so
// dropping an `entry.category` check at the top would short-circuit
// the keyword matching below it.
//
// Order:
//   a. (reserved) explicit category field on the entry — wire-level
//      future-proofing; today's sse.go doesn't carry one, so this
//      rung is a no-op until a sensor/fan struct grows one.
//   b. config-level hints: entry.is_pump → system; entry.type ==
//      'nvidia' → gpu.
//   c. friendly-name keyword match against the human-facing name the
//      UI already renders ("CPU Temperature", "GPU Fan", etc.).
//   d. fallback: 'other'.
//
// The keyword lists are deliberately narrow: classifying a "System
// Fan" as CPU because the name contains the substring "sy" is worse
// than classifying it as "other". Word boundaries on the short
// tokens (CPU / GPU / VGA) keep them from matching e.g. "occupied"
// or a motherboard name that happens to contain those letters.
function classifyCategory(name, entry){
  if(entry && typeof entry.category === 'string' && entry.category){
    return entry.category;
  }
  if(entry && entry.is_pump) return 'system';
  if(entry && entry.type === 'nvidia') return 'gpu';

  // Normalise underscores and dashes to whitespace before matching
  // so \b treats "cpu_fan" the same as "CPU Fan". Without this the
  // underscore is a \w character and the GPU/CPU/NVMe word tokens
  // never hit their word boundary inside a sysfs-style identifier.
  const n = (name || '').toLowerCase().replace(/[_\-]+/g, ' ');
  if(/\bgpu\b|\bvga\b|nvidia|radeon|amdgpu|geforce|quadro/.test(n)) return 'gpu';
  if(/\bcpu\b|\bcore\b|\bpackage\b|\bsocket\b/.test(n)) return 'cpu';
  if(/\bnvme\b|\bssd\b|\bhdd\b|\bdrive\b|\bdisk\b/.test(n)) return 'storage';
  if(/\bpump\b/.test(n)) return 'system';
  if(/\bsys\s*fan|\bsystem\b|\bchassis\b|\bcase\s*fan|\bmb\s*fan|\bmotherboard\b|\bambient\b|\bpch\b/.test(n)) return 'system';
  return 'other';
}

// DASHBOARD_GROUPS is the display order + friendly label for each
// category classifyCategory can emit. "Other" lives last so it acts
// as the catch-all at the bottom of the section; empty groups are
// filtered out at render time so a machine without NVMe temps
// doesn't show a bare "Storage" header.
const DASHBOARD_GROUPS = [
  { id: 'cpu',     label: 'CPU' },
  { id: 'gpu',     label: 'GPU' },
  { id: 'system',  label: 'System' },
  { id: 'storage', label: 'Storage' },
  { id: 'other',   label: 'Other' },
];

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
