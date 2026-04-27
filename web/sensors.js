/* sensors.js — Sensors page interactivity.
 * - Theme toggle
 * - Source filter on/off (left rail)
 * - Click row to highlight + open detail rail
 * - Search filter
 */
(function () {
  'use strict';

  var html = document.documentElement;
  var btn  = document.getElementById('theme-toggle');
  if (btn) {
    btn.addEventListener('click', function () {
      var cur = html.getAttribute('data-theme') === 'light' ? 'light' : 'dark';
      html.setAttribute('data-theme', cur === 'light' ? 'dark' : 'light');
    });
  }

  // source rail toggle
  document.querySelectorAll('.s-source').forEach(function (s) {
    s.addEventListener('click', function () {
      s.classList.toggle('is-active');
      applySearch();
    });
  });

  // row select → highlight, optional detail update (we keep one fixed for the mock)
  document.querySelectorAll('.s-row').forEach(function (r) {
    r.addEventListener('click', function () {
      document.querySelectorAll('.s-row').forEach(function (x) { x.classList.remove('is-selected'); });
      r.classList.add('is-selected');
    });
  });

  var search = document.getElementById('sensors-search-input');
  if (search) search.addEventListener('input', applySearch);

  function applySearch() {
    var q = (search && search.value || '').trim().toLowerCase();
    document.querySelectorAll('.s-row').forEach(function (r) {
      var label = (r.querySelector('.label') ? r.querySelector('.label').textContent : '').toLowerCase();
      var path  = (r.querySelector('.path')  ? r.querySelector('.path').textContent  : '').toLowerCase();
      r.style.display = (!q || label.indexOf(q) >= 0 || path.indexOf(q) >= 0) ? '' : 'none';
    });
  }
})();
