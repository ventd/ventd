// shared/brand.js — injects the spinning propeller logo into every .brand-mark
// (kept tiny + pure DOM so it works under strict CSP) AND installs the CSRF
// fetch wrapper that echoes the `ventd_csrf` cookie value into an
// X-CSRF-Token header on every state-changing fetch (RULE-WEB-CSRF-TOKEN-
// REQUIRED-ON-STATE-CHANGE, v0.5.31).
(function () {
  'use strict';

  // ---------------------------------------------------------------
  // CSRF fetch wrapper — runs first so every subsequent fetch in the
  // page picks up the X-CSRF-Token header automatically. Pages don't
  // need to be touched; existing fetch() calls work transparently.
  //
  // The CSRF cookie (ventd_csrf) is set by the server on login as
  // non-HttpOnly so this script can read it. The session cookie
  // (ventd_session) remains HttpOnly. Server-side requireCSRF
  // middleware constant-time compares the X-CSRF-Token header
  // against the session's bound CSRF token; the cookie is only
  // the read-side of the synchroniser-token pattern.
  // ---------------------------------------------------------------
  function getCSRFToken() {
    var name = 'ventd_csrf=';
    var parts = (document.cookie || '').split(';');
    for (var i = 0; i < parts.length; i++) {
      var p = parts[i].replace(/^\s+/, '');
      if (p.indexOf(name) === 0) {
        return decodeURIComponent(p.substring(name.length));
      }
    }
    return '';
  }

  if (typeof window !== 'undefined' && typeof window.fetch === 'function' && !window.__ventdCSRFWrapped) {
    var nativeFetch = window.fetch.bind(window);
    window.fetch = function (input, init) {
      init = init || {};
      var method = ((init.method || (input && input.method) || 'GET') + '').toUpperCase();
      if (method === 'POST' || method === 'PUT' || method === 'PATCH' || method === 'DELETE') {
        var token = getCSRFToken();
        if (token) {
          var headers = new Headers(init.headers || (input && input.headers) || {});
          if (!headers.has('X-CSRF-Token')) {
            headers.set('X-CSRF-Token', token);
          }
          init.headers = headers;
        }
      }
      return nativeFetch(input, init);
    };
    window.__ventdCSRFWrapped = true;
  }

  var SVG_NS = 'http://www.w3.org/2000/svg';

  function buildPropeller() {
    var svg = document.createElementNS(SVG_NS, 'svg');
    svg.setAttribute('class', 'brand-prop');
    svg.setAttribute('viewBox', '-32 -32 64 64');
    svg.setAttribute('aria-hidden', 'true');

    // gradient defs
    var defs = document.createElementNS(SVG_NS, 'defs');
    var grad = document.createElementNS(SVG_NS, 'linearGradient');
    grad.setAttribute('id', 'ventd-blade');
    grad.setAttribute('x1', '0'); grad.setAttribute('y1', '0');
    grad.setAttribute('x2', '1'); grad.setAttribute('y2', '1');
    var s1 = document.createElementNS(SVG_NS, 'stop');
    s1.setAttribute('offset', '0'); s1.setAttribute('stop-color', '#56e3c9');
    var s2 = document.createElementNS(SVG_NS, 'stop');
    s2.setAttribute('offset', '1'); s2.setAttribute('stop-color', '#17a892');
    grad.appendChild(s1); grad.appendChild(s2);
    defs.appendChild(grad);
    svg.appendChild(defs);

    // 3 blades, 120° apart
    var bladeD = 'M -2 -3 C -6 -20 -22 -24 -26 -14 C -22 -10 -12 -6 0 -2 Z';
    [0, 120, 240].forEach(function (deg) {
      var p = document.createElementNS(SVG_NS, 'path');
      p.setAttribute('d', bladeD);
      p.setAttribute('fill', 'url(#ventd-blade)');
      if (deg) p.setAttribute('transform', 'rotate(' + deg + ')');
      svg.appendChild(p);
    });

    // hub
    var hubBg = document.createElementNS(SVG_NS, 'circle');
    hubBg.setAttribute('r', '3.6'); hubBg.setAttribute('fill', '#081518');
    svg.appendChild(hubBg);
    var hubFg = document.createElementNS(SVG_NS, 'circle');
    hubFg.setAttribute('r', '1.6'); hubFg.setAttribute('fill', '#17a892');
    svg.appendChild(hubFg);

    return svg;
  }

  function paint() {
    var marks = document.querySelectorAll('.brand-mark');
    marks.forEach(function (m) {
      if (m.querySelector('svg.brand-prop')) return; // already painted
      m.appendChild(buildPropeller());
    });
  }

  // paintActiveNav highlights the sidebar entry that matches the current
  // pathname. Each .nav-item in shared/sidebar.html carries data-page; the
  // first segment of window.location.pathname (or "dashboard" for /) is
  // compared to that attribute. Keeping the highlight here means every
  // page's static sidebar markup is byte-identical to shared/sidebar.html
  // (RULE-UI-03), without needing per-page active-class duplication.
  function paintActiveNav() {
    var path = (window.location.pathname || '/').replace(/\/+$/, '') || '/';
    var page = path === '/' ? 'dashboard' : path.replace(/^\//, '').split('/')[0];
    var items = document.querySelectorAll('.sidebar .nav-item');
    items.forEach(function (a) {
      if (a.dataset && a.dataset.page === page) a.classList.add('active');
      else a.classList.remove('active');
    });
  }

  function init() {
    paint();
    paintActiveNav();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
