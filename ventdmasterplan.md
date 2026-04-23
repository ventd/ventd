# VENTD MASTERPLAN

**Document purpose:** The engineering plan of record for ventd. Defines the
product vision, the phase structure, the task catalogue, and the rules of
engagement for building it. This document is normative — when reality
diverges from this document, fix one of them deliberately.

**Operating model:** Solo developer + Claude (design in chat, implementation
in Claude Code). One human, one codebase, one shipping cadence.

**Companion:** `ventdtestmasterplan.md` — every P-task here has a matching
T-task there. Code plan + test plan are separate files but one product.

**Last substantive revision:** 2026-04-22 (post-Cowork cleanup, pre-v0.4.0).

---

## 0 · Glossary

| Term | Meaning |
|---|---|
| **Developer** | The solo human owner. Designs, reviews, merges, ships. |
| **Claude (chat)** | Design work on the Max plan. Flat-rate. Used for architecture, spec drafting, code review, debugging strategy. |
| **Claude Code / CC** | Per-token implementation environment. Runs against a spec, produces a PR, commits at green-test boundaries. |
| **Spec** | A markdown file in `specs/` defining scope, PR sequence, DoD, and non-goals for one piece of work. Every CC session runs against a spec. |
| **Phase** | A coherent body of work (e.g. "Phase 2 — Backend portfolio") that produces a shippable minor release. |
| **Track** | A parallel work stream within a phase (e.g. IPMI, LIQUID, CROSEC). Tracks may progress independently when dependencies allow. |
| **Task** | A unit of work with a P-code (P2-IPMI-01, etc.). Becomes one or more specs. Becomes one or more PRs. |
| **Gate** | A condition that must hold before a task can start. Usually "dependency X merged." |

---

## 1 · North star

Ventd becomes *the* fan controller. Differentiators, in priority order:

1. **Works on every system** — Linux first (v1.0), then Windows / macOS /
   BSD as separate phases. Architecture: amd64, arm64, riscv64.
2. **Works on every fan** — hwmon, NVML, IPMI, Corsair/NZXT/Lian Li USB AIOs,
   laptop EC (Framework/ThinkPad/Dell/HP/ASUS/Lenovo Legion), ARM SBC PWM,
   Apple Silicon SMC, Asahi.
3. **Self-calibrating** — learns each fan's behaviour opportunistically during
   normal use; no 60-second fan-howl ritual.
4. **Learning controller** — per-machine thermal model, PI-then-MPC, genuinely
   quieter than any curve.
5. **Hot-plug aware** — notices new hardware in under a second, recalibrates,
   rebinds, all without operator input.
6. **Profile-sourced** — every first boot benefits from every prior boot;
   curated hardware-profiles database grows as users contribute.
7. **Acoustically intelligent** — detects bearing degradation via microphone
   fingerprinting; dithers synchronised fans to break beat frequencies.
8. **Still a single, auditable, deprivileged binary** — all of the above
   ships in ventd, or in a clearly-bounded sidecar with minimal capability
   grant.

**Non-negotiables:**

- No runtime dependency on Python/Node/Ruby/Java.
- No CGO in the main binary. Native code only via purego-loaded stable C-ABI
  or separately-packaged signed sidecars.
- Zero-capability systemd unit stays zero-capability; privileged actions move
  to scoped oneshots.
- Every exit path, graceful or otherwise, restores hardware to a firmware-safe
  state within 2 s.

**Scope boundaries (updated 2026-04-22):**

- Linux-first through v1.0. Windows is a separate product effort, scheduled
  post-v1.0 (see §16).
- No GUI (desktop or mobile). The web UI covers every use case.

### What ventd explicitly does NOT do

Consolidated from the masterplan, MARKETSTRATEGY.md §3, and FEATURE-IDEAS
rejections. These are permanent non-goals. If a feature request touches
one, the answer is no — not "maybe in v2."

- **RGB control.** OpenRGB owns this lane. Integration story only: ventd
  reads RGB device state for sensing (devices often expose temps/fans)
  and shares state via `/run/ventd/shared`. Ventd does not render RGB
  effects.
- **Disk SMART monitoring.** `smartd` is mature and ubiquitous.
- **CPU frequency scaling.** `tuned`, `cpupower`, and `auto-cpufreq`
  already do this well. Ventd publishes thermal headroom (P7-COORD-01)
  for those tools to read; it does not manage CPU clocks.
- **Benchmarking or stress-testing.** `stress-ng`, `s-tui`, `stress`
  exist. Ventd reads load from them; it does not reimplement them.
- **Desktop GUI (Qt, GTK, Electron, native).** The web UI covers every
  use case. A desktop GUI would add 200+ MB of deps and five linkage
  modes for no user benefit the web UI doesn't already deliver.
- **Native mobile apps (iOS, Android).** The web UI is mobile-responsive.
  Native apps are a maintenance burden for marginal gain.
- **Overclocking.** Dangerous, out of scope, politically fraught.
- **Logo before v1.0.** The GitHub default avatar and the dashboard
  screenshot are the project's visual identity until 1.0 ships. Time
  spent on branding before then is time not spent on code.
- **Conference talks before v1.0.** The project isn't stable enough to be
  the subject of a talk. FOSDEM et al. can wait.

---

## 2 · Repo & working conventions

- **Upstream:** `ventd/ventd` (public, GPL-3.0). `ventd/hardware-profiles`
  (public, CC-BY-4.0) holds the curated profile DB.
- **Default branch:** `main`.
- **Branch naming:** `<topic>` or `<issue-N>-<topic>`. No agent-namespaced
  branches.
- **PR policy:** Every non-trivial change goes through a PR. CI must be green
  before merge. Squash-merge by default; linear history on main.
- **Branch protection:** `main` requires CI green + linear history. Developer
  is the only merge actor.
- **Commits:** Conventional commits
  (`feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `chore:`). Present-tense
  imperative subject, ≤72 chars. Body wraps at 72.
- **No force-push to main. No amending merged commits.**
- **CHANGELOG discipline:** Every PR updates `## [Unreleased]` in
  `CHANGELOG.md` unless it's a no-op (docs typo, etc.).

### The README never promises what isn't shipped

This is a named principle because the failure mode of underdocumented
projects is the README drifting 6–18 months ahead of the code — which is
how vaporware reads.

- A feature does NOT appear in the README Features section until it ships
  in a tagged release.
- Features landed in `main` but not yet tagged may appear in a "What's
  coming" or roadmap section, labeled "currently in development" or
  "landed in main, next release."
- When a release tag is cut, a single pre-drafted commit migrates the
  shipped feature from "What's coming" to "Features" — so the tag and
  the README update land within 60 seconds of each other.
- The README at `HEAD` of `main` must match the most recent release tag,
  plus post-release bug fixes. It does not describe work in progress.

Applied ruthlessly. If in doubt about whether a feature is "shipped,"
check whether it's in a tagged binary users can download. If not, it's
not in the README.

---

## 3 · How work happens (the actual workflow)

This replaces the old §3–§6 Cowork orchestration loop. The new loop is:

1. **Design in chat** (claude.ai on Max plan). Produces a spec file under
   `specs/`. Specs are the authoritative brief for implementation work.
2. **Implement in CC**, one spec at a time. CC reads the spec, opens PRs,
   commits at green-test boundaries. No subagents, no mid-session Opus.
3. **Merge and ship.** When a spec's DoD is satisfied and CI is green,
   merge. When a phase's specs are all merged, cut a release tag (see
   `docs/release-process.md`).

That's it. The complexity that was in §3–§6 of the old masterplan served a
multi-agent team that no longer exists. Solo-dev + spec-driven CC is
sufficient.

**Spec structure** (see `specs/README.md` for the template):

```
specs/
  README.md                     index + ordering guidance
  spec-01-<slug>.md             one spec per shippable unit
  spec-02-<slug>.md
  ...
```

A spec is expected to cost $10–30 of CC tokens to execute. Specs that look
like they'll cost $100+ are too big — split them.

---

## 4 · Release cadence

- **Semver.** Major = compat break (none before v1.0). Minor = new shipped
  capability. Patch = fixes against the current minor.
- **Releases are not calendar-driven.** A release cuts when a Phase boundary
  closes, when a security-critical fix ships, or when `## [Unreleased]` has a
  coherent user-facing story.
- **See `docs/release-process.md`** for pre-release + post-release audit
  workflow (`pre-release-check.yml`, `drew-audit.yml`).
- **See `release-notes/`** for per-version release planning.

---

## 5 · Current state (2026-04-22)

| Aspect | Status |
|---|---|
| Current release | v0.3.x shipping imminently (PR #285 IPMI merged 2026-04-18) |
| Next release | v0.4.0 — first of the Phase 2 backends (Corsair USB AIO) |
| HAL landed | Yes (Phase 1 closed) |
| Hot-loop optimised | Yes |
| Fingerprint DB | 3 entries; needs expansion to 25+ in Phase 5 |
| Safety invariants bound | hwmon-safety.md (1 file, 12 invariants); 10 more rule files planned in test masterplan |
| Platform coverage | Linux amd64 + arm64, glibc + musl. Everything else is roadmap. |
| Cowork agent scaffolding | Removed 2026-04-22 (see cleanup PRs). |

---

## 6 · Dependency graph

```
                     Phase 0: Foundation                    [DONE]
                           │
                     ┌─────┴─────┐
                     │           │
               Phase 1 Core:   Phase 1 DB:                   [DONE]
               HAL + Hot-loop  Fingerprint DB (v1)
                     │           │
       ┌──────┬──────┴──────┬────┴────┬──────┐
       │      │             │         │      │
 Phase 2a  Phase 2b      Phase 2c  Phase 2d Phase 2e
 IPMI      LIQUID        CROSEC    PWMSYS   ASAHI
 [DONE]    [ACTIVE]      [next]    [next]   [next]
       │      │             │         │      │
       └──────┴──────┬──────┴─────────┴──────┘
                     │
               Phase 3: Install + Modprobe                   [partial — install.sh exists]
                     │
                     ├───────────────────┐
               Phase 4:            Phase 5:
               Controller (PI→MPC) Calibration + Profile capture
                     │                   │
                     └─────────┬─────────┘
                               │
                         Phase 6: Cross-platform             [SEPARATE PRODUCT — see §16]
                               │
                         Phase 7: Advanced sensing
                               │
                         Phase 8: Observability & fleet
                               │
                         Phase 9: UX + UEFI
                               │
                         Phase 10: Release supply chain
```

Phases 2a–2e run in parallel once HAL was landed (it is). Phase 10 work runs
opportunistically — some of it's already done.

---

## 7 · Task catalogue

Format: `TASK-ID | Track | Depends on | Files | Goal`

Only Steps/DoD lines that differ from the source `ventdmasterplan.md` prior
revision are repeated. Unchanged tasks keep their prior Steps/DoD verbatim —
point CC at the git history of this file for the full detail.

### Phase 0 — Foundation [COMPLETE]

**P0-01 — state file scaffolding.** Superseded by the post-Cowork cleanup.
No longer applicable.

**P0-02 — public roadmap** (`docs/roadmap.md` + README link). Shipped.

**P0-03 — hardware-report issue template** (`.github/ISSUE_TEMPLATE/**`).
Shipped.

### Phase 1 — Core refactor [COMPLETE]

All P1 tasks shipped in v0.3.x:

- P1-HAL-01 — FanBackend interface.
- P1-HAL-02 — calibration via FanBackend.
- P1-HOT-01 — per-tick allocation elimination.
- P1-HOT-02 — symmetric error handling on PWM writes.
- P1-FP-01 — fingerprint DB v1 (3 entries — expansion tracked as Phase 5
  profile-library work).
- P1-FP-02 — opt-in remote refresh from `ventd/hardware-profiles`.
- P1-MOD-01 — modules.alias parsing.
- P1-MOD-02 — append-not-overwrite persistModule.

### Phase 2 — Backend portfolio [IN PROGRESS]

**P2-IPMI-01 | IPMI | P1-HAL-01 | `internal/hal/ipmi/**`** — COMPLETE
(v0.3.x).

**P2-IPMI-02 | IPMI | P2-IPMI-01 | `internal/hal/ipmi/**`, `deploy/ventd.service`, `deploy/ventd-ipmi.service`** — COMPLETE (v0.3.x).

> Current spec: `specs/spec-01-ipmi-polish.md` — shipping polish for v0.3.x.

**P2-USB-BASE | USBBASE | P1-HAL-01 | `go.mod`, `internal/hal/usbbase/**`** — NEXT
Goal: shared USB HID primitives used by every USB-attached backend.
DoD: `internal/hal/usbbase` package compiles, has a `Device`+`Matcher`+`Watch`
interface, tests green against `fakehid` fixture.

**P2-LIQUID-01 | LIQUID | P2-USB-BASE | `internal/hal/liquid/**`, `deploy/90-ventd-liquid.rules`** — SPLIT from original.
Original P2-LIQUID-01 tried to ship Corsair + NZXT + Lian Li in one PR.
Split into per-vendor: **this task now covers Corsair only.**
NZXT and Lian Li are P2-LIQUID-01b and P2-LIQUID-01c.
Rationale: per-vendor PRs ship independently, three release posts, failure
isolation.
Goal: Corsair Commander Core / Core XT / Commander Pro via USB HID.
DoD: `ventd --list-fans` enumerates the pump + case fans with correct role
classification; `.claude/rules/liquid-safety.md` invariants bound.
> Spec: `specs/spec-02-corsair-aio.md`.

**P2-LIQUID-01b | LIQUID | P2-LIQUID-01 | same files**
Goal: NZXT Kraken X3/Z3.
DoD: as P2-LIQUID-01 but for NZXT VID/PID range.

**P2-LIQUID-01c | LIQUID | P2-LIQUID-01 | same files**
Goal: Lian Li UNI Hub + iCUE LINK System Hub.
DoD: daisy-chain addressing handled.

**P2-LIQUID-02 | LIQUID | P2-LIQUID-01c | `internal/hal/liquid/**`**
Goal: Aqua Computer Quadro/Octo, EK Loop Connect, Gigabyte AORUS RGB Fusion.
Reads first; writes optional and deferred.
DoD: each family in registry with discover/read; status surfaces in UI.

**P2-CROSEC-01 | CROSEC | P1-HAL-01 | `internal/hal/crosec/**`** — unchanged.
Framework/Chromebook EC via /dev/cros_ec. Ship this before ThinkPad — the
Framework community writes blog posts.

**P2-CROSEC-02 | CROSEC | P2-CROSEC-01 | `internal/hal/crosec/**`** — unchanged.
ThinkPad (thinkpad_acpi), Dell (dell-smm-hwmon), HP (hp-wmi).

**P2-PWMSYS-01 | PWMSYS | P1-HAL-01 | `internal/hal/pwmsys/**`** — unchanged.
ARM SBC PWM via /sys/class/pwm/pwmchipN/pwmN.

**P2-ASAHI-01 | ASAHI | P1-HAL-01 | `internal/hal/asahi/**`** — unchanged.
Apple Silicon Asahi backend.

### Phase 3 — Install & module subsystem [PARTIAL]

Existing `scripts/install.sh` works for common distros. Remaining work:

**P3-MODPROBE-01 | MODPROBE | P1-MOD-02 | `deploy/ventd-modprobe@.service` (new), `cmd/ventd-modprobe/main.go` (new), `deploy/ventd.service`** — unchanged.

**P3-UDEV-01 | UDEV | P1-FP-01 | `deploy/90-ventd-hwmon.rules`, `deploy/90-ventd-gpus.rules` (new)** — unchanged.

**P3-INSTALL-01 | INSTALL | P3-MODPROBE-01 | `scripts/install.sh`** — unchanged.

**P3-INSTALL-02 | INSTALL | P3-INSTALL-01 | `scripts/install.sh`**
Goal: detect fancontrol/thinkfan/CoolerControl; refuse or coexist.

**P3-IMPORT-01 | IMPORT | P3-INSTALL-02 | `cmd/ventd/import.go` (new)** — NEW, from FEATURE-IDEAS FP-07.
Goal: `ventd --import-from coolercontrol` reads existing CoolerControl
config-ui.json and emits equivalent ventd.yaml. One-way migration. Same for
`fancontrol` (bash variables) and `thinkfan` YAML.
DoD: three importers landed; round-trip test against committed fixtures.

**P3-RECOVER-01 | RECOVER | P1-HAL-01 | `cmd/ventd-recover/main.go`** — COMPLETE.

### Phase 4 — Control algorithm [NEXT MAJOR PHASE]

> Spec: `specs/spec-04-pi-autotune.md`.

**Phase 4 shipping order** (per `marketresearch.md §5` adoption priority
matrix, ranked leverage÷cost descending):

1. **P4-SLEEP-01** — resume-from-sleep. Leverage 5, ~170 LoC. Both
   competitors handle resume; ventd doesn't. Biggest visible improvement
   per line of code.
2. **P4-PI-01** — PI controller core. Foundational for everything else
   in this phase.
3. **P4-HYST-01** — banded hysteresis. Must land before P4-PI-02 —
   autotune chatters without it.
4. **P4-LATCH-01 + P4-STEP-01** — silence-detection safety latch paired
   with step-size thresholds. Land together or step thresholds are unsafe.
5. **P4-PI-02** — Ziegler-Nichols autotune.
6. **P4-HWCURVE-01** — hardware curve offload. Leverage 5, ~230 LoC.
   Ships once MiniPC (or other hardware) confirms the sysfs pattern is
   present. Biggest single control-loop architectural win.
7. **P4-INTERFERENCE-01** — third-party PWM drift detection. Now
   positioned as "strictly stronger than both competitors" on mainline
   hwmon (not just parity with CoolerControl).
8. **P4-DITHER-01** — per-curve dither to break beat frequencies.
9. **P4-MPC-01** — deferred. See §below.

**P4-PI-01 | CTRL | P1-HOT-01 | `internal/curve/pi.go` (new), `internal/controller/controller.go`, `internal/config/config.go`**
Goal: PI curve with anti-windup, NaN fallback, IntegralClamp.
See spec for full control law + invariants.
DoD: `.claude/rules/pi-stability.md` with 7 bound invariants; `PropPIStability`
passes 10k random plants.

**P4-HYST-01 | CTRL | P1-HOT-01 | `internal/controller/controller.go`**
Banded hysteresis (quiet/normal/boost). Must land before P4-PI-02 — autotune
chatters without it.

**P4-PI-02 | CTRL | P4-HYST-01 | `internal/calibrate/autotune.go` (new)**
Goal: Ziegler-Nichols relay autotune, Tyreus-Luyben gain option (decision
deferred to Opus consult — see spec).
Five safety sentinels, non-negotiable.
DoD: gains within 10% of analytic optimum on synthetic plant; all 5 sentinels
have abort-path tests.

**P4-DITHER-01 | CTRL | P1-HOT-01 | `internal/controller/controller.go`, `internal/config/config.go`**
Per-curve dither_pct 0–5 to break beat frequencies.

**P4-MPC-01 | CTRL | P4-PI-01 + 30 days production PI | `internal/curve/mpc.go` (new), `go.mod`** — DEFERRED.
MPC only ships after PI has 30 days of production soak time. Fallback to PI
on residual is what makes MPC safe; that fallback needs PI to be battle-
tested first. Spec to be written after P4-PI-02 merges.

**P4-SLEEP-01 | CTRL | P1-HAL-01 | `internal/controller/sleep.go` (new)** — NEW, from marketresearch §4.1 (CoolerControl comparison).
Goal: DBus sleep listener + resume state machine.
Rationale: CoolerControl has explicit PrepareForSleep/PrepareForShutdown;
fan2go has emergent behaviour; ventd has neither. Resume-after-sleep is a
common user complaint and a real gap.
DoD: resume triggers device reinit; configurable `startup_delay`; integration
test via fakedbus fixture.

**P4-INTERFERENCE-01 | CTRL | P1-HAL-01 | `internal/controller/interference.go` (new)** — NEW, market-research gap.
Goal: detect third-party PWM writes (BIOS, vendor tool) and recover.
Rationale: fan2go's mode-drift correction; CoolerControl's partial coverage.
Ventd currently has none. Mentioned in `marketresearch.md` (issue #533).
DoD: external pwm_enable change detected within 2 ticks; controller re-asserts
or yields per policy.

**P4-STEP-01 | CTRL | P1-HOT-01 | `internal/controller/controller.go`** — NEW, market-research gap.
Goal: step-size thresholds + safety latch (CoolerControl parity).
DoD: PWM change per tick capped; latch prevents oscillation in degenerate
curves. Must land in the same PR as P4-LATCH-01 (silence detector) —
step thresholds without a safety latch can suppress all writes.

**P4-LATCH-01 | CTRL | P1-HOT-01 | `internal/controller/controller.go`** — NEW, market-research gap.
Goal: silence-detection safety latch. Forces a write every `response_delay`
seconds regardless of thresholds.
Rationale: prerequisite for P4-STEP-01. Without it, a user configuring
step thresholds can accidentally suppress all writes under certain
temperature profiles.
DoD: latch triggers after N ticks without a write; bypass on first trigger
is respected; max thresholds still honoured.

**P4-HWCURVE-01 | CTRL | P1-HAL-01 | `internal/hal/hwmon/backend.go`, `internal/controller/controller.go`** — NEW, highest-leverage item per marketresearch.md §5.
Goal: hardware curve offload for NCT6775/NCT6798/IT87 Super I/O chips.
Detect via sysfs file existence probing (`pwm<N>_auto_point<M>_pwm` +
`pwm<N>_auto_point<M>_temp`). When supported, upload curve points directly
to chip sysfs and set `pwm_enable` to AUTO — the chip runs the curve
autonomously in firmware.
Rationale: single biggest control-loop architectural win available from
competitor analysis. Covers ~80-90% of DIY desktop motherboards. Daemon
CPU drops to near-zero for offloaded channels; daemon crashes and
suspend/resume transitions become transparent. No vendor-specific protocol
code needed — all kernel hwmon ABI.
Steps: (1) add `supports_hardware_curve` to `Caps`. (2) probe on enumerate.
(3) dispatch branch in controller tick — skip user-space interpolation for
offloaded channels. (4) upload function: write each `(temp, pwm)` pair to
`pwm<P>_auto_point<N>_{temp,pwm}`, associate temp→PWM via
`pwm<P>_auto_channels_temp`, set `pwm_enable` to chip-specific AUTO value
(NCT6775 uses `SMART_FAN_IV_VALUE`, others use 2). (5) normalisation +
range clamp per `CURVE_RANGE_TEMP` / `CURVE_RANGE_PWM`.
DoD: ≥ 2 chip families detected on developer-available hardware; curve
points round-trip through sysfs cleanly; daemon CPU measurably lower on
offloaded channels; controller correctly dispatches to tick-driven path
when chip lacks capability.
**Blocked on:** verifying the MiniPC's Super I/O exposes
`pwm<N>_auto_point_*` sysfs files. If not, this becomes a community-
validation task — ventd could ship the code and let the profile DB
record which boards have it.

### Phase 5 — Calibration & profile capture

> Spec: `specs/spec-03-profile-library.md`.

This phase is the highest-leverage phase in the plan — the profile flywheel
is what makes ventd's "zero-config on any hardware" vision real. See the spec
for the schema-freeze opus consult and the 25-entry seed target.

**P5-PROF-SCHEMA-01 | PROF | P1-FP-01 | `internal/hwdb/schema.go` (new)**
Goal: freeze v1 schema for `profiles.yaml`. Opus consult before implementation.
DoD: 7 bound invariants in `.claude/rules/hwdb-schema.md`; schema documented
in `docs/hwdb-schema.md`.

**P5-PROF-MATCH-01 | PROF | P5-PROF-SCHEMA-01 | `internal/hwdb/match.go`**
Goal: deterministic three-tier matcher (exact → family → heuristic → nil).
DoD: benchmark ≤ 1ms on 500-entry DB; every DMI string from past issue
reports becomes a regression test row.

**P5-PROF-SEED-01 | PROF | P5-PROF-MATCH-01 | `internal/hwdb/profiles.yaml`**
Goal: 25 seed entries. Every entry cites a source.
DoD: pre-commit PII grep returns zero matches; `verified:` flag honest.

**P5-PROF-CAPTURE-01 | PROF | P5-PROF-SCHEMA-01 | `internal/hwdb/capture.go` (new)**
Goal: local anonymised profile capture. No network, no PR.
DoD: `FuzzAnonymise` 10 minutes, zero PII leakage; `/var/lib/ventd/profiles-pending/`
file mode 0640.

**P5-PROF-CAPTURE-02 | PROF | P5-PROF-CAPTURE-01 | extends above**
Goal: opt-in submission flow — `ventd --submit-profile <id>` pushes via the
`ventd/hardware-profiles` PR path. Default off. User reviews diff before push.
DoD: dry-run mode; explicit user confirmation.

**P5-LIVECAL-01 | LIVECAL | P4-PI-01 | `internal/calibrate/live.go` (new)** — unchanged.

**P5-HEALTH-01 | HEALTH | P5-LIVECAL-01 | `internal/monitor/health.go` (new)** — unchanged.

**P5-TRUST-01 | TRUST | P5-PROF-SCHEMA-01 | extends schema** — NEW, from FEATURE-IDEAS FP-05.
Goal: per-sensor trust levels in the schema.
Rationale: some sensors are wrong (stuck-at, wrong offset). UI surfaces
untrusted; curves refuse to consume them unless override.
DoD: `sensor_quirks` section in schema; UI shows trust badges; regression
test for 3 known-bad-sensor board entries.

### Phase 6 — Cross-platform [DEFERRED — see §16]

Windows, macOS, BSD, illumos, Android are all post-v1.0 and architecturally
separate from the Linux product. All P6-* tasks moved to the Windows
subproject (separate repo, separate masterplan when it spins up).

**The one exception:** cross-compile smoke tests for Linux CI remain here to
catch accidental POSIX-specific code creeping in. Those live in CI matrix and
don't need P6 task entries.

### Phase 7 — Advanced sensing

**P7-ACOUSTIC-01 | ACOUSTIC | P5-HEALTH-01 | `internal/acoustic/**` (new), `go.mod`** — unchanged.

**P7-ACOUSTIC-02 | ACOUSTIC | P7-ACOUSTIC-01 | `internal/acoustic/**`** — unchanged.

**P7-FLOW-01 | FLOW | P2-LIQUID-01 | `internal/hal/liquid/**`** — unchanged.

**P7-COORD-01 | COORD | P4-MPC-01 | `internal/thermal/coord.go` (new)** — unchanged.

**P7-APP-PROFILES-01 | APPPROF | P8-METRICS-01 | `internal/apphook/**` (new)** — NEW, from FEATURE-IDEAS FP-01.
Goal: workload-driven profile switching (Blender/compile/game launches
triggers "heavy" profile; exit → "quiet").
Via eBPF sched_process_exec (Linux ≥ 5.8) or /proc polling fallback.
DoD: rule DSL `{ app: "blender*", profile: "heavy" }`; false-positive guard
(sustained load required, not just process launch).

### Phase 8 — Observability & fleet

**P8-METRICS-01** — Prometheus /metrics (opt-in). Unchanged.
**P8-OTEL-01** — OTel traces. Unchanged.
**P8-HISTORY-01** — 30-min in-process ring + /api/history. Unchanged.
**P8-CLI-01** — `ventdctl` CLI over unix socket. Unchanged.
**P8-FLEET-01** — mDNS + memberlist gossip. Unchanged.
**P8-FLEET-02** — fleet view in UI. Unchanged.

**P8-ALERTS-01 | ALERTS | P8-METRICS-01 | `internal/alerts/**` (new)** — NEW, from FEATURE-IDEAS FP-04.
Goal: user-configurable thermal/fan alerts (CPU >90°C for 20s, fans maxed
10+ min, RPM drop = bearing precursor). Delivery: desktop notification,
webhook, email.
DoD: built-in defaults (conservative); custom rules in config.

**P8-ROLLBACK-01 | ROLLBACK | P0 | `internal/config/history.go` (new)** — NEW, from FEATURE-IDEAS FP-06.
Goal: config snapshot on every Apply; one-click "Rollback to X".
DoD: last 30 applies retained; UI shows timestamp + change summary.

### Phase 9 — UX & pre-OS

**P9-WIZARD-01** — zero-click wizard. Unchanged.
**P9-I18N-01** — locale infra + 6 languages. Unchanged.
**P9-EXPR-01** — expression curve with sandboxed eval. Unchanged.
**P9-UEFI-01** — signed UEFI DXE stub. DEFERRED post-v1.0; hardware bricking
risk too high to take on pre-1.0.

### Phase 10 — Release supply chain [OPPORTUNISTIC]

**P10-SBOM-01** — CycloneDX + SPDX. Unchanged.
**P10-SIGN-01** — cosign keyless + SLSA L3. Unchanged.
**P10-REPRO-01** — reproducible builds. Unchanged.
**P10-PERMPOL-01** — Permissions-Policy + ETag. Unchanged.

These can land anytime. The `drew-audit.yml` workflow already stubs them as
`[SKIP]` gates — each one activates as its task merges.

---

## 8 · What blocks a merge

Every PR must satisfy, non-negotiable:

- `go vet ./...` clean.
- `go test -race ./...` green.
- `golangci-lint run` clean.
- `gofmt -d .` returns nothing.
- Safety invariants preserved — watchdog.Restore paths, PWM clamping,
  pwm_enable save/restore unchanged unless the PR explicitly targets them.
- `.claude/rules/*.md` files stay bound to their subtests (enforced by
  meta-lint CI).
- CHANGELOG updated under `## [Unreleased]`.
- Single-static-binary preserved (no CGO, NVML stays dlopen).
- New direct dependencies justified in the PR description. Default answer: no.

---

## 9 · Decision heuristics

When the plan is ambiguous about what to do next:

1. **Prefer shippable over complete.** A v0.4.x with Corsair-only AIO beats
   a v0.5.0 with Corsair+NZXT+Lian Li that slips 6 weeks.
2. **Prefer unblocking over polishing.** If Phase 2 backends are blocked on
   a schema question, answer the schema question even if it feels like Phase
   5 work.
3. **Prefer cheap wins early in the CC budget month.** Mechanical work
   (Haiku), refactors (Sonnet) first; hard design (Opus chat) when stuck.
4. **Prefer user-visible over internal.** A new backend that enumerates is
   worth more than a 10% hot-loop speedup, even if the latter is "more
   interesting."
5. **Prefer observability before optimisation.** Can't optimise what you
   can't see. P8-METRICS-01 has a surprisingly high ROI if you're about to
   spend weeks on MPC tuning.

---

## 10 · Escalation (things that need human decision-making)

These require the developer to stop and think, not hand off to a CC session:

- Schema changes to `profiles.yaml`, `calibration.json`, or the web API
  response shapes after the schema has been frozen in a released version.
- New runtime dependencies (cgo, network deps, Python runtimes).
- Security-sensitive paths outside the PR's scope (watchdog, recover,
  sdnotify, modprobe allowlist).
- LICENSE, SECURITY.md, CODEOWNERS, deploy sandbox files.
- Any change where the PR would be the first of a new phase — phase
  transitions benefit from a human sanity check.
- Hardware-bricking risk (UEFI, firmware-level writes, unsigned drivers).

When one of these comes up: stop the current CC session, open a chat, design
in Claude, update the masterplan or specs, then resume.

---

## 11 · Solo-dev hygiene

1. **Weekly `git log main` review.** Subject lines only. Anything odd → stop
   and investigate.
2. **Weekly cost review.** Check Claude spend against the budget. Adjust
   model tier assignments in next week's specs if over.
3. **Monthly phase checkpoint.** End of each phase = behaviour-level review
   on real hardware before tagging. Nothing substitutes for running the
   binary on the desktop.
4. **`HALT` file pattern** (optional). Write `HALT` to `.ventd-halt` in home
   dir; CC can be scripted to check for this and refuse to start new work.
   Only useful if you routinely trigger CC via automation.

---

## 12 · Budget discipline

The previous team of agents cost $600/weekend. That's not sustainable. Rules
for keeping the cost curve flat:

- **Design in claude.ai (flat-rate Max).** Implementation in CC (per-token).
  Never the reverse.
- **One spec per CC session.** Fresh context at every spec boundary.
- **No Opus inside CC.** Ever. Opus consults happen in chat; output becomes
  a spec file; CC runs Sonnet against the spec.
- **No subagents inside CC.** Parallelism is a cost multiplier, not a speed
  multiplier, for solo-dev pace.
- **Cap session duration at 30 minutes of real work.** Long sessions explore
  rabbit holes.
- **Use Haiku for mechanical work** — test fixture corpus, YAML blob
  generation, commit messages, doc formatting. 1/15 the cost of Sonnet.
- **Commit at green-test boundaries.** Frequent commits = free undo. Without
  them, "undo the last three changes" is genuinely expensive.
- **Reference files with `@file`, not prose.** Point at the file; don't
  describe it.
- **Delete stale conversations.** Old threads cost nothing sitting there, but
  reopening one inherits context.

Target: $10–30 per spec execution. $300/month total CC spend across the
whole project. If a month runs over, a week of chat-only work pays it down.

---

## 13 · Public roadmap (source for README "What's coming")

> **ventd 1.0 arc.** Linux-first: the single fan-control daemon that works
> on every Linux fan — hwmon, GPU, IPMI, USB AIOs, laptop EC, ARM SBC PWM,
> Apple Silicon Asahi. Self-calibrating, profile-sourced, learning-controlled.
> Windows, macOS, and BSD as separate products after 1.0. One static binary,
> zero capabilities, 2-second guaranteed safe exit.

Keep this paragraph in sync with `docs/roadmap.md` (longer form).

---

## 14 · Review checklist (applies to every PR)

Simplified from the old 17-row Cowork checklist. The rows that survived:

| Row | Check |
|---|---|
| R1 | Branch name matches convention |
| R2 | Commits follow conventional-commits format |
| R3 | CI green OR failures explicitly explained |
| R4 | Every DoD criterion visibly satisfied in the diff |
| R5 | No new direct dependencies OR explicitly justified |
| R6 | Safety invariants preserved |
| R7 | No secrets, credentials, or real hardware fingerprints committed |
| R8 | Public API unchanged unless the PR targets it |
| R9 | CHANGELOG updated under `## [Unreleased]` |
| R10 | Every `.claude/rules/*.md` touched has matching subtest edits |
| R11 | Every closed issue in `Fixes:` has a `TestRegression_Issue<N>_*` |
| R12 | New safety-critical function bound to a rule invariant |
| R13 | New goroutine has documented lifecycle (ctx or stop channel) |
| R14 | New backend passes `TestHAL_Contract` |
| R15 | New public metric/route/config field reflected in docs golden test |

R10–R15 are inherited from the test masterplan.

---

## 15 · What this document does NOT do

- **Schedule specific calendar dates.** Releases are triggered, not scheduled.
- **Dictate which agent runs which task.** There are no agents. There's one
  developer using Claude via chat and CC.
- **Define the test plan.** See `ventdtestmasterplan.md`.
- **Substitute for specs.** P-codes here are scaffolds; specs under `specs/`
  are normative for implementation.
- **Cover the Windows product.** That's a separate masterplan when it
  spins up.

---

## 16 · Windows subproject (separate)

Decision made 2026-04-22: Windows is a separate product, post-v1.0 Linux.
Rationale:
- ventd's "works on any hardware, zero-config" vision is Linux-native
  architecturally. hwmon gives it; Windows fights it.
- Linux homelab/NAS audience (TrueNAS, Unraid, Proxmox, OMV) is the ICP.
- The `unprivileged-user + udev DAC grant + 2-second safe exit` story is a
  selling point that does not translate to Windows.
- Windows needs either a signed kernel driver (months + $$$) or vendor-
  specific ACPI methods (~30% board coverage). Either path is a new product.

When Windows starts, it gets its own repo, its own masterplan, and shares
~55% of code with this one (curve engine, controller, web UI, config,
calibration math). The HAL and safety model are new.

Do not add P6-WIN-* tasks here.

---

## 17 · Initial developer actions (post-cleanup, fresh start)

As of 2026-04-22, these are the immediate moves:

1. Complete the repo cleanup (three PRs: `cleanup/remove-cowork`,
   `cleanup/rewrite-claude-docs`, `cleanup/archive-stale-validation`).
2. Delete the remote `cowork/state` branch:
   `git push origin --delete cowork/state`.
3. Execute `specs/spec-01-ipmi-polish.md` to close v0.3.x polish. Ship the
   v0.3.x tag.
4. Plan v0.4.0: Corsair AIO via `specs/spec-02-corsair-aio.md`. Opus consult
   on protocol framing in chat; hand the output to CC.
5. In parallel (if budget allows): Opus consult on hwdb schema freeze
   (spec-03) so Phase 5 can start without blocking on Phase 2 completion.
6. Read `CHANGELOG.md` weekly. Ship a release when `## [Unreleased]` has a
   coherent story.

---

**End of document.** This plan is maintained by the developer. Revisions
happen in PRs, not in commits to a `cowork/state` branch. When any part of
this document disagrees with reality, fix one of them deliberately — don't
let drift accumulate.
