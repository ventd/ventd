# R28 Stage 1.5 — synthesis of four audit reports

**Date:** 2026-05-03
**Inputs:** four parallel audit agents, all complete:
- `2026-05-r28-catalog-audit.md` — 47 catalog files + autoload.go (6 P0, 7 P1, 5 P2)
- `2026-05-r28-rule-audit.md` — ~70 rule files (3 P0, 8 P1, 7 P2, 6 missing rules + 2 sibs)
- `2026-05-r28-decision-log-resolutions.md` — 8 R28 master §5 items resolved with primary sources
- `2026-05-r28-codebase-audit.md` — Go + web + scripts (~2200 LOC dead code, 12 atomic-write reimpls)

This doc ranks the union into one PR sequence Phoenix can land in order. Each row has a clear blast radius, dependency, and "why not later" note.

## Cross-agent reconciliations (where audits contradicted or sharpened each other)

| # | Topic | Catalog audit | Rule audit | Decision-log | Resolution |
|---|---|---|---|---|---|
| R1 | NCT6797D / 0xd450 | P2-1 ("verify against driver source") | – | §5.1: **0xd450 is unambiguously NCT6797D in mainline nct6775**; Agent C's "nct6687d misidentifies" claim was wrong about mechanism (nct6687d keys off hwmon-name, not Super-I/O ID) | Catalog P2-1 collapses. **No code change** — but audit any ventd row that conflates 0xd450 → NCT6687. |
| R2 | ThinkPad fan2_input=65535 kernel commit | – | P1: rule body cites v6.2 | §5.5: actually landed **v6.1** (Jelle van der Waa, 2022-10-19, commit `a10d50983f7`) | Update rule-body comment from v6.2 → v6.1. No behaviour change. |
| R3 | RDNA3 OD_FAN_CURVE all-zero fix | – | "missing RULE-EXPERIMENTAL-AMD-OVERDRIVE-05" | §5.7: fix `3e6dd28a110` landed **v7.0**, NOT v7.1 as R28 master claimed. Affects `smu_v13_0_0` / `smu_v13_0_7` = RX 7900 XTX/XT/GRE. | New rule gates on `kernel < 7.0` not `< 7.1`. |
| R4 | it87 per-driver branching | P0-2 ("apply unconditionally") | – | §5.3: ventd does **not** branch kernel-version today; per-driver `ignore_resource_conflict=1` (kernel ≥6.2, commit `12c44ab8b40`) is only a *future Stage 1B/1C entry* | Catalog P0-2 is real. Need new RULE-MODPROBE-OPTIONS-04 to gate. |
| R5 | Steam Deck `steamdeck-hwmon` | P1: missing rows | – | §5.6: driver is **NOT in mainline** as of v7.1-rc1; OOT-only via `philipl/steamdeck-kernel-driver` DKMS or SteamOS kernel | Catalog rows must declare `capability: ro_pending_oot` with the DKMS source, not assume mainline. |
| R6 | dell-smm-hwmon `restricted=0` | P0-1: notes recommend it | – | §5.2: ventd behaviour is already correct (never sets `restricted=0`); only the misleading comment in `boards/dell.yaml:54` needs removing | Catalog P0-1 narrows: comment-only fix, not a behaviour fix. |
| R7 | IT8689E mainline kernel landing | P1-7: missing kernel gate | – | §5.8: confirmed **v7.1** (only tag containing `66b8eaf8def`) | Catalog P1-7 is correctly framed; gate on `kernel ≥ 7.1`. |
| R8 | NVML helper recursion guard | – | – | §5.4: rule structurally correct; new datacenter-GPU work has test obligation only | No code change. Test bound check belongs to S2-5 PR. |
| R9 | `internal/doctor/` package | – | – | – (codebase audit only) | **Keep.** Task #71 in-progress (v0.5.10 Doctor surface). Codebase audit's "delete" recommendation is wrong — package is scaffolding for active work. |
| R10 | `internal/coupling/signguard/` | – | 3 active rule bindings (`RULE-SGD-VOTE-01/NOISE-01/CONT-01`) | – | **Keep** rules. Codebase audit says "zero Go importers" — that's a real wiring gap to investigate, but **don't delete the package without first auditing whether v0.5.9 PR-A.3 should have wired it.** |
| R11 | `internal/ndjson/` | – | – | – | **Keep.** Task #64 pending (maintainer-side diag-ingest endpoint #809). Wire format candidate. |

## Ranked PR sequence — Stage 1.5

Bundle into the smallest number of PRs that don't fight each other on the same files. Each PR independent and revertible.

### PR-1 — comment / doc surgery (S, ~50 LOC, no schema change, no behaviour change)

Pure text edits. Lowest risk, fastest review.

- Fix `boards/dell.yaml:54` misleading comment about `restricted=0` (catalog P0-1 / decision-log §5.2).
- Update RULE-HWMON-SENTINEL-FAN's rule body: cite kernel commit `a10d50983f7` landed **v6.1** (was v6.2) (decision-log §5.5).
- Update rule body of RULE-PROBE-* container/virt rules to remove "v6.2" claims that should be "v6.1".
- 5 spec-drift comments referencing v0.4.x in v0.5.11 codebase (codebase audit) — replace or remove.
- Remove the dead top-level `install.sh` (codebase audit) — README + goreleaser both ship `scripts/install.sh`.
- Remove `web/setup/probe.html` (codebase audit) — embedded but no route serves it.

**Why first:** zero risk, clears the lowest-effort items, pulls noise out of the diff for subsequent PRs.

### PR-2 — catalog YAML defects (M, ~300 LOC YAML, no schema change)

All YAML edits to `internal/hwdb/catalog/{boards,chips,drivers}/*.yaml`. No Go changes.

From catalog audit:
- **P0-3** Add MS-7D25 row to `boards/msi.yaml` — Phoenix's HIL board, currently DMI-only via autoload.go.
- **P0-4** Add `fan_control_blocked_by_bmc: true` overlay to HPE Gen10/Gen11 ProLiant board rows (R28 §5.4 / Finding 4).
- **P1-3** Replace `experimental=1` with `fan_control=1` in `drivers/thinkpad_acpi.yaml`.
- **P2-1** Audit any catalog row conflating 0xd450 → NCT6687; correct to NCT6797D (decision-log §5.1).
- **P2-2** Add `superio_chip` anchor to `generic-nct6798`.
- **P1-2** Steam Deck Jupiter + Galileo board rows with `capability: ro_pending_oot` and the DKMS source declared (decision-log §5.6).

**Why second:** catalog edits don't touch Go code; if a row has the wrong shape, schema-validation tests fail loudly at build time. Independent of PR-3.

### PR-3 — atomic-write helper consolidation (M, ~250 LOC Go, no schema change, fixes correctness gap)

Codebase audit found 12 atomic-write reimplementations across packages, **3 missing dir-fsync**. This is a correctness defect, not just a style cleanup — without dir-fsync, a power-loss after rename can leave the directory entry pointing at an empty file.

- New `internal/iox/atomicwrite.go` — single canonical helper with the full `tempfile + fsync + rename + dir-fsync` pattern.
- Migrate the 12 call sites (`state`, `calibrate`, `coupling`, `marginal`, `confidence`, `web/authpersist`, `web/selfsigned`, `signature`, `grub`, `hwmon/autoload`, `hwdb/capture`, `config`).
- Add `RULE-IOX-01` (atomic-write contract) bound to a new test that asserts the helper survives a synthetic crash mid-rename.

**Why third:** independent of catalog work. Fixes a real bug. Is exactly the kind of structural cleanup that pays back across every package.

### PR-4 — dead-code removal (M, ~1500 LOC removed, no behaviour change)

Pure deletion. Run after PR-3 so atomic-write call sites are migrated first (otherwise the dead helpers might look "in use").

Safe to delete (no roadmap conflict, codebase audit confirmed zero importers):
- `cmd/cowork-query/` (566 LOC) — no `.cowork/` dir on disk.
- 9 stub `testfixtures/fake*` packages.
- Dead exports across `calibration`, `idle`, `durabilityState`, `cmd/ventd/calibrate.go`.

**Hold for Phoenix's review (do NOT include):**
- `internal/doctor/` — #71 in-progress (R9 reconciliation).
- `internal/coupling/signguard/` — 3 rules bound, may be legitimate wiring gap (R10).
- `internal/ndjson/` — #64 pending (R11).

**Why fourth:** smaller blast radius once PR-3 has resolved the duplicates that contained call sites.

### PR-5 — schema v1.3 + kernel-version-gated catalog rows (L, ~600 LOC, schema change + new rules)

Catalog audit's PR B + decision-log resolution items that need a schema field that doesn't exist yet.

Schema additions:
- `kernel_version: {min: "X.Y", max: "X.Y"}` on driver rows.
- `is_pump: bool` and `pump_floor_pwm: int` on fan rows.
- `blacklist_before_install: [string]` on driver rows.

New rules (4 from rule audit, 2 from decision-log, 2 schema):
- `RULE-PUMPFLOOR-20` (rule audit P0; hardware-damage potential).
- `RULE-THERMABORT-21` (rule audit P0; throttle-distorted calibration).
- `RULE-EXPERIMENTAL-AMD-OVERDRIVE-05` (decision-log §5.7; gate on kernel < 7.0).
- `RULE-MODPROBE-OPTIONS-04` (decision-log §5.3; per-driver branching for it87).
- `RULE-HWDB-PR2-15` and `RULE-HWDB-PR2-16` (schema-validation rules for the new fields).

Catalog edits enabled by the schema:
- **P0-2** it87 `ignore_resource_conflict=1` gated on kernel <6.2 (cmdline path) and ≥6.2 (per-driver path).
- **P0-5** `generic-nct6799` gated on kernel <6.5 (already-native case).
- **P0-6** AIO pump rows (Aquacomputer, ASUS Ryujin, Gigabyte Waterforce) get `is_pump: true` + `pump_floor_pwm: 153` (60% of 255).
- **P1-1** `nct6683` declares `blacklist_before_install: [nct6683]`.
- **P1-5** RDNA4 catalog overlay.
- **P1-7** IT8689E gated on kernel ≥7.1 (decision-log §5.8 confirmed).

New it87 modprobe-option allowlist entries (extends #841's allowlist):
- `it87 → ignore_resource_conflict=1` (kernel ≥6.2 only, gated by RULE-MODPROBE-OPTIONS-04).
- `it87 → force_id=0x8689` (per detected chip ID).

**Why fifth:** schema bumps are heavyweight; needs migrations + new rules + bound tests. Bundled so the schema bump justifies the work and every kernel-gated entry uses the new field consistently.

### PR-6 — RPM cap class-aware (M, ~150 LOC + tests)

Rule audit P0 — `PlausibleRPMMax=10000` rejects valid Delta/Sanyo Denki server fans (12k–22k). Operator-visible bug today on every server-class host.

- Refactor `IsSentinelRPM(raw)` → `IsSentinelRPM(raw, class sysclass.SystemClass)`.
- `ClassServer` → 25 000 RPM cap. All other classes keep 10 000.
- Update `RULE-HWMON-SENTINEL-FAN-IMPLAUSIBLE` rule body to document the class-aware behaviour.
- Add fixtures + bound test for the server-class branch.

**Why sixth:** independent of all earlier PRs but touches a hot-path safety predicate. Worth landing alone so any regression is unambiguous.

### PR-7 — sub-rules + threshold reconciliation (M, ~200 LOC + tests)

Rule audit P1s.

- RULE-PROBE-02 add a 4th source (cpuinfo `hypervisor` flag) to close MicroVM/Firecracker recall gap; keep ≥3 threshold (rule audit's specific recommendation).
- RULE-CALIB-PR2B-01/02/03 reconcile 200-RPM threshold with RULE-POLARITY-03's 150-RPM threshold; pick one.
- RULE-CAL-ZERO-DURATION — extend default to 5 s OR add a per-class override for NAS-HDD fans (rule audit P1).
- RULE-ENVELOPE-14b (time-delayed BIOS revert at t+1, t+5, t+15) — sibling extension (rule audit's missing-sib).
- RULE-ENVELOPE-14c (range-selective override) — sibling extension.

**Why seventh:** these are smaller-blast tweaks to existing rules, but each requires its own test fixtures. Bundling them keeps the rule-tweak diff coherent without spreading across many small PRs.

### PR-8 — slow-test cleanup + race-skip resolution (S, ~100 LOC)

Codebase audit found `internal/calibrate/safety_test.go` does 12 real-time `time.Sleep ≥2s` calls. Clock injection collapses it to milliseconds. Plus the `internal/web/schedule_test.go:216` race-skip from #812 that was never resolved.

**Why last:** test-only changes, low priority but easy when you're already touching the surrounding code.

## Out of scope — Phoenix-decision items

These need explicit Phoenix call before they can ship:

1. **Delete `internal/doctor/`?** Codebase audit says yes (no `ventd doctor` subcommand exists); task #71 says no (in-progress). Phoenix to confirm whether v0.5.10 work is alive or shelved.
2. **Delete `internal/coupling/signguard/`?** 3 rules bound, zero Go importers — this is a wiring gap, not a dead module. The right fix may be to wire signguard into v0.5.9 PR-A.3, not delete it. Phoenix to confirm signguard's intended consumers.
3. **Delete `internal/ndjson/`?** Task #64 pending. Phoenix to confirm whether the diag-ingest endpoint will use it (keep) or a different format (delete).
4. **Smart-mode load test design** (`2026-05-smartmode-loadtest-design.md`) — three new endpoints proposed; Phoenix asked to "sit on" the implementation. Stays parked.

## Estimated effort

| PR | Effort | LOC | Risk | Lands when |
|---|---|---|---|---|
| PR-1 | S | ~50 | none | tonight |
| PR-2 | M | ~300 | low (YAML only) | tonight |
| PR-3 | M | ~250 | medium (touches every package) | tonight or tomorrow |
| PR-4 | M | -1500 | low (deletion) | after PR-3 |
| PR-5 | L | ~600 | medium (schema bump) | tomorrow |
| PR-6 | M | ~150 | low (well-tested predicate) | tomorrow |
| PR-7 | M | ~200 | low (threshold tweaks) | this week |
| PR-8 | S | ~100 | none | this week |

**Net diff:** ~+700 / -1750 = ~-1050 LOC, plus 8+ new rules, plus a real correctness fix (atomic-write dir-fsync), plus catalog correctness for hostile hardware (HPE iLO + dell-smm + Steam Deck + RDNA3/4 + MS-7D25).

## Recommended starting point

PR-1 first — pure-text, zero risk, takes the easy wins out of the way and clears noise from subsequent diffs. Auto mode says execute, so I'll open PR-1 next unless Phoenix overrides.
