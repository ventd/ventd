// devices.js — theme toggle + chevron expand/collapse
(function () {
  'use strict';

  var toggle = document.getElementById('theme-toggle');
  if (toggle) {
    toggle.addEventListener('click', function () {
      var r = document.documentElement;
      r.dataset.theme = r.dataset.theme === 'dark' ? 'light' : 'dark';
    });
  }

  // chip rows toggle their attached <div class="entities"> sibling
  var rows = document.querySelectorAll('.chip-row');
  rows.forEach(function (row) {
    row.addEventListener('click', function (e) {
      if (e.target.closest('.action-btn')) return;
      row.classList.toggle('is-expanded');
      var next = row.nextElementSibling;
      if (next && next.classList.contains('entities')) {
        next.classList.toggle('is-collapsed', !row.classList.contains('is-expanded'));
      }
    });
    // keyboard support
    row.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        row.click();
      }
    });
  });

  // initial: rows that aren't expanded should hide their entities
  rows.forEach(function (row) {
    if (!row.classList.contains('is-expanded')) {
      var next = row.nextElementSibling;
      if (next && next.classList.contains('entities')) {
        next.classList.add('is-collapsed');
      }
    }
  });
})();
