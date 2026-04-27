/* logs.js — Logs page interactivity.
 * - Theme toggle
 * - Click row to expand details
 * - Level chip on/off
 * - Source filter on/off
 * - Follow toggle (visual only)
 * No streaming — this is a static design fidelity mock with seeded entries.
 */
(function () {
  'use strict';

  // ── theme ──────────────────────────────────────────────────
  var html = document.documentElement;
  var btn  = document.getElementById('theme-toggle');
  if (btn) {
    btn.addEventListener('click', function () {
      var cur = html.getAttribute('data-theme') === 'light' ? 'light' : 'dark';
      html.setAttribute('data-theme', cur === 'light' ? 'dark' : 'light');
    });
  }

  // ── expand log rows ───────────────────────────────────────
  document.querySelectorAll('.log-row').forEach(function (row) {
    row.addEventListener('click', function (e) {
      // ignore clicks inside the detail action buttons
      if (e.target.closest('button')) return;
      row.classList.toggle('is-open');
    });
  });

  // ── level filter chips ────────────────────────────────────
  document.querySelectorAll('.logs-level-row').forEach(function (r) {
    r.addEventListener('click', function () {
      r.classList.toggle('is-off');
      r.classList.toggle('is-on', !r.classList.contains('is-off'));
      applyFilters();
    });
  });

  // ── source toggle ─────────────────────────────────────────
  document.querySelectorAll('.logs-source').forEach(function (s) {
    s.addEventListener('click', function () {
      s.classList.toggle('is-off');
      s.classList.toggle('is-active', !s.classList.contains('is-off'));
      applyFilters();
    });
  });

  // ── time-range tabs ───────────────────────────────────────
  document.querySelectorAll('.logs-time-btn').forEach(function (b) {
    b.addEventListener('click', function () {
      document.querySelectorAll('.logs-time-btn').forEach(function (x) { x.classList.remove('is-active'); });
      b.classList.add('is-active');
    });
  });

  // ── follow toggle ─────────────────────────────────────────
  var follow = document.getElementById('logs-follow');
  if (follow) {
    follow.addEventListener('click', function () {
      follow.classList.toggle('is-paused');
      var label = follow.querySelector('.follow-label');
      if (label) {
        label.textContent = follow.classList.contains('is-paused') ? 'paused' : 'live · following';
      }
    });
  }

  // ── search filter (substring, case-insensitive) ───────────
  var search = document.getElementById('logs-search-input');
  if (search) {
    search.addEventListener('input', applyFilters);
  }

  function applyFilters() {
    var activeLevels = {};
    document.querySelectorAll('.logs-level-row').forEach(function (r) {
      activeLevels[r.dataset.level] = !r.classList.contains('is-off');
    });
    var activeSources = {};
    document.querySelectorAll('.logs-source').forEach(function (s) {
      activeSources[s.dataset.source] = !s.classList.contains('is-off');
    });
    var q = (search && search.value || '').trim().toLowerCase();

    document.querySelectorAll('.log-row').forEach(function (row) {
      var lvl = row.dataset.level;
      var src = row.dataset.source;
      var msg = (row.querySelector('.msg') ? row.querySelector('.msg').textContent : '').toLowerCase();
      var ok = (activeLevels[lvl] !== false) && (activeSources[src] !== false);
      if (ok && q) ok = msg.indexOf(q) >= 0;
      row.style.display = ok ? '' : 'none';
    });

    // recount visible
    var n = document.querySelectorAll('.log-row:not([style*="display: none"])').length;
    var ct = document.getElementById('logs-visible-count');
    if (ct) ct.textContent = n;
  }
})();
