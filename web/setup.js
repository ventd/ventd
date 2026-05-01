// setup.js — first-boot screen.
//
// Wires the password-only first-boot form to ventd's auth API:
//   GET  /api/v1/auth/state   → { first_boot: bool }
//   GET  /api/v1/version       → { version: "...", ... }
//   POST /login                → form-encoded { new_password }
//
// On a successful first-boot login the daemon sets a session cookie and
// returns 200; we then redirect to /.

(function () {
  'use strict';

  // ── Theme toggle ──
  // Default is dark (brand identity, hardcoded on <html data-theme="dark">).
  // Honour an explicit user override stored from a previous visit; ignore
  // prefers-color-scheme so a user who reaches this screen for the first
  // time sees the canonical brand styling regardless of OS theme.
  var root = document.documentElement;
  try {
    var stored = window.localStorage.getItem('ventd-theme');
    if (stored === 'light' || stored === 'dark') root.dataset.theme = stored;
  } catch (_) {}
  var themeBtn = document.getElementById('theme-toggle');
  if (themeBtn) {
    themeBtn.addEventListener('click', function () {
      var next = root.dataset.theme === 'dark' ? 'light' : 'dark';
      root.dataset.theme = next;
      try { window.localStorage.setItem('ventd-theme', next); } catch (_) {}
    });
  }

  // ── Populate host + version ──
  var hostEl = document.getElementById('host-name');
  if (hostEl) hostEl.textContent = window.location.host;

  fetch('/api/v1/version', { credentials: 'same-origin' })
    .then(function (r) { return r.ok ? r.json() : null; })
    .then(function (data) {
      var v = document.getElementById('host-version');
      if (v && data && data.version) v.textContent = data.version;
    })
    .catch(function () {});

  // ── If already past first boot, send the user to the login page ──
  fetch('/api/v1/auth/state', { credentials: 'same-origin' })
    .then(function (r) { return r.ok ? r.json() : null; })
    .then(function (data) {
      if (data && data.first_boot === false) {
        window.location.replace('/login');
      }
    })
    .catch(function () {});

  // ── Password strength + confirm match ──
  var strengthEl = document.getElementById('pw-strength');
  var strengthFill = document.getElementById('pw-strength-fill');
  var strengthLabel = document.getElementById('pw-strength-label');
  var pwField = document.getElementById('setup-password');
  var confirmField = document.getElementById('setup-confirm');
  var card = document.querySelector('.setup-card');

  // ── Password reveal ──
  // The single reveal toggle on the create-password field also flips the
  // confirm field, so the operator only needs one button on a screen
  // where the two inputs always carry the same value.
  var pwReveal = document.getElementById('pw-reveal');
  if (pwField && pwReveal) {
    pwReveal.addEventListener('click', function () {
      var nextType = pwField.type === 'password' ? 'text' : 'password';
      pwField.type = nextType;
      if (confirmField) confirmField.type = nextType;
    });
  }

  function classifyStrength(p) {
    if (!p) return null;
    var s = 0;
    if (p.length >= 8) s++;
    if (p.length >= 12) s++;
    if (/[a-z]/.test(p) && /[A-Z]/.test(p)) s++;
    if (/[0-9]/.test(p)) s++;
    if (/[^a-zA-Z0-9]/.test(p)) s++;
    if (s <= 1) return 'weak';
    if (s === 2) return 'fair';
    if (s === 3) return 'strong';
    return 'max';
  }
  function setStrength(level) {
    if (!strengthEl) return;
    if (!level) { strengthEl.hidden = true; return; }
    strengthEl.hidden = false;
    strengthFill.classList.remove('setup-strength-fill--weak','setup-strength-fill--fair','setup-strength-fill--strong','setup-strength-fill--max');
    strengthLabel.classList.remove('setup-strength-label--weak','setup-strength-label--fair','setup-strength-label--strong');
    strengthFill.classList.add('setup-strength-fill--' + level);
    var labelClass = level === 'max' ? 'strong' : level;
    strengthLabel.classList.add('setup-strength-label--' + labelClass);
    strengthLabel.textContent = level === 'max' ? 'excellent' : level;
  }
  if (pwField) {
    pwField.addEventListener('input', function () {
      setStrength(classifyStrength(pwField.value));
      updateConfirmStatus();
    });
  }

  function updateConfirmStatus() {
    if (!card || !confirmField) return;
    var pw = pwField ? pwField.value : '';
    var c = confirmField.value;
    var state = 'empty';
    if (c === '') state = 'empty';
    else if (c === pw) state = 'match';
    else if (pw.startsWith(c) || c.length < pw.length) state = 'typing';
    else state = 'mismatch';
    card.dataset.confirmState = state;
  }
  if (confirmField) confirmField.addEventListener('input', updateConfirmStatus);

  // ── Submit ──
  var form = document.getElementById('setup-form');
  var submitBtn = document.getElementById('setup-submit');
  var submitLabel = document.getElementById('setup-submit-label');
  var errorEl = document.getElementById('setup-error');

  function showError(msg) {
    if (!errorEl) return;
    errorEl.textContent = msg;
    errorEl.hidden = false;
  }
  function clearError() {
    if (!errorEl) return;
    errorEl.hidden = true;
    errorEl.textContent = '';
  }
  function setBusy(busy) {
    if (!submitBtn) return;
    submitBtn.disabled = busy;
    if (submitLabel) submitLabel.textContent = busy ? 'Creating account…' : 'Create Password & Continue';
  }

  if (form) {
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      clearError();

      var pw    = pwField ? pwField.value : '';
      var conf  = confirmField ? confirmField.value : '';

      if (pw.length < 8) { showError('Password must be at least 8 characters.'); pwField && pwField.focus(); return; }
      if (pw !== conf)   { showError('Passwords do not match.'); confirmField && confirmField.focus(); return; }

      setBusy(true);
      var body = new URLSearchParams();
      body.set('new_password', pw);

      fetch('/login', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString()
      })
        .then(function (resp) {
          if (resp.ok) {
            window.location.assign('/');
            return;
          }
          return resp.json().catch(function () { return null; }).then(function (data) {
            var msg = data && data.error ? data.error : ('Setup failed (HTTP ' + resp.status + ').');
            showError(msg);
            setBusy(false);
          });
        })
        .catch(function (err) {
          showError('Network error: ' + (err && err.message ? err.message : 'unable to reach the daemon.'));
          setBusy(false);
        });
    });
  }
})();
