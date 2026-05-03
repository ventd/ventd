# R28 Codebase Audit — Go, Web, and Deploy Assets

**Date:** 2026-05-03
**Scope:** `/root/ventd-tests/internal/**`, `/root/ventd-tests/cmd/**`,
`/root/ventd-tests/tools/**`, `/root/ventd-tests/web/**` (JS/CSS/HTML), and
`/root/ventd-tests/deploy/`. Excluded by parallel-agent rules:
`internal/hwdb/profiles-v1.yaml`, `internal/hwmon/autoload.go`, the eight R28
decision-log items, and `.claude/rules/*.md`.
**Toolchain notes:** `go vet ./...` passes clean. No `golangci-lint` /
`staticcheck` / `deadcode` binaries are installed in this sandbox; this audit
relies on manual `grep`-based call-site verification, plus reading the
imports of every consumer of each candidate package. Network-restricted: I
could not `go install` linters.

---

## Summary

ventd is in solid shape for a 0.5-line release. The control-loop hot path
(`internal/controller/`) has been deliberately optimised (Opt-1 buffer
reuse, Opt-2 compiled-curve cache, Opt-3 atomic config-pointer load,
Opt-4 max-RPM cache); the HAL boundary is clean; the rule-binding
discipline is rigorously enforced. There is no `io/ioutil`, no
`strings.Replace(..., -1)`, and no log-line `fmt.Sprintf` in the per-tick
path. `go vet ./...` is clean.

The high-leverage finds are all in vestigial / partly-wired code, not in
the production hot path:

1. **`internal/coupling/signguard/`** is fully implemented + tested but
   has zero production importers — it is a stranded R27 prototype.
2. **`internal/doctor/`** holds one `CheckAMDOverdrive` function and its
   rule-binding test, but no `ventd doctor` subcommand exists to consume
   it.
3. **`internal/ndjson/`** is a 475-line schema-versioned NDJSON writer
   with zero production consumers (the diag bundle does not use it).
4. **9 of the 17 `internal/testfixture/` packages** (`base`,
   `fakecfg`, `fakedbus`, `fakeliquid`, `fakemic`, `fakenvml`,
   `fakesmc`, `fakeuevent`, `fakewmi`) are referenced only by their
   own `_test.go` files — total ≈ 200 lines, all dead.
5. **Two competing `install.sh`** files (top-level 9.6 KB and
   `scripts/install.sh` 57 KB) — README and goreleaser both point at
   the latter; the top-level is dead.
6. **`web/setup/probe.html`** is embedded but no HTTP route serves it.
7. **`atomicWrite` semantics** are open-coded ≥ 12 times across
   `internal/state/state.go`, `internal/calibrate/calibrate.go`,
   `internal/coupling/persistence.go`,
   `internal/marginal/persistence.go`,
   `internal/confidence/layer_a/persistence.go`,
   `internal/web/authpersist/persist.go`,
   `internal/web/selfsigned.go`,
   `internal/signature/hash.go`,
   `internal/grub/cmdline.go`,
   `internal/hwmon/autoload.go`,
   `internal/hwdb/capture.go`, and
   `internal/config/config.go` — one shared helper would shrink the
   surface area.

### Defect class × effort

| Class                  | S    | M    | L    | total |
| ---------------------- | ---- | ---- | ---- | ----- |
| obsolete-module        | 3    | 2    | 0    | 5     |
| dead-code              | 8    | 1    | 0    | 9     |
| duplicate              | 1    | 2    | 0    | 3     |
| inefficient            | 0    | 1    | 0    | 1     |
| deprecated-stdlib      | 0    | 0    | 0    | 0     |
| stale-comment          | 4    | 0    | 0    | 4     |
| build-weight           | 1    | 0    | 0    | 1     |
| slow-test              | 1    | 1    | 0    | 2     |
| web-cleanup            | 3    | 0    | 0    | 3     |
| spec-drift             | 5    | 0    | 0    | 5     |
| **total**              | **26** | **7** | **0** | **33** |

S = under 30 min, M = 1-3 hrs, L = 3+ hrs. No L-class items found.

---

## High-impact wins (top 10, should-ship-soon = yes)

In approximate ROI order. Every entry is S or low-M.

1. **Delete `internal/coupling/signguard/`** (212 lines incl. test).
   No importers; the rule file `.claude/rules/signguard.md` and its
   bound subtests would also need to be removed or marked
   `<!-- rulelint:allow-orphan -->` until v0.5.8's actual integration
   PR lands. Effort: S. **(Verify with the parallel rules-audit agent
   before deleting — `signguard.md` references it.)**
2. **Delete `internal/doctor/`** (125 lines incl. test). No `ventd
   doctor` subcommand; `RULE-EXPERIMENTAL-AMD-OVERDRIVE-03` would need
   re-binding to a place that's actually called, or downgraded.
   Effort: S.
3. **Delete `internal/ndjson/`** (475 lines incl. tests). Zero
   production consumers, no rule file binds to it. Effort: S.
4. **Delete the 9 stub `internal/testfixture/fake*` packages plus
   `base/`** that are never imported outside their own `_test.go`
   files. ≈ 200 lines. Effort: S.
5. **Delete top-level `/root/ventd-tests/install.sh`** (9 661 bytes).
   `README.md:127` and `.goreleaser.yml:144,175` both ship and document
   `scripts/install.sh`. The top-level file claims a different curl-pipe
   URL (`/main/install.sh`) which does not exist on the remote.
   Effort: S.
6. **Consolidate atomic-tempfile-rename into a single helper.** Twelve
   independent implementations duplicate the rand-suffix +
   `O_EXCL|O_CREATE` + `Sync` + `Rename` + dir-fsync sequence. Promote
   `state.atomicWrite` to an exported helper or move to a tiny
   `internal/atomicio` package and replace the others. Effort: M
   (mostly mechanical search-and-replace, but each site has its own
   subtle mode/permission choice that needs preserving for RULE-STATE-09
   and RULE-DIAG-PR2C-04).
7. **Delete `cmd/cowork-query/`** (566 lines). No `.cowork/` directory
   exists in the tree, no Makefile target invokes it, no test imports
   it. The labeller rule and drift-baseline workflow reference
   `.cowork/**` paths but never invoke this binary. Effort: S.
   *(Confirm with Phoenix that the cowork harness has been retired.)*
8. **Delete `web/setup/probe.html`** (and the `setup/probe.html`
   entry in `web/embed.go:9`). No `mux.Handle("/setup/probe...`)
   route; no JS references it; embedded but unreachable. Effort: S.
9. **Delete `examples/ventd.toml`** — TOML in a YAML-only project,
   no consumers, comment admits "TOML reference is provided for human
   readability only." Misleads users. Effort: S.
10. **Wire `idle.SetExtraBlocklist` to `Config.SignatureLearningDisabled`
    (or remove it).** The function is exported and bound by
    `RULE-IDLE-06`, but nothing in `cmd/ventd/main.go` ever calls it,
    so the operator-extended process blocklist documented in the rule
    is effectively a no-op. Either delete it (and amend the rule) or
    plumb the config field through. Effort: M.

---

## Obsolete modules

### `internal/coupling/signguard/`
- **Where:** `internal/coupling/signguard/signguard.go` (128 LOC) +
  `signguard_test.go` (89 LOC).
- **Defect class:** obsolete-module.
- **Current state:** Implements R27's wrong-direction polarity-prior
  detector. The package has a complete API (`NewDetector`, `Add`,
  `Confirmed`, `VoteWindow`, `VoteThreshold`, `NoiseFloorDelta`) and a
  rule file (`.claude/rules/signguard.md`) with three bound subtests.
  Zero production importers — `grep -r 'ventd/internal/coupling/signguard'
  internal/ cmd/ tools/` returns nothing.
- **Proposed change:** Delete the package and the rule file together,
  OR mark the rule entries `<!-- rulelint:allow-orphan -->` until the
  v0.5.8.1 wiring PR lands. The cleanest action is deletion until the
  consumer PR arrives — orphan code rots faster than orphan rules.
- **Risk if unchanged:** Maintenance cost (a real R27 fix that needs
  it has to remember the package exists); confusion for future
  contributors.
- **Effort:** S. **Should-ship-soon:** yes.

### `internal/doctor/`
- **Where:** `internal/doctor/checks/experimental.go` (41 LOC) +
  `experimental_test.go` (84 LOC).
- **Defect class:** obsolete-module.
- **Current state:** Single `CheckAMDOverdrive` function + struct.
  Bound by `RULE-EXPERIMENTAL-AMD-OVERDRIVE-03`. No `ventd doctor`
  subcommand exists — `grep -rn "ventd doctor\|/api/doctor"` returns
  nothing in `cmd/`, `internal/web/`, or any handler.
- **Proposed change:** Decide: either add a `ventd doctor`
  subcommand that consumes this entry (the catalog implies one is
  planned), or delete the package and reassign the rule binding to
  the corresponding `internal/experimental/checks/` test (which
  already exists).
- **Risk if unchanged:** Implementation drift; rule binding pins
  unreachable code.
- **Effort:** S to delete, M to wire. **Should-ship-soon:** yes
  (delete now, restore in the PR that ships `doctor`).

### `internal/ndjson/`
- **Where:** `reader.go`, `ring.go`, `schema.go`, `writer.go` plus
  tests (475 LOC total).
- **Defect class:** obsolete-module.
- **Current state:** Schema-versioned NDJSON writer + reader with
  ring-buffer rotation. Doc comment claims it is "shared between the
  diagnostic bundle (internal/diag) and the spec-05-prep trace
  harness." `internal/diag/` does not import it; `spec-05-prep` is
  not a real package. The `SchemaVersion` constants
  (`SchemaPWMWriteV1`, `SchemaThermalTraceV1`, `SchemaDiagBundleV1`,
  `SchemaVentdJournalV1`) are referenced only by ndjson's own tests.
- **Proposed change:** Delete entirely. The diag bundle uses
  msgpack (observation log) and JSON (REDACTION_REPORT,
  experimental-flags). NDJSON has no consumers and would need to be
  reintroduced if a future spec-05-prep harness genuinely lands.
- **Risk if unchanged:** 475 LOC of dead infrastructure; the
  `RotationPolicy` shape there is subtly different from the one in
  `internal/state/log.go`, which invites bugs.
- **Effort:** S. **Should-ship-soon:** yes.

### `cmd/cowork-query/`
- **Where:** `cmd/cowork-query/main.go` (566 LOC).
- **Defect class:** obsolete-module.
- **Current state:** CLI for inspecting `.cowork/events.jsonl`. No
  `.cowork/` directory in the tree; no Makefile target builds or runs
  it; goreleaser does not ship it. `.github/labeler.yml` and one
  workflow reference `.cowork/**` paths — likely an old session-log
  feature that was retired.
- **Proposed change:** Delete the directory. Confirm with Phoenix
  that cowork session-log tooling is gone before pulling the trigger.
- **Risk if unchanged:** 566 LOC dead-weight + a binary that is
  built by `go build ./...` for no reason.
- **Effort:** S. **Should-ship-soon:** yes (if Phoenix confirms).

### `internal/testfixture/{base,fakecfg,fakedbus,fakeliquid,fakemic,fakenvml,fakesmc,fakeuevent,fakewmi}`
- **Where:** 9 packages, ~200 LOC.
- **Defect class:** obsolete-module.
- **Current state:** Each is a 24-line stub (`Pump()`, `Cool()`,
  etc., recording calls into `base.CallRecorder`). `base` is only
  used by these 8 stubs; no other test code imports any of them. The
  six "real" testfixtures (`fakehwmon`, `fakehid`, `fakeipmi`,
  `fakeprocsys`, `fakepwmsys`, `faketime`, `fakedmi`, `fakedt`,
  `fakecrosec`) are widely used.
- **Proposed change:** Delete the 9 stub packages. If a future
  test wants a fakeNVML it can use the existing `fakenvml` real
  fixture under `internal/testfixture` … wait, there isn't one.
  These stubs are placeholders for fixtures that were never built
  out. They cost compile time on every `go test ./...`.
- **Risk if unchanged:** Misleads contributors who think there is
  fixture support for these subsystems. Compile + lint overhead.
- **Effort:** S. **Should-ship-soon:** yes.

---

## Dead code (table)

| File | Symbol | Lines | Note |
| ---- | ------ | ----- | ---- |
| `internal/calibration/result.go` | `SchemaVersion` const | 5 | Exported, zero callers — `grep -rn "calibration\.SchemaVersion"` is empty. |
| `internal/idle/cpuidle.go:95` | `CPUIdleRatio` (exported func) | 11 | `captureCPUStat` is captured into `Snapshot.CPUStat` but never consumed for any logic; the whole CPUStat path is vestigial. |
| `internal/idle/cpuidle.go:160` | `SetExtraBlocklist` | 5 | Bound by RULE-IDLE-06 but never wired from config. |
| `internal/idle/durability.go:41` | `(*durabilityState).IdleSince` | 1 | Zero callers. |
| `internal/observation/rotation.go` | (entire file) | 7 | Empty doc-only file; the actual rotation logic lives in `writer.go`. |
| `web/setup/probe.html` | embedded asset | 1 (in embed list) | No mux route. |
| `examples/ventd.toml` | example config | ~70 | TOML file in YAML-only project. |
| `deploy/github-mcp.service` | systemd unit | ~25 | Not in `.goreleaser.yml`'s `nfpms.contents`, not in install scripts; `grep -rn 'github-mcp'` returns nothing. |
| `cmd/ventd/calibrate.go:initCalibrationStore` | placeholder helper | 11 | Wraps `calibstore.NewStore` and is called once from `main.go:172` solely so "the integration path is exercised at build time even before the wizard calls runCalibrationProbe" — the `_ = ` discard signals it. The wizard now calls the real path. Inline-and-delete. |

Total dead-code LOC ≈ **140 production + 600 stub-fixture + 770 obsolete-package = ~1500 LOC**.

---

## Duplicates and consolidations

### atomic-tempfile-rename open-coded ≥ 12 times

**Files:**
- `internal/state/state.go:74` (`atomicWrite`, the canonical one)
- `internal/calibrate/calibrate.go:1218` (`atomicWriteBytes`)
- `internal/coupling/persistence.go:98` (inline)
- `internal/marginal/persistence.go:132` (inline)
- `internal/confidence/layer_a/persistence.go:121` (inline)
- `internal/web/authpersist/persist.go:97` (inline + `.bak`
  rollback variant)
- `internal/web/selfsigned.go:177` (inline)
- `internal/signature/hash.go:130` (inline, mode 0600)
- `internal/grub/cmdline.go:122` (inline)
- `internal/hwmon/autoload.go:926` (inline)
- `internal/hwdb/capture.go:79` (inline)
- `internal/config/config.go:843` (inline)

Each callsite re-implements the rand-suffixed tmp file +
`O_WRONLY|O_CREATE|O_EXCL` + `Write` + `Sync` + `Close` + `Rename`
sequence. Three of them additionally fsync the parent directory
(`state.atomicWrite`, `calibrate.atomicWriteBytes`, and one in
`authpersist`); the others omit it, which is a correctness gap on
power-loss. **Proposed:** introduce `internal/atomicio.WriteFile(path,
data, mode)` (or export `state.atomicWrite`) and migrate every site;
this both shrinks the codebase and tightens RULE-STATE-01 / RULE-DIAG-PR2C-04
durability across the board. Effort: M.

### Duplicate sentinel filtering

`internal/hal/hwmon/sentinel.go` (`IsSentinelRPM`, `IsSentinelSensorVal`)
is the canonical sentinel filter; bound by RULE-HWMON-SENTINEL-*. A
second filter exists at the JSON-serialise boundary in
`internal/monitor/` (`isSentinelMonitorVal`, called from `Scan()`).
RULE-HWMON-SENTINEL-STATUS-BOUNDARY explicitly mandates the
double-filter, so this is intentional, not a duplicate. Document the
relationship more clearly in `sentinel.go`'s package comment but do
**not** consolidate.

### Two RotationPolicy types
`internal/state.RotationPolicy` and `internal/ndjson` (the latter
under `ring.go`) carry similar fields. Once `ndjson` is deleted (see
above), this defect resolves.

---

## Inefficient patterns (controller hot path)

The controller's tick (`internal/controller/controller.go:411`) is
already optimised:

- **Opt-1:** `rawSensorsBuf`, `smoothedBuf`, `sentinelBuf`,
  `sensorInvalidSince` are pre-allocated maps reused per tick.
- **Opt-2:** `compiledCurve` cached across ticks; rebuild only on
  config-pointer change OR when the comparable `curveSig` differs.
- **Opt-3:** `cfg.Load()` exactly once per tick.
- **Opt-4:** `fan*_max` cached on first RPM-target write.
- No `fmt.Sprintf` in the hot path. No `strings.Builder` candidates
  remaining.

The only candidate I found anywhere near the hot path:

### `internal/observation/writer.go:175` — `MarshalRecord` on every Append

`MarshalRecord` allocates a fresh msgpack buffer per record. At the
project's stated 0.5 Hz – 2 Hz controller cadence this is ~2-8
KB/s of churn — invisible to GC, fine. Worth flagging only because
the comment in `record.go` claims privacy review, not performance.
**No change recommended for v0.5.11**; revisit if controller cadence
ever ticks faster than 5 Hz.

### Single legitimate `fmt.Sprintf` finding
`internal/observation/writer.go:252` —
`key := fmt.Sprintf("%s/%d", kvClassPrefix, id)` runs once per
channel during construction. Not hot path. No change.

---

## Deprecated stdlib

**Clean.** `grep -rn '"io/ioutil"'` returns nothing. No `strings.Replace
(.*-1)` pattern. No `flag.Bool` patterns that would benefit from
`cmp.Or`. The codebase is on Go 1.25.9 and uses `os.ReadFile` /
`os.WriteFile` everywhere. **Should-ship-soon:** N/A — already done.

The one nit: `internal/calibrate/calibrate.go:1220` and
`internal/state/state.go:80` use `crypto/rand.Read(suf[:])` for an
8-byte tmp-suffix. Go 1.24 added `rand.Text()` for short random
identifiers — these would become slightly cleaner with `rand.Text()
[:16]`, but the saving is cosmetic only.

---

## Stale comments

| File:line | Comment | Status |
| --------- | ------- | ------ |
| `internal/web/security.go:53` | `// TODO: raise to 31536000 (1 year) once deployments are stable.` | Still relevant — should become an issue. |
| `internal/hwdiag/hwdiag.go:71` | `// empty the UI still shows the button but disables it with a TODO tooltip` | Still relevant — should become an issue. |
| `tools/regresslint/main.go:233` | `// TODO: flip -strict=true once the backlog of unlabeled closed bugs is triaged (TX-REGRESSION-AUDIT)` | Should become an issue (TX-REGRESSION-AUDIT). |
| `internal/preflight/checks/secure_boot.go:258` | `"ventd-XXXX" where XXXX is 4 hex chars from crypto/rand` | Not a TODO — comment uses XXXX as a literal placeholder. False positive. |

The codebase is exceptionally clean of stale TODO/FIXME/HACK markers
— total of three real entries. Spec-version drift is more common
(see § Spec drift below).

---

## Build / dependency cleanup

### `go.mod` audit

| Module | Used by | Verdict |
| ------ | ------- | ------- |
| `github.com/dchest/siphash` | `internal/signature/hash.go` (RULE-SIG-HASH-01) | needed |
| `github.com/ebitengine/purego` | `internal/nvidia/nvidia.go` (purego dlopen for NVML) | needed |
| `github.com/go-rod/rod` | `internal/web/e2e_test.go` only | **flag** |
| `github.com/vmihailenco/msgpack/v5` | observation, marginal, coupling, confidence, envelope | needed |
| `golang.org/x/crypto` | `internal/web/auth.go` (bcrypt only) | needed |
| `golang.org/x/sys` | several | needed |
| `gonum.org/v1/gonum` | `internal/coupling`, `internal/marginal` (RLS Sherman-Morrison) | needed |
| `gopkg.in/yaml.v3` | config, hwdb | needed |
| `go.uber.org/goleak` | several `_test.go` files | needed |

**`github.com/go-rod/rod`** is a transitive heavy chain
(`fetchup`, `goob`, `got`, `gson`, `leakless`). It is used by exactly
one test file (`internal/web/e2e_test.go`). The test already
`t.Skipf`s when no Chromium is available. Two options:
1. Move e2e tests behind a build tag (`//go:build e2e`) and add a
   `make e2e` target. This drops the rod dependency from default
   `go test` builds.
2. Keep as-is.

Option 1 is mechanical (one tag + a Makefile line) and saves CI
time. **Effort:** S. **Should-ship-soon:** no — defer to v0.6.0
unless e2e timing becomes painful.

### Embedded files

`web/embed.go:9` lists `setup/probe.html` (see § Dead code).
Otherwise the embed list matches the actual served pages.

---

## Tests

### Slow tests
- `internal/calibrate/safety_test.go` uses real-time `time.Sleep`
  with `ZeroPWMMaxDuration = 2s` 12 times — each subtest waits ≥ 2 s
  for the sentinel to fire. Total wall-clock ≈ 25 s for the file.
  **Proposed:** inject the timer via the existing
  `faketime` fixture so the suite collapses to milliseconds.
  **Effort:** M (the sentinel uses `time.AfterFunc`, so refactoring
  needs a clock-injection seam). **Should-ship-soon:** yes — paid
  back across every CI run.

### Race-detector skip
`internal/web/schedule_test.go:216` —
`t.Skip("FIXME(#812): pre-existing race on arm64 race-detector run...")`.
A pinned skip on a known race that was filed but never fixed.
**Proposed:** prioritise issue #812 in the v0.5.11 backlog or
re-design the override-clears-on-boundary subtest to be deterministic.
**Effort:** S to triage; unknown to fix. **Should-ship-soon:** investigate.

### Fixture re-mocking
The setup wizard tests under `internal/web/` re-construct
`calibrate.Manager` fixtures inline rather than via a shared helper.
≥ 14 imports of `internal/calibrate` from `internal/web/*_test.go`
suggest there is a useful test helper waiting to be extracted, but
this is style rather than a real defect. **No action.**

---

## Web layer

### CSS hex/rgba violations
RULE-UI-02 mandates token-only colours in page-specific CSS. After
filtering out comment lines (issue references like `#821`, `#800`),
**all page CSS is clean** — zero literal colour values outside
`web/shared/`. Good.

### Demo-mode fallback
`web/dashboard.js` retains 16 references to a `demo` mode that polls
a synthetic data loop when the API is unreachable. With the API
surface stable in v0.5.x, this is now mostly UI polish. Worth a pass
to see whether the synthetic loop should be replaced with a "Daemon
unreachable" banner.

| File | Lines | Demo refs | Verdict |
| ---- | ----- | --------- | ------- |
| `web/dashboard.js` | 746 | 16 | Functional but verbose; consider trimming to a single banner. |
| `web/sensors.js` | 244 | 2 | One catch + one demo() — minimal; keep. |
| `web/settings.js` | 342 | 2 | "demo" suffixes on listen address + commit when API absent — keep. |

### Inline event handlers
**Zero `onclick=`** in any `web/*.html`. Already addEventListener
throughout. Good.

### `web/setup/probe.html`
Embedded but not routed. Delete (see § Dead code). Effort: S.

### File sizes
No JS file exceeds 1500 lines. The largest is `calibration.js` at
1177 lines (within ladder; no split needed for v0.5.11).

---

## Spec drift

Five comment-level references that point at older spec versions:

| File:line | Stale reference | Suggested edit |
| --------- | --------------- | -------------- |
| `internal/fallback/role.go:58` | `// preserves v0.4.x` | "preserves the reactive curve baseline" |
| `internal/web/profiles.go:31` | `(v0.2.x config, or a v0.3 config that hasn't defined any yet)` | drop the version range |
| `internal/controller/blended.go:6,214` | `existing v0.4.x reactive curve` / `the v0.4.x output` | "the reactive curve" |
| `internal/hal/liquid/corsair/{corsair,probe,devices}.go` | "deferred to v0.4.1", "Empty for v0.4.0", "iCUE LINK System Hub — deferred to v0.4.1." | refresh against v0.5.11 reality |
| `internal/hal/liquid/corsair/protocol.go:58` | `// read LED count (not used in v0.4.0)` | drop the version |

All trivial documentation-only edits. **Should-ship-soon:** yes —
bundle into the cleanup PR.

---

## Recommended Stage 1.5 cleanup PR plan

Two PRs maximise reviewability:

### PR 1 — "Stage 1.5: drop vestigial code"
Single mechanical PR. Reviewable in 30 minutes. No behaviour change.

- Delete `cmd/cowork-query/` (566 LOC) — confirm with Phoenix first.
- Delete `internal/coupling/signguard/` (212 LOC) + amend
  `.claude/rules/signguard.md` (or add `<!-- rulelint:allow-orphan -->`
  per the docs-first workflow).
- Delete `internal/doctor/` (125 LOC) + reassign
  RULE-EXPERIMENTAL-AMD-OVERDRIVE-03 binding to
  `internal/experimental/checks/` (the test exists already — it is
  only the binding that has to move).
- Delete `internal/ndjson/` (475 LOC).
- Delete the 9 stub testfixture packages
  (`base`, `fakecfg`, `fakedbus`, `fakeliquid`, `fakemic`,
  `fakenvml`, `fakesmc`, `fakeuevent`, `fakewmi`) (~200 LOC).
- Delete top-level `install.sh` (9.6 KB).
- Delete `web/setup/probe.html` and remove from `web/embed.go`.
- Delete `examples/ventd.toml`.
- Delete `deploy/github-mcp.service`.
- Delete `internal/observation/rotation.go` (placeholder file).
- Delete `internal/calibration/result.go` (`SchemaVersion` const).
- Inline + delete `cmd/ventd/calibrate.go::initCalibrationStore`.
- Delete `internal/idle/cpuidle.go::CPUIdleRatio` and the
  `CPUStat` field on Snapshot if not consumed (verify with
  parallel hardware-catalog agent that none of the autoload code
  reads CPUStat).
- Delete `(*durabilityState).IdleSince` (`internal/idle/durability.go:41`).

Net: **~2 200 LOC removed** + 1 systemd unit + 2 stale shell scripts
+ embed-list cleanup.

**CI gates:** `go vet ./...` passes; `make test` passes; `make
lint` passes (rulelint will flag the moved binding immediately).

### PR 2 — "Stage 1.5: consolidate atomic-write helper + spec drift"
Slightly higher risk because it touches twelve persistence sites.

- Add `internal/atomicio/atomicio.go` (or export
  `state.atomicWrite` as `state.AtomicWrite` and document the
  contract). Include the dir-fsync that three sites currently miss.
- Migrate the 12 callsites listed under § Duplicates. Preserve
  per-site mode (0o600 for redactor, 0o640 for state, 0o644 for
  config, etc.).
- Refresh stale spec-version comments listed under § Spec drift.
- Wire `idle.SetExtraBlocklist` to
  `Config.SignatureLearningDisabled` (or similar) — pick whichever
  config key the operator-extended blocklist is meant to read, or
  delete the function and amend RULE-IDLE-06.

**CI gates:** `go vet ./...`; `make test` (RULE-STATE-01,
RULE-DIAG-PR2C-04, RULE-STATE-09 must still pass); `make
safety-run`.

### Items deferred past v0.5.11

- `internal/calibrate/safety_test.go` clock-injection refactor
  (test-speed; not user-visible).
- `go-rod/rod` build-tag isolation (CI speed; risk of
  goimports churn).
- `crypto/rand.Read` → `crypto/rand.Text` for tmp-suffix (cosmetic).
- Dashboard demo-mode trimming (UX polish, not regression risk).
- Issue #812 race-skip resolution (separate investigation).

---

## Closing note

ventd does not have a maintainability problem. It has the residue of
five months of fast iteration: half-built features whose code shipped
ahead of their wiring (signguard, doctor), prototype tooling that
served its purpose and was forgotten (cowork-query, ndjson), and 12
places that learned the same atomicWrite pattern independently. All
of it is mechanical to remove. The signal-to-noise ratio of the
production hot path is high; the rule-binding discipline is doing
real work; the HAL boundary is clean. The two-PR plan above brings
the line count down by ~2 200 LOC without touching any behaviour
that ships.
