# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
## [Unreleased]

## [v0.5.17] - 2026-05-04

### Headline
- **Real Layer-C predicted ΔT forecast on the dashboard hero cards** (#945, closes #43, P0) — the v0.5.15 dashboard removed the fake 12-sample client-side linear-regression forecast and left a forecast-shaped hole. v0.5.17 fills it with the real model output: the daemon's existing `marginal.Snapshot.MarginalSlope` (`β_0 + β_1·load`, the Path-A formula from RULE-CMB-SAT-01) is plumbed through `/api/v1/smart/channels` and the dashboard renders it as an arrow-and-magnitude beneath each hero spark (`↓ 0.042 °C/PWM · last 60 s`). Below the saturation floor (2°C across the full 0-255 ramp) the badge reads `· saturated · last 60 s`; with no usable shard yet (warming up, no samples) it reads only `last 60 s` — never a fabricated number, no theatre.
- **Patch-notes-on-first-login modal** (#942, closes #48) — after the in-UI Update button (#934) rolls the daemon to a new tag, the operator's first post-update page load surfaces a dismissible modal containing the CHANGELOG section(s) for everything between their last-seen version and the current daemon version. Backend parses `/usr/share/doc/ventd/CHANGELOG.md`, splits on `## [vX.Y.Z]` headings, returns sections newer than the `since` query param. Cached after first parse; invalidated only by daemon restart, which matches the install + restart cycle exactly. Frontend renders safe markdown-to-DOM (textContent only — no innerHTML, RULE-UI-01).
- **First-visit walkthrough banners on Dashboard, Hardware, and Smart** (#947, closes #36) — small dismissible card injected at the top of each of the three pages a first-time operator is most likely to land on. 2-3 plain-text paragraphs explain what they're looking at, why the signals are shaped the way they are, and where to look next. Persists `ventd-walkthrough-<page>` in localStorage so subsequent loads skip. Content stays honest — it describes what's actually on the screen and traces each signal back to its real backend source.

### Fixed
- **`is-just-changed` decision flash freshness gate** (#946, closes #35) — the yellow halo on a fan tile is supposed to fire in lock-step with a new entry being unshifted onto the decision feed. The flash flag was a bare bool, so any stale flag that survived a render cycle without being consumed could re-trigger the keyframe later when the tile next re-rendered. The corresponding decision entry by then may have been pushed below the visible 8-row window — operator sees a phantom flash with nothing to explain it. Stamp the flash with an epoch-ms timestamp and only consume it when ≤ 3 s old; stale flashes drop silently.
- **/calibration page CSP regression** (#944, closes #42) — the system-card body had `style="padding-top: 6px;"` inline, which violates the daemon's strict CSP (`style-src 'self'`, no `unsafe-inline`). The browser silently dropped the override and emitted a console warning since the CSP shipped. Move the override to a `.tight-top` modifier class on `.v2-card-body`.
- **Pre-existing bug in `aliveModeWorkloadLabel`** (in #945) — was walking `aliveState.channels.channels` (object-style) when `/api/v1/smart/channels` actually returns a bare JSON array. The workload-mode label has been silently empty since #39 landed; now correctly shows the modal signature label across channels.

### CI / chore
- **gofmt drift cleanup** (#943) — `golangci-lint`'s gofmt step caught two drifts that local pre-push missed (`golangci-lint` isn't in the local sweep): per-key whitespace alignment in `monitor.ecMirrorChips` (added in #939) and doc-comment bullet chars + struct-tag column alignment in `release_notes.go` (added in #942). Pure cosmetic; no behaviour change.
- **Dead-code removal** (#948) — drop unused `changelogCachePath` var and `resetChangelogCacheForTest` func from `release_notes.go`; both were introduced in #942 but never read by any caller.

### Honest framing
v0.5.17 closes the largest remaining P0 from the v0.5.14 dashboard bug-hunt (the missing real forecast — Phoenix's frustrated "WHY AREN'T WE USING THE MODEL we spent hours researching" feedback) plus three operator-facing UX completions: the first-visit walkthrough that explains each new page, the patch-notes modal that tells operators what just changed after they click Update, and the freshness gate that stops phantom flashes on the dashboard. With v0.5.17 every visible signal on the dashboard, hardware, and smart pages traces to a real backend signal — the no-theatre rule is now uniformly enforced.

## [v0.5.16] - 2026-05-04

### Headline
- **NCT6687 desktop super-I/O dedup fix** (#939, closes #40) — the v0.5.14+ `dedupMirrorFans` pass collapsed real distinct fans on Phoenix's MSI Z690-A NCT6687 chip whose idle RPMs coincidentally fell within ±10 RPM of each other. Outcome: 1 fan visible despite the board having 7 PWM headers. Tag the chips that genuinely mirror tachs (thinkpad_acpi family + applesmc + macsmc-hwmon + surface_fan + dell-smm-hwmon + asus-ec-sensors / asus-wmi-sensors + hp-wmi-sensors) and only apply dedup to those. Desktop super-I/O (nct6687, nct6798, it8688, etc.) get all their fan_input readings preserved.
- **Hero spark Y-axis: slow-EMA auto-fit** (#940) — the v0.5.15 fixed-pin to 20-100 °C made small idle deltas (2-3 °C variance, normal at idle) render ~1 px tall on the 48 px card — visually flat. Switch to a slow-EMA-smoothed auto-fit (alpha = 0.05; ~30 s lag at 1.5 s tick) so the axis range moves continuously but slowly. Single-poll outliers don't visibly rescale; sustained workload changes shift the bounds within ~30 s. Padded ±2 °C from the smoothed bounds; range floored at 4 °C to avoid divide-by-zero on a perfectly stable temp. The line now shows real variance at useful resolution while preserving the per-poll layout stability that was the original rationale for pinning.

### Honest framing
v0.5.16 is a tight follow-up to v0.5.15's no-theatre sweep — two HIL-driven fixes landed in the post-tag verification on Phoenix's MSI Z690-A desktop. The in-UI Update button (v0.5.15 #934) makes this kind of rapid follow-up zero-friction: tag → click Apply on the settings page → daemon rolls forward without restart resetting calibration progress.

## [v0.5.15] - 2026-05-04

### Headline
- **No-theatre web UI sweep + in-UI update button.** A single-day audit + bug-squash session driven by Phoenix's UX feedback that the v0.5.14 dashboard was "constantly flicking" and "doesnt seem like its actually real". Outcome: every cosmetic animation that didn't trace to a real backend signal removed, the dashboard hero spark stabilised + smoothed, the renderer fight that produced the flat→jagged→flat alternation killed at its source, and a settings-page Update affordance so operators can roll forward without losing calibration progress.
- **In-UI update button** (#934) — new \`/api/v1/update/check\` polls GitHub releases-latest; new \`/api/v1/update/apply\` spawns \`scripts/install.sh\` detached with \`VENTD_VERSION\` set, daemon dies during the install's \`systemctl restart\` and comes back under the new binary. \`/var/lib/ventd\` state (calibration JSON, smart shards under \`smart/shard-{B,C}/\`, \`.signature_salt\`, \`state.yaml\`) persists across the restart; RULE-ENVELOPE-09 confirms in-flight calibration sweeps resume from the last completed step. Settings page Update section shows installed + latest, Check button + Apply button, polls \`/healthz\` for re-up and reloads the page.

### No-theatre cleanup
The v0.5.14 dashboard / smart / hardware pages shipped a number of cosmetic animations that animated continuously regardless of any real backend event. Phoenix's rule (saved as auto-memory feedback): *every visible signal must trace to real backend research, not cheap client-side fakes*. All of the below removed in this release:
- **Smart Bridge "continuous loop" rotating spotlight** (#932) — was rotating every 600 ms through 6 sub-steps without being tied to any actual sub-step tick events.
- **Smart Scope tach-wobble** (#932) — was a synthetic Lissajous wobble keyed off \`opp.tick_count\`, not real RPM data. Replaced with the real PWM hold line + a caption that the daemon doesn't surface probe-time tach samples yet.
- **Hardware Topology view animateMotion packets** (#932) — daemon→chip and chip→sensor edges had decorative packet animations with no real bus events behind them.
- **Hardware Topology daemon-glow pulse** (#932) — fixed-cycle pulse unrelated to real daemon activity.
- **Hardware Inventory rail CouplingMini packet pulses** (#932).
- **Dashboard coupling-map active-edge pulses** (#932) — the active-class colour change already communicates "fan is running"; the moving packet implied a per-event data-flow signal we don't actually have.

### Dashboard hero card stabilisation
The v0.5.14 dashboard hero CPU/GPU cards were the most visually broken element — Phoenix described them as flicking constantly and showing fabricated forecasts (e.g. \`+138.6 °C / 30 s\`). Sequence of fixes:
- **Decision detector** (#925, #926) — the v0.5.14 dashboard inferred "decisions" from any 2 pp duty change between consecutive \`/api/v1/status\` polls, but the controller's natural curve micro-recompute jitter swung PWM ~2-3 pp every poll. 30-s sample on Phoenix's MSI Z690-A: daemon journal had ZERO controller log lines while the dashboard would have emitted 30 fake decisions. Replaced with a windowed-delta detector (3-poll window, 10 pp threshold, 6 s per-fan rate limit). Real ramps now fire one event each; sensor-noise micro-recomputes are silenced.
- **Hero forecast badge** (#928, #930) — the v0.5.14 forecast was a 12-sample client-side linear regression on raw sensor history. Removed entirely. The daemon-backed predicted ΔT from Layer-C marginal RLS lands via #43 (P0 follow-up).
- **Hero spark Y-axis pinned** (#930) to 20–100 °C so the line evolves left-to-right with new samples instead of rescaling per poll.
- **EMA-smoothed hero spark line** (#933) — alpha = 0.4 — to suppress per-poll sensor jitter that rendered as visual chaos within a single frame. Big number above the spark is still the raw current reading.
- **Killed dual-renderer fight** (commit 67ae370) — the OLD \`/api/v1/status\` 1 Hz writer and the NEW alive-overlay 1.5 s writer were both setting the same \`hero-cpu-path\` SVG d-attribute with different data. Disabled the old writer; alive overlay owns the hero spark now. This was the source of Phoenix's "every poll the line resets to flat then back to jagged".
- **Pinned narrator strip** (#929) — was rotating through past decisions every 6 s, giving the impression of constant new activity. Now pins to the most-recent decision until a newer one arrives, transitions to "system idle — no decisions in N s" after 12 s of silence.
- **Smart-mode "Last probe" formatter** (#929) — was rendering Go's zero-time as "17753741h ago"; now shows "—" / never (closes #921).
- **\`/api/v1/profile/active\` GET branch** (#929) — was POST-only; dashboard.js polled it as GET and got 405 silently. Added the GET branch returning \`{"name": "<active>"}\`.

### Backend correctness
- **NVML enumeration fix** (#927) — the v0.5.14 \`/api/v1/hardware/inventory\` returned 7 sensors all with \`id="0"\` and \`kind="temp"\` for the GPU chip regardless of unit. Two readings (210 MHz, 405 MHz GPU clocks) were classified as temperatures, which is why the Hardware page "Hottest" cell read "405°" (#920, #922). Fixed by composing per-NVML sensor IDs as \`gpu<idx>:<metric>\` and classifying \`kind\` from the Metric field rather than the unit string. NVML metrics that don't fit the four-kind enum (util / clocks) drop out of inventory rather than being mislabeled.

### Honest framing
v0.5.15 closes out the visible-flicker / fake-forecast / dual-renderer-fight class of bugs in the v0.5.14 design handoff. The DAEMON's actual smart-mode predictions (Layer-C marginal RLS) still aren't surfaced anywhere on the UI — that's the P0 follow-up (#43) and requires a new daemon endpoint exposing per-sensor predicted ΔT. Today's UI honestly says "—" / "no recent decisions" / "warming" when the model isn't yet contributing, instead of fabricating numbers. The in-UI update button (#934) makes it possible for the smart-mode stack to accumulate days of real samples without losing them to snapshot rebuilds.

## [v0.5.14] - 2026-05-04

### Headline
- **Hardware page** (#912) — second of the cal.ai design-handoff trio. Devices and Sensors collapse into a single `/hardware` page with three views: Inventory (chip → sensor tree, sparklines from a 60-sample per-channel history ring, side rail showing which curves consume the selected sensor), Topology (daemon ← chip ← sensor wiring with animated packet flow), and Heatmap (case-shaped layout with sensors at their operator-supplied (x, y) positions; renders a clean empty state directing the operator to declare positions in YAML when none are set).
- **Smart-mode page** (#913) — new `/smart` surface for the v0.5.5+ smart-mode stack. ventd has been quietly running a continuous learning stack since v0.5.5 (opportunistic probing, Layer A/B/C confidence aggregation, marginal-benefit RLS per workload signature, predictive-vs-reactive PWM blending) but operators had no surface to see any of it. This page makes the AI visible. Reuses the calibration v2 chrome (header, bridge pipeline, scope, fan strips, system card, log card) but rewires every signal to real backend endpoints. Phoenix's framing: *ventd IS an AI constantly improving and calibrating your fans in the background, not a page that pretends to be one.*
- **Dashboard alive overlay** (#914) — third and final cal.ai handoff. Layered on top of the existing `/dashboard` to turn it from a status page into a storyteller: hero spark forecast bands (linear extrapolation of last 12 samples × 30 s), sensor / fan tile intent arrows + flash-on-decision, narrator strip rotating real decisions ("ramped pump_fan from 35% → 42% — cpu_pkg trending up"), insight rail (coupling map with `<animateMotion>` packets when fan duty > 30 %, decision feed, AI brief). Existing polling intact — new 1500 ms tick adds `/api/v1/hardware/inventory` + smart endpoints in parallel without double-fetching `/status`.
- **Runtime-probe `pwm_enable` enum** (#911) — refines #910's hardcoded `pwm_enable = 5` EINVAL fallback. HIL on Phoenix's MSI PRO Z690-A surfaced the in-tree-nct6687 case where the chip rejects `5` as well; the driver build accepts only `{0, 1}`. Replaced with a runtime probe of `{2..7}` on the first EINVAL per pwm path, picking the highest-numbered accepted value (richer-mode-wins on conventional drivers). Probe runs once per pwm path per daemon lifetime (cached in `probedPWMEnableModes`). When the probe finds nothing, surfaces a distinct INFO line ("driver supports only manual control") before falling through to the safe-PWM floor. RULE-HWMON-ENABLE-EINVAL-FALLBACK refined; 3 mode-5-specific tests replaced with 4 probe-based tests.

### Added
- `GET /api/v1/hardware/inventory` composes `monitor.Scan()` with the live config to surface bus / kind / alias / used_by per sensor plus a top-level `curves[]` coupling array. Per-sensor history ring (cap 60 samples, in-process, appended on each call) so sparklines have chronological history without client-side state across reloads.
- `config.Sensor` and `config.Fan` gain an optional `Position{X, Y}` field for the Heatmap view. Operator-supplied via YAML; nil when unset.
- New web pages: `web/hardware.{html,css,js}` (75 / 647 / 1023 lines), `web/smart.{html,css,js}` (72 / 575 / 942 lines).
- Sidebar canonical updated twice: Devices + Sensors collapse into a single Hardware entry (#912); Smart mode entry added under Control between Dashboard and Curves (#913). Propagated byte-for-byte across all 8 sidebar-bearing pages (RULE-UI-03).

### Changed
- `web/dashboard.{html,css,js}` extended with the alive overlay (192 → 261 / 538 → 1009 / 746 → 1749 lines). Existing `/api/v1/status` polling wrapped not replaced; demo-mode banner pathway untouched.

### Fixed
- Excluded-channel handback no longer strands NCT6687D-driven channels at the calibration sweep's last byte when the in-tree driver build rejects both `pwm_enable = 2` (standard auto) and `5` (SmartFan); the runtime probe correctly finds the empty set and falls through to the safe-PWM floor with an explicit INFO line (#911).

### CI / chore
- Five contract tests in `internal/web/hardware_inventory_test.go` cover the load-bearing inventory paths (empty config returns well-formed envelope, alias mapping from config, used_by populated from curves, history ring accumulates chronologically, position propagates).

### Honest framing
v0.5.14 closes out the cal.ai design-handoff trio that v0.5.13 set up — Hardware + Smart-mode + Dashboard alive all land in this tag. Plus the in-tree-nct6687 EINVAL refinement that surfaced from Phoenix's MSI PRO Z690-A HIL run. **No fake AI theatre on any of the new pages**: every visible signal traces to a real endpoint or a real client-side computation over real history. Where data isn't computable from existing endpoints (acoustic dBA estimate, next-probe ETA, per-sensor physical positions on first ship), the affordance is omitted or shows an honest empty state rather than a fabricated number.

## [v0.5.13] - 2026-05-04

### Headline
- **Calibration v2** (#906) — operator-facing calibration takeover replaced with the claude.ai/design v2 layout. Command bridge with phase pipeline + live channels/sps/total/fans-ready stats, oscilloscope with PWM/tach/ADC ribbon, per-fan strips with sparklines + live PWM%/RPM cells, system card, thermal preview, and the climactic compute hero. Vanilla JS (no React/JSX/CDN per RULE-UI-01), token-only colours (RULE-UI-02), demo mode (`?demo=1`) for screenshots and offline preview. Per-fan strips read `FanProbe.CurrentPWM` / `FanProbe.CurrentRPM` directly so the sweep shows real numbers, not em-dashes.
- **Live activity feed via SSE** (#907) — new `GET /api/v1/setup/events` streams the structured `{ts, level, tag, text}` event log that `setup.Manager` appends on every phase transition. Frontend opens an `EventSource`, renders one row per event with colored level glyph + tag pill in the calibration narrator card. "Ring + cursor poll" transport (250 ms tick, 500-entry bounded ring) avoids per-subscriber goroutine plumbing for a write-rare/read-rare workload. `setPhase` is the only emit hook today; per-fan transition emits drop in via the same `EmitEvent` surface in a follow-up.
- **Calibration recovers from BIOS Q-Fan contention** (#905, closes #904) — Backend.Write now detects `EBUSY` on the duty-cycle write (BIOS reasserted `pwm_enable=2` mid-sweep, classic Gigabyte Q-Fan / Smart Fan Control behaviour on IT8xxx chips), drops the cached acquired-state for the channel, re-writes `pwm_enable=1`, and retries the original write exactly once. Single retry only — never spin. New `RULE-HWMON-MODE-REACQUIRE` documents the sustain contract; `RULE-HWMON-ENABLE-MODE` covered the first-write contract previously.

### Changed
- `setup.Manager` gains an in-memory event ring buffer (`events []Event`) plus `EmitEvent` / `EventsSince(cursor)` accessors. Public surface for any future emit hook (per-fan transitions, recovery actions, etc.).
- `internal/hal/hwmon/Backend.Write` split into Write + private `writeDuty` so the EBUSY-retry path can re-invoke the duty write after re-acquiring manual mode without duplicating the rpm-target / pwm dispatch.

### Fixed
- Empty "Instructions" modal, "Calibration could not complete" error banner, and "Calibration finished" done banner all showed on page load on the new calibration takeover (#906) because author CSS rules for those elements set `display: flex|grid` which beats the UA stylesheet's `[hidden] → display: none` default. Added explicit `[hidden] { display: none !important; }` to `web/calibration.css`. Caught from Phoenix's HIL screenshots before ship.

### CI / chore
- Backend test fixtures (`internal/hal/hwmon/export_test.go`) gain `NewBackendForTestWithDuty` so tests can inject the duty-cycle write. Required for the EBUSY-retry binding tests; production callers leave `writeDutyFn` nil and use `NewBackend` unchanged.

### Honest framing
The handoff from claude.ai/design landed with three pages — Calibration (primary), Hardware, and Dashboard. v0.5.13 ships the **Calibration** half (PR-1 layout + PR-2 live feed) plus the **EBUSY-retry** backend fix that surfaced from the first HIL run on the Proxmox host (Gigabyte B550M AORUS PRO + IT8688). The activity feed currently emits one event per phase transition (~7 per run); per-fan transition hooks (`cal.start` / `cal.minspin` / `cal.done`, etc.) are scoped for the next round and drop in via the same `EmitEvent` surface without touching the SSE transport. Hardware page redesign + Dashboard restyle are queued but unstarted; they ride in their own tags once they land.

## [v0.5.12] - 2026-05-04

### Headline
- **R30 acoustic capture + calibration** — `ventd calibrate --acoustic <mic_device>` CLI subcommand wires up mic capture → IEC 61672-1 A-weighting → K_cal reference-tone offset. Split across #886 (A-weighting filter coefficients), #892 (runner extracted to `internal/acoustic/runner`), #887 (`calibrate_acoustic` PhaseGate constructor), #893 (Manager.run gate wiring), #894 (`--mic` flag + adapter). Privacy contract: WAV temp files architecturally denylisted from diag bundles (RULE-DIAG-PR2C-11), no audio device opened by the daemon, only the operator-invoked CLI.
- **R32 quietness-target preset** (#888, #891) — operator-typed dBA cap (Silent=25 / Balanced=32 / Performance=45 dBA per R32 user-perception thresholds, with explicit-value override via the new `dba_target` config field). Cost gate refuses ramps where predicted dBA exceeds the budget; refusal cascade applies after Path-A and benefit-vs-cost (RULE-CTRL-PRESET-04). Wired into `BlendedController.Compute` via the `AcousticBudget` struct from per-fan R33 proxy + per-host R30 K_cal.
- **R31 fan-stall detector — advisory only** (#889) — 2-of-3 detector during calibration soak (broadband rise ≥6 dB / crest factor excess ≥2 / kurtosis excess ≥1.5), triggered when at least 2 cross threshold within a 3 s window. Output is a flag (`AcousticStallSuspected`) propagated to the polarity classifier; **never refuses fan writes**. 5× RULE-STALL-* invariants bound to synthetic-fixture subtests; MIMII real-world validation deferred to a follow-up PR.
- **R36 IT5570 chip-probe fallback** (#885) — schema v1.3's `chip_probe: {hwmon_name}` field for mini-PCs whose BIOS authors leave DMI as the literal string `"Default string"` (Beelink / Minisforum / GMKtec / AceMagic). Tier-1.5 matcher walks `/sys/class/hwmon/*/name` when DMI fingerprint is empty / default, binds to the matching board profile via the `chip_probe` anchor. Confidence 0.85 (vs DMI tier-1 0.9). Existing 16 board rows unaffected.

### Changed
- **Acoustic calibration runner extracted to internal package** (#892) — calibration logic from `cmd/ventd/calibrate_acoustic.go` moved into a reusable internal package so both the CLI subcommand and the wizard PhaseGate drive the same code path without duplication.

### Fixed
- **A-weighting filter coefficients** (#886) — IEC 61672-1:2013 Class 1 cascade now correctly sized for fs=48 kHz (3-stage biquad via canonical bilinear transform). Matches the standard's tolerance band (1 kHz ≈ 0 dB, 100 Hz ≈ -19 dB, 10 kHz ≈ -2.5 dB) within the wider error envelope absorbed for bilinear roll-off near Nyquist.

### CI / chore
- **errcheck discards on six pre-existing offenses** (#890) — explicit `_ =` discards keep the per-package errcheck floor at zero so future violations stand out.
- **CI unblock + diag-send flake registration** (#895, #897, #899, #900, #902) — fixes that were exposed by the v0.5.12 acoustic stack landing on main:
  - `scripts/retry-flaky.sh` rewritten in pure POSIX awk (drops the `python3: command not found` failure on Fedora / Ubuntu 22.04 / Debian 12 / Arch minimal containers).
  - `gcc libc6-dev` added to ubuntu-22.04 + debian-12 prereqs and `gcc` added to opensuse-tumbleweed prereqs (#897). The race detector requires CGO and the previous python3-blocked path masked the missing-compiler error on three distros that don't ship gcc by default. Closes the `-race+CGO` half of the project's known-red CI memory.
  - `defaults.run.shell: bash` set at the build matrix job level (#899) so opensuse-style `/bin/sh` brittleness can't take down post-prereqs steps; `shell: sh -e {0}` override added back to the prereqs step itself (#900) so Alpine's pre-bash-install path keeps working.
  - `docs/binary_size_baseline` BYTES bumped 9441572 → 11821348 (acoustic-stack growth, +25%).
  - `TestHandleDiagSend_IngestRejects_Returns502` registered in `.github/flaky-tests.yaml` (issue #883). Roadmap fix is to inject `diag.Generate` via a `Server` field so the test mocks bundle generation.
  - E2E (browser) job routed through `retry-flaky.sh` so registry entries actually fire on that lane.
  - `build-and-test-opensuse-tumbleweed` lane disabled in the matrix (#902, tracking #901). Every step after `Install prerequisites` fails with `OCI runtime exec failed: exec failed: unable to start container process: exec: "<shell>": executable file not found in $PATH` — `sh`, `bash` (PATH lookup), and absolute `/usr/bin/bash` all hit the same wall even though the binary is verifiably installed. None of the four upstream fixes resolved it. The same `actions/setup-go` runs cleanly on every other distro in the matrix. The matching context in `.github/rulesets/main.json` is left in place (modifying branch protection requires explicit operator OK).

### Honest framing
v0.5.12 closes the R28-R36 acoustic implementation arc that began in v0.5.11. The R30/R31/R33 research bundles synthesise into one operator-visible behaviour: type `25 dBA` in the Silent preset, the daemon opens the mic on demand for one-time calibration, the cost gate refuses ramps that would exceed the budget. Privacy-sensitive surfaces (mic capture, ALSA device opening) are gated behind an explicit opt-in (`--mic` flag); the WAV temp files are architecturally denylisted from diag bundles; the only persisted artefacts are the K_cal scalar offset + dBA-vs-PWM curve in the calibration JSON. R31's stall detector is intentionally advisory-only this release — it surfaces a flag but never blocks fan writes; refusal-on-stall is deferred until MIMII validation lands in a follow-up PR. The PR-3 cost-gate work in #888 also lifts the v0.5.11 `CostFactorBalanced=0.01°C/PWM` synthetic constant out of the controller in favour of the per-fan R33 `CostRate` measurement, which is the load-bearing simplification that makes the dBA budget refusal computationally cheap on the hot path.

## [v0.5.11] - 2026-05-03

### Headline
- **R33 no-mic acoustic proxy** (#867) — new `internal/acoustic/proxy/` package estimates per-fan and per-host loudness from PWM/RPM/blade-pass heuristics alone (no microphone, no audio library linked). Four-term sum (S_tip + S_tone + S_motor + S_pump) over 9 fan classes, 13 R33-LOCK-* invariants bound 1:1 to subtests. Score is dimensionless (`au`), within-host comparable; absolute dBA conversion still requires R30 mic calibration. Foundation for v0.5.12's per-fan cost-gate refactor.
- **Schema v1.3** (#866) — board profiles gain optional `pwm_groups: [{channel, fans}]` for the energetic-sum penalty when one PWM channel drives N fans (R29's Z690-A finding); driver profiles gain `blacklist_before_install: [module]` (generalises the MS-7D25 nct6683 blacklist) and `kernel_version: {min, max}` (R36's per-row kernel gates). Three new RULE-HWDB-PR2-15/16/17 validators. Existing v1.2 catalog rows pass through unchanged.
- **CostRate helper** (#868) — exposes `acoustic.proxy.CostRate(class, rpm, ..., rpmPerPWM, preset)` returning marginal acoustic cost in au per PWM unit, with preset multipliers Silent=3.0 / Balanced=1.0 / Performance=0.2. Wired in v0.5.12 PR-E to replace the synthetic 0.01°C/PWM `CostFactorBalanced` constant in the blended controller's cost gate.

### Changed — workflow hygiene + collaboration discipline
- **Full friction audit** (#864) — concurrency cancel-in-progress on every workflow, `paths-ignore` for docs/.claude/CHANGELOG to skip CI on doc-only PRs, `make pre-push` target wrapping `scripts/ci-local.sh`, branch-cleanup workflow sweeping merged branches >7 days, attribution-lint CI gate (`.github/workflows/no-ai-attribution.yml`) blocking AI footers at the line-anchored regex level, HIL smoke workflow scaffolded, RULE-INDEX.md un-tracked (now generated locally; was a serial rebase-conflict source), `release-changelog.yml` auto-CHANGELOG workflow + `cliff.toml` retired in favour of manual CHANGELOG entries per release.
- **Collaboration audit** (#862) — `.claude/rules/collaboration.md` rewritten end-to-end: standing-delegations section pre-authorises `git tag` / `goreleaser release` / branch deletion / rebase / flake-rerun, CI-flake threshold raised 20→45 min (5-distro × race matrix takes 25-40 min on green), design-conflict-based rebase-escalation replaces count-based, `gh pr merge --auto` + `scripts/dev/{prs,wait-and-merge}.sh` documented, GraphQL `updatePullRequest` workaround for the `gh pr edit --body` projects-classic deprecation captured.
- **Rule staleness pass** (#865) — removed setup-token from web-ui.md + usability.md (eliminated in v0.5.8.1 #765/#794, first-boot is password-set-on-empty-auth.json now), removed NixOS from supported-distros pending modprobe.d-fragment work, attribution.md adds the CI gate as the enforcement reference.

### Honest framing
- v0.5.11 ships PR-A/B/C-1/C-2 from `tingly-twirling-duckling.md` — the catalog/schema/proxy half of the R28-R36 implementation arc. The v0.5.12 acoustic trio (PR-D `ventd calibrate --acoustic`, PR-E quietness-target preset, PR-F acoustic stall verification) is the larger commitment and ships under its own tag because the privacy-sensitive surfaces (mic capture, ALSA device opening) deserve a release-notes pass + documentation focus separate from this catalog/cost-gate work.

## [v0.5.10] - 2026-05-03

### Headline
- **Doctor surface** (#813) — runtime issue-detection CLI + 14 detectors covering preflight regression, polarity flip, DKMS status, userspace-daemon conflict, modules-load drift, battery transition, AppArmor profile drift, kmod-loaded check, experimental flags, container post-boot, calibration freshness, hwmon-index swap, DMI fingerprint match parity, permissions audit, GPU readiness, kernel-update transition. Severity rolls up to exit code (0 OK / 1 Warning / 2 Blocker / 3 Error). KV-backed suppression with TTL + acknowledge-forever.
- **Probe-then-pick driver selection** (#824) — replaces the static catalog→chip→driver pick with a candidate loop that tries each driver and trusts the kernel's chip-ID rejection as the authoritative signal. A stale catalog entry now costs ~30s of compile time, not 12 hours of debugging. Was the load-bearing fix from the MS-7D25 IT8688E→NCT6687D incident.
- **R36 OEM mini-PC catalog** — 20 board entries × 7 vendors (Beelink, MINISFORUM, GMKtec, AceMagic, Topton/CWWK, GEEKOM, AOOSTAR) (#861) closing R28 §3 long-tail gap. Three motherboard supply pools collapse the 8-vendor list: AMD Phoenix/Hawk Point + ITE IT5570; Intel Alder Lake-N + ITE IT8613E; AMD Rembrandt + ASUS PN53.
- **Preflight orchestrator** (#816) — terminal-first interactive auto-fixes for DKMS / Secure Boot / kernel-headers / build-tools / module conflicts. Replaces the legacy implicit-precondition install path with explicit predict-then-fix-or-abort.

### Added — research bundle (~5500 lines, foundation for v0.5.12 acoustic features)
- R28 master synthesis + 9-agent failure-mode survey (#842, #843, #846)
- R29 — acoustic capture analysis (Phoenix's MSI Z690-A under Tdarr) (#860)
- R30 — microphone calibration procedure (#856)
- R31 — fan-stall acoustic signatures (bearing wear / blade flutter / pump cavitation) (#855)
- R32 — user-perception thresholds (Whisper/Office/Performance preset rationale) (#854)
- R33 — no-mic acoustic proxy (four-term sum + 15 LOCK invariants) (#857)
- R36 — OEM mini-PC EC firmware survey (#859)

### Added — recovery + classifier (R28 Stage 1)
- ThinkPad fan_control classifier rule (RULE-WIZARD-RECOVERY-10) (#831)
- Vendor-daemon + NixOS probes (RULE-WIZARD-RECOVERY-11/12) (#832)
- Laptop-class FailureClass extensions (#830)
- AMD OverDrive bit detection probe (RULE-WIZARD-RECOVERY-13) (#835)
- DetectVendorDaemon wiring into wizard preflight (#840)
- /api/setup/apply-monitor-only endpoint for vendor-daemon deferral (#838)
- /api/hwdiag/modprobe-options-write endpoint for ThinkPad fan_control (#841)
- Reboot-prompt UX for cards whose effect needs next boot (#818) (#828)

### Added — observation / diag / iox
- iox canonical atomic-write helper + 6 call-site migrations (#848)
- diag export-observations subcommand activates internal/ndjson (#845)
- Cpuinfo hypervisor flag as 4th virt-detect source (#851)

### Changed
- README honesty pass per github-page-audit + Sponsors button (#829)
- Sign-guard wired into opportunistic prober + marginal runtime (#844)
- Sub-absolute-zero temperature now rejected as sentinel/underflow (#837)
- PlausibleRPMMax raised 10000→25000 to admit server-class fans (Delta/Sanyo Denki 12k-22k) (#850)
- Container detection adds /run/.containerenv + /proc/1/environ sources (#836)

### Calibration / smart-mode
- Calibration UX rework: min phase duration, in-grid done banner, finalising spinner (#826)
- Clock injection on ZeroPWMSentinel — sub-millisecond tests (was 14s of real-time sleep) (#853)

### Fixed
- MS-7D25 (PRO Z690-A DDR4) selects nct6687d, not it8688e (#822) — the 12-hour-debug catalog defect
- Dashboard demo-mode false positive + recovery + visible signal + logout (#820) (#827)
- Dashboard shows duty % when fan has no tach (NVIDIA, etc.) (#825)
- Topbar popover stacking renders above dash-hero cards (#823)
- thinkpad_acpi catalog: experimental=1 → fan_control=1 (canonical name) (#847)

### CI / chore
- R28 catalog audit P0/P1 fixes (#847)
- Dead packages + 3 unused exports removed (R28 codebase audit) (#849)
- Issue466_ReloadFailureIsNonFatal registered as arm64 timing flake (#852)
- CHANGELOG backfill for missing v0.5.8.1 + v0.5.9 sections (#858)

## [v0.5.9] - 2026-05-02

### Added
- Wizard recovery — diag bundle button on calibration error banner (#799)- V1.x amendment — extend pwm_control allowlist for modern hardware (#801)- Aggregator (R12 chain) — v0.5.9 PR-A.2 (#802)- IMC-PI confidence-gated blended controller — v0.5.9 PR-A.3 (#803)- Smart-mode preset enum + setpoints map — v0.5.9 PR-A.4 (#804)- Wire confidence-gated controller into hot path — v0.5.9 PR-B (#807)- Per-failure-class wizard + doctor classifier (#800) (#810)- Predictive preflight + controlled install pipeline + gated wizard (v0.5.9 PR-D) (#811)
### CI
- Switch Ubuntu host AppArmor step to canonical mirror + retry (#806)
### Documentation
- V0.5.9 — confidence-gated controller + predictive install (#814)
### Fixed
- IPv6 regex over-matched ISO-8601 time-of-day (#808)- Restore pwm_enable=2 on channels excluded from doneFans (#753) (#758)## [v0.5.8.1] - 2026-05-01

### Added
- Layer-A confidence estimator (conf_A) — v0.5.9 PR-A.1 (#760)- One-line curl-pipe-bash installer (#764)- SUID-root helper for unprivileged NVML writes (#771)
### Documentation
- V0.5.9 confidence-gated controller design (#752)
### Hwmon/install
- Fail-fast on missing kernel version + 5min install timeout (#775)
### Setup
- Re-run daemon probe + persist outcome after driver install / load-module (#776)
### V0.5.8.1
- Plumb SensorReadings into the observation log (#756)- Flip daemon to root, drop layered-elevation theatre (#794)- Rc4 follow-ups from HIL — apparmor syntax + dashboard sparklines + ReadWritePaths (#798)## [v0.5.8] - 2026-05-01

### Added
- v0.5.8 PR-A — Layer-C per-(channel, signature) marginal-benefit RLS estimator: `internal/marginal/` package implementing `d_C = 2` model `ΔT_per_+1_PWM = β_0 + β_1·load` per R10 §10.1, dual-path saturation per R11 §0 (Path A predicted + Path B observed), three-condition warmup gate with parent Layer-B clearance, R12 bounded-covariance clamp, weighted-LRU shard eviction (cap 32/channel), κ-deferred activation (τ_retry=1h), OAT cross-channel gate (R28 mitigation), persistence at `smart/shard-C/<channel>-<sig>.cbor` per R15 §104. Plus `internal/coupling/signguard/` — 5-of-7 sign-vote consuming v0.5.5 opportunistic-probe ground-truth (R27 wrong-prior detection). 22 RULE-CMB-* + 3 RULE-SGD-* bindings. (#741)
- v0.5.8 PR-B — daemon wiring: `cmd/ventd/main.go` launches `marginal.Runtime` alongside `coupling.Runtime`; `Config.SmartMarginalBenefitDisabled` toggle. RULE-CMB-WIRING-01 + RULE-CMB-WIRING-03. (#742)
- `internal/idle.CaptureLoadAvg` exported for v0.5.8 Layer-C load proxy.

## [v0.5.7] - 2026-05-01

### Added
- v0.5.7 PR-A — Layer-B thermal coupling estimator: `internal/coupling/` package implementing per-channel RLS estimator with rank-1 Sherman-Morrison updates (`gonum mat.SymRankOne`), bounded-covariance directional forgetting (R12 ceiling `tr(P) ≤ 100`), three-condition warmup gate (`n_samples ≥ 5·d²` AND `tr(P) ≤ 0.5·tr(P_0)` AND `κ ≤ 10⁴`), three-way κ classification (R9 §9.4), signed Pearson co-varying fan detection (`ρ > 0.999`), spec-16 KV persistence with `hwmon_fingerprint` invalidation. Lock-free Snapshot.Read via `atomic.Pointer` for the controller hot loop. Determinism replay test, observability via structured slog, privacy invariants (no comm names in Snapshot). 13 RULE-CPL-* bindings. (#736)
- v0.5.7 PR-B — daemon wiring: `cmd/ventd/main.go` launches `coupling.Runtime` alongside controllers; one shard per controllable channel; `N_coupled = 0` (R9 §U4 well-posed reduced model); `hwmon_fingerprint = dmiFingerprint`. RULE-CPL-WIRING-01..03. (#738)

## [v0.5.6] - 2026-05-01

### Added
- v0.5.6 PR-A — workload signature library + integration tooling: `internal/signature/` package (SipHash-2-4 keyed by per-install salt, EWMA-multiset with K-stable promotion, 128-bucket weighted-LRU, spec-16 KV persistence), `internal/proc/walker.go` shared process walker, `internal/idle/blocklist_export.go` re-exports R5 maintenance-class names. Plus integration tooling: `tools/rulelint --suggest --check-binding-uniqueness`, `internal/testfixture/fakeprocsys`, `internal/smartmode/` cross-spec integration tests, `scripts/ci-local.sh`, `scripts/install-git-hooks.sh`, `scripts/retry-flaky.sh`, `.github/flaky-tests.yaml` (#730)
- v0.5.6 PR-B — controller wiring + Settings toggle + closes the v0.5.4 obsWriter gap. Every successful PWM write emits an observation record stamped with the current signature label. (#731)

### Changed
- `internal/idle/idle_test.go` and `internal/probe/opportunistic/helpers_test.go` migrated to the shared `fakeprocsys` test fixture, removing duplicated inline `makeIdleProcRoot` helpers.

## [v0.5.5] - 2026-05-01

### Added
- v0.5.5 PR-A — Layer A gap-fill probe library: `internal/idle/OpportunisticGate`, `internal/probe/opportunistic` (Detector / Scheduler / Prober / install_marker), schema bump 1→2 with `EventFlag_OPPORTUNISTIC_PROBE`, `Config.NeverActivelyProbeAfterInstall` toggle, 18 RULE-OPP-* invariants (#725)
- v0.5.5 PR-B — daemon wiring + Settings "Smart mode" toggle + dashboard probe-in-flight pill + `/api/v1/probe/opportunistic/status` endpoint (#726)

## [v0.5.4] - 2026-05-01

### Added
- Passive observation log (v0.5.4 smart-mode) (#693)
- v0.5.5 opportunistic probing spec (#724)

### Changed
- Redesign every page on the new design system (#704)
- CC sessions now load `.claude/RULE-INDEX.md` instead of fanning out across all rule files. Rules are read on demand via `go run ./tools/rule-index`. Reduces session preload by ~24-48k tokens. (#686)

### Fixed
- First-boot apparmor + listen + sd_notify so fresh installs actually work (#702)
- Stop upper-casing the first-boot setup token (#712)
- Defer service start until prerequisites are in place (#711)
- CSP style-src violations + /api/v1/profile/active 405 polling (#713)
- Remove dead /logs link from every sidebar (#715)
- Drop inline <script> from / redirect helper (#714)
- No-fan-host escape from /calibration; persistent applied marker (#717)
- Inline change-password form + JSON body matching the API (#720)
- Progress.Applied honours marker; idle preconditions go fork-free (#723)

### Documentation
- HIL soak log + open-items list for fix(first-boot) (#703)

### Tests
- Stop scheduler goroutine race and context leaks in test helpers (#687)

## [v0.5.3] - 2026-04-29

### Added
- Envelope C/D probe + idle gate wiring (v0.5.3 PR-B) (#685)

## [v0.5.2.1] - 2026-04-29

### Added
- Sysclass + idle foundation (PR-A) (#682)
### Chore
- Apply Thariq 9-rule audit to all SKILL.md files and add cost-routing reference (#683)
### Documentation
- Research archive + spec-16 post-ship cleanup + untracked spec drafts (#677)
### Fixed
- Serve ui/index.html at root instead of web mockups index (#674)- Remove dead savedFan code and harden Alpine torn-read test (#676)- Add GetFanControlPolicy to nonvidia stub (#681)

## [v0.5.2] - 2026-04-27

### Added
- Cross-backend polarity probe + wizard screen (spec-v0_5_2) (#673)## [v0.5.1] - 2026-04-27

### Added
- Install design system foundation and new landing page (#661)- Schema v1.2 — typed experimental block with Levenshtein forward-compat (#662)- Framework scaffolding (spec-15 PR 1) (#663)- Amd_overdrive F1 — precondition, HAL gate, RDNA4 kernel check (#664)- Persistent state foundation + smart-mode README (spec-16 PR 1) (#669)- Catalog-less hardware probe and three-state wizard fork (spec-v0_5_1) (#670)
### Chore
- Ignore worktrees, log spec-12 PR 1 cost calibration (.41)- Ignore worktrees, log spec-15 PR 1 cost calibration ($9.78)- Remove leaked CC prompt files (#671)
### Documentation
- Spec-14a + spec-14b — hardware-profiles repo design + submission flow (#658)- Add v0.6.0 mockup screenshots and HTML preview (#665)- Update screenshots to v0.6.0 UI and add page gallery (#666)- Add spec-16, smart-mode, v0.5.1 probe, and amendment specs (#667)- Remove superseded spec-04 pi-autotune and amendment (#668)
### Fixed
- Wire os.DirFS defaults for SysFS/ProcFS/RootFS in New() (#672)## [v0.5.0] - 2026-04-26

### Added
- Freeze v1 schema (spec-03 PR 1) (#629)- Add CC tooling bundle (preflight, release-validate, templates) (#632)- Spec-03 PR 2a — three-tier matcher and chip-family catalog (#635)- Spec-03 PR 2b — runtime probe + unified types + apply-path enforcement (#636)- Spec-03 PR 2c - diagnostic bundle, redactor, NDJSON substrate (#639)- Spec-03 PR 2d — GPU vendor catalog (NVIDIA/AMD/Intel) (#643)- Spec-03 PR 3 — board catalog seed (15 entries) (#644)- Spec-03 PR 4 - schema v1.1 (bios_version, dt_fingerprint, unsupported) (#646)- Spec-03 PR 5 — scope-C catalog seed + spec-12/13 docs (#647)- Capture pipeline — pending profiles after calibration (spec-03) (#649)
### Chore
- Add triage-run.sh + verify-local.sh (#628)- Post-spec-03 PR 4/5/gaps/capture cleanup (#653)
### Documentation
- Add spec-03 PR 1 working document- Log PR #632 cost + spend velocity tracking (#634)- V0.5.0 spec batch + GPU catalog research (#641)- Predictive thermal thesis + v0.4.0 Corsair / v0.3.1 IPMI shipped (#642)- PR 4/5 gaps - hwdb-schema v1.1 section, amendment status, changelog (#648)- V0.5.0 hardware database, runtime probe, ventd diag (#654)## [v0.4.1] - 2026-04-25

### Added
- Rulelint pattern for CI action pinning (spec-08) (#621)- AppArmor profiles + HIL validation (spec-06 PR 2) (#627)
### Documentation
- Add v0.4.1 backlog specs 06-08 (#618)- Reflect v0.3.1 IPMI ship + v0.4.0 Corsair ship (#620)
### Fixed
- Install-contract drift fixes (spec-06 PR 1) (#625)## [v0.4.0] - 2026-04-24

### Added
- USB HID primitives + fakehid fixture (#591)- Pure-Go hidraw substrate, drop go-hid (#593)- Commander Core / Core XT / ST backend (#608)- Udev uaccess rule for Corsair + architectural guardrail tests (#611)- PR 4 — VID gate + docs + CHANGELOG for v0.4.0 (#615)
### Documentation
- Add hidraw substrate spec + amendment + rules (#592)- Add hwmon research reference- Record deferred hwmon gotchas from §17 gap analysis- Liquid-safety.md invariants for Corsair backend (#596)- Add RULE-LIQUID-07 for kernel driver yield (#601)- Seed TESTING.md with hardware and VM inventory (#610)- Consolidate TESTING.md at repo root (#612)- Add manual-file-placement duplicate check (#613)- Add spec-05 predictive thermal shell + amendments (#614)## [v0.3.1] - 2026-04-23

### Added
- Apple Silicon detection + hwmon wrapping (P2-ASAHI-01) (#279)- ARM SBC sysfs PWM backend (P2-PWMSYS-01) (#277)- Shared USB HID primitive layer (P2-USB-BASE) (#281)- Framework + Chromebook EC backend (P2-CROSEC-01) (#282)- Native IPMI backend via /dev/ipmi0 ioctl (P2-IPMI-01) (#285)- PI curve with anti-windup + per-channel state (P4-PI-01) (#315)- Drew-audit.yml workflow for post-tag audit gates (#354)- Recognise // regresses #N and // covers #N magic-comment annotations (closes #330) (#352)- Per-session git worktree isolation for CC spawns (#396)- Auto-label PRs by changed paths and title prefix (#407)- Daily stale role:atlas escalation workflow (#408)- Allowlist-scope guard for CC task PRs (#409)- Audit-signal workflow — escalate silent Cassidy audits (IMPROV-F) (#414)- Spawn_cc_inline, wait_for_session, spawn_cc_batch (#423)- Automated drift detection replacing ultrareview (#425)- Add github-mcp.service for ci-log-tools build deployment (#428)- Ops-mcp — scoped host operations MCP server (#429)- Fuzz-smoke.yml + fuzz-long.yml scaffolding for TX-FUZZ-CORPUS-GROW (#381)- Pre-release-check.yml pre-tag validation workflow (RELEASE-CHECK-01, closes #328)- CycloneDX + SPDX SBOMs on every release (P10-SBOM-01)- Cowork-query CLI for events.jsonl (#426)- Release-time git-cliff CHANGELOG automation (closes #385) (#450)- Cosign keyless + SLSA L3 provenance (P10-SIGN-01) (#447)- Pve-template-prep.yml workflow for Arch/Void/Alpine templates (#395)- Reproducible builds + rebuild-and-diff verification (P10-REPRO-01) (#516)- Injection seam for fixture-driven tests (#531) (#540)- Add 18 new tools, fix INSTALL.md paths, logrotate, sudoers expansion (#565)- Ship logrotate fragment for audit log (#562) (#575)- Add filter/session/guardrail hooks- Add ventd-rulelint skill (skill-creator evaled)- Add ventd-specs skill (skill-creator evaled)- Privilege-separated sidecar (spec-01 PR 2) (#588)- DMI gate + vendor profiles + v0.3.1 changelog (#590)
### CI
- Replace per-PR CHANGELOG edits with release-time git-cliff (#410)
### Changed
- Extract shared Base struct (closes #271) (#314)
### Chore
- Add PR template with Risk class + Verification + Concerns + Deviations schema (per Cassidy D) (#346)- SHA-pin actions, digest-pin containers, hash-verify Alpine Go (SUPPLY-CI-HYGIENE-01) (#355)- Activate drew-audit Gates 2/3/4 (SBOM + cosign + repro) (#523)- Gitignore .mcp.json at repo root- Enforce LF line endings (WSL2 CRLF workaround)- Track hooks, skills, and settings; ignore only local- Remove spec-writer (superseded by ventd-specs) and eval workspaces
### Documentation
- Drop model-mismatch abort rule- HTTP API reference (closes #269) (#309)- GHSA policy + v0.3.0 stdlib CVE advisory draft (SUPPLY-GHSA-01)- Promote Safety to named section; reorder Install to lead with inspect-first (#567)- Correct infrastructure inventory for solo-dev workflow (#580)- Diet CLAUDE.md to 67 lines; move infra to docs/- Commit masterplan + test masterplan to repo- Bind ipmi-safety invariants to subtests- Split PR 2 into 2a+2b to fix sidecar spec gap- Bind legacy orphan subtests to invariant rules (#586)- Revise PR split for privilege-separation sidecar (#587)
### Fixed
- Scheduler↔manual-override race (fixes #289 concern 1) (#294)- Guard read_no_mutation type assertion with fileBacked check (closes #266) (#295)- Reject sensor/fan name collisions (closes #293) (#319)- Fsync tmp file and parent dir in mergeModuleLoadFile (closes #311) (#336)- Reset failure counter after Restore to stop log spam (closes #306) (#338)- Close-checks + mu-serialise per-handle I/O (closes #305 concerns 1-2) (#340)- ErrNotPermitted sentinel restores fatal-on-permission for manual-mode (closes #288) (#341)- Error on non-zero Restore cc + Zone=0 fallback + response bounds check (closes #307) (#361)- Verified-first match pass so unverified profiles can't shadow verified ones (closes #308) (#362)- Split peek/redirect timeouts in tlsSniffListener + escape redirect body against XSS (#364)- Plug CAUGHT #7 durability gaps in calibrate/config/selfsigned (closes #376 #378 #365) (#412)- Surface parseIndex errors with log (closes #380)- Persist setup token to /var/lib/ventd (survives reboot) (#383)- AppArmor profile blocks cert-gen and hwmon reads on fresh Ubuntu (#459) (#462)- Correct setup-token recovery instructions and rotation (#458) (#464)- Replace self-restart with in-process config reload (#466) (#478)- Add ventd user to NVIDIA device group + actionable NVML diagnostic (#461) (#465)- Persist admin credentials to auth.json, protect from config-save overwrite (#463) (#481)- Reject 0xFFFF sentinel reads to prevent fan-to-max on invalid sensor (#460) (#469)- Add authPath param to runDaemonInternal signature — unbreak main- Reject module names with whitespace in sensors-detect parser (#489)- Collapse curve editor form to single column on mobile (#488) (#491)- Install ventd-recover binary + OnFailure hook (#484) (#494)- Atomic-write with unique tmp path, fix ubuntu-arm64 abort-persists race (#467) (#492)- Curve editor form hydration + PATCH semantics (#483) (#493)- Heuristic sensor binding + accurate error messages for mini PC / idle hardware (#504) (#521)- Store acquired-path after write success, not before (closes #348) (#528)- VENTD_STATE_DIR env override for scratch-sysroot test harness (#532)- AppArmor profile v2 + systemd directory modes (closes #498) (#527)- Apply retry+RestoreOne to manual-mode write path via writeWithRetry helper (closes #272) (#357)- Unique tmp suffix to eliminate concurrent-Save race (#515) (#538)- Wizard state cache TTL + server re-validation on load (#502) (#542)- Immediate RestoreOne on sentinel first-tick when !hasLastPWM (#512) (#539)- Suppress sentinel values at status/SSE serialization boundary — v2 (#460) (#522)- Replace gate2 cliff.toml tautology with cc-count check (#563)- Remove streamable_http_path="/" so /mcp endpoint works (#568)- Sync pwmsys_test.go with new fakepwmsys fixture API (closes #552) (#572)- Gate slider value updates on drag-active state to prevent mid-gesture jumps (#507) (#570)- Update github-mcp Docker tag from ci-log-tools to mcp-t12 (#561) (#574)
### Rules
- Bind hwmon-safety.md invariants to RULE- headings (closes #313) (#337)- Bind calibration safety invariants to rulelint RULE- headings (closes #235) (#363)
### Tests
- Add registry unit tests (closes #267, ultrareview-1) (#276)- Extend RULE-WD-RESTORE-EXIT subtest with RestoreOne leg (fixes #287) (#299)- Extend RULE-WD-RESTORE-EXIT subtest with RestoreOne leg (fixes #287) (#300)- Add ErrNotPermittedFatal_ManualMode regression test (closes #347) (#353)- E2e coverage for panic button and profile switching (#427)- Add TestRegression_Issue86_HwmonRenumberColdBoot (closes #86 from regression backfill) (#439)- Add TestRegression_Issue59_TLSConfigMigration (closes #59 from regression backfill) (#437)- Add TestRegression_Issue103_HwmonStartupRetry (closes #103 from regression backfill) (#438)- Fakeipmi fixture — Options, canned responses, BusyCount (T-IPMI-01a) (#530)- First-boot → configured reload starts controllers (#514) (#545)- Fakedmi fixture — DMI sysfs tree (T0-INFRA-fakedmi) (#550)- Fakepwmsys fixture (T0-INFRA-fakepwmsys)- 22-entry fingerprint match suite + determinism property (T-FP-01) (#571)- Add fakeipmi fixture scaffolding- Add IPMI safety invariant tests (T-IPMI-01)- T-IPMI-02 sidecar privilege-boundary verification (#589)
### Build
- Commit binary-size baseline + drift check (#453) (#537)
### Dead
- Prune 6 unreachable functions (closes #268, ultrareview-1) (#278)## [v0.3.0] - 2026-04-18

### Added
- Group dashboard by category with collapsible sections on mobile (#130)- Gate action=added rebind behind hwmon.dynamic_rebind, drop udev-settle ordering (#125)- Version metadata, health probes, /api/v1 alias (#155)- Reject min_pwm:0 without allow_stop:true at load (#158)- Empty-state copy for every dashboard section (#185)- Visual binding + clickable mix sources (#190)- Apply diff modal + /api/config/dryrun (#195)- Populated Settings modal + system status endpoints (#197)- Rescan hardware button + /api/debug/hwmon telemetry (Session C 2h) (#209)- Panic button + profiles (Session C 2e) (#212)- Wizard chip-name explainer during calibration (Session D 3g) (#220)- Hysteresis + smoothing per curve (Session D 3a) (#221)- Multi-point curves with interactive editor (Session D 3b) (#222)- Curve simulation preview with live projections (Session D 3d) (#224)- Migrate PWM 0-255 to percentage 0-100 (Session D 3f) (#226)- Proxmox-driven cross-distro install harness (#160)- Time-series sparklines on dashboard cards (#223)- Scheduled profile switching (#225)- Persist security-module load outcome + startup WARN (#232)- Surface setup URL + token in a visually distinct block (#231)- Redirect HTTP-&gt;HTTPS on the TLS port via first-byte sniff (#233)- Fingerprint-keyed hwdb (#246)- Baseline settings.json for non-interactive CC sessions (#250)- Permissions-Policy + ETag on embedded UI (P10-PERMPOL-01) (#253)- Opt-in remote refresh with SHA-256 pin (#257)- Symmetric retry+RestoreOne on PWM write failure (P1-HOT-02) (#263)
### CI
- Expand matrix to Ubuntu 24.04, Fedora 41, Arch, Alpine (#114)- Stable matrix names for ruleset alignment (#159)- AppArmor profile smoke check + arm64 matrix row (#219)- Add runner-smoke workflow for self-hosted HIL verification (#249)- Regression-test-per-closed-bug lint (T0-META-02) (#254)
### Changed
- Extract sysfs/procfs roots into Manager for testability (#163)- FanBackend interface (#247)- Drive via hal.FanBackend (P1-HAL-02) (#262)
### Chore
- Bump actions/cache from 4 to 5 (#83)- Add CC issue-logger shell library (#137)- Issue and pull-request templates (#150)- Makefile + fix feature.yml label and PR template refs (#154)- Gofmt sweep + CI gate (#156)- Delete empty t.Run pass-through stubs left by #163 (#175)- Track .claude/rules/*.md in version control (#218)- Ignore .cowork/ outside the cowork/state branch (#236)- Event-sourced state + dashboard (#248)- Bump Go toolchain to go1.25.9 (closes 17 stdlib CVEs) (#270)
### Documentation
- Add v0.3.0 plan (#110)- Correct reboot-survival gate reference to #111 (#113)- Mark v0.3.0 CI-matrix gate as landed in #114 (#117)- Expand v0.3 plan and draft v0.4 plan (#120)- Record v0.3 controller-safety fixes in CHANGELOG, COVERAGE, v0.3 plan (#127)- Record Batch 2 fixes and API metadata work (#157)- Record setup.Manager root extraction (#163) (#166)- Cross-link rig reboot-PASS tracker #167 (#169)- Refresh internal/setup after #163 lands (#170)- Document non-linux contributor workflow (#205)- CHANGELOG narrative for Phase 2 UI + refresh stale systemd unit comment (#213)- Add public roadmap (#237)- Add hardware-report issue template (#238)- Add regression-test checkbox to PR template (T0-META-03) (#240)- Add CLAUDE.md priming Claude Code for Cowork task activation (#243)- Correct feature list to shipped capability; add roadmap section (#264)- Finalize v0.3.0 release notes
### Fixed
- Allow ssh + web UI in ufw-incus.rules (#108)- Allow the installer's PWM-holder preflight to pass when ventd is already running (#107) (#109)- Rebind controllers on action=added topology change (#86 Proposal 3) (#112)- Close allow_stop + ctx-cancel exit-path safety gaps (#124)- Attach profile on both /usr/bin and /usr/local/bin (#134)- Initialise collection fields in Empty/Default (#151)- Nct6683 module + sensors-detect Driver variant (#153)- Race_count subshell and default label set (#152)- Reboot-survival verifier as a shipping artifact (#111) (#164)- Single source of truth for systemd unit and udev rules (#161)- HEALTHCHECK probes both HTTP and HTTPS to survive wizard-triggered TLS activation (#176)- Declare color-scheme so browser dark-mode overrides don't fight the theme toggle (#203)- HTTPS URL on re-install + CRLF self-heal + LF pin (#207)- Profile dropdown fires `change`, not `click` (#214)- Stabilise TestE2E_SettingsModal_PopulatedSections wait gate (#217)- HandleSystemReboot refuses 409 when running in a container (#230)- Add relative-path Write allowlist for repo dirs (#256)- Append-not-overwrite in persistModule (#261)
### Performance
- Drop modinfo shellouts, parse modules.alias directly (P1-MOD-01) (#259)- Eliminate per-tick allocations (#260)
### Security
- Add Dockerfile, compose, and device-passthrough docs (#142)
### T0-INFRA-03
- Faketime fixture — Clock + goroutine-safe timers (#245)
### T0-META-01
- Rule-to-subtest binding lint (#244)
### Tests
- Bind hwmon-safety invariants to table-driven cases (#118)- Bind wizard orchestration invariants to table-driven cases (#136)- Cover hwmon scan path via injected sysfs root (#138)- Cover autoload parsers and driver-need heuristics (#139)- Setup wizard state machine + reboot handler invariants (#178)- Diagnostic suite + one-command runner + Claude Code workflows (#227)- Add fixture library skeleton (#239)- Implement fakehwmon fixture (T0-INFRA-02) (#241)- Bind watchdog safety invariants (T-WD-01) (#255)- T-HAL-01 — lock backend contract invariants (#258)
### Deploy
- Fix apparmor profile parse failure on Debian 13 (#204)
### Docker
- VENTD_GID build-arg + image build CI (#162)
### Packaging
- Add AUR PKGBUILDs for ventd-bin and ventd (#119)- Add NixOS flake and module (#145)
### Validation
- Spare-VM UFW dry-run harness (#99)## [v0.2.0] - 2026-04-15

### Added
- Lucide icon sprite replaces unicode glyphs (#48)- Server-sent events for live fan state, keep polling fallback (#47)- Phase 4.5 foundation — breakpoints, drawer sidebar, header reflow (#57)- Phase 4.5 PR 2 — card grid reflow and mobile card internals (#69)- Phase 4.5 PR 3 — touch-aware curve editor (#89)- Phase 4.5 PR 4 — 44x44 touch targets + responsive modals (#92)
### CI
- Add govulncheck workflow (#14)- Resolve checkout@v6 / govulncheck-action auth conflict (#41)
### Changed
- Split styles/app.css into tokens / base / layout / components- Split ui/scripts/app.js into 5 defer-loaded modules
### Chore
- Surface ParseUint errors and replace Fprintf with slog (#11)- Wrap stdlib errors across internal/* (#16)- Bump actions/setup-go from 5 to 6 (#23)- Bump goreleaser/goreleaser-action from 6 to 7 (#22)- Gitignore AGENTS.md (#29)- Finalize v0.2.0 entry (#46)- Un-draft v0.2.0 after rig PASS (#80) (#87)
### Documentation
- Install-smoke notes for alpine (pass) and void (blocked)- Coverage snapshot (#17)- Correctness sweep across README and docs/ (#19)- NVIDIA GPU fan control setup guide for v0.2.0 (#34)- Add first-boot setup screenshot (#53)- Add dashboard screenshot (#77)- Swap dashboard.png placeholder for real screenshot (#62, #77) (#79)- Mark v0.2.0 release blocked pending cold-boot first-boot-mode fix (#104)- Un-draft v0.2.0 after cold-boot re-verify (9/9 PASS) (#106)
### Fixed
- Surface ParseUint errors in nvidia sensor read (#13)- Phase 0 — unblock CSP, first-boot probe, lockout UI (#26)- Phase 0.5 — drop CSP 'unsafe-inline' from script-src and style-src- Resolve hwmon symlinks in buildChipMap (#31)- Disambiguate multi-match chip_name via hwmon_device (#42)- Ensure /etc/ventd config ownership is ventd:ventd (#38)- Pre-validate generated config before the review screen (#32)- Gate case_curve on len(gpuFans) > 0 (#33)- Move OnFailure= to [Unit] so recovery actually fires (#66)- Honour hwmon_device= on single-candidate resolver match (#86)- Order ventd after systemd-udev-settle so hwmon chips enumerate first (refs #86) (#101)- Enforce 44×44 touch targets on slider thumb, checkbox, and edit-icon (refs #68) (#102)- Don't mistake a hwmon udev race for a missing config (#103) (#105)
### Security
- Security PR 1 — login rate limit, CSRF, cookies, body caps, setup token (#4)- Refuse to start when configured TLS cert/key are missing (#9)- Run ventd as User=ventd (#12)- V0.2.0 notes (#52)
### Tests
- Pure-logic tests for curve and calibrate (#18)- Decouple TestLoad_NoChipNameNoOpsBackwardCompat from host /sys (#49)- Decouple TestCheckResolvable_EmptyChipNameIgnored from host /sys (#63)
### Config
- ResolveHwmonPaths for chip-name-based re-anchoring (#20)- Auto-populate TLS paths from /etc/ventd/tls.{crt,key} on load (#71)
### Deploy
- Security PR 2 — systemd unit hardening (#6)- Own /etc/ventd via ConfigurationDirectory (#10)- Zero-terminal install + sandbox-correct module loading + ChipName auto-resolve (#21)- Make the README's safety promises actually true (#25)
### Hwmon
- Quiet module-persist warning under sandbox (#15)
### Install
- Preflight conflict checks for fan daemons and port 9999 (#8)- Refresh systemd units on every run so upgrades pick up unit changes (#65)
### Release-notes
- Add v0.2.0-readiness tracker for overnight ops (#64)- Audit gaps vs merged PR list (#67)- Update for #58/#60 closures (#72)- Record post-#70 VM matrix + goreleaser re-verify (#75)- Record #59 closure and PR #74 harness update (#78)- Add upgrade-path notes for #65 and #71 (#81)- Watchdog recovery highlight + readiness final state (#85)- Final handoff — touch-aware curve editor + CHANGELOG marked done (#91)
### Safety
- Refuse plaintext LAN binds; auto-gen TLS on first boot; XFF trust model (#5)
### Setup
- Reject wizard configs the resolver would refuse on next boot (#56)- One-click module remediation for missing hwmon chips (#73)
### Shutdown
- Integration test + goleak regression guard (#7)
### Style
- Switch body to system sans, keep mono for numeric readouts (#39)
### Validation
- Fresh-VM install smoke harness for v0.2.0 (#45)- Phoenix-MS-7D25 v0.2.0 final runbook (PR-F3.0) (#44)- V0.2.0 fresh-VM smoke — all four distros PASS (#50)- V0.2.0 final rig re-verify on phoenix-MS-7D25 (FAIL) (#54)- Proxmox-driven fresh-VM install smoke harness (#51)- V0.2.0 post-unit-fix cross-distro re-verify (#70)- --refresh-images flag + corrupted-cache recovery docs (#74)- Fix 0a.i/0a.iii false positives on real-world configs (#76)- V0.2.0 final rig re-verify on phoenix-MS-7D25 (PASS) (#80)- Upgrade-path smoke for TLS config migration (#82)- UFW rules for Incus bridge + enable procedure (#88)- Assert resolved pwm_path lives under configured hwmon_device (refs #86) (#100)
### Wip
- Phase 0.5 — utility CSS classes + index.html data-action conversion- Phase 0.5 group 1 — sensor cards data-action conversion- Phase 0.5 group 2 — fan cards data-action conversion- Phase 0.5 group 3 — curve cards data-action conversion- Phase 0.5 group 4 — curve editor data-action conversion- Phase 0.5 group 5 — header, sidebar, hw device toggle- Phase 0.5 group 6 — modals, setup wizard, last inline styles- PR-B0.5 checkpoint — state.js + api.js drafted, not yet wired## [v0.1.1] - 2026-04-14

### Documentation
- Add Tier 0.3 and install-smoke validation notes
### Install
- Use portable sha256sum check for busybox compat
### Release
- Dual-variant builds and musl-compatible installer
### Safety
- Shutdown-clean sessions, panic-safe watchdog, mode-guarded PWM- Consolidate JSON encoder logging and drain errCh on shutdown (#3)## [v0.1.0] - 2026-04-14

### Hwmon
- Drop ineffectual default in watcher.promote
### Install
- Auto-start the service and shrink post-install output
### Release
- Auto-publish release artifacts (drop draft gate)
### Scripts
- Make install.sh pipe-installable with release download + checksum verify