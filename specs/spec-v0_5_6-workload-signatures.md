# spec-v0_5_6 — Workload signature learning + classification

**Status:** DESIGN. Drafted 2026-05-01.
**Ships as:** v0.5.6 (sixth smart-mode behaviour patch).
**Depends on:**
- v0.5.4 passive observation log (shipped) — supplies the per-tick
  Record stream and the `signature_label` / `signature_promoted`
  fields that v0.5.6 populates. No schema bump.
- v0.5.3 idle gate / R5 blocklist (shipped) — supplies the
  maintenance-class process list that v0.5.6 reuses as a positive-
  label dictionary.
- v0.5.0.1 spec-16 persistent state (shipped) — supplies the KV
  storage shape used for the signature library.

**Consumed by:**
- v0.5.7 Layer B coupling — uses `signature_label` to disambiguate
  workload-conditional thermal coupling matrices.
- v0.5.8 Layer C marginal-benefit — keys per-(channel, workload-
  signature) RLS state on the labels v0.5.6 emits.
- v0.5.9 confidence-gated controller — `conf_C` rises with per-
  signature residual stability.
- v0.5.10 doctor — the `--reveal-signatures` CLI flag (R7 §Q6) lands
  with doctor; v0.5.6 ships the underlying mechanism, v0.5.10 the
  surface.

**References:**
- `specs/spec-smart-mode.md` §6.6 (workload signature learning) and
  the new §6.6.1 to be amended in by this patch.
- `specs/spec-12-amendment-smart-mode-rework.md` §3.5
  (`RULE-UI-SMART-07` — Settings toggle), §5 (mockup work
  assignment).
- `specs/spec-v0_5_4-passive-observation.md` and
  `docs/research/r-bundle/ventd-passive-observation-log-schema.md`
  (record schema; `SignatureLabel`, `SignaturePromoted`,
  `EventFlag_SIGNATURE_PROMOTED`, `EventFlag_SIGNATURE_RETIRED`
  already reserved).
- `docs/research/r-bundle/R7-workload-signature-hash.md` — locked
  design of record. This spec consumes R7 §Q1–Q6 verbatim.
- `docs/research/r-bundle/R8-R12-tachless-fallback-and-blended-confidence.md`
  — R12's `conf_C` consumes the residual stream this patch enables.

---

## 1. Why this patch exists

Smart-mode's Layer C (v0.5.8) needs to learn **per-workload**
marginal-benefit functions: ramping a CPU fan from PWM 120 to 140
under a kernel build produces a different ΔT than the same ramp
under idle web browsing. Without a workload identity, Layer C either
collapses all behaviours into one estimator (loses fidelity) or
shards by something arbitrary (loses convergence).

v0.5.6 ships the **workload signature library** — the layer that
turns the running process set into a stable, opaque, privacy-safe
label. Subsequent patches consume the labels:

- v0.5.7 keys thermal-coupling estimators on `(channel, signature)`.
- v0.5.8 keys RLS marginal-benefit estimators on `(channel,
  signature)`.
- v0.5.9 weights confidence per `(channel, signature)`.
- v0.5.10 surfaces signature-correlated abort and convergence
  history.

Per R7's analysis the design must:

- Survive workload-launch churn (Steam game launch produces 10–20
  distinct exes in the first 5 seconds; gcc/cc1/ld churn at 200
  spawns/sec; Chrome Site-Isolation tab churn).
- Resist rainbow-table reversal of leaked diag bundles (`comm` is a
  low-entropy input from a small universe; only a per-install secret
  defends).
- Run within the controller's existing tick budget (≤2 µs of CPU per
  2 s tick at 200 processes).
- Persist across daemon restart deterministically (same workload →
  same label after reboot).

## 1.1 Ground-up principle

**R7 is the design-of-record. v0.5.6 transcribes R7 into Go and
tests; it does not re-litigate hash choice, top-N criterion, hash
inputs, stability mechanism, library size, or persistence shape.**

Future revisions to any of those decisions require an amendment
to R7, not to this patch's spec.

---

## 2. Scope

### 2.1 In scope

**Signature library (`internal/signature/`):**

- New `Hasher` type wrapping `github.com/dchest/siphash` (CC0,
  pure-Go, ~200 LOC, no transitive deps, no CGO). Keyed by the per-
  install salt. `HashComm(comm string) uint64` is the only hashing
  entry point.
- New `Library` type implementing R7's EWMA-multiset + K-stable-
  promotion algorithm (R7 §Q4 pseudocode). Tick cadence 2 s.
- Per-install salt at `/var/lib/ventd/.signature_salt`, mode 0600,
  owner `ventd:ventd`, generated on first daemon start via
  `crypto/rand`. R7 §Q6 contract.
- 128-bucket library with weighted-LRU eviction (τ = 14 days)
  per R7 §Q5.
- Persistence to spec-16 KV under `signature/<label>` namespace,
  msgpack-encoded `Bucket{Version, HashAlg, LabelKind, RLSState,
  FirstSeenUnix, LastSeenUnix, HitCount, CurrentEWMA}`. R7 §Q5.
- Reserved-label override: when an R5 maintenance-class process
  dominates the K=4 set, label is `maint/<canonical-name>` from
  R5's existing blocklist. R7 §Q2 (B).
- Container/VM and hardware-refusal inheritance: when R1 reports
  Tier-2 BLOCK or R3 reports `HardwareRefused()`, library
  initialises into a permanent-disabled state and emits the
  fixed label `fallback/disabled`.

**Process walker (`internal/proc/`):**

- New shared package with `Walk(procRoot string) []ProcessSample`,
  reading `/proc/[pid]/comm`, CPU jiffies (utime+stime), RSS from
  `/proc/[pid]/statm`, and PPid from `/proc/[pid]/status`.
- EWMA-CPU computation: per-process exponential-weighted CPU share
  over the last 10 s (5 ticks at 2 s each).
- Existing `internal/idle/cpuidle.go:captureProcesses` migrates to
  call `internal/proc.Walk(...).FilterBlocklist(...)`. Idle's
  blocklist scan becomes a thin filter over the shared walker.

**R5 blocklist export (`internal/idle/blocklist.go`):**

- Extract the hard-coded blocklist into an exported `Blocklist` type
  with `Names() []string` and `IsMaintenance(comm string) (canonical
  string, ok bool)`. v0.5.6 does NOT change the list contents.
- `internal/signature/blocklist.go` is a thin re-export wrapper.
  Single source of truth: `internal/idle/blocklist.go`.

**Controller→observation wiring (`internal/controller/`,
`cmd/ventd/main.go`):**

- The v0.5.4 obsWriter is currently constructed but never invoked
  from the controller hot loop (the comment in main.go reads
  `consumed by controller tick wiring in a follow-up spec`).
  v0.5.6 closes that gap: every controller tick that writes a PWM
  also calls `obsWriter.Append(&Record{ ... SignatureLabel: lib.Label() ... })`.
- The signature library's tick goroutine runs at 2 s independently
  of the controller's 0.5 Hz hot loop. Controller reads
  `lib.Label()` (atomic snapshot) on each tick and stamps the
  observation record.

**Settings toggle (`web/settings.html`, `web/settings.js`,
`web/settings.css`, `internal/config/config.go`):**

- New `Config.SignatureLearningDisabled bool` (default false —
  learning enabled in auto mode).
- Settings → Smart mode section gains a "Workload signature
  learning" toggle alongside the v0.5.5 opportunistic-probing
  toggle. Per spec-12 amendment §3.5 / `RULE-UI-SMART-07`.
- When any channel is in manual mode, the toggle is greyed out
  with explanation tooltip "manual mode disables signature
  learning for that channel" (per spec-smart-mode §7.4).

**Tests (synthetic, all CI):**

- Hash: `TestHasher_Determinism`,
  `TestHasher_KeyedAgainstRainbowTable`,
  `TestHasher_SaltRotationInvalidatesLabels`.
- Library: the four R7 §Q4 flap scenarios:
  - `TestLibrary_SteamLaunch_StabilisesOnGameAfter12s`
  - `TestLibrary_KernelBuild_StabilisesOnCC1WithinHalfLife`
  - `TestLibrary_ChromeSiteIsolation_SingleBucket`
  - `TestLibrary_SystemdResolved_NeverAppears` (R7 review flag #1
    proof)
- Library state: `TestLibrary_KStablePromotionRequires3Ticks`,
  `TestLibrary_DropEpsilonRemovesStaleHashes`,
  `TestLibrary_TopKBy Weight`, `TestLibrary_KthreadFilter`.
- Maintenance class: `TestLibrary_PlexTranscoderEmitsMaintLabel`,
  `TestLibrary_BlocklistDictionarySharedWithIdle`.
- Persistence: `TestLibrary_WarmRestartFromKV`,
  `TestLibrary_LRUEvictionPreservesRecentlyDormant`,
  `TestLibrary_BucketCountCapAt128`.
- Disable paths: `TestLibrary_DisabledInContainer_EmitsFallback`,
  `TestLibrary_DisabledOnSteamDeck_NeverInstantiates`.
- Process walker: `TestProcWalker_ReadsCommUtimeRSSPPid`,
  `TestProcWalker_EWMAConvergesOver10s`.
- Wiring: `TestController_StampsSignatureLabelOnObsRecord`,
  `TestController_ObsRecordEmittedEveryTick`.
- Privacy: `TestSalt_FilePermissionsAre0600`,
  `TestSalt_NeverInDiagBundle`,
  `TestRecord_SignatureLabelHexOnly_NoCommNameLeak`.

### 2.2 Out of scope

- **`ventd doctor --reveal-signatures` CLI.** R7 §Q6 specifies this
  but the `ventd doctor` subcommand itself ships in v0.5.10. v0.5.6
  exposes no operator-side reveal mechanism beyond inspecting the
  KV directly via `ventd state dump` (existing, post-spec-16).
- **`ventd ctl rotate-salt` CLI.** Same reason. For v0.5.6, salt
  rotation happens via filesystem: delete
  `/var/lib/ventd/.signature_salt`, daemon regenerates on next
  start, all existing labels become unrecoverable but not
  meaningfully harmful (RLS state under stale labels gets LRU-
  evicted naturally).
- **`[smart.signature]` config knobs.** R7 §Confidence recommends
  defer-to-v1.0 for K, M, half-life, bucket-count tuning. Hard-
  coded in `internal/signature/config.go` with the R7-locked
  defaults.
- **Per-channel signature learning disable.** spec-12 amendment
  §3.5 implies system-wide toggle only; per-channel disable is
  controlled indirectly via per-channel manual mode. v0.5.6 does
  not add a per-channel toggle.
- **Layer C consumption (RLS estimator).** v0.5.6 emits labels;
  v0.5.8 wires them into RLS. No estimator code in this patch.
- **R7's `--reveal-signatures` doctor flag.** Deferred to v0.5.10.
- **Bazel/Buck2 reserved labels.** R7's HIL-gap note flags that the
  fleet doesn't run these toolchains; defer reserved-label
  expansion until field telemetry justifies.

---

## 3. Invariant bindings

`.claude/rules/signature.md` binds 1:1 to subtests in
`internal/signature/`, `internal/proc/`, and the controller tests.
Enforced by `tools/rulelint`.

| Rule | Binding |
|---|---|
| `RULE-SIG-HASH-01` | Hash function MUST be SipHash-2-4 keyed by the per-install salt; output 64-bit, hex-rendered. |
| `RULE-SIG-HASH-02` | Hash input MUST be `/proc/PID/comm` only; cmdline / exe / parent-comm forbidden. |
| `RULE-SIG-HASH-03` | Hashing MUST be deterministic across daemon restarts given the same salt and comm. |
| `RULE-SIG-SALT-01` | Salt file MUST be 32 random bytes at `/var/lib/ventd/.signature_salt`, mode 0600, owner ventd:ventd. |
| `RULE-SIG-SALT-02` | Salt MUST be excluded from diag bundles (P9 redactor enforcement) and from all log lines. |
| `RULE-SIG-SALT-03` | Missing salt file MUST trigger fresh-salt regeneration; existing labels become naturally unrecoverable but RLS state survives under the new labels via LRU re-population. |
| `RULE-SIG-LIB-01` | The contribution gate MUST be `EWMA_cpu > 5% of one core OR RSS > 256 MiB`, kthreads excluded (PPid==2 OR comm starts with `[`). |
| `RULE-SIG-LIB-02` | The signature label MUST be the top-K=4 hashes by EWMA weight, sorted lexicographically, '\|'-joined, max 80 chars. |
| `RULE-SIG-LIB-03` | The K-stable promotion gate MUST require M=3 consecutive ticks of identical top-K before label change. |
| `RULE-SIG-LIB-04` | EWMA half-life MUST be 2 seconds, matching R11's fast-loop. |
| `RULE-SIG-LIB-05` | Library MUST be capped at 128 buckets with weighted-LRU eviction (score = hits × exp(-age/14d)). |
| `RULE-SIG-LIB-06` | A maintenance-class process dominating the top-K MUST emit the reserved label `maint/<canonical-name>` from R5's blocklist. |
| `RULE-SIG-LIB-07` | When R1 reports Tier-2 BLOCK or R3 reports HardwareRefused, library MUST emit the fixed label `fallback/disabled` and never write to KV. |
| `RULE-SIG-LIB-08` | A user-set `Config.SignatureLearningDisabled == true` MUST behave identically to the disable paths above. |
| `RULE-SIG-PERSIST-01` | Persistence MUST use spec-16 KV under namespace `signature`, key `<label>`, msgpack-encoded Bucket with FirstSeenUnix, LastSeenUnix, HitCount, CurrentEWMA. |
| `RULE-SIG-PERSIST-02` | Daemon start MUST load all KV buckets under namespace `signature`, populate the in-memory map, and resume EWMA from each bucket's CurrentEWMA. |
| `RULE-SIG-CTRL-01` | Every controller tick that writes a PWM MUST emit one observation Record with `SignatureLabel` populated and `SignaturePromoted` set on the tick promotion fires. |
| `RULE-SIG-CTRL-02` | The signature label MUST be readable from the controller without blocking on the signature tick goroutine (atomic snapshot). |
| `RULE-SIG-UI-01` | Settings page MUST expose the "Workload signature learning" toggle in the Smart mode section, default false (=enabled). |
| `RULE-SIG-UI-02` | Toggle MUST be greyed out with explanation tooltip when any channel is in manual mode. |

---

## 4. Subtest mapping

| Rule | Subtest |
|---|---|
| RULE-SIG-HASH-01 | `internal/signature/hash_test.go:TestHasher_Determinism`, `TestHasher_OutputIs64BitHex` |
| RULE-SIG-HASH-02 | `internal/signature/hash_test.go:TestHasher_RejectsNonCommInput` (compile-time + runtime) |
| RULE-SIG-HASH-03 | `internal/signature/hash_test.go:TestHasher_DeterministicAcrossRestarts` |
| RULE-SIG-SALT-01 | `internal/signature/hash_test.go:TestSalt_FilePermissionsAre0600`, `TestSalt_LengthIs32Bytes` |
| RULE-SIG-SALT-02 | `internal/signature/hash_test.go:TestSalt_NeverInDiagBundle` |
| RULE-SIG-SALT-03 | `internal/signature/hash_test.go:TestSalt_RegenerationOnMissingFile` |
| RULE-SIG-LIB-01 | `internal/signature/library_test.go:TestLibrary_GateRejectsBelowThresholds`, `TestLibrary_KthreadFilter` |
| RULE-SIG-LIB-02 | `internal/signature/library_test.go:TestLibrary_TopKByWeight`, `TestLibrary_LabelLengthCappedAt80Chars` |
| RULE-SIG-LIB-03 | `internal/signature/library_test.go:TestLibrary_KStablePromotionRequires3Ticks` |
| RULE-SIG-LIB-04 | `internal/signature/library_test.go:TestLibrary_EWMAHalfLifeIs2s` |
| RULE-SIG-LIB-05 | `internal/signature/library_test.go:TestLibrary_BucketCountCapAt128`, `TestLibrary_LRUEvictionPreservesRecentlyDormant` |
| RULE-SIG-LIB-06 | `internal/signature/library_test.go:TestLibrary_PlexTranscoderEmitsMaintLabel` |
| RULE-SIG-LIB-07 | `internal/signature/library_test.go:TestLibrary_DisabledInContainer_EmitsFallback`, `TestLibrary_DisabledOnSteamDeck_NeverInstantiates` |
| RULE-SIG-LIB-08 | `internal/signature/library_test.go:TestLibrary_HonoursToggleOff` |
| RULE-SIG-PERSIST-01 | `internal/signature/persistence_test.go:TestPersistence_KVRoundTrip` |
| RULE-SIG-PERSIST-02 | `internal/signature/persistence_test.go:TestLibrary_WarmRestartFromKV` |
| RULE-SIG-CTRL-01 | `internal/controller/controller_test.go:TestController_StampsSignatureLabelOnObsRecord` |
| RULE-SIG-CTRL-02 | `internal/signature/library_test.go:TestLibrary_LabelReadIsLockFree` |
| RULE-SIG-UI-01 | Playwright walkthrough: settings page renders the toggle |
| RULE-SIG-UI-02 | Playwright walkthrough: manual-mode channel disables the toggle visually |

Plus the four R7-mandated flap-scenario tests:

| Scenario | Subtest |
|---|---|
| Steam game launch | `TestLibrary_SteamLaunch_StabilisesOnGameAfter12s` |
| Kernel build (cc1 churn) | `TestLibrary_KernelBuild_StabilisesOnCC1WithinHalfLife` |
| Chrome Site Isolation | `TestLibrary_ChromeSiteIsolation_SingleBucket` |
| systemd-resolved non-cycling | `TestLibrary_SystemdResolved_NeverAppears` |

---

## 5. Success criteria

### 5.1 Synthetic CI tests

All ~25 named subtests pass on every PR. `tools/rulelint` reports
zero unbound rules, zero unused subtests.

### 5.2 Behavioural HIL

Per R7 §HIL-validation:

**Proxmox host (192.168.7.10):** kernel build → assert signature
converges on `{cc1, make, ld, as}` within ~6 s; Rust build → assert
similar shape; idle desktop → assert stable single signature for
≥30 minutes; ZFS scrub → assert `maint/<zfs-related>` reserved
label.

**MiniPC Celeron (when online):** 24 h soak with synthetic mixed
workload; assert no spurious signature changes during quiet periods
(gate calibration on slowest fleet member).

**13900K + RTX 4090 (dual-boot Linux):** Steam game launch end-to-
end; Chrome Site-Isolation 16-tab session; Firefox Project Fission;
Plex transcode → assert `maint/plex-transcoder`.

**Steam Deck:** library never instantiates; observation log emits
`fallback/disabled` exclusively.

### 5.3 Time-bound metric

Signature converges within 1 EWMA half-life (2 s) for kernel build;
within ~12 s for Steam launch (per R7 §Q4). HIL must validate both.

---

## 6. Privacy contract

Inherited from R7 §Q6 in full. Specifically:

- 32-byte per-install salt at `/var/lib/ventd/.signature_salt`,
  mode 0600, generated atomically on first start.
- SipHash-2-4 keyed by the salt's first 16 bytes (the second 16
  reserved for forward compatibility — HKDF derivation if a future
  spec wants per-channel hash domain separation).
- `signature_label` in the observation log is opaque hex (or
  `maint/<canonical>`) — never plaintext comm.
- Salt file excluded from diag bundles by the P9 redactor.
- No network egress of any signature data.
- `--reveal-signatures` debug flag: deferred to v0.5.10's doctor
  CLI.

The maintenance-class reserved labels (`maint/rsync`, `maint/plex-
transcoder`, etc.) are *plaintext* in the observation log because
they are publicly documented constants from R5's blocklist; they
leak no machine-specific information.

---

## 7. Failure modes enumerated

1. **Salt file missing on startup.** Daemon generates fresh salt,
   logs warning, starts learning from scratch. Existing KV
   `signature/<label>` entries become hash-tuple-stale; their RLS
   state is technically reachable but the labels won't match new
   ticks. LRU eviction reclaims them within ~14 days. Acceptable.

2. **Salt file present but mode > 0600.** Daemon refuses to start
   with diagnostic; operator must `chmod 600` and restart.
   Strict-by-default preserves the privacy invariant.

3. **Spec-16 KV write failure during library save.** Treated
   advisory: warn, continue. Loss of one bucket means LRU
   reclamation runs slightly later than it would have. Self-
   corrects on next successful write.

4. **Bucket count exceeds 128 due to a stuck-at-promotion bug.**
   Hard cap enforced — eviction always runs before insertion when
   library is at capacity.

5. **Process walker slow on /proc enumeration (high process
   count).** Walker has a 50 ms wall-clock budget per tick;
   exceeding it logs a warning and uses the previous tick's
   ProcessSample slice as fallback for that tick.

6. **PSI unavailable (kernel < 4.20 or PSI disabled).** Walker's
   EWMA-CPU computation falls back to differencing
   `/proc/[pid]/stat` jiffies directly; no functional impact.

7. **Container detection flips mid-runtime.** R1 inheritance is
   evaluated at library construction time; runtime container-
   transition (e.g., user enters a privileged namespace) does NOT
   re-evaluate. Acceptable: ventd's deployment model is host-only
   anyway.

8. **Toggle flipped to ON mid-tick.** Library halts further
   updates on next tick; existing in-memory state preserved (no
   wipe). Flip OFF resumes updates from the preserved state.

9. **Bug in maintenance-class detection emits a hash where a
   `maint/...` label should be.** Layer C operates correctly
   regardless (one extra bucket, no correctness impact); v0.5.10
   doctor surface will likely surface the misclassification via
   "common signatures whose top-K only differs by one process."

10. **Race between signature library tick and controller tick.**
    Library label read is atomic snapshot (`sync.Map` or
    `atomic.Pointer`-backed). Controller never blocks on library;
    library never blocks on controller.

---

## 8. PR sequencing

### 8.1 PR-A (logic, hermetically testable)

```
internal/signature/hash.go
internal/signature/hash_test.go
internal/signature/library.go
internal/signature/library_test.go
internal/signature/persistence.go
internal/signature/persistence_test.go
internal/signature/blocklist.go         (re-exports R5)
internal/signature/eviction.go
internal/signature/config.go
internal/signature/disable.go            (R1/R3 inheritance gates)
internal/proc/walker.go
internal/proc/walker_test.go
internal/idle/blocklist.go               (extract from preconditions.go)
.claude/rules/signature.md
specs/spec-v0_5_6-workload-signatures.md (this file)
go.mod, go.sum                            (add github.com/dchest/siphash)
```

Total LOC estimate: ~1,640 (per R7 Part 3 budget).

### 8.2 PR-B (wiring + UI)

```
cmd/ventd/main.go                        (launch lib, pass writer to controller)
internal/controller/controller.go        (consume Label() + Append() per tick)
internal/controller/controller_test.go   (TestController_StampsSignatureLabelOnObsRecord)
internal/config/config.go                (SignatureLearningDisabled field)
web/settings.html                        (new toggle in Smart mode section)
web/settings.js                          (read/write toggle, grey-out on manual mode)
web/settings.css                         (re-uses v0.5.5 .set-toggle slider)
.claude/rules/signature.md (extend)      (RULE-SIG-CTRL-*, RULE-SIG-UI-*)
```

Total LOC estimate: ~250.

PR-B is HIL-only verification (visual critique on fc-test-ubuntu-
2404 + Proxmox host).

---

## 9. Estimated cost

- Spec drafting (chat): $0 (this document, on Max plan).
- PR-A CC implementation (Sonnet): **$10–18** per R7's "3-4 days
  of focused work" budget; transcription rather than exploration.
- PR-B CC implementation (Sonnet): **$5–10**. Closes the v0.5.4
  obsWriter→controller gap as part of the wiring.
- Total: **$15–28**, slightly above `spec-smart-mode.md` §13's
  $15-25 projection because PR-B carries the v0.5.4 gap-closure
  work too. Justified — Layer A coverage was previously incomplete
  without this.

---

## 10. References

- `specs/spec-smart-mode.md` §6.6 (workload signature learning),
  §6.6.1 (this patch's domain), §7.4 (manual mode disables
  signature learning per channel).
- `specs/spec-12-amendment-smart-mode-rework.md` §3.5,
  `RULE-UI-SMART-07`.
- `specs/spec-v0_5_4-passive-observation.md`.
- `docs/research/r-bundle/R7-workload-signature-hash.md` — design
  of record, every section verbatim.
- `docs/research/r-bundle/bundle-update-2026-04-28-r7r8r12r14.md`
  §1 (architectural concepts list).
- `docs/research/r-bundle/R5-defining-idle-multi-signal-predicate.md`
  — the blocklist this patch reuses as a positive-label dictionary.
