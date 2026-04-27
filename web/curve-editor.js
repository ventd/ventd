// curve-editor.js — mockup interactivity (theme + chip/segment selection)
(function () {
  'use strict';

  var toggle = document.getElementById('theme-toggle');
  if (toggle) {
    toggle.addEventListener('click', function () {
      var r = document.documentElement;
      r.dataset.theme = r.dataset.theme === 'dark' ? 'light' : 'dark';
    });
  }

  // single-select group helper: pick one element in a group, mark .is-active
  function bindSingleSelect(selector) {
    var nodes = document.querySelectorAll(selector);
    nodes.forEach(function (n) {
      n.addEventListener('click', function () {
        nodes.forEach(function (m) { m.classList.remove('is-active'); });
        n.classList.add('is-active');
      });
    });
  }

  bindSingleSelect('.preset-chip');
  bindSingleSelect('.editor-mode-toggle button');
  bindSingleSelect('.sensor-option');
})();
