# Pass 3 — Rule↔Binding Integrity (RULE-STATE-* sub-pass)

**Date**: 2026-05-11
**Baseline commit**: `f95ba76` (main after pass-3-wd)
**Scope**: every `RULE-STATE-*` rule file in `docs/rules/RULE-STATE-NN.md` plus `RULE-STATE-MIGRATION-V1-V2-NOOP.md` — **11 rules / 17 bindings**.

## Inventory

11 rule files (single-h1 format): RULE-STATE-01 atomic write, -02 blob SHA256, -03 log O_DSYNC, -04 torn-record skip, -05 schema version, -06 PID file, -07 transaction atomic commit, -08 log rotation, -09 file mode repair, -10 directory bootstrap, -12 free-space guard (the recent #1066-era rule), -MIGRATION-V1-V2-NOOP.

(Note: there is no RULE-STATE-11 — the number was skipped.)

## Findings

### SOLID (11/11)

Every rule's bound subtest exercises the production primitive directly:

| rule | bound test | how exercised |
|---|---|---|
| RULE-STATE-01 | TestRULE_STATE_01_AtomicWrite | invokes `db.Set` against an `openKV(tmpdir)`; pre-seeds a stale `.tmp.*` to simulate prior-crash; asserts tempfile + rename + fsync semantics |
| RULE-STATE-02 | TestRULE_STATE_02_BlobSHA256Verification | exercises `BlobDB.Read` against synthetic magic / length / SHA256 mismatches; verifies the `found=false` contract on any mismatch |
| RULE-STATE-03 | TestRULE_STATE_03_LogOAppendODsync | static analysis on `log.go` plus runtime check that flags include `O_APPEND \| O_DSYNC` |
| RULE-STATE-04 | TestRULE_STATE_04_LogTornRecordSkip | injects torn payload + CRC-mismatched record; asserts iteration skips and continues |
| RULE-STATE-05 | TestRULE_STATE_05_SchemaVersionCheck | invokes `CheckVersion(dir)` with downgrade / upgrade / missing-file fixtures; asserts `errors.Is(ErrDowngrade)` + migration dispatch |
| RULE-STATE-06 | TestRULE_STATE_06_PIDFileMultiProcess | invokes `AcquirePID(dir)` twice with a live + stale PID; asserts `*ErrAlreadyRunning` + stale-PID overwrite |
| RULE-STATE-07 | TestRULE_STATE_07_TransactionAtomicCommit | exercises `db.WithTransaction(fn)` with both happy + rollback paths; asserts post-transaction state matches contract |
| RULE-STATE-08 | TestRULE_STATE_08_LogRotationNoRecordLoss | constructs LogDB, sets rotation policy, exercises rotation with concurrent writers; asserts no append-after-rename window |
| RULE-STATE-09 | TestRULE_STATE_09_FileModeRepair | seeds state.yaml at mode 0600; invokes openKV; asserts `repairMode` chmods to 0640 + WARN logged |
| RULE-STATE-10 | TestRULE_STATE_10_DirectoryBootstrap | invokes `state.Open(missing-dir, logger)`; asserts initDirs creates the full hierarchy |
| RULE-STATE-12 | TestRULE_STATE_12_FreeSpaceGuard (4 subtests) | injects via `ensureFreeSpaceFn` seam; asserts `Set` / `Delete` / `WithTransaction` ALL refuse before in-memory mutation; on-disk content unchanged; seam restored lets subsequent writes pass |
| RULE-STATE-MIGRATION-V1-V2-NOOP | 4 subtests | `v1_to_v2_migrator_is_registered`, `upgrade_v1_to_currentVersion_runs_migrator_and_bumps_sentinel`, `noop_migrator_does_not_mutate_sibling_files_in_state_dir`, `currentVersion_is_at_least_2` — exercises every clause of the rule |

### BORDERLINE / WEAK / GHOST

**None.**

## Why this family audits so cleanly

Same pattern as RULE-WD-*: the state primitives (`KVDB`, `BlobDB`, `LogDB`, `CheckVersion`, `AcquirePID`) are pure functions / methods on a state-store type, callable in isolation against `t.TempDir()`. No "drive Manager.run end-to-end" plumbing needed.

The trickiest rule in the family — RULE-STATE-12, which makes claims about call-site behaviour ("Set / Delete / WithTransaction MUST refuse before mutating in-memory state") — is solidly bound through the `ensureFreeSpaceFn` seam. The test invokes the real `db.Set` and asserts both in-memory AND on-disk state remain pre-mutation. This is exactly the right shape for a "claim about call site" rule, and it's the model the smart-mode-wiring WEAK rules (#1075) should adopt.

## Running tally (6 sub-passes complete)

| sub-pass | total | SOLID | BORDERLINE | WEAK | GHOST |
|---|---|---|---|---|---|
| smart-mode-wiring | 5 | 2 | 0 | 3 | 0 |
| RULE-CPL-* | 15 | 13 | 2 | 0 | 0 |
| RULE-CMB-* | 26 | 22 | 4 | 0 | 0 |
| RULE-POLARITY-* | 11 | 9 | 1 | 0 | 1 (filed) |
| RULE-WD-* | 11 | 11 | 0 | 0 | 0 |
| RULE-STATE-* | 11 | 11 | 0 | 0 | 0 |
| **total** | **79** | **68** | **7** | **3** | **1** |

## Filing

No fileable issues from this sub-pass.

## Next

RULE-HWMON-* (18 rules in hwmon-safety.md + hwmon-sentinel.md combined). Per the heuristic, expect predominantly SOLID — pure-function tests against `fakehwmon` fixtures.
