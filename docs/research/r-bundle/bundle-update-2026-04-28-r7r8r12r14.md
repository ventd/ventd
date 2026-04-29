# Bundle Update Patch — 2026-04-28 (post-R7/R8/R12/R14)

**Purpose:** Three things to apply to the project knowledge before next chat.

1. Status-table update for `ventd-smart-mode-research-bundle.md`
2. spec-05 amendment flag (RLS bounded-covariance forgetting requirement)
3. spec-smart-mode amendment flag (global-gate stall-fraction threshold for low-channel-count systems)

---

## 1. Bundle status-table update

Replace the C.1 table in `ventd-smart-mode-research-bundle.md` with this:

```markdown
## C.1 Research program status as of 2026-04-28

| Item | Theme | Status | Spec target |
|---|---|---|---|
| R1 | Tier-2 virt/container detection | ✅ Complete | spec-v0_5_1 § Tier-2 |
| R2 | Ghost hwmon taxonomy | ✅ Complete | spec-v0_5_1 § Probe pipeline |
| R3 | Steam Deck refusal | ✅ Complete | spec-v0_5_1 § hardware_refusal |
| R4 | Envelope C abort thresholds | ✅ Complete | spec-v0_5_3 |
| R5 | Idle gate signals | ✅ Complete | spec-v0_5_3 |
| R6 | Polarity midpoint | ✅ Complete | spec-v0_5_2 |
| R7 | Workload signature hash | ✅ Complete | spec-smart-mode § Layer C / new §6.6.1 |
| R8 | Fallback signals (no tach) | ✅ Complete | spec-smart-mode § new 6.7 |
| R9 | Identifiability of thermal model | ⏸ Not started | spec-smart-mode § Layer B |
| R10 | RLS shards architecture | ⏸ Not started | spec-smart-mode § Layer B |
| R11 | Sensor noise floor | ✅ Complete | spec-sensor-preference + spec-driver-quirks |
| R12 | Confidence formula | ✅ Complete | spec-smart-mode §7, §8 |
| R13 | Doctor diagnostic depth | ⏸ Not started | spec-10 amendment |
| R14 | Calibration time budget | ✅ Complete | spec-12 wizard, spec-08 calibration, spec-16 |
| R15 | spec-05 audit | ⏸ Not started | spec-05 amendment |

**Progress: 11 of 15 complete.**
```

Replace C.2 with this (additions are R7/R8/R12/R14 architectural concepts):

```markdown
## C.2 Architectural concepts that emerged during research (PENDING ingestion to spec-smart-mode.md)

1. **`hardware_refusal` class** (R3) — parallel to virt_refusal/permission_refusal. First member: Steam Deck.
2. **Latency-vs-τ admissibility rule** (R11) — cross-cutting sensor selection principle.
3. **Dual-condition tests (range AND slope)** (R11 §6) — propagated to idle gate, BIOS-fight detection, all detectors.
4. **Per-class safety ceilings, NOT global** (R4 review flag) — override-flag bounds must be class-specific.
5. **Per-message-id opt-outs vs blanket acknowledgments** (R3 review flag).
6. **NEW (R7) — Per-install salt as keyed-PRF input.** SipHash-2-4 keyed with per-install 32-byte salt at `/var/lib/ventd/.signature_salt` (mode 0600). Defeats rainbow-table reversal of leaked diag bundles. Salt rotation = `ventd ctl rotate-salt`.
7. **NEW (R7) — Maintenance-class reserved labels.** R5 idle-gate blocklist doubles as positive-label dictionary; processes like plex-transcoder produce `maint/<canonical>` labels rather than hash-tuples. Cardinality control mechanism.
8. **NEW (R7) — EWMA-weighted hash multiset over top-N snapshot.** Top-N snapshots demonstrably flap under Steam launches and gcc/cc1/ld churn; multiset with K-stable promotion (M=3 ticks) is the default model.
9. **NEW (R8) — Seven-tier fallback chain for tach-less channels.** Monotonically-decreasing conf_A ceilings (1.00 → 0.00) per tier. Channels degrade gracefully through coupled-channel inference, BMC IPMI, EC stepped, thermal inversion, RAPL load proxy, pwm_enable echo, open-loop.
10. **NEW (R8) — Thermal-only stall watchdog.** Fires at half R4's hardware ceiling for graceful handoff. ventd's R8 stall window (30 s) is not the R12 confidence τ (30 s); same number, separate filters.
11. **NEW (R8) — Pure-Go in-band IPMI marshaller.** ~600 LOC, four IPMI commands (Get SDR Repository Info, Reserve SDR, Get SDR, Get Sensor Reading). Replaces spec-15-experimental ipmi/idrac9_legacy_raw track.
12. **NEW (R12) — Bumpless-transfer smoothness guarantee.** 30 s LPF + 0.05/s Lipschitz cap on w_pred. Cold-start hard-pinned to 0 for 5 min after Envelope C (10 min after Envelope D).
13. **NEW (R12) — RLS forgetting strategy constraint.** spec-05 RLS must use directional forgetting or EFRA; pure exponential is forbidden because tr(P) is unbounded under non-persistent excitation, and conf_C requires tr(P) bounded. **See §2 below for spec-05 amendment flag.**
14. **NEW (R12) — Confidence persistence stores inputs, not outputs.** Counts, residuals, RLS state in spec-16 KV; conf_X scalars recomputed every tick. Allows formula evolution without state migration.
15. **NEW (R14) — Three-tier wizard progress UX.** W1 spinner (<10 s) → W2 determinate bar (10 s–2 min) → W3 walk-away affordance (>2 min). Tied to Nielsen response-time thresholds.
16. **NEW (R14) — Stage-within-channel checkpoint granularity.** Per-PWM-point inside Envelope C; bounds crash-recovery redundant work to ≤8.5 min.
17. **NEW (R14) — "Calibrated = ready to safely run" framing.** Continuous learning is ventd's normal mode; first-run wizard only seeds it. Counters the "calibrated = optimised forever" misconception.
```

Update C.3 HIL fleet status — add the new gaps surfaced by R7/R8/R12/R14:

```markdown
## C.3 HIL fleet status

**Confirmed:**
- Proxmox host (5800X + RTX 3060)
- MiniPC (Celeron)
- 13900K + RTX 4090 desktop (dual-boot)
- 3 laptops (any OS installable)
- Steam Deck
- TerraMaster F2-210 NAS

**HIL gaps (existing):**
- Class 4 server CPU: no native fleet member.
- F2-210 limitations: ARM not x86, kernel possibly <4.20, vendor-proprietary fan control.
- Dell laptop: not in fleet — `dell-smm-hwmon` validation theoretical.

**HIL gaps (NEW from R7/R8/R12/R14):**
- **R7 Bazel/Buck2 compile workloads:** no fleet member runs these; reserved-label set may need `maint/bazel`, `maint/buck2` post-1.0.
- **R8 Tier-2 BMC IPMI:** no BMC in fleet. Pure-Go IPMI marshaller validated against captured fixtures only; needs early-access deployment with iDRAC/iLO.
- **R8 Tier-4 thermal inversion on NAS:** F2-210 too limited; smart-mode disabled by default on TerraMaster until field-validated.
- **R8 Framework EC stall behaviour:** Framework laptop not in fleet; Tier-3 EC-stepped path is mainline-kernel-validated only.
- **R12 conf_B sliding-window stability on Class-7 long-τ NAS:** 1h × 24 windows may need lengthening to 4h × 24 for τ ~ 5 min channels.
- **R12 cold-start hard-pin duration (5 min):** subjective UX; A/B testable post-1.0.
- **R14 8-channel NAS worst-case budget:** F2-210 is 2-bay; 8-channel × 3-HDD-channel worst case extrapolated, not measured.
- **R14 hardware-refusal graceful-exit path:** Steam Deck excluded; Framework not in fleet. Static-analysis + synthetic fault injection only.
```

---

## 2. spec-05 amendment flag — RLS bounded-covariance forgetting

**File:** `spec-05-predictive-thermal.md` (or `spec-05-amendment-rls-forgetting.md` if amendment-style)

**Status:** REQUIRED before R15 audit can complete. Surfaced by R12 §Q1 (conf_C formula).

**The contradiction:**

R12's `conf_C` formula uses `tr(P)` of the RLS covariance matrix as its confidence proxy. This is canonical RLS practice (Jia COMS 4770, Haber, MathWorks `recursiveLS`). However, `tr(P)` is **only bounded** under one of two forgetting strategies:

- **Directional forgetting** (Lai-Bernstein 2024 SIFt-RLS, arxiv 2404.10844)
- **Exponential Forgetting and Resetting Algorithm (EFRA)** with bounded covariance (IEEE 8814711)

Under **pure exponential forgetting** (the default in most RLS textbooks), `tr(P)` grows without bound during periods of non-persistent excitation — which on ventd is the *normal* operating regime (workload signatures are bursty; long stretches of single-bucket steady-state). An unbounded `tr(P)` makes `conf_C` formula undefined and breaks the smoothness guarantee.

**Required amendment:**

spec-05 must specify one of:

1. **Directional forgetting (recommended).** Forgetting factor applied only along directions of observed information; preserves prior in unobserved directions. Mathematically clean. Reference implementation: SIFt-RLS (Lai-Bernstein 2024).
2. **EFRA.** Covariance reset when `tr(P)` exceeds a threshold; simpler but introduces discontinuities.
3. **Constant-trace RLS.** Forces `tr(P) = const` after each update; loses the meaningful confidence signal. *Rejected* — incompatible with R12.

**Acceptance criterion:** spec-05's RLS update equation, when transcribed to Go, must produce a `tr(P)` that does not grow without bound under a sustained single-workload-signature trace of ≥7 days. Test fixture: replay a real Layer C trace from the Proxmox host idle workload for 7 simulated days; assert `max(tr(P)) ≤ 2 × tr(P_init)`.

**Action:** before launching R15 audit, verify which forgetting strategy spec-05 currently specifies. If pure exponential or unspecified, add the amendment as a precondition for R15.

---

## 3. spec-smart-mode amendment flag — global-gate stall-fraction for low-channel-count systems

**File:** `spec-smart-mode.md` § global-gate composition (where R12's `w_pred_system` is specified)

**Status:** Should ship with v0.5.10 doctor recovery-surface patch (final smart-mode patch before v0.6.0 tag).

**The contradiction:**

R12 §Q6 specifies the global-gate kill threshold as:

```
fraction_of_channels_in_stall < 0.5
```

This breaks for 2-channel systems: a single stalled fan on a 2-channel NAS means 0.5 of channels are in stall, which trips the threshold. ventd would force all `w_pred = 0` after a single fan failure on a 2-bay NAS — overly aggressive.

**Required amendment:**

Replace the threshold rule with:

```
stall_count >= max(1, ceil(0.5 * N))
```

For N=1: trigger at 1 stall (all-or-nothing — correct).
For N=2: trigger at 2 stalls (both fans stalled — correct, not just one).
For N=3: trigger at 2 stalls (majority — correct).
For N=4: trigger at 2 stalls (half — correct).
For N=8: trigger at 4 stalls (half — correct).
For N≥3: behaves as the original "fraction ≥ 0.5" rule.
For N=1, N=2: tightens to "all stalled" rather than "half stalled."

**Single-channel systems (N=1):** any stall trips the gate, which is correct. No fans available means smart-mode contributes nothing regardless.

**Test fixture:** unit test the gate function across N ∈ {1, 2, 3, 4, 6, 8, 12} with stall counts {0, 1, ⌈N/2⌉-1, ⌈N/2⌉, N-1, N} and assert behaviour matches the table above.

**Action:** add this to the spec-smart-mode §global-gate amendment list. Implement in `internal/confidence/global_gate.go` per R12 §Implementation File Targets.

---

## 4. CC prompt impact assessment

None of the three amendments above blocks the next CC prompt (`cc-prompt-spec-16.md` for v0.5.0.1 spec-16 persistent state). spec-16 is foundational and orthogonal to these amendments.

**Sequence still stands:**
1. Ship v0.5.0.1 (spec-16) per existing CC prompt.
2. Ship v0.5.1 (catalog-less probe + Tier-2 detection + 3-state wizard fork) — ingests R1, R2, R3.
3. Continue v0.5.x sequence.
4. Apply spec-05 amendment before R15 audit (which happens before v0.6.0 tag).
5. Apply spec-smart-mode global-gate amendment in v0.5.10 doctor patch.

---

## 5. Files to update in project knowledge

After saving R7/R8+R12/R14 long-form artifacts, also update:

- `ventd-smart-mode-research-bundle.md` — replace C.1, C.2, C.3 sections per §1 above.
- `spec-05-predictive-thermal.md` — add bounded-covariance amendment per §2 above (or create `spec-05-amendment-rls-forgetting.md` if preferred).
- `spec-smart-mode.md` — add global-gate stall-fraction amendment per §3 above.

That's the full closeout for this chat. Next chat: R9 + R10 batched.
