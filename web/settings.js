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

  function loadConfig() {
    fetch('/api/v1/config', { credentials: 'same-origin' })
      .then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      })
      .then(function (c) {
        setT('set-listen', (c.web && c.web.listen) || '—');
        setT('set-tls', (c.web && c.web.tls_cert) ? 'enabled' : 'off');
        setT('set-ttl', (c.web && c.web.session_ttl) || 'default');
        setT('set-active', c.active_profile || '—');
        setT('set-curves', (c.curves && c.curves.length) || 0);
        setT('set-fans',   (c.fans && c.fans.length) || 0);
        setT('set-proxy', (c.web && c.web.trust_proxy && c.web.trust_proxy.length) ? c.web.trust_proxy.join(', ') : 'none');
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
  $('set-change-pw').addEventListener('click', function () {
    var current = window.prompt('Current password:');
    if (current == null) return;
    var next = window.prompt('New password (min 8 chars):');
    if (next == null) return;
    if (next.length < 8) { alert('Password must be at least 8 characters.'); return; }
    var confirm = window.prompt('Confirm new password:');
    if (confirm !== next) { alert('Passwords do not match.'); return; }
    var body = new URLSearchParams();
    body.set('current_password', current);
    body.set('new_password', next);
    fetch('/api/v1/set-password', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString()
    })
      .then(function (r) {
        if (r.ok) { alert('Password updated.'); return; }
        return r.json().then(function (j) { alert('Failed: ' + (j && j.error || r.status)); });
      })
      .catch(function (e) { alert('Failed: ' + (e && e.message)); });
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
