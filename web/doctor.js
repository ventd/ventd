// doctor.js — poll /api/v1/doctor and render Severity-grouped Fact
// cards. Pure read; no operator actions yet beyond pointing at the
// per-class remediation docs. Suppression UI lands in a follow-up.

(function () {
  'use strict';

  var POLL_INTERVAL_MS = 5000; // matches doctorReportCacheTTL on the server

  function $(id) { return document.getElementById(id); }

  // SEVERITY constants mirror internal/doctor/severity.go's String()
  // output: "ok" | "info" | "warning" | "blocker" | "error".
  var SEVERITIES = ['blocker', 'warning', 'error', 'ok', 'info'];

  // Per-FailureClass docs links lived here in the first cut but
  // RULE-UI-01 forbids external URLs in shipped JS. Operators see the
  // class token (e.g. "secure_boot", "dkms_build_failed") and can
  // look it up on the project wiki separately. A future PR can add a
  // local /docs/<class> proxy route + relative links if we want
  // one-click navigation back.

  function fetchReport() {
    return fetch('/api/v1/doctor', { credentials: 'same-origin', cache: 'no-store' })
      .then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      });
  }

  // prettyFailureClass maps the doctor's snake_case failure-class
  // enum (mirrors internal/recovery/classify.go) to a human-readable
  // label. Doctor cards previously rendered the raw enum string
  // (`driver_wont_bind`, `dkms_build_failed`, etc.) which reads as
  // developer vocabulary to first-time operators. The raw value is
  // preserved on the card's hover title so diag tooling that searches
  // for the enum still works. Unknown classes fall back to title-cased
  // segments. (#1228 / #1254 child fix.)
  var FAILURE_CLASS_PRETTY = {
    'secure_boot':            'Secure Boot blocked',
    'missing_module':         'Driver module missing',
    'missing_headers':        'Kernel headers missing',
    'dkms_build_failed':      'DKMS build failed',
    'apparmor_denied':        'AppArmor denied access',
    'missing_build_tools':    'Build tools missing',
    'dkms_state_collision':   'DKMS state collision',
    'in_tree_conflict':       'In-tree driver conflict',
    'containerised':          'Running inside container',
    'package_manager_busy':   'Package manager busy',
    'daemon_not_root':        'Daemon not running as root',
    'read_only_rootfs':       'Root filesystem read-only',
    'disk_full':              'Disk full',
    'concurrent_install':     'Concurrent install in progress',
    'acpi_resource_conflict': 'ACPI resource conflict',
    'driver_wont_bind':       "Driver won't bind",
    'vendor_daemon_active':   'Vendor daemon active',
    'thinkpad_acpi_disabled': 'ThinkPad ACPI disabled',
    'nixos_path_ignored':     'NixOS path ignored'
  };
  function prettyFailureClass(raw) {
    if (FAILURE_CLASS_PRETTY[raw]) return FAILURE_CLASS_PRETTY[raw];
    // Unknown / new class: title-case the snake_case segments.
    return String(raw || '').split('_').map(function (seg) {
      return seg ? seg.charAt(0).toUpperCase() + seg.slice(1) : seg;
    }).join(' ');
  }

  // Render the rollup pill in the topbar. Matches RULE-DOCTOR-SEVERITY-01
  // ordering (Blocker > Warning > Error > OK).
  function paintRollupPill(severity, factCount) {
    var pill = $('doc-rollup-pill');
    if (!pill) return;
    pill.classList.remove('ok', 'warn', 'is-rollup-blocker', 'is-rollup-warning', 'is-rollup-ok');
    var label;
    if (severity === 'blocker') {
      label = factCount + ' blocker' + (factCount === 1 ? '' : 's');
      pill.classList.add('is-rollup-blocker');
    } else if (severity === 'warning') {
      label = factCount + ' warning' + (factCount === 1 ? '' : 's');
      pill.classList.add('is-rollup-warning');
    } else {
      label = 'all clear';
      pill.classList.add('is-rollup-ok');
    }
    pill.textContent = label;
  }

  function paintGenerated(generatedISO) {
    var el = $('doc-generated');
    if (!el) return;
    if (!generatedISO) { el.textContent = '—'; return; }
    var d = new Date(generatedISO);
    if (isNaN(d.getTime())) { el.textContent = generatedISO; return; }
    var hh = String(d.getHours()).padStart(2, '0');
    var mm = String(d.getMinutes()).padStart(2, '0');
    var ss = String(d.getSeconds()).padStart(2, '0');
    el.textContent = hh + ':' + mm + ':' + ss;
  }

  // Build a Fact card. Pure DOM construction — no innerHTML so the
  // detail / journal text can never inject markup (every detector
  // emits operator-readable strings, but the boundary is enforced).
  function renderFactCard(fact) {
    var card = document.createElement('div');
    card.className = 'doc-card';

    var head = document.createElement('div');
    head.className = 'doc-card-head';
    var title = document.createElement('h2');
    title.className = 'doc-card-title';
    title.textContent = fact.title || '(no title)';
    head.appendChild(title);
    if (fact.detector) {
      var det = document.createElement('span');
      det.className = 'doc-card-detector';
      det.textContent = fact.detector;
      head.appendChild(det);
    }
    if (fact.class && fact.class !== 'unknown') {
      var cls = document.createElement('span');
      cls.className = 'doc-card-class';
      cls.textContent = prettyFailureClass(fact.class);
      cls.title = fact.class; // keep raw enum reachable via hover for diag
      head.appendChild(cls);
    }
    card.appendChild(head);

    if (fact.detail) {
      var d = document.createElement('p');
      d.className = 'doc-card-detail';
      d.textContent = fact.detail;
      card.appendChild(d);
    }

    var meta = document.createElement('div');
    meta.className = 'doc-card-meta';
    var stamp = (fact.observed || '').slice(11, 19); // HH:MM:SS
    var line = 'observed ' + (stamp || '—');
    if (fact.entity_hash) line += ' · ' + fact.entity_hash;
    meta.appendChild(document.createTextNode(line));
    card.appendChild(meta);

    if (Array.isArray(fact.journal) && fact.journal.length > 0) {
      var pre = document.createElement('pre');
      pre.className = 'doc-card-journal';
      pre.textContent = fact.journal.join('\n');
      card.appendChild(pre);
    }
    return card;
  }

  function renderDetectorErrorCard(err) {
    var card = document.createElement('div');
    card.className = 'doc-card';

    var head = document.createElement('div');
    head.className = 'doc-card-head';
    var title = document.createElement('h2');
    title.className = 'doc-card-title';
    title.textContent = 'Detector failed: ' + (err.detector || '(unknown)');
    head.appendChild(title);
    card.appendChild(head);

    if (err.error) {
      var d = document.createElement('p');
      d.className = 'doc-card-detail';
      d.textContent = err.error;
      card.appendChild(d);
    }
    return card;
  }

  function renderReport(report) {
    var loading = $('doc-loading');
    if (loading) loading.hidden = true;

    var facts = (report && Array.isArray(report.facts)) ? report.facts : [];
    var detErrs = (report && Array.isArray(report.detector_errors)) ? report.detector_errors : [];

    // Group facts by severity. Bug-hunt finding (Agent 1 #2): info
    // used to roll into the "ok" group, making informational facts
    // (active experimental flags, etc.) visually invisible — operator
    // saw a green OK count and missed that flags they care about
    // are on. Info now has its own section + blue tag.
    var groups = { blocker: [], warning: [], error: [], ok: [], info: [] };
    facts.forEach(function (f) {
      // String() coercion mirrors the rollup-pill path below: a daemon
      // older than v0.5.27 marshalled Severity as a uint8 (0/1/2),
      // crashing toLowerCase() and taking the whole page down. New
      // daemons send the string form per RULE-DOCTOR-08; this stays
      // defensive across the upgrade window.
      var s = String(f.severity || 'ok').toLowerCase();
      if (!groups[s]) s = 'ok';
      groups[s].push(f);
    });

    SEVERITIES.forEach(function (s) {
      var section = $('doc-group-' + s);
      var list = $('doc-list-' + s);
      var count = $('doc-count-' + s);
      if (!section || !list || !count) return;
      var bucket = groups[s] || [];
      list.textContent = '';
      bucket.forEach(function (f) { list.appendChild(renderFactCard(f)); });
      count.textContent = String(bucket.length);
      section.hidden = bucket.length === 0;
    });

    // Detector errors group.
    var detSection = $('doc-group-detectors');
    var detList = $('doc-list-detectors');
    var detCount = $('doc-count-detectors');
    if (detSection && detList && detCount) {
      detList.textContent = '';
      detErrs.forEach(function (e) { detList.appendChild(renderDetectorErrorCard(e)); });
      detCount.textContent = String(detErrs.length);
      detSection.hidden = detErrs.length === 0;
    }

    // Empty-state card when nothing surfaced.
    var emptyEl = $('doc-empty');
    if (emptyEl) {
      var anything = facts.length > 0 || detErrs.length > 0;
      emptyEl.hidden = anything;
    }

    // Rollup pill: severity is the worst across facts (server already
    // computed Report.severity per RULE-DOCTOR-02).
    var rollup = (report && report.severity) ? String(report.severity).toLowerCase() : 'ok';
    var blockerN = groups.blocker.length;
    var warnN = groups.warning.length;
    paintRollupPill(rollup, blockerN > 0 ? blockerN : warnN);
    paintGenerated(report && report.generated);

    var meta = $('doc-meta');
    if (meta) {
      var detList2 = facts.map(function (f) { return f.detector; }).filter(Boolean);
      var unique = [];
      detList2.forEach(function (d) { if (unique.indexOf(d) < 0) unique.push(d); });
      meta.textContent =
        facts.length + ' fact' + (facts.length === 1 ? '' : 's') +
        ' across ' + unique.length + ' detector' + (unique.length === 1 ? '' : 's') +
        (detErrs.length > 0 ? ' · ' + detErrs.length + ' detector error' + (detErrs.length === 1 ? '' : 's') : '') +
        ' · schema ' + ((report && report.schema_version) || '?');
    }
  }

  function tick() {
    fetchReport()
      .then(renderReport)
      .catch(function (err) {
        var loading = $('doc-loading');
        if (loading) {
          loading.hidden = false;
          loading.textContent = 'Doctor poll failed: ' + err.message;
        }
      });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () {
      tick();
      setInterval(tick, POLL_INTERVAL_MS);
    });
  } else {
    tick();
    setInterval(tick, POLL_INTERVAL_MS);
  }
})();
