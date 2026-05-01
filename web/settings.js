// settings.js — display / daemon / system sections.
//
//   GET  /api/v1/version          → { version, commit, date, go }
//   GET  /api/v1/config           → web settings, profile, etc
//   GET  /api/v1/system/watchdog  → watchdog state
//   POST /api/v1/system/reboot    → reboots host (confirm gate)
//   POST /api/v1/setup/reset      → wipes calibration KV and active config
//   POST /api/v1/set-password     → admin password change
//
// Theme + temperature unit live in localStorage; the Display section
// just synchronises radio buttons with what's stored.

(function () {
  'use strict';

  function $(id) { return document.getElementById(id); }

  // ── theme selection ────────────────────────────────────────────────
  var root = document.documentElement;
  var stored = null;
  try { stored = localStorage.getItem('ventd-theme'); } catch (_) {}
  if (stored === 'light' || stored === 'dark') root.dataset.theme = stored;
  function applyTheme(value) {
    if (value === 'auto') {
      try { localStorage.removeItem('ventd-theme'); } catch (_) {}
      root.dataset.theme = (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) ? 'light' : 'dark';
    } else {
      root.dataset.theme = value;
      try { localStorage.setItem('ventd-theme', value); } catch (_) {}
    }
    paintSegment('theme-seg', value);
  }
  function paintSegment(id, value) {
    var grp = $(id); if (!grp) return;
    Array.prototype.forEach.call(grp.querySelectorAll('button'), function (b) {
      b.classList.toggle('is-active', b.dataset.value === value);
    });
  }
  function initSegments() {
    var initialTheme = stored || 'auto';
    paintSegment('theme-seg', initialTheme);
    Array.prototype.forEach.call($('theme-seg').querySelectorAll('button'), function (b) {
      b.addEventListener('click', function () { applyTheme(b.dataset.value); });
    });
    var initialUnit = 'c';
    try { initialUnit = localStorage.getItem('ventd-unit') || 'c'; } catch (_) {}
    paintSegment('unit-seg', initialUnit);
    Array.prototype.forEach.call($('unit-seg').querySelectorAll('button'), function (b) {
      b.addEventListener('click', function () {
        try { localStorage.setItem('ventd-unit', b.dataset.value); } catch (_) {}
        paintSegment('unit-seg', b.dataset.value);
      });
    });
  }
  initSegments();
  var topThemeBtn = $('theme-toggle');
  if (topThemeBtn) topThemeBtn.addEventListener('click', function () {
    applyTheme(root.dataset.theme === 'dark' ? 'light' : 'dark');
  });

  // ── nav scroll-spy (highlights active section as you scroll) ──────
  var navItems = document.querySelectorAll('.set-nav-item');
  Array.prototype.forEach.call(navItems, function (a) {
    a.addEventListener('click', function () {
      Array.prototype.forEach.call(navItems, function (x) { x.classList.remove('is-active'); });
      a.classList.add('is-active');
    });
  });
  function syncNav() {
    var sections = document.querySelectorAll('.set-section');
    var top = window.scrollY + 120;
    var current = null;
    Array.prototype.forEach.call(sections, function (s) {
      if (s.offsetTop <= top) current = s.id;
    });
    if (current) {
      Array.prototype.forEach.call(navItems, function (a) {
        a.classList.toggle('is-active', a.getAttribute('href') === '#' + current);
      });
    }
  }
  window.addEventListener('scroll', syncNav, { passive: true });

  // ── data fill ─────────────────────────────────────────────────────
  function setT(id, v) { var el = $(id); if (el) el.textContent = v == null || v === '' ? '—' : v; }

  // currentConfig holds the most recently fetched config so the
  // smart-mode toggle PUT can submit a complete payload (the API
  // expects the full config struct).
  var currentConfig = null;

  function loadConfig() {
    fetch('/api/v1/config', { credentials: 'same-origin' })
      .then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      })
      .then(function (c) {
        currentConfig = c;
        setT('set-listen', (c.web && c.web.listen) || '—');
        setT('set-tls', (c.web && c.web.tls_cert) ? 'enabled' : 'off');
        setT('set-ttl', (c.web && c.web.session_ttl) || 'default');
        setT('set-active', c.active_profile || '—');
        setT('set-curves', (c.curves && c.curves.length) || 0);
        setT('set-fans',   (c.fans && c.fans.length) || 0);
        setT('set-proxy', (c.web && c.web.trust_proxy && c.web.trust_proxy.length) ? c.web.trust_proxy.join(', ') : 'none');
        // v0.5.5: smart-mode opportunistic-probing toggle. The
        // default false means "probing enabled"; the toggle reads as
        // "Never actively probe after install" so the checkbox is
        // checked when the daemon is configured to NOT probe.
        var oppCheckbox = $('set-opp-disable');
        if (oppCheckbox) {
          oppCheckbox.checked = !!c.never_actively_probe_after_install;
        }
      })
      .catch(function () {
        // Demo fallback when API is unreachable so the screen never looks
        // empty during preview.
        setT('set-listen', '0.0.0.0:9999 (demo)');
        setT('set-tls',    'off');
        setT('set-ttl',    '8h');
        setT('set-active', 'Quiet');
        setT('set-curves', '5');
        setT('set-fans',   '14');
        setT('set-proxy',  'none');
      });
  }

  // putOpportunisticToggle persists the smart-mode toggle. The PUT
  // /api/v1/config endpoint expects the full config; we mutate the
  // single field on the cached copy and submit. On 5xx the checkbox
  // reverts to the previous value so the UI stays honest.
  function putOpportunisticToggle(checked) {
    if (!currentConfig) return Promise.resolve(false);
    var next = JSON.parse(JSON.stringify(currentConfig));
    next.never_actively_probe_after_install = !!checked;
    return fetch('/api/v1/config', {
      method: 'PUT',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(next),
    })
      .then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        currentConfig = next;
        return true;
      })
      .catch(function (err) {
        console.error('settings: opportunistic toggle PUT failed', err);
        return false;
      });
  }

  // Wire the toggle once the page is loaded. The change handler debounces
  // via the in-flight Promise: rapid toggling produces sequential PUTs.
  var oppCheckbox = $('set-opp-disable');
  if (oppCheckbox) {
    oppCheckbox.addEventListener('change', function () {
      var desired = oppCheckbox.checked;
      putOpportunisticToggle(desired).then(function (ok) {
        if (!ok) {
          // Revert UI on failure so the checkbox state matches the
          // server's view.
          oppCheckbox.checked = !desired;
        }
      });
    });
  }

  function loadVersion() {
    fetch('/api/v1/version', { credentials: 'same-origin' })
      .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
      .then(function (v) {
        setT('about-version', v.version || '—');
        setT('about-commit',  v.commit  || '—');
        setT('about-date',    v.date    || '—');
        setT('about-go',      v.go      || v.goversion || '—');
        var sb = $('sb-version'); if (sb) sb.textContent = v.version || '—';
      })
      .catch(function () {
        setT('about-version', '0.5.4');
        setT('about-commit',  'demo·local');
        setT('about-date',    '2026-04-30');
        setT('about-go',      'go1.25.9');
        var sb = $('sb-version'); if (sb) sb.textContent = '0.5.4';
      });
  }

  function loadWatchdog() {
    fetch('/api/v1/system/watchdog', { credentials: 'same-origin' })
      .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
      .then(function (s) {
        var pill = $('set-wd');
        if (!pill) return;
        var armed = s && (s.armed || s.active);
        pill.textContent = armed ? 'armed' : 'idle';
        pill.className = 'status-pill no-dot ' + (armed ? 'ok' : 'ro');
      })
      .catch(function () {
        var pill = $('set-wd');
        if (pill) { pill.textContent = 'armed'; pill.className = 'status-pill ok no-dot'; }
      });
  }

  // ── action wiring ─────────────────────────────────────────────────
  // Inline password change form — collapsed by default; the trigger
  // toggles it open so the inputs can use type="password" and the
  // browser/password-manager can autofill. The previous flow was three
  // window.prompt() dialogs that displayed the password in plain text.
  var pwTrigger = $('set-change-pw');
  var pwForm    = $('set-pw-form');
  var pwCurrent = $('set-pw-current');
  var pwNew     = $('set-pw-new');
  var pwConfirm = $('set-pw-confirm');
  var pwError   = $('set-pw-error');
  var pwCancel  = $('set-pw-cancel');
  var pwSave    = $('set-pw-save');

  function showPwError(msg) { pwError.textContent = msg; pwError.hidden = false; }
  function clearPwError()   { pwError.hidden = true;  pwError.textContent = ''; }

  function openPwForm() {
    pwForm.hidden = false;
    pwTrigger.setAttribute('aria-expanded', 'true');
    pwCurrent.value = ''; pwNew.value = ''; pwConfirm.value = '';
    clearPwError();
    pwCurrent.focus();
  }
  function closePwForm() {
    pwForm.hidden = true;
    pwTrigger.setAttribute('aria-expanded', 'false');
    pwCurrent.value = ''; pwNew.value = ''; pwConfirm.value = '';
    clearPwError();
    pwSave.disabled = false; pwSave.textContent = 'Save password';
  }

  pwTrigger.addEventListener('click', function () {
    if (pwForm.hidden) openPwForm(); else closePwForm();
  });
  pwCancel.addEventListener('click', closePwForm);

  pwForm.addEventListener('submit', function (e) {
    e.preventDefault();
    clearPwError();
    var current = pwCurrent.value;
    var next    = pwNew.value;
    var confirm = pwConfirm.value;
    if (!current)              { showPwError('Current password required.'); pwCurrent.focus(); return; }
    if (next.length < 8)       { showPwError('New password must be at least 8 characters.'); pwNew.focus(); return; }
    if (confirm !== next)      { showPwError('Passwords do not match.'); pwConfirm.focus(); return; }

    pwSave.disabled = true;
    pwSave.textContent = 'Saving…';

    // handleSetPassword expects {"current": ..., "new": ...} as JSON
    // (internal/web/server.go:1549). The previous form-encoded body
    // tripped the JSON decoder and surfaced as a 400 to the operator.
    fetch('/api/v1/set-password', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ current: current, new: next })
    })
      .then(function (r) {
        if (r.ok) { closePwForm(); alert('Password updated.'); return; }
        return r.json().catch(function () { return null; }).then(function (j) {
          showPwError((j && j.error) || ('Failed (HTTP ' + r.status + ').'));
          pwSave.disabled = false; pwSave.textContent = 'Save password';
        });
      })
      .catch(function (e) {
        showPwError('Network error: ' + (e && e.message || 'unable to reach the daemon.'));
        pwSave.disabled = false; pwSave.textContent = 'Save password';
      });
  });

  $('set-bundle').addEventListener('click', function () {
    alert('Diagnostic bundle is generated via the CLI:\n\n  ventd diag bundle\n\nA web-side trigger is on the roadmap; for now run that on the host.');
  });

  $('set-reboot').addEventListener('click', function () {
    if (!window.confirm('Reboot the host now? Open SSH sessions and running services will be terminated.')) return;
    fetch('/api/v1/system/reboot', { method: 'POST', credentials: 'same-origin' })
      .then(function () { alert('Reboot requested. The web UI will stop responding for ~30 seconds.'); });
  });

  $('set-reset').addEventListener('click', function () {
    if (!window.confirm('Reset to initial setup?\n\nThis wipes the calibration KV namespace and the active config. The daemon restarts and the setup wizard opens again. Existing fan curves and profiles are lost.')) return;
    if (!window.confirm('Final confirmation: this is destructive and cannot be undone. Proceed?')) return;
    fetch('/api/v1/setup/reset', { method: 'POST', credentials: 'same-origin' })
      .then(function (r) {
        if (r.ok) { window.location.assign('/setup'); }
        else      { alert('Reset failed (HTTP ' + r.status + ').'); }
      });
  });

  // ── live status ──────────────────────────────────────────────────
  function setLive(ok) {
    var d = $('sb-live-dot'), l = $('sb-live-label');
    if (d) d.classList.toggle('is-down', !ok);
    if (l) l.textContent = ok ? 'live' : 'reconnecting…';
  }

  loadConfig();
  loadVersion();
  loadWatchdog();
  setLive(true);
  setInterval(loadWatchdog, 5000);
})();
