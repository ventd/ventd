// login.js — post-first-boot login screen.
// Uses the same setup.css as setup.html for visual coherence; the form
// posts a password (no setup_token) to /login.

(function () {
  'use strict';

  // theme — same default rule as setup.js.
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

  // Populate host + version
  var hostEl = document.getElementById('host-name');
  if (hostEl) hostEl.textContent = window.location.host;
  fetch('/api/v1/version', { credentials: 'same-origin' })
    .then(function (r) { return r.ok ? r.json() : null; })
    .then(function (data) {
      var v = document.getElementById('host-version');
      if (v && data && data.version) v.textContent = data.version;
    })
    .catch(function () {});

  // If first-boot, redirect to /setup so the user creates a password instead.
  fetch('/api/v1/auth/state', { credentials: 'same-origin' })
    .then(function (r) { return r.ok ? r.json() : null; })
    .then(function (data) {
      if (data && data.first_boot === true) window.location.replace('/setup');
    })
    .catch(function () {});

  // Password reveal
  var pwField = document.getElementById('login-password');
  var pwReveal = document.getElementById('pw-reveal');
  if (pwField && pwReveal) {
    pwReveal.addEventListener('click', function () {
      pwField.type = pwField.type === 'password' ? 'text' : 'password';
    });
  }

  // Submit
  var form = document.getElementById('login-form');
  var submitBtn = document.getElementById('login-submit');
  var submitLabel = document.getElementById('login-submit-label');
  var errorEl = document.getElementById('login-error');

  function showError(msg) { errorEl.textContent = msg; errorEl.hidden = false; }
  function clearError() { errorEl.hidden = true; errorEl.textContent = ''; }
  function setBusy(b) {
    submitBtn.disabled = b;
    submitLabel.textContent = b ? 'Signing in…' : 'Sign in';
  }

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    clearError();
    var pw = pwField.value;
    if (!pw) { showError('Password required.'); pwField.focus(); return; }
    setBusy(true);
    var body = new URLSearchParams();
    body.set('password', pw);
    fetch('/login', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString()
    })
      .then(function (r) {
        if (r.ok) { window.location.assign('/'); return; }
        return r.json().catch(function () { return null; }).then(function (j) {
          var msg;
          if (r.status === 429)       msg = 'Too many failed attempts — try again later.';
          else if (r.status === 401)  msg = (j && j.error) || 'Incorrect password.';
          else                        msg = (j && j.error) || ('Login failed (HTTP ' + r.status + ').');
          showError(msg);
          setBusy(false);
          pwField.focus(); pwField.select();
        });
      })
      .catch(function (err) {
        showError('Network error: ' + (err && err.message || 'unable to reach the daemon.'));
        setBusy(false);
      });
  });
})();
