"use strict";

// sparkline.js — client-side time-series buffer + tiny SVG generator
// for the per-card sparkline dropped into sensor and fan tiles.
//
// Storage model: each metric (sensor name or fan name) keeps a plain
// array of {t, v} objects in historyBuf. Seeded from GET /api/history
// on page load; every subsequent SSE `status` frame appends a single
// point per metric. Capped per-metric so a tab left open overnight
// doesn't grow the buffer without bound.
//
// No rAF, no chart library, no resize observer. Cards are re-rendered
// by render.js on every SSE tick; the sparkline HTML is embedded
// directly in the card template so it repaints alongside the value.

// historyBuf: { "<metric name>": [{t: unixSeconds, v: number}, ...] }
//
// Global by design — render.js calls historyFor() from inside its
// card template functions and api.js writes via pushHistorySample()
// from applyStatus(). A module-level Map would just add a getter
// boundary without a corresponding benefit.
var historyBuf = {};

// HISTORY_MAX bounds per-metric memory on the client. Matches the
// server's ring capacity (3600/2 + 1 = 1801 at 2 s interval, but
// rounding up to 2000 gives slack if the daemon restarts with a
// faster sampler). Oldest samples drop off FIFO.
var HISTORY_MAX = 2000;

// Sparkline render dimensions. The SVG uses viewBox + responsive CSS
// so 80×30 on desktop collapses to 60×24 on narrow viewports without
// regenerating the SVG path.
var SPARK_W = 80;
var SPARK_H = 30;

// pushHistorySample appends one (t, v) pair for metric. Called once
// per metric per SSE status frame. Drops silently on malformed
// inputs so a single malformed server frame doesn't NaN the whole
// UI.
function pushHistorySample(metric, t, v){
  if(!metric) return;
  if(typeof v !== 'number' || !isFinite(v)) return;
  var arr = historyBuf[metric];
  if(!arr){
    arr = [];
    historyBuf[metric] = arr;
  }
  arr.push({t: t|0, v: v});
  if(arr.length > HISTORY_MAX){
    arr.splice(0, arr.length - HISTORY_MAX);
  }
}

// historyFor returns the live array for a metric, or an empty array
// if nothing has been recorded yet. The returned reference is the
// same one stored in historyBuf — callers must treat it as read-only.
function historyFor(metric){
  return historyBuf[metric] || [];
}

// historyValues extracts just the numeric values from a history
// array. Convenience wrapper so sparkline callers don't have to map
// every time.
function historyValues(metric){
  var arr = historyBuf[metric];
  if(!arr) return [];
  var out = new Array(arr.length);
  for(var i = 0; i < arr.length; i++) out[i] = arr[i].v;
  return out;
}

// updateHistoryFromStatus drains one status snapshot (the SSE frame
// payload shape) into the client buffer. Centralised so applyStatus
// in api.js doesn't have to know the buffer layout.
function updateHistoryFromStatus(snap){
  if(!snap) return;
  var t = 0;
  if(snap.timestamp){
    var parsed = Date.parse(snap.timestamp);
    if(!isNaN(parsed)) t = (parsed / 1000) | 0;
  }
  if(t === 0) t = (Date.now() / 1000) | 0;
  if(snap.sensors){
    for(var i = 0; i < snap.sensors.length; i++){
      var s = snap.sensors[i];
      pushHistorySample(s.name, t, s.value);
    }
  }
  if(snap.fans){
    for(var j = 0; j < snap.fans.length; j++){
      var f = snap.fans[j];
      pushHistorySample(f.name, t, f.duty_pct);
    }
  }
}

// loadHistory seeds historyBuf from /api/history once on page load.
// Silent on failure — the buffer will still fill in from the SSE
// stream after a couple of ticks, it just won't have back-history
// to draw until then. Called once from setup.js bootstrap.
async function loadHistory(){
  try {
    var r = await fetch('/api/history');
    if(!r.ok) return;
    var j = await r.json();
    if(j && j.metrics){
      for(var name in j.metrics){
        if(!Object.prototype.hasOwnProperty.call(j.metrics, name)) continue;
        var samples = j.metrics[name];
        if(!Array.isArray(samples)) continue;
        // Copy, don't alias — we keep ownership of the array so
        // subsequent pushHistorySample mutations don't confuse the
        // JSON parser's downstream consumers.
        var copy = new Array(samples.length);
        for(var i = 0; i < samples.length; i++){
          copy[i] = {t: samples[i].t|0, v: +samples[i].v};
        }
        historyBuf[name] = copy;
      }
    }
  } catch(_){ /* non-fatal — sparklines still fill from SSE */ }
}

// sparkline returns the HTML for a <svg> sparkline given an array of
// numeric values. Returns '' when fewer than 2 points exist so a
// brand-new sensor shows nothing rather than a degenerate dot.
//
// stroke="currentColor" + vector-effect="non-scaling-stroke" makes
// the path inherit the wrapper's colour (so tc-cool / tc-warm CSS
// classes on the wrapper drive the hue) and keeps stroke width
// visually stable when CSS resizes the SVG on narrow viewports.
function sparkline(values){
  if(!values || values.length < 2) return '';
  var min = values[0], max = values[0];
  for(var i = 1; i < values.length; i++){
    var v = values[i];
    if(v < min) min = v;
    if(v > max) max = v;
  }
  var range = max - min;
  if(range === 0) range = 1;
  var step = SPARK_W / (values.length - 1);
  // Inset 1 px top and bottom so the stroke centre line never sits
  // on the SVG edge — visually the same as padding the box.
  var usableH = SPARK_H - 2;
  var d = '';
  for(var k = 0; k < values.length; k++){
    var x = k * step;
    var y = SPARK_H - 1 - ((values[k] - min) / range) * usableH;
    d += (k === 0 ? 'M' : 'L') + x.toFixed(1) + ',' + y.toFixed(1) + ' ';
  }
  return '<svg class="sparkline" viewBox="0 0 ' + SPARK_W + ' ' + SPARK_H +
    '" preserveAspectRatio="none" aria-hidden="true" focusable="false">' +
    '<path d="' + d + '" fill="none" stroke="currentColor" stroke-width="1.5" ' +
    'stroke-linecap="round" stroke-linejoin="round" ' +
    'vector-effect="non-scaling-stroke"/></svg>';
}

// sparklineHTML renders the full <div class="sparkline-wrap …"> for
// a metric, inheriting a semantic colour class (tc-cool / dc-high /
// etc.) that the caller supplies. Returns '' when the buffer has no
// data yet so the card doesn't display an empty placeholder on its
// first paint.
function sparklineHTML(metric, colorClass){
  var vals = historyValues(metric);
  var svg = sparkline(vals);
  if(!svg) return '';
  var cls = 'sparkline-wrap' + (colorClass ? ' ' + colorClass : '');
  return '<div class="' + cls + '">' + svg + '</div>';
}
