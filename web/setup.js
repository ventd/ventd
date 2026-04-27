// setup.js — first-boot screen interactivity (theme + tweaks)
(function () {
  'use strict';

  // theme toggle
  var toggle = document.getElementById('theme-toggle');
  if (toggle) {
    toggle.addEventListener('click', function () {
      var r = document.documentElement;
      r.dataset.theme = r.dataset.theme === 'dark' ? 'light' : 'dark';
    });
  }

  // password reveal
  var pwField   = document.getElementById('setup-password');
  var pwReveal  = pwField && pwField.parentElement.querySelector('.setup-input-action');
  if (pwField && pwReveal) {
    pwReveal.addEventListener('click', function () {
      pwField.type = pwField.type === 'password' ? 'text' : 'password';
    });
  }

  // token paste shortcut — best-effort, falls through if clipboard API gated
  var tokenField   = document.getElementById('setup-token');
  var tokenPaste   = tokenField && tokenField.parentElement.querySelector('.setup-input-action');
  if (tokenField && tokenPaste) {
    tokenPaste.addEventListener('click', async function () {
      try {
        var t = await navigator.clipboard.readText();
        tokenField.value = (t || '').trim().toUpperCase();
      } catch (_) {
        tokenField.focus();
      }
    });
  }

  // ─── Tweaks (uses the system protocol expected by the host) ──
  var card  = document.querySelector('.setup-card');
  var panel = document.getElementById('tweaks-panel');
  var close = document.getElementById('tweaks-close');

  function showPanel() { if (panel) panel.hidden = false; }
  function hidePanel() {
    if (panel) panel.hidden = true;
    try { window.parent.postMessage({type: '__edit_mode_dismissed'}, '*'); } catch (_) {}
  }

  // register listener BEFORE announcing availability
  window.addEventListener('message', function (e) {
    if (!e.data || typeof e.data !== 'object') return;
    if (e.data.type === '__activate_edit_mode')   showPanel();
    if (e.data.type === '__deactivate_edit_mode') hidePanel();
  });
  try { window.parent.postMessage({type: '__edit_mode_available'}, '*'); } catch (_) {}

  if (close) close.addEventListener('click', hidePanel);

  // wire each radiogroup → updates data-* on .setup-card
  document.querySelectorAll('.tweaks-segments').forEach(function (group) {
    var key = group.dataset.tweak;
    var btns = group.querySelectorAll('button');
    btns.forEach(function (b) {
      b.addEventListener('click', function () {
        btns.forEach(function (x) { x.setAttribute('aria-checked', 'false'); });
        b.setAttribute('aria-checked', 'true');
        if (card && key) card.dataset[camel(key)] = b.dataset.value;
      });
    });
  });

  // toggles → boolean data-* on .setup-card
  document.querySelectorAll('.tweaks-toggle').forEach(function (group) {
    var key = group.dataset.tweak;
    var btn = group.querySelector('.tweaks-toggle-btn');
    if (!btn) return;
    btn.addEventListener('click', function () {
      var on = btn.classList.toggle('is-on');
      btn.setAttribute('aria-pressed', on ? 'true' : 'false');
      if (card && key) card.dataset[camel(key)] = on ? 'true' : 'false';
    });
  });

  // initial sync of card dataset from current selections
  if (card) {
    document.querySelectorAll('.tweaks-segments').forEach(function (g) {
      var k = g.dataset.tweak;
      var sel = g.querySelector('button[aria-checked="true"]');
      if (k && sel) card.dataset[camel(k)] = sel.dataset.value;
    });
    document.querySelectorAll('.tweaks-toggle').forEach(function (g) {
      var k = g.dataset.tweak;
      var btn = g.querySelector('.tweaks-toggle-btn');
      if (k && btn) card.dataset[camel(k)] = btn.classList.contains('is-on') ? 'true' : 'false';
    });
  }

  function camel(s) { return s.replace(/-([a-z])/g, function (_, c) { return c.toUpperCase(); }); }
})();
