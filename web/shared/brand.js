// shared/brand.js — injects the spinning propeller logo into every .brand-mark
// (kept tiny + pure DOM so it works under strict CSP)
(function () {
  'use strict';

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

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', paint);
  } else {
    paint();
  }
})();
