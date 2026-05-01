// calibration.js — first-boot calibration takeover.
//
// Polls /api/v1/setup/status (and /api/v1/status for live RPM) and drives:
//   • the 7-step phase pipeline (detecting → finalizing)
//   • the running fan card (PWM, RPM, phase, progress, sparkline, scatter)
//   • the per-fan roster (status pills + start/stop/maxRPM as it lands)
//   • the elapsed/remaining clock and rotating flavour message
//   • the done banner with Apply, and the error banner with Retry
//
// When the API is unreachable (e.g. previewing the page without a daemon)
// the demo-loop fallback runs a synthetic calibration so the design and
// motion stay verifiable.

(function () {
  'use strict';

  // ── theme toggle (dark default; localStorage override only) ─────────
  var root = document.documentElement;
  try {
    var stored = localStorage.getItem('ventd-theme');
    if (stored === 'light' || stored === 'dark') root.dataset.theme = stored;
  } catch (_) {}
  var themeBtn = document.getElementById('theme-toggle');
  if (themeBtn) themeBtn.addEventListener('click', function () {
    var next = root.dataset.theme === 'dark' ? 'light' : 'dark';
    root.dataset.theme = next;
    try { localStorage.setItem('ventd-theme', next); } catch (_) {}
  });

  // ── phase pipeline order matches setupmgr Phase strings ─────────────
  var PHASES = [
    'detecting', 'installing_driver', 'scanning_fans',
    'detecting_rpm', 'probing_polarity', 'calibrating', 'finalizing'
  ];

  var PHASE_HEADLINES = {
    detecting:         'Discovering your hardware',
    installing_driver: 'Loading the right kernel module',
    scanning_fans:     'Mapping every PWM controller',
    detecting_rpm:     'Pairing each PWM with its tachometer',
    probing_polarity:  'Testing duty-cycle direction',
    calibrating:       'Sweeping each fan across its range',
    finalizing:        'Building your custom thermal curve'
  };

  var PHASE_FLAVOURS = [
    'Calibration runs once per host. Walk away — your work is saved as it goes.',
    'Each fan is held at a step for ~3s so the tachometer can settle.',
    'Aborts on any sensor above 85 °C. Firmware control re-arms within 2s.',
    'Stop and start PWM are derived from the lowest steady RPM, not guessed.',
    'Polarity probe catches BIOSes that wire fan headers backwards.',
    'Found markers (yellow) are the boundaries your curves will respect.',
    'The thermal curve is shaped to your actual CPU + GPU power envelopes.'
  ];

  // ── element handles ─────────────────────────────────────────────────
  var pipelineEl     = document.getElementById('cal-pipeline');
  var pipelineFill   = document.getElementById('cal-pipeline-fill');
  var headlineEl     = document.getElementById('cal-headline');
  var elapsedEl      = document.getElementById('cal-elapsed');
  var remainingEl    = document.getElementById('cal-remaining');
  var liveNameEl     = document.getElementById('cal-live-name');
  var liveSourceEl   = document.getElementById('cal-live-source');
  var livePwmEl      = document.getElementById('cal-live-pwm');
  var livePwmBarEl   = document.getElementById('cal-live-pwm-bar');
  var liveRpmEl      = document.getElementById('cal-live-rpm');
  var liveSparkPath  = document.getElementById('cal-live-spark-path');
  var livePhaseEl    = document.getElementById('cal-live-phase');
  var livePhaseSubEl = document.getElementById('cal-live-phase-sub');
  var liveProgressEl = document.getElementById('cal-live-progress');
  var liveProgBar    = document.getElementById('cal-live-progress-bar');
  var rosterEl       = document.getElementById('cal-roster');
  var counterDoneEl  = document.getElementById('cal-counter-done');
  var counterTotalEl = document.getElementById('cal-counter-total');
  var totalsDoneEl   = document.getElementById('cal-totals-done');
  var totalsRunEl    = document.getElementById('cal-totals-running');
  var totalsQueueEl  = document.getElementById('cal-totals-queued');
  var totalsSkipEl   = document.getElementById('cal-totals-skipped');
  var totalsSkipPill = document.getElementById('cal-totals-skipped-pill');
  var profileCard    = document.getElementById('cal-profile-card');
  var profileBoardEl = document.getElementById('cal-profile-board');
  var profileCpuRow  = document.getElementById('cal-profile-cpu-row');
  var profileCpuEl   = document.getElementById('cal-profile-cpu');
  var profileCpuTdp  = document.getElementById('cal-profile-cpu-tdp');
  var profileGpuRow  = document.getElementById('cal-profile-gpu-row');
  var profileGpuEl   = document.getElementById('cal-profile-gpu');
  var profileGpuTdp  = document.getElementById('cal-profile-gpu-tdp');
  var profileChipRow = document.getElementById('cal-profile-chip-row');
  var profileChipEl  = document.getElementById('cal-profile-chip');
  var doneBanner     = document.getElementById('cal-done-banner');
  var doneSubEl      = document.getElementById('cal-done-sub');
  var applyBtn       = document.getElementById('cal-apply-btn');
  var errorBanner    = document.getElementById('cal-error-banner');
  var errorSubEl     = document.getElementById('cal-error-sub');
  var retryBtn       = document.getElementById('cal-retry-btn');
  var skipBtn        = document.getElementById('cal-skip-btn');
  var bundleBtn      = document.getElementById('cal-bundle-btn');
  var bundleStatusEl = document.getElementById('cal-bundle-status');
  var abortBtn       = document.getElementById('cal-abort');
  var flavourEl      = document.getElementById('cal-flavour');
  var samplesG       = document.getElementById('cal-samples');
  var markersG       = document.getElementById('cal-markers');
  var curveLine      = document.getElementById('cal-curve-line');
  var curveArea      = document.getElementById('cal-curve-area');
  var cursorLine     = document.getElementById('cal-cursor');
  var cursorDot      = document.getElementById('cal-cursor-dot');
  var cursorHalo     = document.getElementById('cal-cursor-halo');

  // ── chart geometry ──────────────────────────────────────────────────
  var CHART_W = 800;
  var CHART_H = 320;

  function pwmToX(pwm)        { return (pwm / 255) * CHART_W; }
  function rpmToY(rpm, max)   {
    var m = Math.max(max || 0, 1);
    return CHART_H - Math.min(rpm / m, 1) * (CHART_H - 8);
  }

  // ── sparkline buffer ────────────────────────────────────────────────
  var sparkBuf = []; // last N RPM samples for the current fan
  var SPARK_N = 60;
  var SPARK_W = 240;
  var SPARK_H = 36;
  function pushSpark(rpm) {
    sparkBuf.push(rpm);
    if (sparkBuf.length > SPARK_N) sparkBuf.shift();
    if (!liveSparkPath) return;
    if (sparkBuf.length < 2) { liveSparkPath.setAttribute('d', ''); return; }
    var max = 1, min = Infinity;
    for (var i = 0; i < sparkBuf.length; i++) {
      if (sparkBuf[i] > max) max = sparkBuf[i];
      if (sparkBuf[i] < min) min = sparkBuf[i];
    }
    var range = Math.max(max - min, max * 0.1, 1);
    var d = '';
    for (var j = 0; j < sparkBuf.length; j++) {
      var x = (j / (SPARK_N - 1)) * SPARK_W;
      var y = SPARK_H - 2 - ((sparkBuf[j] - min) / range) * (SPARK_H - 4);
      d += (j === 0 ? 'M ' : ' L ') + x.toFixed(1) + ' ' + y.toFixed(1);
    }
    liveSparkPath.setAttribute('d', d);
  }
  function resetSpark() { sparkBuf = []; if (liveSparkPath) liveSparkPath.setAttribute('d', ''); }

  // ── scatter (PWM, RPM) accumulator for the active fan ──────────────
  // Reset whenever the active fan changes; keep one trail per sweep.
  var samples = [];        // [{x, y}]
  var activeFanKey = null; // PWMPath of currently animated fan
  var lastAppendT = 0;

  function resetScatter() {
    samples = [];
    while (samplesG && samplesG.firstChild) samplesG.removeChild(samplesG.firstChild);
    while (markersG && markersG.firstChild) markersG.removeChild(markersG.firstChild);
    if (curveLine) curveLine.setAttribute('d', '');
    if (curveArea) curveArea.setAttribute('d', '');
    if (cursorLine) { cursorLine.setAttribute('x1', -10); cursorLine.setAttribute('x2', -10); }
    if (cursorDot)  { cursorDot.setAttribute('cx',  -10); cursorDot.setAttribute('cy', CHART_H); }
    if (cursorHalo) { cursorHalo.setAttribute('cx', -10); cursorHalo.setAttribute('cy', CHART_H); }
  }

  function appendSample(pwm, rpm, maxRpm) {
    var x = pwmToX(pwm);
    var y = rpmToY(rpm, maxRpm);
    samples.push({ x: x, y: y, pwm: pwm, rpm: rpm });
    if (samplesG) {
      var dot = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
      dot.setAttribute('class', 'cal-sample');
      dot.setAttribute('cx', x.toFixed(1));
      dot.setAttribute('cy', y.toFixed(1));
      dot.setAttribute('r', 3);
      samplesG.appendChild(dot);
    }
    redrawCurve();
    if (cursorLine) { cursorLine.setAttribute('x1', x); cursorLine.setAttribute('x2', x); }
    if (cursorDot)  { cursorDot.setAttribute('cx', x);  cursorDot.setAttribute('cy', y); }
    if (cursorHalo) { cursorHalo.setAttribute('cx', x); cursorHalo.setAttribute('cy', y); }
  }

  function redrawCurve() {
    if (samples.length < 2) return;
    // Draw a smoothed line through samples sorted by PWM. We just pick a
    // monotonic PWM filter so duplicate-x samples don't make the curve loop.
    var sorted = samples.slice().sort(function (a, b) { return a.x - b.x; });
    var d = '', dArea = '';
    for (var i = 0; i < sorted.length; i++) {
      var p = sorted[i];
      d += (i === 0 ? 'M ' : ' L ') + p.x.toFixed(1) + ' ' + p.y.toFixed(1);
    }
    dArea = d + ' L ' + sorted[sorted.length - 1].x.toFixed(1) + ' ' + CHART_H + ' L ' + sorted[0].x.toFixed(1) + ' ' + CHART_H + ' Z';
    if (curveLine) curveLine.setAttribute('d', d);
    if (curveArea) curveArea.setAttribute('d', dArea);
  }

  function drawMarker(pwm, label) {
    if (!markersG) return;
    var x = pwmToX(pwm);
    var ln = document.createElementNS('http://www.w3.org/2000/svg', 'line');
    ln.setAttribute('class', 'cal-marker-line');
    ln.setAttribute('x1', x); ln.setAttribute('x2', x);
    ln.setAttribute('y1', 0); ln.setAttribute('y2', CHART_H);
    markersG.appendChild(ln);
    var tx = document.createElementNS('http://www.w3.org/2000/svg', 'text');
    tx.setAttribute('class', 'cal-marker-label');
    tx.setAttribute('x', x + 4);
    tx.setAttribute('y', 14);
    tx.textContent = label;
    markersG.appendChild(tx);
  }

  // ── pipeline ────────────────────────────────────────────────────────
  function paintPipeline(currentPhase) {
    var idx = PHASES.indexOf(currentPhase);
    if (idx < 0 && currentPhase) idx = -1; // unknown phase keeps everything queued
    var steps = pipelineEl ? pipelineEl.querySelectorAll('.pipe-step') : [];
    for (var i = 0; i < steps.length; i++) {
      steps[i].classList.remove('is-active', 'is-done');
      if (idx >= 0 && i < idx)      steps[i].classList.add('is-done');
      else if (idx >= 0 && i === idx) steps[i].classList.add('is-active');
    }
    var pct = idx < 0 ? 0 : ((idx + 0.4) / PHASES.length) * 100;
    if (pipelineFill) pipelineFill.style.width = pct.toFixed(1) + '%';
  }

  // ── elapsed clock ───────────────────────────────────────────────────
  var startedAt = null;
  function fmtClock(secs) {
    if (secs == null || !isFinite(secs) || secs < 0) return '—';
    var m = Math.floor(secs / 60), s = Math.floor(secs % 60);
    return (m < 10 ? '0' : '') + m + ':' + (s < 10 ? '0' : '') + s;
  }
  function tickClock(progress) {
    if (!startedAt) startedAt = Date.now();
    var elapsed = Math.floor((Date.now() - startedAt) / 1000);
    if (elapsedEl) elapsedEl.textContent = fmtClock(elapsed);
    if (!remainingEl) return;
    if (!progress || !progress.fans || progress.fans.length === 0) {
      remainingEl.textContent = '—';
      return;
    }
    var total = progress.fans.length;
    var doneFans = 0;
    for (var i = 0; i < progress.fans.length; i++) {
      if (progress.fans[i].cal_phase === 'done' || progress.fans[i].cal_phase === 'skipped') doneFans++;
    }
    if (doneFans === 0) { remainingEl.textContent = '—'; return; }
    var rate = elapsed / doneFans;       // seconds per completed fan
    var remain = Math.max(0, Math.round(rate * (total - doneFans)));
    remainingEl.textContent = '~' + fmtClock(remain);
  }

  // ── flavour rotator ─────────────────────────────────────────────────
  var flavourIdx = 0;
  function rotateFlavour() {
    if (!flavourEl) return;
    flavourEl.classList.add('cal-flavour--fade');
    setTimeout(function () {
      flavourIdx = (flavourIdx + 1) % PHASE_FLAVOURS.length;
      flavourEl.textContent = PHASE_FLAVOURS[flavourIdx];
      flavourEl.classList.remove('cal-flavour--fade');
    }, 600);
  }
  setInterval(rotateFlavour, 8000);

  // ── per-fan roster ──────────────────────────────────────────────────
  function fanIcon() {
    return '<svg class="cal-fan-blade" viewBox="0 0 24 24" aria-hidden="true">' +
      '<circle cx="12" cy="12" r="2" fill="currentColor"/>' +
      '<path d="M12 4 C 14 6 14 9 12 12 C 9 12 6 13 4 11 C 6 8 9 6 12 4 Z" fill="currentColor" opacity="0.85"/>' +
      '<path d="M20 12 C 18 14 15 14 12 12 C 12 9 11 6 13 4 C 16 6 18 9 20 12 Z" fill="currentColor" opacity="0.85"/>' +
      '<path d="M12 20 C 10 18 10 15 12 12 C 15 12 18 11 20 13 C 18 16 15 18 12 20 Z" fill="currentColor" opacity="0.85"/>' +
      '<path d="M4 12 C 6 10 9 10 12 12 C 12 15 13 18 11 20 C 8 18 6 15 4 12 Z" fill="currentColor" opacity="0.85"/>' +
      '</svg>';
  }
  function calStatusPill(p) {
    switch ((p && p.cal_phase) || 'pending') {
      case 'done':        return '<span class="status-pill ok">done</span>';
      case 'calibrating': return '<span class="status-pill info"><span class="cal-pulse-dot cal-pulse-dot--info"></span>measuring</span>';
      case 'skipped':     return '<span class="status-pill warn">skipped</span>';
      case 'error':       return '<span class="status-pill warn">error</span>';
      default:            return '<span class="status-pill ro">queued</span>';
    }
  }
  function fanResult(p) {
    if (!p) return '';
    if (p.cal_phase === 'done') {
      var bits = [];
      if (p.start_pwm != null) bits.push('start <strong>' + p.start_pwm + '</strong>');
      if (p.stop_pwm  != null) bits.push('stop <strong>'  + p.stop_pwm  + '</strong>');
      if (p.max_rpm)           bits.push('<strong>' + p.max_rpm + '</strong> rpm');
      return '<div class="cal-roster-result">' + bits.join(' · ') + '</div>';
    }
    if (p.cal_phase === 'calibrating') {
      var pct = Math.max(0, Math.min(100, p.cal_progress || 0));
      // CSP forbids inline style="" attributes under style-src 'self';
      // emit the percentage as a data attribute and a sibling pass over
      // .cal-roster-progress-fill applies element.style.width via CSSOM.
      return '<div class="cal-roster-progress"><div class="cal-roster-progress-fill" data-progress="' + pct + '"></div></div>';
    }
    return '';
  }
  function rowClass(p) {
    switch ((p && p.cal_phase) || 'pending') {
      case 'done':        return 'is-done';
      case 'calibrating': return 'is-running';
      case 'skipped':     return 'is-skipped';
      default:            return 'is-queued';
    }
  }
  function rosterSortKey(p) {
    // running first, then queued, then done at bottom
    switch (p.cal_phase) {
      case 'calibrating': return 0;
      case 'pending':     return 1;
      case 'error':       return 2;
      case 'done':        return 3;
      case 'skipped':     return 4;
      default:            return 5;
    }
  }

  function renderRoster(fans) {
    if (!rosterEl) return;
    if (!fans || fans.length === 0) {
      rosterEl.innerHTML = '<div class="cal-roster-empty">Waiting for hardware scan…</div>';
      return;
    }
    var sorted = fans.slice().sort(function (a, b) {
      var ka = rosterSortKey(a), kb = rosterSortKey(b);
      if (ka !== kb) return ka - kb;
      return (a.name || '').localeCompare(b.name || '');
    });
    var done = 0, run = 0, queued = 0, skipped = 0;
    var html = '';
    for (var i = 0; i < sorted.length; i++) {
      var f = sorted[i];
      switch (f.cal_phase) {
        case 'done':        done++; break;
        case 'calibrating': run++;  break;
        case 'skipped':     skipped++; break;
        default:            queued++; break;
      }
      html += '<div class="cal-roster-row ' + rowClass(f) + '">'
            +   '<span class="cal-roster-icon">' + fanIcon() + '</span>'
            +   '<div class="cal-roster-meta">'
            +     '<div class="cal-roster-name">' + escapeHTML(f.name || '(unnamed)') + '</div>'
            +     '<div class="cal-roster-source">' + escapeHTML(shortSource(f)) + '</div>'
            +   '</div>'
            +   '<div class="cal-roster-state">'
            +     calStatusPill(f)
            +     fanResult(f)
            +   '</div>'
            + '</div>';
    }
    rosterEl.innerHTML = html;
    // Apply width from data-progress now that the markup is in the DOM.
    Array.prototype.forEach.call(
      rosterEl.querySelectorAll('.cal-roster-progress-fill[data-progress]'),
      function (el) { el.style.width = el.dataset.progress + '%'; }
    );
    if (counterDoneEl)  counterDoneEl.textContent  = done;
    if (counterTotalEl) counterTotalEl.textContent = sorted.length;
    if (totalsDoneEl)   totalsDoneEl.textContent   = done;
    if (totalsRunEl)    totalsRunEl.textContent    = run;
    if (totalsQueueEl)  totalsQueueEl.textContent  = queued;
    if (totalsSkipEl)   totalsSkipEl.textContent   = skipped;
    if (totalsSkipPill) totalsSkipPill.hidden = skipped === 0;
  }
  function shortSource(f) {
    if (!f) return '';
    if (f.type === 'nvidia') return 'nvml · ' + (f.pwm_path || '');
    var p = f.pwm_path || '';
    var slash = p.lastIndexOf('/');
    return slash > 0 ? p.substring(0, slash + 1) + p.substring(slash + 1) : p;
  }
  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  // ── system profile pane ─────────────────────────────────────────────
  function renderProfile(p) {
    if (!profileCard) return;
    var hasAny = !!(p.board || p.chip_name || (p.profile && (p.profile.cpu_model || p.profile.gpu_model)));
    profileCard.hidden = !hasAny;
    if (!hasAny) return;
    if (p.board) profileBoardEl.textContent = p.board;
    else profileBoardEl.textContent = 'Discovered hardware';
    var pr = p.profile || {};
    if (pr.cpu_model) {
      profileCpuRow.hidden = false;
      profileCpuEl.textContent = pr.cpu_model;
      profileCpuTdp.textContent = pr.cpu_tdp_w ? pr.cpu_tdp_w + ' W' : '';
    }
    if (pr.gpu_model) {
      profileGpuRow.hidden = false;
      profileGpuEl.textContent = pr.gpu_model;
      profileGpuTdp.textContent = pr.gpu_power_w ? pr.gpu_power_w + ' W' : '';
    }
    if (p.chip_name) {
      profileChipRow.hidden = false;
      profileChipEl.textContent = p.chip_name;
    }
  }

  // ── live card update from a Progress payload ────────────────────────
  function updateLiveCard(p, liveStatus) {
    headlineEl.textContent = PHASE_HEADLINES[p.phase] || (p.phase_msg || 'Calibrating');
    livePhaseSubEl.textContent = p.phase_msg || '';

    var running = null;
    if (p.fans && p.fans.length > 0) {
      for (var i = 0; i < p.fans.length; i++) {
        if (p.fans[i].cal_phase === 'calibrating') { running = p.fans[i]; break; }
      }
    }
    if (running) {
      if (activeFanKey !== running.pwm_path) {
        activeFanKey = running.pwm_path;
        resetScatter();
        resetSpark();
      }
      liveNameEl.textContent = running.name || '(fan)';
      liveSourceEl.textContent = shortSource(running);
      var pct = Math.max(0, Math.min(100, running.cal_progress || 0));
      liveProgressEl.textContent = pct;
      liveProgBar.style.width = pct + '%';

      // Approximate the PWM step from progress + sweep range. Real samples
      // come from /api/v1/status when available — see below.
      var span = Math.max(255 - (running.stop_pwm || 0), 32);
      var pwmTarget = 255 - Math.round((pct / 100) * span);
      livePwmEl.textContent = pwmTarget;
      livePwmBarEl.style.width = ((pwmTarget / 255) * 100).toFixed(1) + '%';

      // Phase label sub-text
      livePhaseEl.textContent = pct < 30 ? 'descend'
                              : pct < 70 ? 'measuring'
                              :            'mapping';

      // Pull a real RPM sample if liveStatus has it; else estimate.
      var rpm = null;
      if (liveStatus && liveStatus.fans) {
        for (var j = 0; j < liveStatus.fans.length; j++) {
          if (liveStatus.fans[j].pwm_path === running.pwm_path) {
            rpm = liveStatus.fans[j].rpm; break;
          }
        }
      }
      if (rpm == null) {
        // Fallback: estimate from running fan profile (smooth visual).
        var maxR = running.max_rpm || 1800;
        rpm = Math.round(maxR * Math.max(0, (pwmTarget - (running.stop_pwm || 30)) / Math.max(255 - (running.stop_pwm || 30), 1)));
      }
      liveRpmEl.textContent = rpm;
      pushSpark(rpm);

      // Append a scatter sample at most every 220ms so the chart stays
      // dense enough to look alive but doesn't spam DOM.
      var now = Date.now();
      if (now - lastAppendT > 220) {
        appendSample(pwmTarget, rpm, running.max_rpm || 2400);
        lastAppendT = now;
      }
    } else {
      // No fan currently calibrating — clear cursor, dim the live card.
      liveNameEl.textContent = (p.phase === 'finalizing' || p.done) ? 'All fans calibrated' : 'Preparing next fan…';
      liveSourceEl.textContent = '—';
      livePhaseEl.textContent = p.phase || 'idle';
    }

    // Markers from any newly-completed fan in the active sweep group.
    if (p.fans) {
      for (var k = 0; k < p.fans.length; k++) {
        var f = p.fans[k];
        if (f.cal_phase === 'done' && f.pwm_path === activeFanKey) {
          // Once the active fan is done, draw its markers and freeze chart.
          if (f.stop_pwm)  drawMarker(f.stop_pwm,  'stop ' + f.stop_pwm);
          if (f.start_pwm) drawMarker(f.start_pwm, 'start ' + f.start_pwm);
        }
      }
    }
  }

  // ── poll loop ───────────────────────────────────────────────────────
  var pollTimer = null;
  var liveTimer = null;
  var liveStatusCache = null;
  var pollInterval = 700;
  var inDemoMode = false;

  function poll() {
    fetch('/api/v1/setup/status', { credentials: 'same-origin' })
      .then(function (r) {
        if (r.status === 401 || r.status === 403) {
          // No session — bounce to setup screen.
          window.location.replace('/setup');
          return null;
        }
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      })
      .then(function (p) {
        if (!p) return;
        if (!p.needed && p.applied) {
          // Setup already done — straight to dashboard / root.
          window.location.replace('/');
          return;
        }
        if (!p.running && !p.done && p.needed && !p.error) {
          // Auto-start on first arrival, the way the existing wizard does.
          fetch('/api/v1/setup/start', { method: 'POST', credentials: 'same-origin' });
        }
        applyProgress(p, liveStatusCache);
      })
      .catch(function (err) {
        // Network error: drop into demo mode so the page never looks frozen.
        if (!inDemoMode) {
          inDemoMode = true;
          startDemo();
        }
      });
  }
  function pollLive() {
    fetch('/api/v1/status', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (s) { if (s) liveStatusCache = s; })
      .catch(function () {});
  }

  function applyProgress(p, liveStatus) {
    paintPipeline(p.phase);
    tickClock(p);
    updateLiveCard(p, liveStatus);
    renderRoster(p.fans || []);
    renderProfile(p);

    // brand-mark spinning while work in flight
    var bm = document.querySelector('.cal-head .brand-mark');
    if (bm) {
      if (p.done || p.error) bm.classList.remove('spinning');
      else bm.classList.add('spinning');
    }

    if (p.error) {
      errorBanner.hidden = false;
      errorSubEl.textContent = p.error;
      // Surface "Continue without fan control" only when calibration
      // finished but discovered zero fans — the only case where
      // /api/v1/setup/apply will fall back to monitor-only mode.
      // Retry stays available so the operator can re-run discovery
      // after fixing a missing kernel module / cabling / etc.
      if (skipBtn) skipBtn.hidden = !(p.done && (!p.fans || p.fans.length === 0));
    } else {
      errorBanner.hidden = true;
      if (skipBtn) skipBtn.hidden = true;
    }

    if (p.done && !p.error && !p.applied) {
      doneBanner.hidden = false;
      var summaryFans = (p.fans || []).filter(function (f) { return f.cal_phase === 'done'; }).length;
      doneSubEl.textContent = 'Calibrated ' + summaryFans + ' fan'
        + (summaryFans === 1 ? '' : 's')
        + '. Apply to take over from firmware control and start using your custom curve.';
    } else if (p.applied) {
      // Daemon will restart; redirect once /api/ping comes back up.
      doneBanner.hidden = false;
      doneSubEl.textContent = 'Restarting daemon — this page will reload.';
      waitForRestart();
    }
  }

  // ── waitForRestart: poll /api/ping after Apply, reload when daemon is back
  function waitForRestart() {
    if (pollTimer) clearInterval(pollTimer);
    if (liveTimer) clearInterval(liveTimer);
    var iv = setInterval(function () {
      fetch('/api/v1/ping', { cache: 'no-store' })
        .then(function (r) { if (r.ok) { clearInterval(iv); window.location.replace('/'); } })
        .catch(function () {});
    }, 1500);
  }

  // ── abort ───────────────────────────────────────────────────────────
  if (abortBtn) abortBtn.addEventListener('click', function () {
    if (!confirm('Abort calibration and return to setup? Your password is saved; this only stops the sweep.')) return;
    fetch('/api/v1/setup/calibrate/abort', { method: 'POST', credentials: 'same-origin' })
      .then(function () {
        // Wait one tick for status to settle, then go to setup.
        setTimeout(function () { window.location.replace('/setup'); }, 300);
      });
  });

  // ── apply ───────────────────────────────────────────────────────────
  if (applyBtn) applyBtn.addEventListener('click', function () {
    applyBtn.disabled = true;
    applyBtn.textContent = 'Applying…';
    fetch('/api/v1/setup/apply', { method: 'POST', credentials: 'same-origin' })
      .then(function (r) {
        if (!r.ok) {
          applyBtn.disabled = false;
          applyBtn.textContent = 'Apply & Continue';
          errorBanner.hidden = false;
          errorSubEl.textContent = 'Apply failed (HTTP ' + r.status + ').';
          return;
        }
        doneSubEl.textContent = 'Applying… restarting daemon.';
        waitForRestart();
      })
      .catch(function (err) {
        applyBtn.disabled = false;
        applyBtn.textContent = 'Apply & Continue';
        errorBanner.hidden = false;
        errorSubEl.textContent = 'Apply failed: ' + (err && err.message || 'network error');
      });
  });

  // ── retry ───────────────────────────────────────────────────────────
  if (retryBtn) retryBtn.addEventListener('click', function () {
    errorBanner.hidden = true;
    if (skipBtn) skipBtn.hidden = true;
    fetch('/api/v1/setup/reset', { method: 'POST', credentials: 'same-origin' })
      .then(function () {
        setTimeout(function () { fetch('/api/v1/setup/start', { method: 'POST', credentials: 'same-origin' }); }, 200);
      });
  });

  // ── send diagnostic bundle (#792 wizard recovery surface) ──────────
  // POST /api/v1/diag/bundle generates a redacted tarball server-side and
  // returns its filename + download URL. Triggering the download with a
  // hidden anchor click avoids navigating away from the wizard, so the
  // operator stays on the calibration page after delivery.
  function setBundleStatus(text, isError) {
    if (!bundleStatusEl) return;
    bundleStatusEl.hidden = !text;
    bundleStatusEl.textContent = text || '';
    bundleStatusEl.classList.toggle('is-error', !!isError);
  }
  if (bundleBtn) bundleBtn.addEventListener('click', function () {
    bundleBtn.disabled = true;
    var oldLabel = bundleBtn.textContent;
    bundleBtn.textContent = 'Generating…';
    setBundleStatus('Collecting logs and redacting hostnames…', false);
    fetch('/api/v1/diag/bundle', { method: 'POST', credentials: 'same-origin' })
      .then(function (r) {
        if (!r.ok) {
          return r.text().then(function (t) { throw new Error(t || ('HTTP ' + r.status)); });
        }
        return r.json();
      })
      .then(function (j) {
        var a = document.createElement('a');
        a.href = j.download_url;
        a.download = j.filename || '';
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        setBundleStatus('Bundle ready: ' + j.filename + ' (downloading).', false);
      })
      .catch(function (err) {
        setBundleStatus('Could not generate bundle: ' + (err && err.message || 'network error'), true);
      })
      .then(function () {
        bundleBtn.disabled = false;
        bundleBtn.textContent = oldLabel;
      });
  });

  // ── skip: opt into monitor-only mode when no fans are discoverable ──
  // Hits /api/v1/setup/apply with no generated config — the daemon-side
  // empty-fanset escape (handleSetupApply) writes config.Empty(), marks
  // setup applied (with persistent marker), and triggers a reload. Once
  // /api/v1/ping comes back, waitForRestart redirects to /.
  if (skipBtn) skipBtn.addEventListener('click', function () {
    if (!confirm('Continue without fan control? ventd will run in monitor-only mode.')) return;
    skipBtn.disabled = true;
    skipBtn.textContent = 'Continuing…';
    fetch('/api/v1/setup/apply', { method: 'POST', credentials: 'same-origin' })
      .then(function (r) {
        if (r.ok) {
          errorBanner.hidden = true;
          doneBanner.hidden = false;
          doneSubEl.textContent = 'Restarting daemon — this page will reload.';
          waitForRestart();
          return;
        }
        skipBtn.disabled = false;
        skipBtn.textContent = 'Continue without fan control';
        errorSubEl.textContent = 'Could not switch to monitor-only mode (HTTP ' + r.status + ').';
      })
      .catch(function (err) {
        skipBtn.disabled = false;
        skipBtn.textContent = 'Continue without fan control';
        errorSubEl.textContent = 'Could not switch to monitor-only mode: '
          + (err && err.message || 'network error');
      });
  });

  // ── demo mode (used when API is unreachable) ────────────────────────
  // Keeps the page visually alive for review/preview without backend.
  function startDemo() {
    if (pollTimer) clearInterval(pollTimer);
    if (liveTimer) clearInterval(liveTimer);
    var demoFans = [
      { name: 'CPU fan',         pwm_path: 'pwm1', max_rpm: 2310 },
      { name: 'Front intake',    pwm_path: 'pwm2', max_rpm: 1820 },
      { name: 'Front mid',       pwm_path: 'pwm3', max_rpm: 1820 },
      { name: 'Rear exhaust',    pwm_path: 'pwm4', max_rpm: 1900 },
      { name: 'Top exhaust 1',   pwm_path: 'pwm5', max_rpm: 1700 },
      { name: 'Top exhaust 2',   pwm_path: 'pwm6', max_rpm: 1700 },
      { name: 'AIO pump',        pwm_path: 'pump', max_rpm: 2840, is_pump: true },
      { name: 'AIO fan 1',       pwm_path: 'aio1', max_rpm: 2400 },
      { name: 'GPU 0 fan 0',     pwm_path: 'gpu0', max_rpm: 3000 },
      { name: 'PSU fan',         pwm_path: 'psu',  max_rpm: 0 }
    ];
    var phases = PHASES.slice();
    var phaseI = 0;
    var fanI = 0;
    var fanProgress = 0;
    headlineEl.textContent = PHASE_HEADLINES[phases[0]];

    var demoState = {
      needed: true, running: true, done: false, applied: false,
      phase: phases[0],
      phase_msg: 'Discovering hardware…',
      board: 'Demo · ASUS Z790-A',
      chip_name: 'NCT6798D',
      profile: { cpu_model: 'Demo i7-13700K', cpu_tdp_w: 250, gpu_model: 'Demo RTX 4090', gpu_power_w: 450 },
      fans: demoFans.map(function (f) {
        return { name: f.name, type: f.pwm_path === 'gpu0' ? 'nvidia' : 'hwmon',
                 pwm_path: f.pwm_path, cal_phase: 'pending', cal_progress: 0,
                 max_rpm: f.max_rpm, is_pump: f.is_pump };
      })
    };

    function step() {
      if (phaseI < 5) {
        // pre-calibrate phases run for 2.4s each
        phaseI++;
        if (phaseI < phases.length) {
          demoState.phase = phases[phaseI];
          demoState.phase_msg = PHASE_HEADLINES[phases[phaseI]];
        }
        applyProgress(demoState, null);
        if (phaseI < 5) setTimeout(step, 2400);
        else            setTimeout(step, 800);
        return;
      }
      // calibration phase: walk fans
      demoState.phase = 'calibrating';
      demoState.phase_msg = 'Sweeping each fan from 100% to 0%';
      if (fanI >= demoFans.length) {
        demoState.phase = 'finalizing';
        applyProgress(demoState, null);
        setTimeout(function () {
          demoState.phase = 'finalizing';
          demoState.done = true;
          applyProgress(demoState, null);
        }, 2000);
        return;
      }
      var f = demoState.fans[fanI];
      f.cal_phase = 'calibrating';
      f.cal_progress = fanProgress;
      // synthesize PSU as skipped
      if (f.pwm_path === 'psu') {
        f.cal_phase = 'skipped';
        fanI++; fanProgress = 0;
        applyProgress(demoState, null);
        setTimeout(step, 200);
        return;
      }
      applyProgress(demoState, null);
      fanProgress += 14;
      if (fanProgress >= 100) {
        f.cal_phase = 'done';
        f.cal_progress = 100;
        f.start_pwm = 30 + Math.floor(Math.random() * 24);
        f.stop_pwm  = Math.max(20, f.start_pwm - 12);
        fanI++;
        fanProgress = 0;
        setTimeout(step, 350);
      } else {
        setTimeout(step, 320);
      }
    }
    setTimeout(step, 700);
  }

  // ── start ───────────────────────────────────────────────────────────
  paintPipeline('detecting');
  flavourEl.textContent = PHASE_FLAVOURS[0];
  poll();
  pollTimer = setInterval(poll,    pollInterval);
  liveTimer = setInterval(pollLive, 800);
})();
