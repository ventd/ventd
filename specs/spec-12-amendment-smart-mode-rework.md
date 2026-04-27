# spec-12 amendment — Smart-mode UI rework

**Status:** AMENDMENT to `spec-12-ui-redesign.md`. Drafted 2026-04-27
following Fork D smart-mode pivot.
**Scope:** Captures UI changes required across spec-12's PR sequence to
support the smart-mode architecture defined in `spec-smart-mode.md`.
**Estimated rework:** ~60-70% of existing spec-12 mockup content.
**References:** `spec-smart-mode.md` (design of record),
`specs/spec-12-ui-redesign.md` (base spec being amended).

---

## 1. Why this amendment exists

spec-12's original mockup bundle (`ventd-dashboard-mockup.html`,
`ventd-curve-editor-mockup.html`, `ventd-calibration-mockup.html`,
`ventd-devices-mockup.html`, `ventd-health-mockup.html`,
`ventd-host-switcher-mockup.html`) was designed against the pre-pivot
ventd model:

- Catalog-hit calibration as the assumed setup path.
- Static current-values display (no learning state, no confidence).
- Health page as a separate destination from doctor.
- No three-state install outcome (control / monitor-only / refused).
- No preset-driven autonomous control (manual curves were the norm).

The smart-mode architecture changes most of these assumptions. This
amendment enumerates the changes and assigns them to the v0.5.x patch
sequence so the UI rework lands incrementally with the corresponding
backend behaviour.

---

## 2. What survives unchanged

The design system survives. Specifically:

- Token system (colors, typography, spacing) — no changes.
- Sidebar shell and navigation structure — no changes.
- Host switcher (`ventd-host-switcher-mockup.html`) — orthogonal to
  smart-mode; no changes.
- Curve editor (`ventd-curve-editor-mockup.html`) — used only in
  manual mode under smart-mode; survives as power-user surface.
- General layout grids, modal patterns, button styles — no changes.

---

## 3. What requires rework, by patch

### 3.1 v0.5.1 — Wizard three-state fork

**Existing mockup state:** Wizard assumes catalog hit and proceeds
to calibration. Single happy path.

**Required changes:**

- **New screen — Probe outcome: refused-virt-or-container.** Diagnostic
  page explaining "ventd should be installed on the host, not the
  guest." Includes detected runtime environment evidence + uninstall
  button. No "continue anyway" option in default UI (`--allow-container`
  is a CLI flag, not a UI option).
- **New screen — Probe outcome: refused-no-sensors.** Diagnostic page
  explaining "ventd cannot find any thermal sensors on this hardware."
  Includes contribute-profile link + uninstall button.
- **New screen — Probe outcome: monitor-only fork.** Three options:
  - Keep ventd as monitoring dashboard
  - Uninstall
  - [tickbox] Contribute anonymised profile of this hardware
- **Existing wizard flow** continues only when probe outcome is
  control mode.

**Mockup deliverables:** Three new HTML mockups —
`ventd-wizard-refused-virt.html`,
`ventd-wizard-refused-no-sensors.html`,
`ventd-wizard-monitor-only-fork.html`.

**Existing spec-12 PR 1 (#661, already shipped):** retained as base.
v0.5.1's PR includes the three new screens as wizard amendments.

### 3.2 v0.5.3 — Calibration progress with idle gate + resume

**Existing mockup state:** `ventd-calibration-mockup.html` shows
in-progress calibration with simple progress bar.

**Required changes:**

- **Pre-calibration screen.** Explicit user idle gate text:
  > "Calibration will take approximately N minutes. For accurate
  > readings:
  > - Close all open applications.
  > - Do not use the machine during calibration.
  > - The fans will run at varying speeds.
  >
  > Click Begin when ready."
  
  Single Begin button. Cancel returns to previous wizard step.

- **In-progress screen.** Per-channel state visible: which channel is
  being probed, what stage (polarity disambiguation / Envelope C
  probe / etc.), elapsed time per channel.

- **Paused-for-user screen.** When load monitor detects background
  activity:
  > "Background activity detected — please ensure system is idle."
  
  Resume button + Cancel button. State preserved for resumption.

- **Per-channel envelope state on completion.** Summary screen shows
  per-channel outcome:
  - "Channel 1 — calibrated successfully (Envelope C)"
  - "Channel 2 — fell back to ramp-up-only (Envelope C aborted:
    thermal slope exceeded)"
  - "Channel 3 — registered as monitor-only (phantom channel detected)"

**Mockup deliverable:** Replace `ventd-calibration-mockup.html` with
multi-state version covering pre / in-progress / paused / complete.

### 3.3 v0.5.7-v0.5.9 — Confidence indicators (load-bearing UI)

**Existing mockup state:** Dashboard, Devices, Curve Editor show
current values without any concept of learning confidence.

**Required changes per page:**

**Dashboard (`ventd-dashboard-mockup.html`):**

- Per-channel confidence pill next to RPM/PWM display:
  - Color-coded (red → cold-start, amber → warming, green →
    predictive, grey → drifting/aborted).
  - Tooltip on hover: "Smart-mode is still learning this channel.
    Reactive control active. Predictive mode in approximately N
    days at current usage."
- Top-of-page mode banner showing aggregate smart-mode state:
  - Cold start / Warming / Predictive mode active / Drifting
  - When Drifting: brief diagnostic + link to doctor.

**Devices page (`ventd-devices-mockup.html`):**

- Per-channel detail row expands to show:
  - Layer A confidence (response curve coverage)
  - Layer B confidence (thermal coupling map)
  - Layer C confidence (marginal benefit per workload signature)
  - Visual representation of curve coverage (which PWM range visited,
    which gaps remain).
- Removal of any "manually edit calibration" button from default
  view; manual mode toggle gates access.

**Curve Editor (`ventd-curve-editor-mockup.html`):**

- Default-mode users do not see this page. Curve editor surfaces in
  the sidebar only when at least one channel is in manual mode.
- Manual-mode toggle per channel. Toggle off → channel returns to
  smart-mode, learned state is preserved (manual override does not
  wipe smart-mode learning when toggled away).

**Mockup deliverables:**

- Update `ventd-dashboard-mockup.html` with confidence pills + mode
  banner.
- Update `ventd-devices-mockup.html` with per-channel confidence
  breakdown.
- Update `ventd-curve-editor-mockup.html` with manual-mode toggle UX.

### 3.4 v0.5.10 — Doctor consolidation with Health page

**Existing mockup state:** `ventd-health-mockup.html` is a separate
page from doctor (CLI-only). Two destinations for "is ventd OK."

**Required changes:**

- **Consolidate Health page into doctor.** Single web UI page named
  "Health" or "Doctor" (rename TBD; lean toward "Health" as the
  user-facing label since doctor's connotation is "something is
  wrong").
- **Page structure (top to bottom):**
  - Live metrics top: per-channel current state, current RPM/temp,
    smart-mode mode banner.
  - Active recovery items middle: list of items requiring user
    attention. Empty when all is well.
  - Expandable "smart-mode internals" fold bottom: confidence per
    layer per channel, recent recalibration events, recent envelope
    aborts, workload signature library state, saturation observations.
- **CLI parity.** `ventd doctor` produces ASCII rendering of the
  same content. Live metrics → table. Recovery items → numbered
  list. Internals → collapsible sections (expanded by default in
  CLI).
- **Sidebar nav update.** Remove separate "Health" entry.
  "Doctor"/"Health" is the consolidated page.

**Mockup deliverable:** Replace `ventd-health-mockup.html` with
consolidated doctor page. Update sidebar mockup to remove duplicate.

### 3.5 Settings page — Reset to initial setup + signature toggle

**Existing mockup state:** Settings page mockup not present in
spec-12 base bundle (deferred).

**Required additions when Settings page lands:**

- **Reset to initial setup button.** Confirmation modal warning per
  spec-v0_5_1 §7.3.
- **Workload signature learning toggle.** Default ON in auto mode.
  Off when in manual mode (greyed out, explanation tooltip).
- **Opportunistic active probing toggle.** Default ON. User can
  disable with explanation that disabling will leave coverage gaps in
  the low end of the response curve.
- **Noise-vs-performance preset selector.** Three options — Silent,
  Balanced, Performance. Live preview text: "Currently: Balanced.
  Smart-mode will weight acoustic cost and thermal benefit roughly
  equally."

**Mockup deliverable:** New `ventd-settings-mockup.html`. Lands with
v0.5.6 (signature learning toggle becomes meaningful) or v0.5.5
(opportunistic probing toggle becomes meaningful), whichever comes
first.

---

## 4. UX consequences of smart-mode autonomy

### 4.1 No thermal targets in default UI

Users in default auto mode never see a "set thermal target" control.
Removed from any mockup that had it. The user's only control surface
in auto mode is preset selection.

If a user wants thermal targets, they must explicitly switch a
channel to manual mode. This is a deliberate friction — it matches
the masterplan's "ventd should require minimal if any user input to
function" principle.

### 4.2 No manual curves in default UI

Manual fan curves are accessed only through manual mode toggle.
Default auto mode has no curve-editing surface. The curve editor
mockup remains for manual users but moves out of the default
navigation.

### 4.3 Load-bearing confidence transparency

Confidence indicators are not optional UI polish. They are how the
user verifies smart-mode is doing its job.

A user who installs ventd, picks Balanced, and walks away should be
able to return three days later, glance at the dashboard, and see
"Predictive mode active" — confirming that ventd successfully
learned their system. Without that confirmation, smart-mode is
opaque autopilot, and users have no honest way to evaluate it.

### 4.4 Doctor as the rare destination

Healthy systems' doctor pages are mostly empty. This is correct.
Users should not visit doctor regularly; they should visit when
something is wrong, find the answer, and leave. The internals fold
exists for the curious user, not as a primary surface.

---

## 5. Mockup work assignment

| Patch | Mockup deliverables | Estimated mockup hours |
|---|---|---|
| v0.5.1 | 3 new wizard screens (refused-virt, refused-no-sensors, monitor-only fork) | 4-6h |
| v0.5.3 | Replace calibration mockup (4 states) | 4-6h |
| v0.5.5 | Settings: opportunistic probing toggle (incremental) | 1-2h |
| v0.5.6 | Settings: signature learning toggle + manual-mode behaviour spec | 2-3h |
| v0.5.7-v0.5.9 | Dashboard + Devices confidence rework (3 mockups updated, multiple states each) | 8-12h |
| v0.5.10 | Health/Doctor consolidation + CLI parity reference | 4-6h |
| **Total** | | **23-35h of mockup work** |

These hours are Phoenix-side design hours, not CC implementation.
Mockups are HTML+CSS in the existing token system; CC implements
the React+web backend against the mockups in each patch's PR.

---

## 6. CC implementation cost impact

This amendment estimates **+$20-40 across spec-12 PR 2-4 retrofits**
on top of the spec-v0_5_1 through spec-v0_5_10 implementation costs.

Distributed roughly as:

- v0.5.1 wizard rework: +$5-10 (incremental on the spec-v0_5_1 PR).
- v0.5.3 calibration UI states: +$5-10 (incremental).
- Settings page additions: +$3-5 each per toggle.
- Dashboard + Devices confidence rework: +$10-15 (most expensive).
- Doctor consolidation: +$5-10.

Total smart-mode UI-rework CC cost is contained within each
patch's existing budget; this amendment doesn't add new PRs, it
extends the scope of patches that were already going to touch UI.

---

## 7. Invariant bindings (RULE-UI-SMART-* extension)

| Rule ID | Statement |
|---|---|
| `RULE-UI-SMART-01` | Default auto mode UI MUST NOT expose thermal target setting on any page. Manual mode toggle is the gate. |
| `RULE-UI-SMART-02` | Default auto mode UI MUST NOT expose manual curve editing on any page. Curve editor sidebar entry visible only when ≥1 channel is in manual mode. |
| `RULE-UI-SMART-03` | Dashboard MUST display per-channel confidence indicator and aggregate smart-mode mode banner. |
| `RULE-UI-SMART-04` | Devices page MUST display per-channel breakdown of Layer A, Layer B, Layer C confidence. |
| `RULE-UI-SMART-05` | Doctor page MUST consolidate live metrics + recovery items + internals fold. Separate "Health" page MUST NOT exist post-v0.5.10. |
| `RULE-UI-SMART-06` | `ventd doctor` CLI output MUST mirror web UI doctor content. ASCII rendering of live metrics + recovery + internals. |
| `RULE-UI-SMART-07` | Settings page MUST expose: noise-vs-perf preset selector (3 options), reset-to-initial-setup button, signature learning toggle, opportunistic probing toggle. |
| `RULE-UI-SMART-08` | Wizard flow MUST handle four outcome states: control mode, monitor-only fork, refused-virt-or-container, refused-no-sensors. No fifth state. |
| `RULE-UI-SMART-09` | Calibration UI MUST be resumable: paused-for-user state preserved, Resume button restores from saved progress. |
| `RULE-UI-SMART-10` | Per-channel confidence indicator MUST be color-coded against a defined token set: cold-start / warming / predictive / drifting / aborted. Tokens added to design system as part of v0.5.7. |

Each rule binds 1:1 to a UI test (Playwright or equivalent) in the
web suite. CLI parity (RULE-UI-SMART-06) binds to a CLI golden-file
test.

---

## 8. Open questions

These remain undecided and are not blockers for v0.5.1 but should be
resolved before v0.5.7-v0.5.10:

1. **Confidence pill visual.** Bar (gradient fill) or pill (discrete
   state) or numeric (percentage)? Lean toward pill with tooltip
   showing percentage on hover.
2. **"Health" vs "Doctor" page label.** "Health" is more user-friendly,
   "Doctor" matches the CLI command. May need both — page titled
   "Health" with footer link "Run `ventd doctor` from the command
   line for the same view."
3. **Manual-mode toggle granularity.** Per-channel manual mode is
   defined; should "manual mode" also be a global preference (lock
   all channels)? Lean yes — adds one switch in Settings.
4. **Workload signature library inspection.** Internals fold shows
   count + top signatures by frequency. Should users see actual
   signature hashes, or aggregate stats only? Lean stats-only —
   hashes are opaque and exposing them gains nothing.

These can be resolved during the v0.5.7+ design iteration, in chat,
before mockups land.

---

## 9. References

- `spec-smart-mode.md` §2, §6, §7, §8, §9 — design of record for
  what UI must support.
- `specs/spec-12-ui-redesign.md` — base spec being amended.
- `specs/spec-12-amendment-oot-driver-install.md` — earlier
  amendment, orthogonal.
- `specs/spec-v0_5_1-catalog-less-probe.md` — wizard rework consumer.

---

**End of amendment.**
