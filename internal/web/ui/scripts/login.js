(function() {
  // Theme
  function applyTheme(t) {
    document.documentElement.setAttribute('data-theme', t);
    // Dark → show sun (click to go light); light → show moon.
    const icon = t === 'dark' ? 'sun' : 'moon';
    document.getElementById('themeBtn').innerHTML =
      '<svg class="icon" aria-hidden="true"><use href="/ui/icons/sprite.svg#' + icon + '"/></svg>';
  }
  let theme = 'dark';
  try { theme = localStorage.getItem('ventd-theme') || 'dark'; } catch(_){}
  applyTheme(theme);
  document.getElementById('themeBtn').addEventListener('click', function() {
    theme = theme === 'dark' ? 'light' : 'dark';
    applyTheme(theme);
    try { localStorage.setItem('ventd-theme', theme); } catch(_){}
  });

  function showMsg(el, text, isErr) {
    el.textContent = text;
    el.className = 'msg ' + (isErr ? 'err' : 'ok');
  }

  // --- Lockout countdown ------------------------------------------------
  //
  // When the daemon replies 429 it carries Retry-After (seconds). Surface
  // that as a live countdown so the operator knows when to try again, and
  // disable the submit buttons until the cooldown expires. Without this
  // the UI appears to just "reject every attempt" and there is no visible
  // path to recovery. (Audit finding S3.)
  var lockoutTimer = null;
  var lockoutEndMs = 0;
  // Buttons the cooldown disables, so we can re-enable exactly what we
  // disabled without accidentally resurrecting a button the page removed.
  var lockedButtons = [];

  function fmtMMSS(totalSec) {
    var m = Math.floor(totalSec / 60);
    var s = totalSec % 60;
    return m + ':' + (s < 10 ? '0' : '') + s;
  }

  function clearLockoutTimer() {
    if (lockoutTimer) { clearInterval(lockoutTimer); lockoutTimer = null; }
  }

  function endLockout() {
    clearLockoutTimer();
    lockoutEndMs = 0;
    lockedButtons.forEach(function(b) {
      b.disabled = false;
      if (b.dataset.originalLabel) {
        b.textContent = b.dataset.originalLabel;
        delete b.dataset.originalLabel;
      }
    });
    lockedButtons = [];
    var msgEls = document.querySelectorAll('.msg');
    msgEls.forEach(function(el) {
      if (el.dataset.lockoutMsg) { el.textContent = ''; el.className = 'msg'; delete el.dataset.lockoutMsg; }
    });
  }

  // startLockout disables the submit buttons and ticks down a mm:ss
  // countdown in the status area of whichever section is currently
  // visible. Called with the Retry-After seconds value from the 429
  // response.
  function startLockout(retryAfterSec, statusEl) {
    if (retryAfterSec <= 0) { endLockout(); return; }
    clearLockoutTimer();
    lockoutEndMs = Date.now() + retryAfterSec * 1000;
    // Disable both possible submit buttons so the operator can't switch
    // tabs and bypass the countdown. Remember their labels so we can
    // restore them cleanly when the cooldown expires.
    lockedButtons = [];
    ['loginBtn', 'firstBootBtn'].forEach(function(id) {
      var b = document.getElementById(id);
      if (!b) return;
      if (!b.dataset.originalLabel) b.dataset.originalLabel = b.textContent;
      b.disabled = true;
      lockedButtons.push(b);
    });

    function tick() {
      var remaining = Math.ceil((lockoutEndMs - Date.now()) / 1000);
      if (remaining <= 0) { endLockout(); return; }
      statusEl.dataset.lockoutMsg = '1';
      showMsg(statusEl, 'Too many attempts. Try again in ' + fmtMMSS(remaining) + '.', true);
    }
    tick();
    lockoutTimer = setInterval(tick, 1000);
  }

  // handleLockout inspects a fetch Response; if it is 429 it installs a
  // countdown against the given message element and returns true so the
  // caller can bail out of its normal success/error branches.
  function handleLockout(res, statusEl) {
    if (res.status !== 429) return false;
    // Retry-After is seconds (we never emit the HTTP-date form). Parse
    // defensively so a missing or malformed header falls back to the
    // limiter's default cooldown of 15 minutes.
    var ra = parseInt(res.headers.get('Retry-After') || '0', 10);
    if (!isFinite(ra) || ra <= 0) ra = 15 * 60;
    startLockout(ra, statusEl);
    return true;
  }

  // --- Login flow -------------------------------------------------------

  // Normal login
  document.getElementById('password').addEventListener('keydown', function(e) {
    if (e.key === 'Enter') document.getElementById('loginBtn').click();
  });

  document.getElementById('loginBtn').addEventListener('click', function() {
    var btn = this;
    if (btn.disabled) return;
    var pw = document.getElementById('password').value;
    var msg = document.getElementById('loginMsg');
    if (!pw) { showMsg(msg, 'Please enter your password', true); return; }
    btn.disabled = true;
    var originalLabel = btn.dataset.originalLabel || btn.textContent;
    btn.dataset.originalLabel = originalLabel;
    btn.textContent = 'Signing in…';

    var body = new URLSearchParams();
    body.append('password', pw);

    fetch('/login', { method: 'POST', body: body })
      .then(function(r) {
        if (handleLockout(r, msg)) return null;
        return r.json().then(function(j) { return {status: r.status, body: j, headers: r.headers}; });
      })
      .then(function(res) {
        if (!res) return; // lockout path already handled
        if (res.status === 200) {
          // Redirect to intended destination or root
          var dest = new URLSearchParams(location.search).get('next') || '/';
          location.href = dest;
          return;
        }
        if (res.status === 400 && res.body && res.body.first_boot) {
          // Daemon is still in first-boot mode — switch views so the
          // operator enters the setup token instead of a password.
          document.getElementById('secLogin').classList.remove('active');
          document.getElementById('secFirstBoot').classList.add('active');
          btn.disabled = false; btn.textContent = originalLabel;
          return;
        }
        showMsg(msg, (res.body && res.body.error) || 'Login failed', true);
        btn.disabled = false; btn.textContent = originalLabel;
      })
      .catch(function() {
        showMsg(msg, 'Network error — is the daemon running?', true);
        btn.disabled = false; btn.textContent = originalLabel;
      });
  });

  // First-boot submit
  document.getElementById('firstBootBtn').addEventListener('click', function() {
    var btn = this;
    if (btn.disabled) return;
    var token   = document.getElementById('setupToken').value.trim();
    var pw      = document.getElementById('newPassword').value;
    var pw2     = document.getElementById('confirmPassword').value;
    var msg     = document.getElementById('firstBootMsg');

    if (!token) { showMsg(msg, 'Setup token is required', true); return; }
    if (pw.length < 8) { showMsg(msg, 'Password must be at least 8 characters', true); return; }
    if (pw !== pw2)    { showMsg(msg, 'Passwords do not match', true); return; }

    var originalLabel = btn.dataset.originalLabel || btn.textContent;
    btn.dataset.originalLabel = originalLabel;
    btn.disabled = true; btn.textContent = 'Setting up…';

    var body = new URLSearchParams();
    body.append('setup_token', token);
    body.append('new_password', pw);

    fetch('/login', { method: 'POST', body: body })
      .then(function(r) {
        if (handleLockout(r, msg)) return null;
        return r.json().then(function(j) { return {status: r.status, body: j}; });
      })
      .then(function(res) {
        if (!res) return;
        if (res.status === 200) {
          showMsg(msg, 'Password set! Redirecting…', false);
          setTimeout(function() { location.href = '/'; }, 800);
          return;
        }
        showMsg(msg, (res.body && res.body.error) || 'Setup failed', true);
        btn.disabled = false; btn.textContent = originalLabel;
      })
      .catch(function() {
        showMsg(msg, 'Network error', true);
        btn.disabled = false; btn.textContent = originalLabel;
      });
  });

  // --- First-boot detection --------------------------------------------
  //
  // Ask /api/auth/state whether a password has been configured yet. This
  // endpoint is a pure read-only lookup; unlike a POST /login with an
  // empty password, it does NOT touch the per-IP rate limiter. That
  // matters because the old probe burned one attempt on every page load
  // and could lock an operator out of their own box before they ever
  // saw a password prompt. (Audit finding S2.)
  fetch('/api/auth/state', { method: 'GET' })
    .then(function(r) { return r.ok ? r.json() : null; })
    .then(function(j) {
      if (j && j.first_boot) {
        document.getElementById('secLogin').classList.remove('active');
        document.getElementById('secFirstBoot').classList.add('active');
      }
    })
    .catch(function() { /* probe is best-effort; fall back to normal login form */ });
})();
