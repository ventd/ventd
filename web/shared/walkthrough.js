// shared/walkthrough.js — first-visit-only welcome banner per page.
//
// Runs on every page that includes this script (dashboard, hardware,
// smart so far). On load:
//   1. Match the page's pathname to a per-page content entry (PAGES).
//   2. If no entry: silent no-op.
//   3. If entry exists AND localStorage 'ventd-walkthrough-<page>' is
//      unset: inject a dismissible card just under the topbar
//      describing what the operator is looking at.
//   4. On dismiss: localStorage.setItem('ventd-walkthrough-<page>', '1')
//      so subsequent loads skip.
//
// All content is plain text rendered through textContent — no
// innerHTML, no markdown library, RULE-UI-01.

(function () {
  'use strict';

  // PAGES: pathname → walkthrough entry. Pathname is matched on suffix
  // so /dashboard.html and /dashboard both hit "dashboard". Each entry
  // is a list of paragraphs the card renders in order. Keep the prose
  // tight — operators read this once.
  var PAGES = {
    dashboard: {
      title: 'Welcome to the Dashboard',
      body: [
        'This is the at-a-glance live view of the system. The top row shows current CPU and GPU temperature with a 60-second spark of past values; the small arrow under each card is Layer-C’s honest prediction of how the next +1 PWM step would change that temperature.',
        'Below the hero, each fan tile shows live RPM and duty. A teal halo on a tile means that fan just changed its PWM — the matching entry appears in the Decisions panel on the right with the cause.',
        'The system narrator above the hero summarises what the controller just did. Everything you see traces to a real backend signal — if a number is unknown, you’ll see “—” rather than a fabricated value.'
      ]
    },
    hardware: {
      title: 'Welcome to Hardware',
      body: [
        'This is the read-only inventory of every chip, fan, and sensor the daemon enumerated on this host. Topology maps daemon → chip → channel; click into a chip card to see its raw hwmon name and the per-channel capability the catalog assigned.',
        'Anything that came back as monitor-only, phantom, or unsupported is flagged with a small label so you can spot quickly which channels the controller will not touch.',
        'No control happens on this page — it’s the source of truth for what the daemon found, useful when you’re deciding whether a board profile change is needed.'
      ]
    },
    smart: {
      title: 'Welcome to Smart Mode',
      body: [
        'This page is the deep-dive view of the smart-mode controller. Per channel you can see Layer-B coupling θ, Layer-C marginal benefit θ (the β₀ + β₁·load formula), the active workload signature, and the blended w_pred weight feeding the controller this tick.',
        'The Bridge view sketches the controller’s tick pipeline; the Scope shows live PWM holds and RPM samples during the daemon’s opportunistic probes. The probe pill in the topbar lights up when one is in flight.',
        'Smart-mode shards persist across daemon restarts — a 5-minute cold-start hard pin guarantees w_pred=0 for every channel right after a fresh calibration completes, then the aggregator ramps up as the model converges.'
      ]
    }
  };

  function $(id) { return document.getElementById(id); }

  function pageKey() {
    var path = (location && location.pathname) || '';
    // Strip trailing slash + .html suffix so /dashboard, /dashboard.html,
    // and /dashboard/ all hit the same key.
    var p = path.replace(/\/$/, '').replace(/\.html$/, '');
    var leaf = p.split('/').pop();
    if (PAGES[leaf]) return leaf;
    // Some pages may live under a sub-path; try the segment before the
    // leaf as well (defensive — current routes are flat).
    var parts = p.split('/').filter(Boolean);
    for (var i = parts.length - 1; i >= 0; i--) {
      if (PAGES[parts[i]]) return parts[i];
    }
    return null;
  }

  function storageKeyFor(page) { return 'ventd-walkthrough-' + page; }

  function alreadySeen(page) {
    try { return localStorage.getItem(storageKeyFor(page)) === '1'; }
    catch (_) { return true; } // no localStorage → treat as seen, never show
  }

  function markSeen(page) {
    try { localStorage.setItem(storageKeyFor(page), '1'); } catch (_) {}
  }

  function renderBanner(page, entry) {
    var main = document.querySelector('main.shell-main');
    if (!main) return;
    if ($('walkthrough-' + page)) return; // idempotent

    var card = document.createElement('section');
    card.id = 'walkthrough-' + page;
    card.className = 'walkthrough';
    card.setAttribute('role', 'note');

    var head = document.createElement('header');
    head.className = 'walkthrough-head';
    var title = document.createElement('h2');
    title.className = 'walkthrough-title';
    title.textContent = entry.title;
    head.appendChild(title);
    var closeBtn = document.createElement('button');
    closeBtn.className = 'walkthrough-close';
    closeBtn.type = 'button';
    closeBtn.setAttribute('aria-label', 'Dismiss welcome card');
    closeBtn.textContent = '×';
    closeBtn.addEventListener('click', function () {
      markSeen(page);
      card.remove();
    });
    head.appendChild(closeBtn);
    card.appendChild(head);

    var body = document.createElement('div');
    body.className = 'walkthrough-body';
    (entry.body || []).forEach(function (paragraph) {
      var p = document.createElement('p');
      p.textContent = paragraph;
      body.appendChild(p);
    });
    card.appendChild(body);

    var foot = document.createElement('footer');
    foot.className = 'walkthrough-foot';
    var ack = document.createElement('button');
    ack.className = 'btn btn--primary';
    ack.type = 'button';
    ack.textContent = 'Got it';
    ack.addEventListener('click', function () {
      markSeen(page);
      card.remove();
    });
    foot.appendChild(ack);
    card.appendChild(foot);

    // Insert just inside <main>, before any other content. The shell
    // topbar lives outside <main>, so this lands above the page body.
    main.insertBefore(card, main.firstChild);
  }

  function check() {
    var page = pageKey();
    if (!page) return;
    if (alreadySeen(page)) return;
    var entry = PAGES[page];
    if (!entry) return;
    renderBanner(page, entry);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', check);
  } else {
    check();
  }
})();
