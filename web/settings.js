/* settings.js — minimal interactivity for the Settings page.
 * Theme toggle, tab nav (left rail switches active section),
 * segmented controls, switch toggles. Static-fidelity mock — no real persistence.
 */
(function () {
  'use strict';

  // ── theme toggle ────────────────────────────────────────────────
  var html = document.documentElement;
  var btn  = document.getElementById('theme-toggle');
  if (btn) {
    btn.addEventListener('click', function () {
      var cur = html.getAttribute('data-theme') === 'light' ? 'light' : 'dark';
      html.setAttribute('data-theme', cur === 'light' ? 'dark' : 'light');
    });
  }

  // ── tab nav: left rail clicks swap which section is .is-active ──
  var tocLinks = document.querySelectorAll('.set-toc a');
  var sections = document.querySelectorAll('.set-section');

  function showSection(id) {
    var hit = false;
    sections.forEach(function (s) {
      var match = s.id === id;
      s.classList.toggle('is-active', match);
      if (match) hit = true;
      if (match) s.scrollTop = 0;
    });
    tocLinks.forEach(function (a) {
      a.classList.toggle('is-current', a.getAttribute('href') === '#' + id);
    });
    if (hit) history.replaceState(null, '', '#' + id);
    return hit;
  }

  tocLinks.forEach(function (a) {
    a.addEventListener('click', function (e) {
      var id = a.getAttribute('href').slice(1);
      if (!id) return;
      e.preventDefault();
      showSection(id);
    });
  });

  // initialise: hash > current TOC entry > first section
  var initial = location.hash.slice(1);
  if (!initial) {
    var cur = document.querySelector('.set-toc a.is-current');
    if (cur) initial = cur.getAttribute('href').slice(1);
  }
  if (!initial && sections[0]) initial = sections[0].id;
  if (initial) showSection(initial);

  // ── segmented controls (theme picker, temp unit) ────────────
  document.querySelectorAll('.set-seg').forEach(function (seg) {
    seg.addEventListener('click', function (e) {
      var t = e.target.closest('button');
      if (!t || !seg.contains(t)) return;
      seg.querySelectorAll('button').forEach(function (b) { b.classList.remove('is-active'); });
      t.classList.add('is-active');
      if (seg.dataset.target === 'theme') {
        var v = t.dataset.value;
        if (v === 'auto') {
          var prefers = window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches;
          html.setAttribute('data-theme', prefers ? 'light' : 'dark');
        } else {
          html.setAttribute('data-theme', v);
        }
      }
    });
  });

  // ── toggle switches ─────────────────────────────────────────
  document.querySelectorAll('.set-toggle').forEach(function (t) {
    t.addEventListener('click', function () {
      t.classList.toggle('is-on');
    });
  });
})();
