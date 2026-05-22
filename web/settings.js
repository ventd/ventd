// settings.js — display / daemon / system sections.
//
//   GET  /api/v1/version          → { version, commit, date, go }
//   GET  /api/v1/config           → web settings, profile, etc
//   GET  /api/v1/system/watchdog  → watchdog state
//   POST /api/v1/system/reboot    → reboots host (confirm gate)
//   POST /api/v1/calibrate/reset  → wipes stored fan calibration data
//   POST /api/v1/admin/factory-reset → fans→BIOS + driver rmmod + state wipe + service disable
//   POST /api/v1/set-password     → admin password change
//
// Theme + temperature unit live in localStorage; the Display section
// just synchronises radio buttons with what's stored.

(function () {
  'use strict';

  function $(id) { return document.getElementById(id); }

  // ── theme selection ────────────────────────────────────────────────
  var root = document.documentElement;
  var stored = null;
  try { stored = localStorage.getItem('ventd-theme'); } catch (_) {}
  if (stored === 'light' || stored === 'dark') root.dataset.theme = stored;
  function applyTheme(value) {
    if (value === 'auto') {
      try { localStorage.removeItem('ventd-theme'); } catch (_) {}
      root.dataset.theme = (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) ? 'light' : 'dark';
    } else {
      root.dataset.theme = value;
      try { localStorage.setItem('ventd-theme', value); } catch (_) {}
    }
    paintSegment('theme-seg', value);
  }
  function paintSegment(id, value) {
    var grp = $(id); if (!grp) return;
    Array.prototype.forEach.call(grp.querySelectorAll('button'), function (b) {
      b.classList.toggle('is-active', b.dataset.value === value);
    });
  }
  function initSegments() {
    var initialTheme = stored || 'auto';
    paintSegment('theme-seg', initialTheme);
    Array.prototype.forEach.call($('theme-seg').querySelectorAll('button'), function (b) {
      b.addEventListener('click', function () { applyTheme(b.dataset.value); });
    });
    var initialUnit = 'c';
    try { initialUnit = localStorage.getItem('ventd-unit') || 'c'; } catch (_) {}
    paintSegment('unit-seg', initialUnit);
    Array.prototype.forEach.call($('unit-seg').querySelectorAll('button'), function (b) {
      b.addEventListener('click', function () {
        try { localStorage.setItem('ventd-unit', b.dataset.value); } catch (_) {}
        paintSegment('unit-seg', b.dataset.value);
      });
    });
    // Phantom-sensor reveal (#796). Persists the operator's "show
    // mirror + phantom fan*_input zones" choice in localStorage as
    // 'ventd-show-phantoms'. The dashboard reads this key on each
    // /api/v1/hardware/inventory fetch and appends
    // ?include_phantoms=1 when truthy.
    var phantomToggle = $('set-show-phantoms');
    if (phantomToggle) {
      var stored = false;
      try { stored = localStorage.getItem('ventd-show-phantoms') === '1'; } catch (_) {}
      phantomToggle.checked = stored;
      phantomToggle.addEventListener('change', function () {
        try {
          if (phantomToggle.checked) {
            localStorage.setItem('ventd-show-phantoms', '1');
          } else {
            localStorage.removeItem('ventd-show-phantoms');
          }
        } catch (_) {}
      });
    }
  }
  initSegments();
  var topThemeBtn = $('theme-toggle');
  if (topThemeBtn) topThemeBtn.addEventListener('click', function () {
    applyTheme(root.dataset.theme === 'dark' ? 'light' : 'dark');
  });

  // ── nav: filter visible section instead of scroll-to-anchor (#751) ─
  //
  // Sidebar entries filter the main panel down to a single section
  // rather than the legacy scroll-spy. The active section is stored
  // on .set-content's data-active-section attribute; CSS hides every
  // other section. Deep-linkable via ?section= or # hash.
  var navItems = document.querySelectorAll('.set-nav-item');
  var setContent = document.querySelector('.set-content');
  var sectionIDs = ['display', 'daemon', 'smart-mode', 'security',
                    'system', 'about', 'update', 'advanced'];
  function isKnownSection(id) {
    for (var i = 0; i < sectionIDs.length; i++) {
      if (sectionIDs[i] === id) return true;
    }
    return false;
  }
  function activateSection(id, updateHistory) {
    if (!isKnownSection(id)) id = 'display';
    setContent.setAttribute('data-active-section', id);
    Array.prototype.forEach.call(navItems, function (a) {
      a.classList.toggle('is-active', a.getAttribute('href') === '#' + id);
    });
    if (updateHistory) {
      try {
        history.replaceState(null, '', '?section=' + id);
      } catch (_) { /* file://, sandboxed iframe — ignore */ }
    }
    // Reset scroll so the operator lands at the top of the chosen
    // section rather than wherever the previous section ended.
    window.scrollTo({ top: 0 });
  }
  Array.prototype.forEach.call(navItems, function (a) {
    a.addEventListener('click', function (e) {
      e.preventDefault();
      var href = a.getAttribute('href') || '';
      var id = href.charAt(0) === '#' ? href.slice(1) : href;
      activateSection(id, true);
    });
  });
  // Initial section: ?section= wins over # hash wins over "display".
  function initialSection() {
    try {
      var m = /[?&]section=([a-z-]+)/.exec(window.location.search || '');
      if (m && isKnownSection(m[1])) return m[1];
    } catch (_) {}
    var h = (window.location.hash || '').replace(/^#/, '');
    if (isKnownSection(h)) return h;
    return 'display';
  }
  activateSection(initialSection(), false);
  // Browser back/forward through ?section= history entries.
  window.addEventListener('popstate', function () {
    activateSection(initialSection(), false);
  });

  // ── data fill ─────────────────────────────────────────────────────
  function setT(id, v) { var el = $(id); if (el) el.textContent = v == null || v === '' ? '—' : v; }

  // currentConfig holds the most recently fetched config so the
  // smart-mode toggle PUT can submit a complete payload (the API
  // expects the full config struct).
  var currentConfig = null;

  function loadConfig() {
    fetch('/api/v1/config', { credentials: 'same-origin' })
      .then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      })
      .then(function (c) {
        currentConfig = c;
        setT('set-listen', (c.web && c.web.listen) || '—');
        setT('set-tls', (c.web && c.web.tls_cert) ? 'enabled' : 'off');
        setT('set-ttl', (c.web && c.web.session_ttl) || 'default');
        setT('set-active', c.active_profile || '—');
        setT('set-curves', (c.curves && c.curves.length) || 0);
        setT('set-fans',   (c.fans && c.fans.length) || 0);
        setT('set-proxy', (c.web && c.web.trust_proxy && c.web.trust_proxy.length) ? c.web.trust_proxy.join(', ') : 'none');
        // v0.5.5: smart-mode opportunistic-probing toggle. The
        // default false means "probing enabled"; the toggle reads as
        // "Never actively probe after install" so the checkbox is
        // checked when the daemon is configured to NOT probe.
        var oppCheckbox = $('set-opp-disable');
        if (oppCheckbox) {
          oppCheckbox.checked = !!c.never_actively_probe_after_install;
        }
        // v0.5.6: workload signature learning toggle. Same shape;
        // the checkbox reads as "Disable signature learning" so the
        // box is checked when the daemon is configured to NOT learn.
        var sigCheckbox = $('set-sig-disable');
        if (sigCheckbox) {
          sigCheckbox.checked = !!c.signature_learning_disabled;
        }
        // #789: acoustic optimisation toggle. Default-on (true).
        // Pointer-bool field on the daemon side: missing/null in JSON
        // means "default true". The checkbox reads as "ON = enabled
        // = quietest-that-still-cools".
        var acousticCheckbox = $('set-acoustic');
        if (acousticCheckbox) {
          acousticCheckbox.checked =
            (c.acoustic_optimisation === undefined ||
             c.acoustic_optimisation === null ||
             c.acoustic_optimisation === true);
        }
      })
      .catch(function (err) {
        // Honest error path — the previous "demo fallback" populated
        // every readout with plausible-looking placeholder values
        // (0.0.0.0:9999, Quiet, 5 curves, 14 fans), which masked a
        // broken daemon as a working one. Bug-hunt finding (Agent 1
        // #4): operator on an unreachable daemon couldn't tell.
        // Render every readout as "—" + a single error line so the
        // failure mode is unmistakable.
        ['set-listen', 'set-tls', 'set-ttl', 'set-active',
         'set-curves', 'set-fans', 'set-proxy'].forEach(function (id) {
          setT(id, '—');
        });
        var msg = (err && err.message) ? err.message : 'unknown error';
        console.error('settings: /api/v1/config GET failed:', msg);
      });
  }

  // Generic toggle helper: mutates one boolean field on the cached
  // currentConfig and PUTs the whole struct. Returns true on 200.
  function putSmartModeToggle(field, checked) {
    if (!currentConfig) return Promise.resolve(false);
    var next = JSON.parse(JSON.stringify(currentConfig));
    next[field] = !!checked;
    return fetch('/api/v1/config', {
      method: 'PUT',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(next),
    })
      .then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        currentConfig = next;
        return true;
      })
      .catch(function (err) {
        console.error('settings: ' + field + ' PUT failed', err);
        return false;
      });
  }

  // Wire the toggles once the page is loaded. The change handler
  // debounces via the in-flight Promise: rapid toggling produces
  // sequential PUTs. On failure the checkbox reverts to keep the UI
  // honest about the server's view.
  var oppCheckbox = $('set-opp-disable');
  if (oppCheckbox) {
    oppCheckbox.addEventListener('change', function () {
      var desired = oppCheckbox.checked;
      putSmartModeToggle('never_actively_probe_after_install', desired).then(function (ok) {
        if (!ok) oppCheckbox.checked = !desired;
      });
    });
  }
  var sigCheckbox = $('set-sig-disable');
  if (sigCheckbox) {
    sigCheckbox.addEventListener('change', function () {
      var desired = sigCheckbox.checked;
      putSmartModeToggle('signature_learning_disabled', desired).then(function (ok) {
        if (!ok) sigCheckbox.checked = !desired;
      });
    });
  }
  // #789: acoustic optimisation toggle. Default-on; checkbox semantics
  // are direct ("ON = enabled" — no inversion).
  var acousticCheckbox = $('set-acoustic');
  if (acousticCheckbox) {
    acousticCheckbox.addEventListener('change', function () {
      var desired = acousticCheckbox.checked;
      putSmartModeToggle('acoustic_optimisation', desired).then(function (ok) {
        if (!ok) acousticCheckbox.checked = !desired;
      });
    });
  }

  // ── Smart-mode quietness preset + dBA override (#789, v0.6 prereq) ──
  // Both fields persist through cfg.smart on /api/v1/config. Server-side
  // validation is enforced by config.validate (RULE-CTRL-PRESET-01 +
  // RULE-CTRL-PRESET-03 + spec-v0_5_9 §3.1) so the UI just defends
  // against the obvious user-input cases.
  var SMART_PRESETS = ['silent', 'balanced', 'performance'];

  function smartCfg() {
    return (currentConfig && currentConfig.smart) || {};
  }
  function activeSmartPreset() {
    var p = (smartCfg().preset || 'balanced').toLowerCase();
    return SMART_PRESETS.indexOf(p) >= 0 ? p : 'balanced';
  }
  function paintPresetSegments(active) {
    SMART_PRESETS.forEach(function (p) {
      var btn = $('set-smart-preset-' + p);
      if (btn) btn.classList.toggle('is-active', p === active);
    });
  }
  function paintDbaOverride() {
    var inp = $('set-smart-dba');
    if (!inp) return;
    var t = smartCfg().dba_target;
    inp.value = (t === undefined || t === null) ? '' : String(t);
  }

  // Mutates a set of fields on cfg.smart (deleting any whose value is
  // null) and PUTs the whole config. On success the server returns the
  // validated config; we cache that as currentConfig so subsequent
  // patches are based on canonical state.
  function putSmartPatch(patch) {
    if (!currentConfig) return Promise.resolve(false);
    var next = JSON.parse(JSON.stringify(currentConfig));
    if (!next.smart) next.smart = {};
    Object.keys(patch).forEach(function (k) {
      if (patch[k] === null) {
        delete next.smart[k];
      } else {
        next.smart[k] = patch[k];
      }
    });
    return fetch('/api/v1/config', {
      method: 'PUT',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(next),
    })
      .then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      })
      .then(function (validated) {
        currentConfig = validated;
        return true;
      })
      .catch(function (err) {
        console.error('settings: smart patch PUT failed', err);
        return false;
      });
  }

  // Wire the preset segment buttons. The set-segments .is-active style
  // mirrors the existing theme/unit toggle pattern; switching is a
  // single PUT then repaint on success.
  SMART_PRESETS.forEach(function (preset) {
    var btn = $('set-smart-preset-' + preset);
    if (!btn) return;
    btn.addEventListener('click', function () {
      if (preset === activeSmartPreset()) return;
      paintPresetSegments(preset); // optimistic
      putSmartPatch({ preset: preset }).then(function (ok) {
        if (!ok) paintPresetSegments(activeSmartPreset()); // rollback
      });
    });
  });

  // Wire the dBA override input. Empty string clears the override
  // (delete cfg.smart.dba_target) so the controller falls back to the
  // preset default. Out-of-range or non-numeric input restores the
  // previous value silently — server-side validation would otherwise
  // 400 and roll back anyway.
  var dbaInput = $('set-smart-dba');
  if (dbaInput) {
    dbaInput.addEventListener('change', function () {
      var raw = dbaInput.value.trim();
      var patch;
      if (raw === '') {
        patch = { dba_target: null };
      } else {
        var v = parseFloat(raw);
        if (!isFinite(v) || v < 10 || v > 80) {
          paintDbaOverride();
          return;
        }
        patch = { dba_target: v };
      }
      putSmartPatch(patch).then(function (ok) {
        if (ok) paintDbaOverride();
      });
    });
  }

  // Initial paint runs after the /api/v1/config GET completes — we
  // hook into the existing loadConfig flow by polling currentConfig
  // briefly. After the first non-null cache, paint and stop.
  (function initialPaintWhenConfigArrives() {
    var tries = 0;
    var t = setInterval(function () {
      tries++;
      if (currentConfig) {
        paintPresetSegments(activeSmartPreset());
        paintDbaOverride();
        clearInterval(t);
      } else if (tries > 40) {
        // 40 × 100 ms = 4 s — give up if /config never returned.
        clearInterval(t);
      }
    }, 100);
  })();

  function loadVersion() {
    fetch('/api/v1/version', { credentials: 'same-origin' })
      .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
      .then(function (v) {
        setT('about-version', v.version || '—');
        setT('about-commit',  v.commit  || '—');
        setT('about-date',    v.date    || '—');
        setT('about-go',      v.go      || v.goversion || '—');
        setT('upd-installed', v.version || '—');
        var sb = $('sb-version'); if (sb) sb.textContent = v.version || '—';
      })
      .catch(function () {
        setT('about-version', '—');
        setT('about-commit',  '—');
        setT('about-date',    '—');
        setT('about-go',      '—');
        setT('upd-installed', '—');
      });
  }

  // ── Update flow ──────────────────────────────────────────────────
  // /api/v1/update/check polls GitHub for the latest release tag.
  // /api/v1/update/apply spawns the install.sh script with the
  // requested VENTD_VERSION; the daemon dies during the install's
  // systemctl restart and comes back under the new binary. After
  // POST /apply we poll /healthz until it returns 200 then reload
  // the page. Calibration / smart shards / config / login persist
  // across the restart via /var/lib/ventd.
  function wireUpdateFlow() {
    var checkBtn = $('upd-check');
    var applyBtn = $('upd-apply');
    var applyRow = $('upd-apply-row');
    var progRow  = $('upd-progress-row');
    var errRow   = $('upd-error-row');
    if (!checkBtn || !applyBtn) return;

    var latestVersion = null;

    var lastApplyRow = $('upd-last-apply-row');
    var lastApplyTailWrap = $('upd-last-apply-tail-wrap');

    // renderLastApplyError populates the persistent "last update attempt
    // failed" surface from /api/v1/update/check.last_apply_error. Visible
    // for both spawn shapes (systemd-run transient unit and nohup-
    // detached subshell — #1305) so an operator who saw "scheduled" then
    // silence has an explanation. Null/undefined clears the row.
    function renderLastApplyError(lae) {
      if (!lastApplyRow) return;
      if (!lae || !lae.status) {
        lastApplyRow.hidden = true;
        return;
      }
      lastApplyRow.hidden = false;
      var title = 'Last update attempt failed';
      if (lae.version) title += ' · ' + lae.version;
      if (lae.at) {
        // RFC3339Nano → readable local time. Truncate to seconds.
        var dt = new Date(lae.at);
        if (!isNaN(dt.getTime())) title += ' (' + dt.toLocaleString() + ')';
      }
      setT('upd-last-apply-title', title);
      setT('upd-last-apply-sub', lae.detail || ('Status: ' + lae.status));
      if (lastApplyTailWrap) {
        if (lae.journal_tail && lae.journal_tail.length > 0) {
          lastApplyTailWrap.hidden = false;
          setT('upd-last-apply-tail', lae.journal_tail);
        } else {
          lastApplyTailWrap.hidden = true;
        }
      }
    }

    checkBtn.addEventListener('click', function () {
      checkBtn.disabled = true;
      checkBtn.textContent = 'Checking…';
      if (errRow) errRow.hidden = true;
      if (applyRow) applyRow.hidden = true;
      fetch('/api/v1/update/check', { credentials: 'same-origin' })
        .then(function (r) { return r.json(); })
        .then(function (j) {
          checkBtn.disabled = false;
          checkBtn.textContent = 'Check for updates';
          // Always surface the last-apply error regardless of the
          // current check outcome — it's a separate signal ("did the
          // most recent apply succeed?") from "is a new release
          // available?".
          renderLastApplyError(j.last_apply_error);
          if (j.error) {
            if (errRow) {
              errRow.hidden = false;
              setT('upd-error-sub', j.error);
            }
            return;
          }
          setT('upd-latest', j.latest || '—');
          latestVersion = j.latest;
          if (j.available && j.latest) {
            if (applyRow) applyRow.hidden = false;
            setT('upd-apply-title', 'Update available · ' + j.latest);
            var subTxt = 'Installed ' + (j.current || '?') + ' → ' + j.latest;
            if (j.published_at) subTxt += ' (published ' + (j.published_at.split('T')[0] || j.published_at) + ')';
            setT('upd-apply-sub', subTxt);
            applyBtn.textContent = 'Apply ' + j.latest;
          } else {
            if (applyRow) applyRow.hidden = true;
            setT('upd-latest', (j.latest || '—') + ' · already on latest');
          }
        })
        .catch(function (e) {
          checkBtn.disabled = false;
          checkBtn.textContent = 'Check for updates';
          if (errRow) {
            errRow.hidden = false;
            setT('upd-error-sub', String(e));
          }
        });
    });

    applyBtn.addEventListener('click', function () {
      if (!latestVersion) return;
      if (!confirm('Apply update to ' + latestVersion + '? The daemon will restart. Calibration and smart-mode state persist; in-flight calibrations resume from the last completed step.')) return;
      applyBtn.disabled = true;
      checkBtn.disabled = true;
      if (applyRow) applyRow.hidden = true;
      if (progRow)  progRow.hidden  = false;
      setT('upd-progress-title', 'Update in progress · ' + latestVersion);
      setT('upd-progress-sub', 'Daemon restarting under the new binary…');

      fetch('/api/v1/update/apply', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ version: latestVersion })
      }).then(function (r) {
        if (!r.ok) {
          return r.text().then(function (t) {
            applyBtn.disabled = false;
            checkBtn.disabled = false;
            if (progRow) progRow.hidden = true;
            if (errRow)  {
              errRow.hidden = false;
              setT('upd-error-sub', 'apply failed: HTTP ' + r.status + ' ' + t);
            }
          });
        }
        // Poll /healthz every 1.5 s for up to 120 s. Once it returns
        // 200, reload the page so we get the new dashboard.js.
        var deadline = Date.now() + 120000;
        var poll = function () {
          if (Date.now() > deadline) {
            setT('upd-progress-sub', 'Daemon did not come back within 2 minutes — check journal: journalctl -u ventd');
            return;
          }
          fetch('/healthz', { credentials: 'same-origin', cache: 'no-store' })
            .then(function (rr) {
              if (rr.ok) {
                setT('upd-progress-sub', 'Daemon back up — reloading…');
                setTimeout(function () { location.reload(); }, 500);
              } else {
                setTimeout(poll, 1500);
              }
            })
            .catch(function () { setTimeout(poll, 1500); });
        };
        setTimeout(poll, 3000);  // give the install script a 3s head start
      });
    });
  }

  function loadWatchdog() {
    fetch('/api/v1/system/watchdog', { credentials: 'same-origin' })
      .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
      .then(function (s) {
        var pill = $('set-wd');
        if (!pill) return;
        var armed = s && (s.armed || s.active);
        pill.textContent = armed ? 'armed' : 'idle';
        pill.className = 'status-pill no-dot ' + (armed ? 'ok' : 'ro');
      })
      .catch(function () {
        var pill = $('set-wd');
        if (pill) { pill.textContent = 'armed'; pill.className = 'status-pill ok no-dot'; }
      });
  }

  // ── action wiring ─────────────────────────────────────────────────
  // Inline password change form — collapsed by default; the trigger
  // toggles it open so the inputs can use type="password" and the
  // browser/password-manager can autofill. The previous flow was three
  // window.prompt() dialogs that displayed the password in plain text.
  var pwTrigger = $('set-change-pw');
  var pwForm    = $('set-pw-form');
  var pwCurrent = $('set-pw-current');
  var pwNew     = $('set-pw-new');
  var pwConfirm = $('set-pw-confirm');
  var pwError   = $('set-pw-error');
  var pwCancel  = $('set-pw-cancel');
  var pwSave    = $('set-pw-save');

  function showPwError(msg) { pwError.textContent = msg; pwError.hidden = false; }
  function clearPwError()   { pwError.hidden = true;  pwError.textContent = ''; }

  function openPwForm() {
    pwForm.hidden = false;
    pwTrigger.setAttribute('aria-expanded', 'true');
    pwCurrent.value = ''; pwNew.value = ''; pwConfirm.value = '';
    clearPwError();
    pwCurrent.focus();
  }
  function closePwForm() {
    pwForm.hidden = true;
    pwTrigger.setAttribute('aria-expanded', 'false');
    pwCurrent.value = ''; pwNew.value = ''; pwConfirm.value = '';
    clearPwError();
    pwSave.disabled = false; pwSave.textContent = 'Save password';
  }

  pwTrigger.addEventListener('click', function () {
    if (pwForm.hidden) openPwForm(); else closePwForm();
  });
  pwCancel.addEventListener('click', closePwForm);

  pwForm.addEventListener('submit', function (e) {
    e.preventDefault();
    clearPwError();
    var current = pwCurrent.value;
    var next    = pwNew.value;
    var confirm = pwConfirm.value;
    if (!current)              { showPwError('Current password required.'); pwCurrent.focus(); return; }
    if (next.length < 8)       { showPwError('New password must be at least 8 characters.'); pwNew.focus(); return; }
    if (confirm !== next)      { showPwError('Passwords do not match.'); pwConfirm.focus(); return; }

    pwSave.disabled = true;
    pwSave.textContent = 'Saving…';

    // handleSetPassword expects {"current": ..., "new": ...} as JSON
    // (internal/web/server.go:1549). The previous form-encoded body
    // tripped the JSON decoder and surfaced as a 400 to the operator.
    fetch('/api/v1/set-password', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ current: current, new: next })
    })
      .then(function (r) {
        if (r.ok) { closePwForm(); alert('Password updated.'); return; }
        return r.json().catch(function () { return null; }).then(function (j) {
          showPwError((j && j.error) || ('Failed (HTTP ' + r.status + ').'));
          pwSave.disabled = false; pwSave.textContent = 'Save password';
        });
      })
      .catch(function (e) {
        showPwError('Network error: ' + (e && e.message || 'unable to reach the daemon.'));
        pwSave.disabled = false; pwSave.textContent = 'Save password';
      });
  });

  $('set-bundle').addEventListener('click', function () {
    alert('Diagnostic bundle is generated via the CLI:\n\n  ventd diag bundle\n\nA web-side trigger is on the roadmap; for now run that on the host.');
  });

  $('set-reboot').addEventListener('click', function () {
    if (!window.confirm('Reboot the host now? Open SSH sessions and running services will be terminated.')) return;
    fetch('/api/v1/system/reboot', { method: 'POST', credentials: 'same-origin' })
      .then(function () { alert('Reboot requested. The web UI will stop responding for ~30 seconds.'); });
  });

  // Reset calibration — clears stored fan characterization (PWM↔RPM
  // curves, response timing, polarity). Curves keep driving fans
  // unchanged; the operator immediately lands on /calibration so they
  // can re-run the sweep (typical trigger: swapped or added a fan).
  var btnResetCal = $('set-reset-calibration');
  if (btnResetCal) btnResetCal.addEventListener('click', function () {
    if (!window.confirm('Reset calibration?\n\nClears stored fan characterization so calibration starts fresh. Existing curves keep driving fans in the interim.\n\nNo config or login change. Use after swapping or adding a fan.')) return;
    btnResetCal.disabled = true;
    fetch('/api/v1/calibrate/reset', { method: 'POST', credentials: 'same-origin' })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, status: r.status, body: j }; }, function () { return { ok: r.ok, status: r.status, body: {} }; }); })
      .then(function (res) {
        if (res.ok) {
          alert('Calibration data cleared. Opening the Calibration page so you can re-run the sweep.');
          window.location.assign('/calibration');
          return;
        }
        var why = (res.body && (res.body.error || res.body.message)) || ('HTTP ' + res.status);
        alert('Reset calibration failed: ' + why + '.');
        btnResetCal.disabled = false;
      })
      .catch(function (err) {
        alert('Reset calibration failed: ' + err.message);
        btnResetCal.disabled = false;
      });
  });

  // Reset to factory — fans handed back to BIOS, OOT driver removed,
  // state wiped, ventd.service disabled+stopped. The daemon goes
  // offline mid-call; we paint a takeover so the user doesn't sit
  // staring at a dead settings page when the connection drops.
  var btnFactory = $('set-factory-reset');
  if (btnFactory) btnFactory.addEventListener('click', function () {
    if (!window.confirm('Reset to factory?\n\nThis hands every fan back to BIOS auto-control, removes the OOT driver ventd installed, wipes ALL ventd state (config, calibration, login credentials, smart-mode learning), and disables + stops the ventd service.\n\nThe web UI goes offline. Your firmware takes over cooling.\n\nThe ventd binary is left on disk — remove it with your package manager or run /usr/local/sbin/ventd-uninstall.\n\nThis cannot be undone.')) return;
    if (!window.confirm('Final confirmation: factory reset is destructive and irreversible. Proceed?')) return;
    btnFactory.disabled = true;
    fetch('/api/v1/admin/factory-reset', { method: 'POST', credentials: 'same-origin' })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, status: r.status, body: j }; }, function () { return { ok: r.ok, status: r.status, body: {} }; }); })
      .then(function (res) {
        if (res.ok) {
          paintFactoryResetTakeover();
          return;
        }
        var why = (res.body && (res.body.error || res.body.message)) || ('HTTP ' + res.status);
        alert('Factory reset failed: ' + why + '.');
        btnFactory.disabled = false;
      })
      .catch(function (err) {
        alert('Factory reset failed: ' + err.message);
        btnFactory.disabled = false;
      });
  });

  function paintFactoryResetTakeover() {
    var html = ''
      + '<main style="min-height:100vh;display:flex;align-items:center;justify-content:center;padding:48px 24px;font-family:inherit;background:var(--bg, #081518);color:var(--fg, #e8f3f1);">'
      + '  <section style="max-width:560px;text-align:left;">'
      + '    <h1 style="margin:0 0 16px;font-size:24px;font-weight:600;">Factory reset in progress</h1>'
      + '    <p style="margin:0 0 12px;line-height:1.6;">Fans have been returned to BIOS control. The OOT driver (if any) is being removed and the ventd service is being disabled and stopped.</p>'
      + '    <p style="margin:0 0 12px;line-height:1.6;">When the daemon shuts down this connection will drop. That is expected — ventd is offline.</p>'
      + '    <p style="margin:0 0 12px;line-height:1.6;">The ventd binary is still on disk. To remove it, run one of:</p>'
      + '    <pre style="background:rgba(255,255,255,0.05);padding:12px 16px;border-radius:6px;font-family:ui-monospace,Menlo,monospace;font-size:13px;overflow-x:auto;">sudo dnf remove ventd     # or apt / pacman / zypper\nsudo /usr/local/sbin/ventd-uninstall</pre>'
      + '    <p style="margin:16px 0 0;line-height:1.6;opacity:0.7;font-size:14px;">You may close this tab.</p>'
      + '  </section>'
      + '</main>';
    document.title = 'ventd — factory reset';
    document.body.innerHTML = html;
  }

  // ── live status ──────────────────────────────────────────────────
  function setLive(ok) {
    var d = $('sb-live-dot'), l = $('sb-live-label');
    if (d) d.classList.toggle('is-down', !ok);
    if (l) l.textContent = ok ? 'live' : 'reconnecting…';
  }

  loadConfig();
  loadVersion();
  loadWatchdog();
  wireUpdateFlow();
  setLive(true);
  setInterval(loadWatchdog, 5000);
})();
