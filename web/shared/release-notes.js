// shared/release-notes.js — patch-notes-on-first-login modal.
//
// Runs on every page that includes this script (dashboard, settings,
// etc.). On load:
//   1. Fetch /api/v1/version → current daemon version.
//   2. Read localStorage 'ventd-last-seen-version' → last version
//      this browser acknowledged.
//   3. If current != last-seen: fetch /api/v1/release-notes?since=
//      <last-seen> for the slice of CHANGELOG sections between them.
//   4. If sections.length > 0: render a dismissible modal with the
//      sections rendered as light-touch markdown (bold, code, lists,
//      links) — no external markdown lib, RULE-UI-01.
//   5. On dismiss: localStorage.setItem('ventd-last-seen-version',
//      current). Subsequent loads skip the fetch + modal.
//
// First-ever load (no last-seen in localStorage) records current
// without showing a modal — the operator hasn't expressed an
// expectation about prior versions, and the CHANGELOG can be 100s
// of lines long.
//
// All visual state derives from real backend signals: daemon's
// VersionInfo + parsed CHANGELOG.md content. No fabrication. Per
// the no-theatre rule.

(function () {
  'use strict';

  var STORAGE_KEY = 'ventd-last-seen-version';

  function $(id) { return document.getElementById(id); }

  // Tiny markdown → safe DOM converter. Handles only the subset that
  // appears in the CHANGELOG: ## headers (turned into h3), ### headers
  // (h4), bullet lists (- or *), inline `code`, **bold**, *italic*,
  // [link](url), paragraphs separated by blank lines. Every text
  // insert goes through textContent — never innerHTML — so the
  // CHANGELOG can't inject a <script> via crafted markdown.
  function renderMarkdown(md) {
    var container = document.createElement('div');
    container.className = 'rn-md';
    var lines = md.split('\n');
    var i = 0;
    while (i < lines.length) {
      var line = lines[i];
      if (line.trim() === '') { i++; continue; }
      // Bullet list
      if (/^[-*]\s+/.test(line)) {
        var ul = document.createElement('ul');
        while (i < lines.length && /^[-*]\s+/.test(lines[i])) {
          var li = document.createElement('li');
          renderInline(li, lines[i].replace(/^[-*]\s+/, ''));
          ul.appendChild(li);
          i++;
        }
        container.appendChild(ul);
        continue;
      }
      // Heading
      var hMatch = /^(#{2,6})\s+(.*)$/.exec(line);
      if (hMatch) {
        var level = Math.min(6, hMatch[1].length + 1); // ## → h3, ### → h4 …
        var h = document.createElement('h' + level);
        renderInline(h, hMatch[2]);
        container.appendChild(h);
        i++;
        continue;
      }
      // Paragraph (collect contiguous non-blank, non-bullet, non-heading lines)
      var p = document.createElement('p');
      var paraLines = [];
      while (i < lines.length && lines[i].trim() !== '' && !/^[-*]\s+/.test(lines[i]) && !/^#{2,6}\s+/.test(lines[i])) {
        paraLines.push(lines[i]);
        i++;
      }
      renderInline(p, paraLines.join(' '));
      container.appendChild(p);
    }
    return container;
  }

  function renderInline(parent, text) {
    // Inline parser: tokenise on **bold**, `code`, [text](url), *italic*.
    // Emit text nodes for everything else (textContent — XSS-safe).
    var re = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*|\[[^\]]+\]\([^)]+\))/g;
    var lastIdx = 0;
    var m;
    while ((m = re.exec(text)) !== null) {
      if (m.index > lastIdx) {
        parent.appendChild(document.createTextNode(text.slice(lastIdx, m.index)));
      }
      var tok = m[0];
      if (tok[0] === '`') {
        var code = document.createElement('code');
        code.textContent = tok.slice(1, -1);
        parent.appendChild(code);
      } else if (tok.slice(0, 2) === '**') {
        var b = document.createElement('strong');
        b.textContent = tok.slice(2, -2);
        parent.appendChild(b);
      } else if (tok[0] === '*') {
        var em = document.createElement('em');
        em.textContent = tok.slice(1, -1);
        parent.appendChild(em);
      } else if (tok[0] === '[') {
        var lm = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(tok);
        if (lm) {
          var a = document.createElement('a');
          a.textContent = lm[1];
          // Only allow https / http / mailto schemes — any other scheme
          // (javascript:, data:, etc.) gets dropped to plain text.
          var href = lm[2];
          if (/^(https?:|mailto:)/i.test(href)) {
            a.href = href;
            a.target = '_blank';
            a.rel = 'noopener noreferrer';
          }
          parent.appendChild(a);
        } else {
          parent.appendChild(document.createTextNode(tok));
        }
      } else {
        parent.appendChild(document.createTextNode(tok));
      }
      lastIdx = m.index + m[0].length;
    }
    if (lastIdx < text.length) {
      parent.appendChild(document.createTextNode(text.slice(lastIdx)));
    }
  }

  function showModal(currentVersion, sections) {
    // Lazily build the modal once; subsequent calls reuse the DOM.
    var modal = $('rn-modal');
    if (!modal) {
      modal = document.createElement('div');
      modal.id = 'rn-modal';
      modal.className = 'rn-modal';
      modal.setAttribute('role', 'dialog');
      modal.setAttribute('aria-labelledby', 'rn-title');
      modal.setAttribute('aria-modal', 'true');

      var card = document.createElement('div');
      card.className = 'rn-card';

      var head = document.createElement('header');
      head.className = 'rn-head';
      var title = document.createElement('h2');
      title.id = 'rn-title';
      title.className = 'rn-title';
      title.textContent = "What's new in " + currentVersion;
      head.appendChild(title);
      var closeBtn = document.createElement('button');
      closeBtn.className = 'rn-close';
      closeBtn.type = 'button';
      closeBtn.setAttribute('aria-label', 'Dismiss release notes');
      closeBtn.textContent = '×';
      closeBtn.addEventListener('click', dismiss);
      head.appendChild(closeBtn);
      card.appendChild(head);

      var body = document.createElement('div');
      body.id = 'rn-body';
      body.className = 'rn-body';
      card.appendChild(body);

      var foot = document.createElement('footer');
      foot.className = 'rn-foot';
      var ack = document.createElement('button');
      ack.className = 'btn btn--primary';
      ack.type = 'button';
      ack.textContent = 'Got it';
      ack.addEventListener('click', dismiss);
      foot.appendChild(ack);
      card.appendChild(foot);

      modal.appendChild(card);
      document.body.appendChild(modal);
    }
    var body = $('rn-body');
    body.textContent = '';
    sections.forEach(function (sec, idx) {
      if (idx > 0) {
        body.appendChild(document.createElement('hr'));
      }
      var versionLine = document.createElement('div');
      versionLine.className = 'rn-version-line';
      var v = document.createElement('span');
      v.className = 'rn-version mono';
      v.textContent = sec.version;
      versionLine.appendChild(v);
      if (sec.date) {
        var d = document.createElement('span');
        d.className = 'rn-date';
        d.textContent = ' · ' + sec.date;
        versionLine.appendChild(d);
      }
      body.appendChild(versionLine);
      body.appendChild(renderMarkdown(sec.markdown || ''));
    });

    function dismiss() {
      try { localStorage.setItem(STORAGE_KEY, currentVersion); } catch (_) {}
      modal.remove();
    }

    // Esc dismisses too.
    document.addEventListener('keydown', function escHandler(e) {
      if (e.key === 'Escape') {
        document.removeEventListener('keydown', escHandler);
        dismiss();
      }
    });
  }

  function check() {
    var lastSeen = '';
    try { lastSeen = localStorage.getItem(STORAGE_KEY) || ''; } catch (_) {}

    fetch('/api/v1/version', { credentials: 'same-origin', cache: 'no-store' })
      .then(function (r) { if (!r.ok) throw new Error('version ' + r.status); return r.json(); })
      .then(function (v) {
        var current = v.version || '';
        if (!current || current === 'dev' || current === 'unknown') return;
        // First-ever load on this browser: record current, no modal.
        if (!lastSeen) {
          try { localStorage.setItem(STORAGE_KEY, current); } catch (_) {}
          return;
        }
        if (lastSeen === current) return;

        // Fetch sections between last-seen and current.
        var url = '/api/v1/release-notes?since=' + encodeURIComponent(lastSeen);
        return fetch(url, { credentials: 'same-origin', cache: 'no-store' })
          .then(function (r) { if (!r.ok) throw new Error('release-notes ' + r.status); return r.json(); })
          .then(function (rn) {
            if (!rn || !Array.isArray(rn.sections) || rn.sections.length === 0) {
              // Nothing to show — record current to avoid re-checking.
              try { localStorage.setItem(STORAGE_KEY, current); } catch (_) {}
              return;
            }
            showModal(current, rn.sections);
          });
      })
      .catch(function () { /* silent — patch notes are non-critical */ });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', check);
  } else {
    check();
  }
})();
