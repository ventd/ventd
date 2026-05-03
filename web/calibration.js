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
  // Visual phase advance is throttled so each phase is visible for at least
  // MIN_PHASE_DISPLAY_MS even when the daemon transitions through it in
  // milliseconds. Phoenix's HIL feedback (#821): "the first three steps
  // were done before the page even loaded". Without throttling, detecting
  // → installing_driver → scanning_fans → detecting_rpm complete in
  // <100ms on a system with no OOT install needed, so the operator can't
  // visually parse what just happened. Throttling renders each step's
  // pulse + sub-text long enough to read.
  var MIN_PHASE_DISPLAY_MS = 700;
  var displayPhase = null;
  var displayPhaseAt = 0;
  var pendingPhase = null;
  var phaseAdvanceTimer = null;
  function setDisplayPhase(p) {
    displayPhase = p;
    displayPhaseAt = Date.now();
    paintPipelineNow(p);
  }
  function adoptPhase(target) {
    if (target == null) return;
    pendingPhase = target;
    if (phaseAdvanceTimer != null) return; // already pumping
    pumpPhaseAdvance();
  }
  function pumpPhaseAdvance() {
    phaseAdvanceTimer = null;
    if (pendingPhase == null) return;
    if (displayPhase == null) {
      setDisplayPhase(pendingPhase);
      if (displayPhase === pendingPhase) { pendingPhase = null; return; }
    }
    if (displayPhase === pendingPhase) { pendingPhase = null; return; }
    var elapsed = Date.now() - displayPhaseAt;
    if (elapsed >= MIN_PHASE_DISPLAY_MS) {
      var idx = PHASES.indexOf(displayPhase);
      var targetIdx = PHASES.indexOf(pendingPhase);
      // Walk one step at a time for forward jumps so each intermediate
      // phase is visible. Backward / unknown jumps go directly.
      var next = (idx >= 0 && targetIdx > idx + 1) ? PHASES[idx + 1] : pendingPhase;
      setDisplayPhase(next);
      if (displayPhase !== pendingPhase) {
        phaseAdvanceTimer = setTimeout(pumpPhaseAdvance, MIN_PHASE_DISPLAY_MS);
      } else {
        pendingPhase = null;
      }
    } else {
      phaseAdvanceTimer = setTimeout(pumpPhaseAdvance, MIN_PHASE_DISPLAY_MS - elapsed);
    }
  }
  function paintPipeline(currentPhase) {
    adoptPhase(currentPhase);
  }
  function paintPipelineNow(currentPhase) {
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

  // ── live activity narrator ──────────────────────────────────────────
  // Synthesises a streaming event log from /api/setup/status polls. Every
  // observed state transition (phase change, per-fan phase change, PWM
  // step, RPM milestone) appends one timestamped line to the activity
  // feed in the right column. Bounded at MAX_ACTIVITY_LINES so the DOM
  // doesn't grow unboundedly on long runs.
  //
  // The narrator is purely client-side — it doesn't need any backend
  // protocol additions. When the daemon eventually exposes per-substep
  // narration as a first-class field (PR-D follow-up), we can replace
  // these synthesised lines with the daemon's authoritative ones.
  var MAX_ACTIVITY_LINES = 100;
  var feedEl = document.getElementById('cal-activity-feed');
  var feedCounterEl = document.getElementById('cal-activity-counter');
  var feedCount = 0;
  var lastDaemonPhase = '';
  var lastFanState = {}; // pwm_path -> { detect_phase, polarity_phase, cal_phase, cal_progress, last_pwm, last_rpm }
  var lastInstallLogIdx = 0;

  function activityPush(kind, fan, msg, detail) {
    if (!feedEl) return;
    // Drop the empty placeholder on first real append.
    if (feedCount === 0) feedEl.innerHTML = '';

    var li = document.createElement('li');
    li.className = 'cal-activity-line cal-activity-line--' + (kind || 'quiet');

    var t = document.createElement('span');
    t.className = 'cal-activity-time mono';
    var now = new Date();
    var hh = ('0' + now.getHours()).slice(-2);
    var mm = ('0' + now.getMinutes()).slice(-2);
    var ss = ('0' + now.getSeconds()).slice(-2);
    var ms = ('00' + now.getMilliseconds()).slice(-3);
    t.textContent = hh + ':' + mm + ':' + ss + '.' + ms;
    li.appendChild(t);

    var msgWrap = document.createElement('span');
    msgWrap.className = 'cal-activity-msg';
    if (fan) {
      var fanSpan = document.createElement('span');
      fanSpan.className = 'cal-activity-fan';
      fanSpan.textContent = fan + ' ';
      msgWrap.appendChild(fanSpan);
    }
    msgWrap.appendChild(document.createTextNode(msg));
    if (detail) {
      var det = document.createElement('span');
      det.className = 'cal-activity-detail';
      det.textContent = ' ' + detail;
      msgWrap.appendChild(det);
    }
    li.appendChild(msgWrap);

    // Newest at top so users see the latest action without scrolling.
    feedEl.insertBefore(li, feedEl.firstChild);
    feedCount++;

    // Trim from the bottom (oldest) when over cap.
    while (feedEl.children.length > MAX_ACTIVITY_LINES) {
      feedEl.removeChild(feedEl.lastChild);
    }
    if (feedCounterEl) {
      feedCounterEl.textContent = feedCount + (feedCount === 1 ? ' event' : ' events');
    }
  }

  function fanLabel(f) {
    return f.label || f.name || f.id || f.pwm_path || '?';
  }

  function narrateProgress(p, liveStatus) {
    if (!p) return;

    // Daemon-level phase transitions.
    if (p.phase && p.phase !== lastDaemonPhase) {
      var phaseMsg = (PHASE_DESCRIPTIONS && PHASE_DESCRIPTIONS[p.phase]) || p.phase;
      var detail = p.phase_msg && p.phase_msg !== phaseMsg ? p.phase_msg : '';
      activityPush('success', null, '▸ ' + phaseMsg, detail ? '— ' + detail : '');
      lastDaemonPhase = p.phase;
    }

    // Driver install log lines stream during installing_driver.
    if (p.install_log && p.install_log.length > lastInstallLogIdx) {
      for (var k = lastInstallLogIdx; k < p.install_log.length; k++) {
        var line = p.install_log[k];
        if (line && line.length > 0) {
          activityPush('quiet', null, line);
        }
      }
      lastInstallLogIdx = p.install_log.length;
    }

    // Per-fan transitions.
    if (p.fans && p.fans.length) {
      for (var i = 0; i < p.fans.length; i++) {
        var f = p.fans[i];
        var key = f.pwm_path || f.id || ('fan_' + i);
        var prev = lastFanState[key] || {};
        var label = fanLabel(f);

        // Detect phase: pending → detecting → found / none / n/a
        if (f.detect_phase && f.detect_phase !== prev.detect_phase) {
          if (f.detect_phase === 'detecting') {
            activityPush('quiet', label, 'Searching for tachometer…');
          } else if (f.detect_phase === 'found') {
            var rpmHint = f.rpm_path ? ('via ' + f.rpm_path.split('/').pop()) : '';
            activityPush('success', label, '✓ Tach paired', rpmHint);
          } else if (f.detect_phase === 'none') {
            activityPush('warn', label, 'No tach found — phantom channel');
          } else if (f.detect_phase === 'n/a') {
            activityPush('quiet', label, 'NVML/IPMI fan — tach not applicable');
          }
        }

        // Polarity phase: pending → testing → normal/inverted/phantom
        if (f.polarity_phase && f.polarity_phase !== prev.polarity_phase) {
          if (f.polarity_phase === 'testing') {
            activityPush('quiet', label, 'Polarity probe — writing PWM=128, holding 3s…');
          } else if (f.polarity_phase === 'normal') {
            activityPush('success', label, '✓ Polarity normal — duty up = speed up');
          } else if (f.polarity_phase === 'inverted') {
            activityPush('warn', label, '⚠ Polarity inverted — header is wired backwards (handled)');
          } else if (f.polarity_phase === 'phantom') {
            activityPush('warn', label, 'Phantom channel — no physical fan reacts');
          }
        }

        // Calibration phase: pending → calibrating → done/skipped/error
        if (f.cal_phase && f.cal_phase !== prev.cal_phase) {
          if (f.cal_phase === 'calibrating') {
            activityPush('quiet', label, 'Calibration sweep started');
          } else if (f.cal_phase === 'done') {
            var startStr = f.start_pwm ? ('start=' + f.start_pwm) : '';
            var stopStr  = f.stop_pwm  ? ('stop=' + f.stop_pwm)   : '';
            var maxStr   = f.max_rpm   ? ('max=' + f.max_rpm + ' RPM') : '';
            var det = [startStr, stopStr, maxStr].filter(function (x) { return x; }).join(' · ');
            activityPush('success', label, '✓ Calibrated', det);
          } else if (f.cal_phase === 'skipped') {
            activityPush('warn', label, 'Skipped — no usable PWM range');
          } else if (f.cal_phase === 'error') {
            activityPush('error', label, '✗ Calibration error', f.error || '');
          }
        }

        // Calibration progress milestones — every 25%.
        if (f.cal_phase === 'calibrating' && typeof f.cal_progress === 'number') {
          var bucket = Math.floor(f.cal_progress / 25) * 25;
          var prevBucket = Math.floor((prev.cal_progress || 0) / 25) * 25;
          if (bucket > prevBucket && bucket > 0) {
            activityPush('quiet', label, 'Sweep ' + bucket + '% complete');
          }
        }

        lastFanState[key] = {
          detect_phase: f.detect_phase,
          polarity_phase: f.polarity_phase,
          cal_phase: f.cal_phase,
          cal_progress: f.cal_progress
        };
      }
    }

    // Done / error edge transitions.
    if (p.done && !lastDaemonPhase.match(/^_done_/)) {
      activityPush('success', null, '✓ Calibration complete');
      lastDaemonPhase = '_done_' + (p.error ? 'err' : 'ok');
    }
    if (p.error && lastDaemonPhase !== '_error_' + p.error) {
      activityPush('error', null, '✗ ' + p.error);
      lastDaemonPhase = '_error_' + p.error;
    }
  }

  // Map for narrateProgress to humanise daemon phase names.
  var PHASE_DESCRIPTIONS = {
    detecting:         'Detecting hardware',
    installing_driver: 'Loading kernel module',
    scanning_fans:     'Scanning PWM controllers',
    detecting_rpm:     'Pairing fans with tachometers',
    probing_polarity:  'Probing duty-cycle polarity',
    calibrating:       'Sweeping fan response curves',
    finalizing:        'Building thermal profile'
  };

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
    // Phases without per-fan progress (detecting, installing_driver,
    // scanning_fans) get an "elapsed" suffix so the operator sees the
    // counter tick — without it the headline is a static line and
    // the wizard appears frozen during the OOT module build (which
    // can take 60s+). Phoenix's HIL feedback: "drivers installing
    // need to show some sort of progress".
    var head = PHASE_HEADLINES[p.phase] || (p.phase_msg || 'Calibrating');
    if (p.phase === 'detecting' || p.phase === 'installing_driver' || p.phase === 'scanning_fans') {
      var elapsed = startedAt ? Math.floor((Date.now() - startedAt) / 1000) : 0;
      if (elapsed >= 2) {
        head = head + ' · ' + elapsed + 's elapsed';
      }
    }
    headlineEl.textContent = head;
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
    narrateProgress(p, liveStatus);

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

      // v0.5.9 wizard recovery cards (#800). Render the per-class
      // remediation list when the classifier matched. Empty array
      // (or ClassUnknown ⇒ single bundle entry already in the
      // existing actions row) hides the cards container.
      renderRecoveryCards(p.remediation || [], p.failure_class || '');
    } else {
      errorBanner.hidden = true;
      if (skipBtn) skipBtn.hidden = true;
      // Hide cards when error clears so a Retry that succeeds
      // doesn't leave stale cards visible.
      var cardsEl = document.getElementById('cal-recovery-cards');
      if (cardsEl) cardsEl.hidden = true;
    }

    // Live card visibility tracks the done state — when calibration is
    // complete the done banner takes over the left column (#821), so
    // hiding the live card avoids a stacked double-render.
    var liveCardEl = document.getElementById('cal-live-card');
    if (p.done && !p.error && !p.applied) {
      doneBanner.hidden = false;
      if (liveCardEl) liveCardEl.hidden = true;
      var summaryFans = (p.fans || []).filter(function (f) { return f.cal_phase === 'done'; }).length;
      doneSubEl.textContent = 'Calibrated ' + summaryFans + ' fan'
        + (summaryFans === 1 ? '' : 's')
        + '. Apply to take over from firmware control and start using your custom curve.';
    } else if (p.applied) {
      // Daemon will restart; redirect once /api/ping comes back up.
      doneBanner.hidden = false;
      if (liveCardEl) liveCardEl.hidden = true;
      doneSubEl.textContent = 'Restarting daemon — this page will reload.';
      waitForRestart();
    } else {
      // Show live card during active calibration (re-shown on Retry after
      // an error path that previously hid it).
      if (liveCardEl) liveCardEl.hidden = false;
    }

    // Finalising-phase spinner overlay — Phoenix's HIL feedback (#821):
    // "curve calc has no spinner". The live card freezes between the last
    // sweep finishing and the done banner appearing because the daemon is
    // building the thermal curve in-process, which can take a couple of
    // seconds on slower CPUs. Toggling .is-finalizing on the live card
    // adds a visible spinner via CSS so the operator knows work is still
    // happening, not that the wizard hung.
    if (liveCardEl) {
      var finalizing = (p.phase === 'finalizing') && !p.done && !p.error;
      liveCardEl.classList.toggle('is-finalizing', finalizing);
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

  // ── v0.5.9 wizard recovery cards (#800) ────────────────────────────
  //
  // renderRecoveryCards rebuilds the cards list from the latest
  // /api/v1/setup/status payload. Cards are stateless; clicking an
  // action button POSTs to action_url and re-renders the result
  // inline (action_post) or in the modal (modal_instr / docs_only
  // links open in a new tab).
  function renderRecoveryCards(remediation, failureClass) {
    var host = document.getElementById('cal-recovery-cards');
    if (!host) return;
    if (!remediation || remediation.length === 0) {
      host.hidden = true;
      host.innerHTML = '';
      return;
    }
    host.hidden = false;
    host.innerHTML = '';

    // Optional class label up top (subtle, just for context).
    if (failureClass) {
      var label = document.createElement('div');
      label.className = 'cal-recovery-class';
      label.textContent = 'Detected: ' + failureClass.replace(/_/g, ' ');
      host.appendChild(label);
    }

    remediation.forEach(function (rem) {
      var card = document.createElement('div');
      card.className = 'cal-recovery-card';
      card.dataset.kind = rem.kind || '';

      var title = document.createElement('div');
      title.className = 'cal-recovery-title';
      title.textContent = rem.label || '';
      card.appendChild(title);

      if (rem.description) {
        var desc = document.createElement('div');
        desc.className = 'cal-recovery-desc';
        desc.textContent = rem.description;
        card.appendChild(desc);
      }

      var actions = document.createElement('div');
      actions.className = 'cal-recovery-actions';

      // Primary button. Behaviour depends on kind:
      //   - action_post : POST action_url, render structured result.
      //   - modal_instr : POST action_url, render commands in modal.
      //   - docs_only   : opens doc_url in a new tab; no POST.
      if (rem.kind === 'docs_only') {
        if (rem.doc_url) {
          var link = document.createElement('a');
          link.className = 'btn btn--primary btn--sm';
          link.href = rem.doc_url;
          link.target = '_blank';
          link.rel = 'noopener noreferrer';
          link.textContent = 'Open instructions';
          actions.appendChild(link);
        }
      } else if (rem.action_url) {
        var btn = document.createElement('button');
        btn.className = 'btn btn--primary btn--sm';
        btn.type = 'button';
        btn.textContent = rem.kind === 'modal_instr' ? 'Show instructions' : 'Apply fix';
        btn.addEventListener('click', function () {
          handleRecoveryAction(card, btn, rem);
        });
        actions.appendChild(btn);
      }

      // Secondary "Learn more" link (renders alongside the button).
      if (rem.doc_url && rem.kind !== 'docs_only') {
        var docLink = document.createElement('a');
        docLink.className = 'cal-recovery-doclink';
        docLink.href = rem.doc_url;
        docLink.target = '_blank';
        docLink.rel = 'noopener noreferrer';
        docLink.textContent = 'Learn more';
        actions.appendChild(docLink);
      }

      card.appendChild(actions);

      // Result container — populated on POST response.
      var result = document.createElement('div');
      result.className = 'cal-recovery-result';
      result.hidden = true;
      card.appendChild(result);

      host.appendChild(card);
    });
  }

  // handleRecoveryAction POSTs to a remediation card's action_url
  // and renders the response (install log / instructions modal).
  function handleRecoveryAction(card, btn, rem) {
    var resultEl = card.querySelector('.cal-recovery-result');
    btn.disabled = true;
    var oldLabel = btn.textContent;
    btn.textContent = '…';
    fetch(rem.action_url, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: rem.action_body
        ? rem.action_body
        : (rem.action_url.indexOf('/diag/bundle') >= 0 ? '' : null),
    })
      .then(function (r) {
        if (!r.ok) {
          return r.text().then(function (t) { throw new Error(t || ('HTTP ' + r.status)); });
        }
        return r.json();
      })
      .then(function (j) {
        // Modal-instr: open modal with command list.
        if (rem.kind === 'modal_instr' && Array.isArray(j.commands)) {
          showInstructionsModal(rem.label, j.detail || '', j.commands);
          btn.disabled = false;
          btn.textContent = oldLabel;
          return;
        }
        // diag-bundle: trigger download.
        if (j.download_url) {
          var a = document.createElement('a');
          a.href = j.download_url;
          a.download = j.filename || '';
          document.body.appendChild(a);
          a.click();
          document.body.removeChild(a);
          renderRecoveryResult(resultEl, true,
            'Bundle ready: ' + (j.filename || '') + ' (downloading).');
          btn.disabled = false;
          btn.textContent = oldLabel;
          return;
        }
        // install-log shape (install-kernel-headers / install-dkms / load-apparmor / load-module).
        if (j.kind === 'install_log') {
          renderRecoveryResult(resultEl, j.success === true,
            j.success ? 'Done.' : (j.error || 'Failed.'),
            j.log || []);
          if (j.success) {
            btn.disabled = true;
            btn.textContent = '✓ Applied';
            // Phoenix's HIL feedback (#818): a successful recovery action
            // whose effect only takes hold on next boot used to leave the
            // operator wondering whether anything happened. When the
            // remediation declares requires_reboot=true, surface a Reboot
            // now / Later prompt below the result so the next step is
            // unambiguous.
            if (rem.requires_reboot) {
              showRebootPrompt(card,
                rem.kind === 'modal_instr'
                  ? 'After confirming the MOK enrollment in firmware MOK Manager at next boot, ventd will sign and load the driver automatically.'
                  : 'A reboot makes the blacklist drop-in fully effective — no stray udev rule or initramfs hook can reload the in-tree driver after the next power-on.');
            }
          } else {
            btn.disabled = false;
            btn.textContent = 'Retry';
          }
          return;
        }
        // Generic OK fallback.
        renderRecoveryResult(resultEl, true, JSON.stringify(j));
        btn.disabled = false;
        btn.textContent = oldLabel;
      })
      .catch(function (err) {
        renderRecoveryResult(resultEl, false,
          (err && err.message) || 'Request failed.');
        btn.disabled = false;
        btn.textContent = oldLabel;
      });
  }

  // showRebootPrompt appends a "Reboot now / Later" prompt below a
  // successful recovery card whose remediation set requires_reboot=true
  // (#818). POSTs to /api/v1/system/reboot on confirm; the existing
  // server-side rebootEnvironmentBlocker decides whether reboot is
  // safe (refuses inside containers / rack chassis without consent).
  function showRebootPrompt(card, hint) {
    var existing = card.querySelector('.cal-recovery-reboot');
    if (existing) return; // idempotent — multiple successful Apply clicks
    var box = document.createElement('div');
    box.className = 'cal-recovery-reboot';
    box.innerHTML =
        '<div class="cal-recovery-reboot-text">'
      +   '<strong>Reboot required.</strong> ' + (hint || 'The fix takes effect on next boot.')
      + '</div>'
      + '<div class="cal-recovery-reboot-actions">'
      +   '<button class="btn btn--ghost cal-recovery-reboot-later" type="button">Later</button>'
      +   '<button class="btn btn--primary cal-recovery-reboot-now" type="button">Reboot now</button>'
      + '</div>';
    card.appendChild(box);
    box.querySelector('.cal-recovery-reboot-later').addEventListener('click', function () {
      box.remove();
    });
    box.querySelector('.cal-recovery-reboot-now').addEventListener('click', function () {
      if (!confirm('Reboot the host now? Any unsaved work in other apps will be lost.')) return;
      var nowBtn = box.querySelector('.cal-recovery-reboot-now');
      nowBtn.disabled = true;
      nowBtn.textContent = 'Rebooting…';
      fetch('/api/v1/system/reboot', { method: 'POST', credentials: 'same-origin' })
        .then(function (r) {
          if (!r.ok) {
            return r.text().then(function (t) { throw new Error(t || ('HTTP ' + r.status)); });
          }
          // Reboot in flight — daemon will go down. The page will eventually
          // fail to refresh; user will see the host coming back up via SSH /
          // power button. No need to wait here.
          nowBtn.textContent = 'Reboot triggered';
        })
        .catch(function (err) {
          nowBtn.disabled = false;
          nowBtn.textContent = 'Reboot now';
          alert('Could not reboot: ' + ((err && err.message) || 'unknown error'));
        });
    });
  }

  function renderRecoveryResult(el, ok, message, log) {
    el.hidden = false;
    el.classList.toggle('is-error', !ok);
    el.innerHTML = '';
    var msg = document.createElement('div');
    msg.className = 'cal-recovery-result-msg';
    msg.textContent = message;
    el.appendChild(msg);
    if (log && log.length > 0) {
      var pre = document.createElement('pre');
      pre.className = 'cal-recovery-result-log';
      pre.textContent = log.join('\n');
      el.appendChild(pre);
    }
  }

  // ── instructions modal (used for MOK enrollment) ───────────────────
  function showInstructionsModal(title, detail, commands) {
    var overlay = document.getElementById('cal-modal-overlay');
    var titleEl = document.getElementById('cal-modal-title');
    var bodyEl = document.getElementById('cal-modal-body');
    if (!overlay || !titleEl || !bodyEl) return;
    titleEl.textContent = title || 'Instructions';
    bodyEl.innerHTML = '';
    if (detail) {
      var p = document.createElement('p');
      p.textContent = detail;
      bodyEl.appendChild(p);
    }
    var pre = document.createElement('pre');
    pre.className = 'cal-modal-commands';
    pre.textContent = commands.join('\n');
    bodyEl.appendChild(pre);

    var copy = document.createElement('button');
    copy.className = 'btn btn--ghost btn--sm';
    copy.type = 'button';
    copy.textContent = 'Copy commands';
    copy.addEventListener('click', function () {
      navigator.clipboard.writeText(commands.join('\n')).then(function () {
        copy.textContent = '✓ Copied';
        setTimeout(function () { copy.textContent = 'Copy commands'; }, 1500);
      });
    });
    bodyEl.appendChild(copy);

    overlay.hidden = false;
  }

  (function setupModalClose() {
    var overlay = document.getElementById('cal-modal-overlay');
    var close = document.getElementById('cal-modal-close');
    if (!overlay || !close) return;
    close.addEventListener('click', function () { overlay.hidden = true; });
    overlay.addEventListener('click', function (e) {
      if (e.target === overlay) overlay.hidden = true;
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && !overlay.hidden) overlay.hidden = true;
    });
  })();

  // ── start ───────────────────────────────────────────────────────────
  paintPipeline('detecting');
  flavourEl.textContent = PHASE_FLAVOURS[0];
  poll();
  pollTimer = setInterval(poll,    pollInterval);
  liveTimer = setInterval(pollLive, 800);
})();
