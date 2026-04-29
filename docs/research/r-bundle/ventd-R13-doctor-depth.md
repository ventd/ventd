# R13 — Doctor Diagnostic Depth (v0.5.10)

**Target output:** spec content for `spec-v0_5_10-doctor-recovery.md` (the patch spec, drafted later in CC chat). This R13 is the design-of-record that spec consumes.

**Status of inputs:** spec-10 (fix-capable rewrite) shelved permanently per smart-mode pivot. v0.5.10 = "Doctor recovery-surface + internals fold + CLI parity." spec-12 amendment locks RULE-UI-SMART-05/06. spec-smart-mode §9 sketches the shape. R13's job is to fill in depth.

**What carries forward from shelved spec-10:**
- Plan/apply pipeline, pkexec integration, RULE-DOCTOR-FIX-01..06 (privilege gates, idempotency, loop-stop confirmation under `--fix-all`, single pkexec session, AppArmor rollback, distro detection)
- `hwdiag.Store` single-sourcing rule (was RULE-DOCTOR-10; renumbered below)
- JSON schema versioning, exit code conventions, <2s detect latency
- `bios_known_bad.go` is **deleted** per smart-mode pivot (replaced by behavioural detection); doctor consumes behavioural detection results from hwdiag.Store

**What's new in R13:**
- Three-surface model (live metrics / recovery items / internals fold) with explicit content per surface
- Recovery-item promotion algorithm (when does a problem become a recovery item)
- Smart-mode-specific data sources (Layer A/B/C confidence, identifiability, signature library state)
- CLI parity rendering rules
- Page-replaces-Health consolidation contract

---

## 1. The three-surface model

Doctor has exactly three surfaces, vertically stacked, top to bottom:

| Surface | Purpose | Always shown? | Content density |
|---|---|---|---|
| **Live metrics** | "Is ventd OK right now?" | Yes | Sparse — 5-8 vital indicators |
| **Recovery items** | "What needs my attention?" | Only when items exist | Variable — 0..N actionable items |
| **Internals fold** | "What is ventd doing under the hood?" | Yes (collapsed by default) | Dense — full smart-mode state dump |

The healthy-system doctor page is dominantly live metrics + collapsed internals fold. Recovery items section is absent entirely on healthy systems. This shape directly supports the "doctor as rare destination" principle (spec-12 amendment §4.4).

---

## 2. Surface 1 — Live metrics

### 2.1 Content (web UI: top of page; CLI: top section)

Sparse. Eight indicators, max. Each is a single line answering a vital-signs question:

| Indicator | Source | Healthy state | Unhealthy state |
|---|---|---|---|
| Daemon status | systemd unit state via dbus | "Running, uptime 4d 12h" | "Stopped" / "Restarting (5 in last hour)" |
| Smart-mode mode | `internal/controller` state | "Auto, balanced preset" | "Manual override on 2 channels" / "Disabled" |
| Aggregate confidence | mean of per-channel R12 conf | "Predictive (0.78)" | "Warming (0.32)" / "Drifting" |
| Active channels | HAL registry count | "8/8 channels controlled" | "6/8 controlled, 2 monitor-only" |
| Last calibration | calibration store newest record | "All channels calibrated within 7d" | "Channel 3 stale (24d)" / "Channel 5 never calibrated" |
| Hardware fingerprint | DMI fingerprint match status | "Profile matched (catalog hit)" | "Catalog miss, calibrated locally" / "BIOS changed since last boot" |
| Recent envelope aborts | hwdiag store, 24h window | "0 in last 24h" | "3 aborts on channel 5 in last 24h" |
| Recent Layer A activations | hwdiag store, 24h window | "0 in last 24h" | "12 hard-cap fires in last 24h" |

Each indicator is one of four colors: green (healthy), yellow (informational/degraded), orange (warning), red (blocker). Smart-mode-specific colors map to the existing token set per RULE-UI-SMART-10.

### 2.2 Algorithm: "what makes a metric live"

A metric is live (qualifies for surface 1) iff it answers "is ventd functionally OK at this moment." Test:

- Could a user reading only this metric, with no other context, decide whether to investigate further? → Live metric.
- Does the metric require interpretation against history or domain knowledge? → Internals fold, not live.

Examples:
- "Daemon status: Running" → live (binary, no interpretation).
- "Page-Hinkley CUSUM `m_k = 4.2`" → internals (requires knowing trip threshold, history).
- "Confidence: Predictive (0.78)" → live (categorical labels are pre-interpreted).
- "RLS `tr(P)` per shard" → internals (raw numerics, requires interpretation).

### 2.3 Refresh cadence

- Web UI: 1 Hz pull from doctor backend (matches controller tick rate).
- CLI: snapshot at invocation time, no refresh. `watch -n 1 ventd doctor` is the user's choice.
- Backend: live metrics derived from in-memory state, never disk reads. <10 ms compute budget.

---

## 3. Surface 2 — Recovery items

### 3.1 Content

A recovery item is an **actionable problem ventd has detected and can either fix automatically or guide the user to fix**. Each item has:

- **Title** — one-line problem statement, written in user-facing terms ("Channel 3 has aborted calibration twice"), not internal terms ("EnvelopeC abort_count > 1").
- **Severity** — Info / Warning / Blocker. Blockers gate auto-mode operation on the affected channel.
- **Detected via** — short trail to the data source (e.g., "calibration store, last 24h").
- **Recommended action** — one or more fix paths, ordered by recommendation:
  - **Auto-fix** (one-click button in web; `--fix` invocation in CLI): doctor runs the fix itself, plan/apply pipeline per inherited RULE-DOCTOR-FIX-* rules.
  - **Guided fix** (instructions for user action ventd cannot perform — e.g. BIOS settings change, hardware inspection): web shows steps, CLI prints the same steps as text.
  - **Acknowledge** (informational items; user clicks "got it" to suppress until next occurrence).
- **Affected entities** — channels, sensors, signatures, etc. Chips/tags in web; comma-separated in CLI.
- **First seen / last seen** — timestamps. Items reappearing after fix get new first-seen.

### 3.2 The recovery-item catalog

Recovery items are pre-defined types, each with a detector function, severity, and fix path. Adding a new recovery-item type is a versioned change to doctor (schema bump if fix shape changes). Initial v0.5.10 catalog:

| ID | Title | Severity | Detector | Fix |
|---|---|---|---|---|
| `RECOVER-001` | Conflicting fan-control daemon running | Blocker | systemd unit list ∩ known conflict set | Auto: stop+disable unit. RULE-DOCTOR-FIX-* loop-stop confirmation if affects loop. |
| `RECOVER-002` | Required kernel module missing | Blocker | `/sys/class/hwmon` walk vs catalog `requires_modules` | Auto: `modprobe` + `dkms` reinstall if available. Distro-specific. |
| `RECOVER-003` | `/var/lib/ventd` permissions wrong | Blocker | `os.Stat` vs install-contract spec | Auto: chown/chmod to `ventd:ventd 0750`. |
| `RECOVER-004` | AppArmor profile unloaded | Warning | aa-status parse | Auto: aa-enforce + restart probe with rollback. |
| `RECOVER-005` | Channel calibration stale (>30d) | Info | calibration store timestamp | Guided: schedule recalibration in next idle window. |
| `RECOVER-006` | Channel calibration aborted twice | Warning | hwdiag store envelope_C abort count | Guided: investigate cooler/airflow; offer "Force Envelope D" option. |
| `RECOVER-007` | Layer C never converged on signature | Info | confidence machinery flag from R12 | Guided: long-term observation; user can retire signature manually. |
| `RECOVER-008` | Stuck cold-start (>7d) | Warning | per-channel learning state from spec-16 | Guided: offer guided warm-up via stress-ng. Optional: auto-run if user accepts. |
| `RECOVER-009` | Hardware change detected since last boot | Info | DMI fingerprint comparison | Auto-recalibrate affected channels in next idle windows; user can defer. |
| `RECOVER-010` | Drift detected, recalibrating | Info | confidence < drift threshold from R12 | None (informational; recalibration happening automatically). |
| `RECOVER-011` | Phantom channel detected | Info | spec-v0_5_2 polarity disambiguation result | Guided: instructions for BIOS verification; offer `ventd calibrate --force-channel N`. |
| `RECOVER-012` | Mode/preset mismatch with workload | Info | preset state vs observed workload pattern | Guided: suggest preset change, link to preset explainer. |
| `RECOVER-013` | Repeated envelope aborts (≥3 in 24h same channel) | Warning | hwdiag store | Guided: investigate hardware; offer permanent Envelope D fallback. |
| `RECOVER-014` | NVML driver too old (<R515) | Warning | NVML version probe | Guided: update driver instructions per distro. |
| `RECOVER-015` | Sensor source flapping | Warning | R11 admissibility check fail | Auto: switch to fallback sensor per spec-sensor-preference. |

15 items at v0.5.10 ship. Catalog is extensible; `internal/doctor/recovery_catalog.go` enumerates types with detector function pointers.

### 3.3 Promotion algorithm: when does a problem become a recovery item

A problem detected by doctor is **promoted to a recovery item** only when all four of the following hold:

1. **Actionable** — there exists either an auto-fix path or a clearly articulated user-action. "ventd is confused" is not actionable; "Channel 3 has aborted calibration twice, here are next steps" is.
2. **Resolvable** — fixing it changes ventd's state in a measurable way. Metrics that fluctuate transiently (e.g., one-off Layer A activation during normal operation) are not items.
3. **Persistent** — the condition has been present for at least N detector runs (default N=3, ~3 minutes at default poll). Filters out flapping. Exception: blockers (severity=Blocker) trigger immediately on first detection.
4. **Not suppressed** — user hasn't acknowledged this item-instance via the Info/Acknowledge path within the suppression window (default 24h for Info, 7d for Warning, never for Blocker).

The promotion algorithm runs in a single goroutine, polled at 60s intervals (matches R9 detector cadence; cheap). Per detector function, it produces an `Item` struct or nil. The aggregator dedupes by `(item_id, affected_entity)` keys, merges with persisted suppression state, and emits the current item list to the doctor backend.

### 3.4 Demotion: when does an item disappear

- **Auto-fix succeeded** → item removed immediately, success entry added to internals fold "recent recoveries" log.
- **Detector function returns nil for K consecutive runs** (K=2): item removed, no-op log entry.
- **User acknowledges** (Info-severity only): item removed for the suppression window.
- **User explicitly resolved (e.g., applied guided fix in BIOS, then doctor re-detects success)** → item removed.
- **Suppression window expired**: item reappears (re-detected from scratch).

---

## 4. Surface 3 — Internals fold

### 4.1 Always present, collapsed by default

The internals fold is a single expandable section beneath recovery items (or directly beneath live metrics if no items exist). When expanded, it dumps full smart-mode state.

### 4.2 Sub-sections (web UI: collapsible groups; CLI: section headers)

Internals are organized into sub-sections, each independently expandable in web; in CLI they appear as headers in fixed order. Order:

1. **Per-channel state** — table with columns: channel, sensor, current PWM, current RPM, learning state (cold-start | warming | converged | drifting | aborted), Layer A confidence, Layer B confidence, Layer C confidence, identifiability classification (R9: healthy | marginal | co-varying-grouped | unidentifiable).
2. **Workload signature library** — count of total signatures known, top 10 signatures by frequency (display: short label per R7; not raw hashes per spec-12 amendment §8 open-question lean), per-signature confidence, last-observed timestamp.
3. **Recent calibration events** — last 20 entries, columns: timestamp, channel, calibration mode (Envelope C | Envelope D | re-cal), trigger reason, outcome (success | aborted-by-user | aborted-by-envelope | aborted-by-watchdog).
4. **Recent envelope aborts** — last 20 entries, columns: timestamp, channel, abort threshold breached, sensor reading at abort, recovery action.
5. **Saturation observations** — per channel, narrative form ("ramping above 75% has produced ΔT < 0.1°C on the last 12 sustained-CPU workloads"). Generated from R10 Layer C state.
6. **R9 identifiability state** — per channel: condition number κ, identifiable subspace dimension, co-varying fan groups detected, last-detection timestamp.
7. **R10 shard state** — per channel: Layer-B shard size and warmup status, count of active Layer-C overlay shards, total memory usage, recent eviction count.
8. **R12 confidence machinery state** — per channel: conf_A, conf_B, conf_C raw values; w_pred current value; LPF/Lipschitz state; drift detector m_k; cold-start hard-pin remaining ticks.
9. **R8 fallback tier state** — per channel: detected tach tier, fallback signal source(s), conf_A ceiling.
10. **Hardware catalog status** — DMI fingerprint, catalog match (yes/no, profile name if yes), profile-tier (catalog hit | catalog-less | refused-virt | refused-no-sensors).
11. **Daemon resource usage** — RSS, goroutine count, GC pause p99, file descriptor count, last 5 minutes' tick latency p50/p99.
12. **spec-15 experimental flags** — read from hwdiag.Store; never re-detected.

### 4.3 Internals depth principle

Internals shows **everything that's behind the abstractions visible elsewhere**. If a confidence indicator on the dashboard says "warming," the internals fold should show the raw conf_A/B/C numbers and the formulas. If a recovery item says "Channel 3 calibration aborted twice," internals should show the abort timestamps, sensor traces at abort, and threshold values.

The principle: **internals never withholds information; live metrics and recovery items withhold detail in service of clarity**.

### 4.4 Refresh cadence

- Web UI: 5 Hz pull from backend when section is expanded (full state dump). Collapsed sections do not pull.
- CLI: snapshot at invocation time (CLI always shows everything; no fold concept in terminal).
- Backend: in-memory snapshots of subsystem state. Includes spec-16 recent-events log read (last 50 entries per category). <50 ms compute budget when expanded.

---

## 5. Data source contract: hwdiag.Store single-sourcing

Inherited from shelved spec-10's RULE-DOCTOR-10. **Doctor never re-detects subsystem state.** Doctor reads hwdiag.Store and renders. Subsystems own their detection. Catalog of publishers:

| Publisher | Component | What it publishes |
|---|---|---|
| `internal/probe` | ComponentProbe | Catalog hit/miss, fingerprint match status |
| `internal/calibration` | ComponentCalibration | Last calibration timestamps per channel, abort counts |
| `internal/controller` | ComponentController | Per-channel learning state, current PWM/RPM, mode |
| `internal/confidence` (R12) | ComponentConfidence | conf_A/B/C, w_pred, drift detector state, cold-start state |
| `internal/coupling` (R9/R10) | ComponentCoupling | Identifiability classifications, shard memory usage |
| `internal/signatures` (R7) | ComponentSignatures | Library size, top-N signatures, per-sig stats |
| `internal/experimental` (spec-15) | ComponentExperimental | Experimental flags, OOT driver state |
| `internal/sensor` (R11) | ComponentSensor | Sensor admissibility, fallback selections, flap detection |
| `internal/fallback` (R8) | ComponentFallback | Fallback tier per channel, conf_A ceiling |
| `internal/probe/polarity` (v0.5.2) | ComponentPolarity | Phantom channel detections |

Doctor's `internal/doctor/checks/` directory is renamed to `internal/doctor/sources/` to reflect the new model. Each source file is a thin renderer over a single hwdiag.Store component.

**Two exceptions (read-only system state outside ventd):**
1. `sources/systemd.go` — queries systemd via dbus for daemon status and conflicting unit list. Not in hwdiag because it's the OS state, not ventd's.
2. `sources/permissions.go` — reads filesystem permissions for ventd state directories. Same reason.

Both are read-only; both have <500ms timeout per inherited RULE-DOCTOR-09.

---

## 6. CLI parity rendering

### 6.1 The parity contract

Per RULE-UI-SMART-06: `ventd doctor` CLI output mirrors web UI doctor content. Same surfaces, same data, ASCII-rendered.

**Rendering rules:**

- CLI never collapses anything. Internals fold is fully expanded in CLI output (no fold concept in terminal).
- CLI uses ANSI colors when stdout is a TTY; falls back to text indicators (`[OK]`, `[WARN]`, `[BLOCK]`) when piped.
- Tables rendered via fixed-width formatting with column truncation. Long sensor names truncated with ellipsis.
- Recovery-item action buttons render as numbered options at the bottom: `Run 'ventd doctor --fix RECOVER-XXX' to apply auto-fix.` for auto-fixable items.
- `ventd doctor --json` emits structured output validating against `docs/doctor-schema.json` (schema_version 1 from inherited RULE-DOCTOR-08).

### 6.2 Output shape

```
ventd doctor                                          [Tue 28 Apr 12:34:56]

LIVE METRICS
  [OK]    Daemon: Running, uptime 4d 12h
  [OK]    Smart-mode: Auto, balanced preset
  [OK]    Confidence: Predictive (0.78 aggregate)
  [WARN]  Channels: 6/8 controlled, 2 monitor-only
  [OK]    Calibration: All channels calibrated within 7d
  [OK]    Hardware: Profile matched (z690-aorus-elite-v1)
  [OK]    Envelope aborts (24h): 0
  [OK]    Hard-cap fires (24h): 0

RECOVERY ITEMS (1)
  [WARN]  RECOVER-007  Layer C never converged on signature
          Affected: signature 'compile-rust' on channel 1 (CPU)
          Detected via: confidence machinery, last 6 days
          Recommended action: long-term observation, or retire signature
          Run: ventd doctor --suppress RECOVER-007 to acknowledge

INTERNALS
  Per-channel state
    ch  sensor       PWM   RPM   state      conf_A  conf_B  conf_C  ident
    0   coretemp     45    1100  converged  0.95    0.82    0.71    healthy
    1   coretemp     30    900   warming    0.50    0.30    0.10    healthy
    ... (truncated)

  Workload signature library
    Total signatures: 47
    Top 10 by frequency:
      1. idle-baseline           412 obs  conf 0.92
      2. browser-active          187 obs  conf 0.85
      ... (truncated)

  ... (other internals sub-sections)
```

### 6.3 CLI-specific commands

Inherited from spec-10 fix-capable design, retained:

- `ventd doctor` — detect, render full output (live + recovery + internals).
- `ventd doctor --json` — structured output.
- `ventd doctor --fix RECOVER-XXX` — apply specific recovery item's auto-fix.
- `ventd doctor --fix-all` — apply all auto-fixable items, with loop-stop confirmation prompt for fixes affecting the running fan-control loop (per inherited RULE-DOCTOR-FIX-03).
- `ventd doctor --suppress RECOVER-XXX` — acknowledge an Info-severity item.
- `ventd doctor --internals` — render internals only (skip live metrics + recovery items header).
- `ventd doctor --watch` — refresh every second, TUI-like.

Removed from inherited spec-10 (replaced):
- `--dry-run` — folded into default behavior; doctor without `--fix` is dry-run by definition.
- `--unsafe-fix-loop` — replaced by interactive prompt under `--fix-all` per RULE-DOCTOR-FIX-03.

---

## 7. Web UI rendering

### 7.1 Page consolidation

Per RULE-UI-SMART-05: separate "Health" page MUST NOT exist post-v0.5.10. The "Doctor" page replaces "Health." Open question from spec-12 amendment §8.2 ("Health" vs "Doctor" page label) resolves to: **page is titled "Doctor" in nav; subtitle "Health and recovery for ventd" appears at top of page**. Compromise between user-friendly framing (Health) and CLI parity (Doctor).

### 7.2 Layout

Single scroll page, three sections, top-to-bottom:
1. **Live metrics** — fixed at top, 8 cards in a 2-column grid (mobile) / 4-column grid (desktop).
2. **Recovery items** — section header with count badge ("Recovery (3)"). Empty state: section omitted entirely on healthy systems. Each item is a card with title, severity chip, expand-for-detail, action buttons.
3. **Internals** — collapsed by default. Section header "Internals (advanced)" with expand toggle. When expanded, sub-sections listed in §4.2 order, each independently collapsible.

### 7.3 Action affordances

- Auto-fix buttons: primary CTA on items with auto-fix paths. Click → confirmation dialog showing what will happen → apply with progress indicator → result toast.
- Guided fix: button reveals inline instructions + optional links to docs.
- Acknowledge: secondary button, dismisses for suppression window.

### 7.4 Real-time updates

- Live metrics: 1 Hz pull (matches controller tick).
- Recovery items: 60s pull (matches detector cadence).
- Internals: 5 Hz pull only when section expanded; otherwise no pull.

---

## 8. Implementation file targets (post-pivot)

```
internal/doctor/
├── recovery_catalog.go           # 15 RECOVER-XXX type definitions
├── recovery_engine.go            # Detector poll loop, promotion algorithm
├── recovery_engine_test.go
├── suppression.go                # Acknowledgment state, persisted via spec-16
├── suppression_test.go
├── sources/                      # (renamed from checks/)
│   ├── controller.go             # ComponentController renderer
│   ├── confidence.go             # ComponentConfidence renderer (R12)
│   ├── coupling.go               # ComponentCoupling renderer (R9/R10)
│   ├── signatures.go             # ComponentSignatures renderer (R7)
│   ├── calibration.go            # ComponentCalibration renderer
│   ├── probe.go                  # ComponentProbe renderer
│   ├── experimental.go           # ComponentExperimental renderer (spec-15)
│   ├── sensor.go                 # ComponentSensor renderer (R11)
│   ├── fallback.go               # ComponentFallback renderer (R8)
│   ├── polarity.go               # ComponentPolarity renderer (v0.5.2)
│   ├── systemd.go                # External: systemd dbus
│   └── permissions.go            # External: filesystem state
├── fixes/                        # (carried over from shelved spec-10 PR2)
│   ├── conflicts.go              # systemctl stop+disable
│   ├── modules.go                # modprobe + dkms
│   ├── permissions.go            # chown/chmod
│   ├── apparmor.go               # aa-enforce + rollback
│   └── ... (other RECOVER-XXX auto-fixes as catalog grows)
├── render_text.go                # CLI text rendering
├── render_json.go                # JSON serialization (schema v1)
├── render_test.go                # Golden-file tests for both
└── runner.go                     # Top-level orchestrator
```

`bios_known_bad.go` is **deleted** (smart-mode pivot replaces with behavioural detection).

---

## 9. Invariant bindings — `.claude/rules/doctor.md`

Renumbered for v0.5.10. Inherits some shelved spec-10 rules verbatim (the structural ones), introduces new rules for recovery-surface model.

| Rule ID | Statement | Source |
|---|---|---|
| RULE-DOCTOR-01 | Doctor's detect path is read-only. No code under `internal/doctor/sources/` writes to `/sys`, `/dev`, `/var/lib/ventd`, or invokes privileged commands. | Inherited |
| RULE-DOCTOR-02 | Detect-mode exit codes are stable: 0=OK, 1=warnings, 2=blockers, 3=doctor errored. | Inherited |
| RULE-DOCTOR-03 | Every source produces structured output regardless of result. JSON output always lists every source that ran. | Inherited |
| RULE-DOCTOR-04 | Detect runs as unprivileged user without panicking. Permission-gated checks degrade gracefully. | Inherited |
| RULE-DOCTOR-05 | DMI fingerprint match prediction uses the same `hwdb.Fingerprint`/`hwdb.Match` paths the runtime uses. | Inherited |
| RULE-DOCTOR-06 | Conflict detection reuses spec-03 amendment `conflicts_with_userspace` resolver. No fork. | Inherited |
| RULE-DOCTOR-07 | Recovery-item catalog entries are validated at compile time: every entry has non-empty title, valid detector function pointer, severity in {Info, Warning, Blocker}, non-empty recommended action. | New (replaces RULE-DOCTOR-07-known-bad) |
| RULE-DOCTOR-08 | Doctor's JSON schema is versioned. spec-12 PR 4 pins schema_version 1. Schema bump is breaking change requiring spec amendment. | Inherited |
| RULE-DOCTOR-09 | Detect runs in <2 seconds wall-clock on dev container. Each source 500ms timeout. | Inherited |
| RULE-DOCTOR-10 | Doctor MUST NOT re-detect any subsystem state already published to `internal/hwdiag.Store`. Doctor reads the store and renders; subsystems own their detection. | Inherited |
| RULE-DOCTOR-11 | Recovery-item promotion requires actionable + resolvable + persistent (≥3 detector runs) + not-suppressed conditions. Blockers exempt from persistent requirement (immediate). | New |
| RULE-DOCTOR-12 | Live metrics surface contains exactly 8 indicators. Adding a 9th requires spec amendment. (Hardcoded count to enforce sparseness.) | New |
| RULE-DOCTOR-13 | Internals fold sub-section count is tracked; adding a sub-section requires updating CLI golden test. | New |
| RULE-DOCTOR-14 | CLI output renders every web-UI surface (live + recovery + internals expanded). `ventd doctor` CLI golden test asserts presence of every section header. | New (RULE-UI-SMART-06 binding) |
| RULE-DOCTOR-FIX-01..06 | (All inherited fix-mode rules from shelved spec-10 PR 2.) | Inherited |

Total: 14 detect rules + 6 fix rules = 20 rules. Matches inherited spec-10 shape.

---

## 10. Cost and validation

### 10.1 CC cost

v0.5.10 pre-spec budget: **$20-30** per spec-smart-mode §13. R13 design fits within that.

Breakdown:
- Sources renderers (10 files, ~50 LOC each, mostly mechanical data transform): **$5-8** Sonnet, possibly Haiku for half.
- Recovery engine + catalog (15 RECOVER types, promotion algorithm): **$8-12** Sonnet.
- Suppression with spec-16 integration: **$3-5** Sonnet.
- CLI render + JSON render + golden tests: **$3-5** mostly Haiku.
- Web frontend (consumes existing API; renders three sections per §7): folded into spec-12 PR 4 budget per spec-12 amendment, **$5-10** of that PR's allocation.

Total: $19-30 + spec-12 PR 4 contribution. Within budget.

### 10.2 Per-patch validation (spec-smart-mode §12 pattern)

1. **Synthetic CI tests:** golden-file test for CLI output across 12 fixture states (healthy, 3 recovery item severities, internals dump, JSON schema). Recovery-item promotion property test (filters work as specified). Source renderers tested against fake hwdiag.Store.
2. **Behavioural HIL:** MiniPC (192.168.7.222) for headless `ventd doctor` SSH parity check; Proxmox host for full web UI doctor + auto-fix flow against deliberately-broken state (e.g. mis-permissioned `/var/lib/ventd`).
3. **Time-bound metric:** `ventd doctor` returns in <2s on MiniPC (RULE-DOCTOR-09 binding).

---

## 11. Open questions (defer to spec-drafting)

1. **Recovery-item suppression persistence:** suppression state lives in spec-16 store. Key shape: `(item_id, affected_entity_hash, suppress_until_unix)`. Confirm during spec drafting.
2. **Internals sub-section ordering customization:** users may want to pin a specific sub-section open or reorder. Defer to post-v0.6.0 polish; v0.5.10 ships fixed order.
3. **Notification surface for new Blocker recovery items:** should ventd push a desktop notification (libnotify) or systemd journal-priority entry when a Blocker appears? Defer; v0.5.10 surfaces only via doctor page.
4. **Recovery item history retention:** how long do resolved items persist in "recent recoveries" in internals? Default 30 days, configurable via spec-16 retention policy. Confirm.

---

## 12. Conclusions actionable for spec-v0_5_10-doctor-recovery.md

When that patch spec is drafted (in chat, $0):

1. **Three-surface model is the structural backbone** — every section of the patch spec maps to one of live metrics / recovery items / internals.
2. **15 RECOVER-XXX types catalog is authoritative** — patch spec lists all 15 with their detectors and fix paths.
3. **20 rule bindings are the test surface** — RULE-DOCTOR-01..14 plus RULE-DOCTOR-FIX-01..06.
4. **Plan/apply pipeline + pkexec from shelved spec-10 carries forward unchanged** — that work is not redone; the recovery-surface model is layered on top of the existing fix infrastructure.
5. **`bios_known_bad.go` deletion is part of v0.5.10**, not a separate patch. Shelved spec-10 PR 1 had it; v0.5.10 removes it as part of recovery-catalog seeding.
6. **CLI parity is enforced by golden tests** (RULE-DOCTOR-14). Adding any UI surface without CLI rendering fails CI.
7. **No schema version bump** vs shelved spec-10 schema. JSON shape changes (e.g. `Issue` → `RecoveryItem`) but schema_version remains 1; spec-12 PR 4 web frontend consumes the same schema. Justification: spec-10's schema never shipped, so v0.5.10 is the de facto first shipping schema.
8. **Spec-16 dependency:** suppression state, recent-events log read for internals fold, hwdiag.Store snapshots for sources. All foundations laid by v0.5.0.1.

---

**R13 audit complete.** Bundle now at 15/15. Smart-mode research program is closed.
