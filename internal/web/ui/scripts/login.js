(function() {
  // Theme
  function applyTheme(t) {
    document.documentElement.setAttribute('data-theme', t);
    document.getElementById('themeBtn').textContent = t === 'dark' ? '◑' : '◐';
  }
  let theme = 'dark';
  try { theme = localStorage.getItem('ventd-theme') || 'dark'; } catch(_){}
  applyTheme(theme);
  document.getElementById('themeBtn').addEventListener('click', function() {
    theme = theme === 'dark' ? 'light' : 'dark';
    applyTheme(theme);
    try { localStorage.setItem('ventd-theme', theme); } catch(_){}
  });

  // Detect first-boot by probing /api/ping then checking 401 response
  // We use a heuristic: if normal login fails with a specific first-boot flag,
  // switch views. For simplicity, we show the first-boot section if localStorage
  // has a flag, or if the login attempt returns first_boot=true.
  var isFirstBoot = false;

  function showMsg(el, text, isErr) {
    el.textContent = text;
    el.className = 'msg ' + (isErr ? 'err' : 'ok');
  }

  // Normal login
  document.getElementById('password').addEventListener('keydown', function(e) {
    if (e.key === 'Enter') document.getElementById('loginBtn').click();
  });

  document.getElementById('loginBtn').addEventListener('click', function() {
    var btn = this;
    var pw = document.getElementById('password').value;
    var msg = document.getElementById('loginMsg');
    btn.disabled = true; btn.textContent = 'Signing in…';

    var body = new URLSearchParams();
    body.append('password', pw);

    fetch('/login', { method: 'POST', body: body })
      .then(function(r) { return r.json().then(function(j) { return {status: r.status, body: j}; }); })
      .then(function(res) {
        if (res.status === 200) {
          // Redirect to intended destination or root
          var dest = new URLSearchParams(location.search).get('next') || '/';
          location.href = dest;
          return;
        }
        if (res.status === 401 && res.body.first_boot) {
          // Server says no password set yet — switch to first-boot UI
          document.getElementById('secLogin').classList.remove('active');
          document.getElementById('secFirstBoot').classList.add('active');
          return;
        }
        showMsg(msg, res.body.error || 'Login failed', true);
        btn.disabled = false; btn.textContent = 'Sign In';
      })
      .catch(function() {
        showMsg(msg, 'Network error — is the daemon running?', true);
        btn.disabled = false; btn.textContent = 'Sign In';
      });
  });

  // First-boot submit
  document.getElementById('firstBootBtn').addEventListener('click', function() {
    var btn = this;
    var token   = document.getElementById('setupToken').value.trim();
    var pw      = document.getElementById('newPassword').value;
    var pw2     = document.getElementById('confirmPassword').value;
    var msg     = document.getElementById('firstBootMsg');

    if (!token) { showMsg(msg, 'Setup token is required', true); return; }
    if (pw.length < 8) { showMsg(msg, 'Password must be at least 8 characters', true); return; }
    if (pw !== pw2)    { showMsg(msg, 'Passwords do not match', true); return; }

    btn.disabled = true; btn.textContent = 'Setting up…';

    var body = new URLSearchParams();
    body.append('setup_token', token);
    body.append('new_password', pw);

    fetch('/login', { method: 'POST', body: body })
      .then(function(r) { return r.json().then(function(j) { return {status: r.status, body: j}; }); })
      .then(function(res) {
        if (res.status === 200) {
          showMsg(msg, 'Password set! Redirecting…', false);
          setTimeout(function() { location.href = '/'; }, 800);
          return;
        }
        showMsg(msg, res.body.error || 'Setup failed', true);
        btn.disabled = false; btn.textContent = 'Create Password & Continue';
      })
      .catch(function() {
        showMsg(msg, 'Network error', true);
        btn.disabled = false; btn.textContent = 'Create Password & Continue';
      });
  });

  // Auto-detect first-boot: check if there is no password hash configured
  // by attempting a login with empty password — server returns first_boot flag.
  fetch('/login', {
    method: 'POST',
    body: new URLSearchParams([['password', '']])
  })
    .then(function(r) { return r.json().then(function(j) { return {status: r.status, body: j}; }); })
    .then(function(res) {
      if (res.status === 401 && res.body.first_boot) {
        document.getElementById('secLogin').classList.remove('active');
        document.getElementById('secFirstBoot').classList.add('active');
      }
    })
    .catch(function() {});
})();
