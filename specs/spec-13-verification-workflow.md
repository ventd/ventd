# spec-13 — Catalog Verification Workflow

**Status:** Draft. Chat-only spec — no Go code lands as part of spec-13 itself, but its design constrains spec-14 (smoke harness) and downstream PRs.
**Bound spec sections:** spec-03 §6 (profile schema, `verified` field), spec-03 capture pipeline (this spec depends on capture pipeline being merged before any automated replay can run).
**Predecessor:** Schema v1.1 + scope-C catalog seed (catalog has 17+ new entries with `verified: false`, no defined path to `verified: true`).
**Successor:** spec-14 catalog smoke harness (implements the CI-side replay machinery this spec specifies).

---

## Why this spec exists

Every board profile in the catalog carries a `verified` boolean. As of scope-C, all entries set `verified: false`. There is no defined process for flipping the flag, and no defined runtime difference between verified and unverified boards. Without explicit semantics:
- The flag accumulates as documentation noise that contributors and users learn to ignore.
- Catalog growth past ~50 boards becomes a trust crisis — no way to distinguish "Phoenix tested this on his ThinkPad" from "Claude inferred this from a forum post."
- spec-04 PI autotune and spec-05 predictive thermal both write per-board state; if the underlying board entry is wrong, autotune learns from incorrect priors.

This spec defines:
1. **What `verified: true` means** — the assertion a verified flag is making.
2. **Who or what flips the flag** — the authority and the mechanism.
3. **What runtime behavior depends on it** — minimal, honest disclosure without scaremongering.
4. **How the verification process is auditable** — captured artifacts, reproducible replay.

---

## §1 — Verification semantics

### What `verified: true` asserts

A board profile with `verified: true` makes three claims simultaneously:

1. **The fingerprint matches.** A real machine matching the entry's `dmi_fingerprint` (or `dt_fingerprint`) was observed. The match wasn't inferred from forum posts or vendor marketing copy.
2. **The driver/chip references resolve.** When the matcher dispatches based on this profile, the named `chip:` and any `additional_controllers[].chip:` references resolve to driver YAMLs that exist in the catalog. No dangling references.
3. **The override flags reflect observed behavior.** Where the entry sets `overrides.requires_watchdog: true`, `overrides.bmc_overrides_hwmon: true`, etc., these were derived from actual observation on the real hardware, not inferred.

**What `verified: true` does NOT assert:**
- That the board's curve is tuned. Curves stay `defaults.curves: []` for both verified and unverified entries; per-board curves are calibration-time output, not catalog data.
- That every BIOS revision in the family works. Verification asserts the specific BIOS_VERSION observed.
- That ventd's autocurve generates a good curve on this board. That's spec-04 / spec-05 territory.
- That fan control physically succeeds. The `overrides.fan_control_blocked_by_*` flags are honest about boards where the matcher dispatches but the backend can't actually drive the fan.

### What `verified: false` means

The default. The entry is a research-grade catalog candidate. It may be entirely correct; it has not been validated against a captured artifact. It is fully usable at runtime — see §3.

---

## §2 — Verification authority and mechanism

### Authority: automated CI replay of captured calibration bundles

Phoenix does NOT manually flip `verified: true → false`. CI does, when a captured calibration bundle for the entry's board id passes replay assertions.

This deliberately removes Phoenix from the per-board verification critical path. Catalog growth doesn't bottleneck on Phoenix's HIL access. Community contributors with access to hardware Phoenix doesn't own (Legion laptops, IPMI servers, Pi 4B with PWM fans, Framework, etc.) can produce captured bundles; CI takes it from there.

### Mechanism: captured bundles as ground truth

Captured calibration bundles produced by spec-03 capture pipeline (`/var/lib/ventd/profiles-pending/<fingerprint>.yaml`) are the verification input.

A captured bundle is a real, anonymised calibration artifact from real hardware. It contains:
- The DMI/DT fingerprint hash of the originating machine.
- The matched board profile id.
- The fingerprint fields that matched (sys_vendor, product_name, board_name, board_version, bios_version OR compatible+model).
- Calibration outputs (per-channel PWM/RPM curves, hwmon readings).
- Driver/chip catalog references that resolved.

**Bundles are donated to the project**: a contributor manually attaches their pending profile YAML to a GitHub PR (or future submission flow). PR template asks for the YAML to be placed under `testdata/captures/<board-id>/<contributor-tag>.yaml`.

### CI replay flow

When a PR adds a new captured bundle to `testdata/captures/<board-id>/`:

1. **CI loads the bundle** via the existing schema loader.
2. **CI looks up the board profile** matching the bundle's fingerprint.
3. **CI runs replay assertions** (see §4). All must pass.
4. **CI bot opens a follow-up PR** flipping `verified: false → true` for that board id, citing the bundle path as evidence.
5. **Phoenix reviews and merges** the verification PR. (Phoenix is in the loop, but only as merge gate — not as per-board test runner.)

The verification PR can be auto-approved if the replay passed, but actual merge stays a human action — this preserves Phoenix's ability to spot anomalies a CI assertion missed.

### Multiple captures per board

A board's verification status reflects the latest replay-passed bundle. If multiple contributors capture the same board with different BIOS revisions, each capture gets stored under `testdata/captures/<board-id>/<bios-rev>-<contributor-tag>.yaml`. Verification flips to `true` on the first successful replay; subsequent captures are additive coverage, not re-verification.

---

## §3 — Runtime behavior

### Functional behavior: identical for verified and unverified

A `verified: false` board gets:
- Full matcher dispatch.
- Full calibration.
- Full curve apply.
- Full autotune (when spec-04 ships).
- Full predictive thermal (when spec-05 ships).

ventd does not gate any feature on the verified flag. A user running ventd on a Legion laptop in scope-C — where `verified: false` — gets the same daemon behavior as a user on a verified ThinkPad. This is intentional: gating features on verification creates a "second-class catalog" effect that would discourage contributors and frustrate users on niche hardware.

### Disclosure: honest, not alarming

Three surfaces disclose verification status without making the user feel broken:

**1. Web UI board info card** displays a small badge:
- `verified: true` → green check + "Verified by CI replay (2026-XX-XX)"
- `verified: false` → grey dash + "Community-contributed, not yet replay-verified"

No yellow warning triangles. No modal popups. No "do you want to continue?" prompts. The information is present for users who care; absent for users who don't.

**2. Startup INFO log line** (ONE line, not three):
```
ventd matched board profile lenovo-legion-5-15ach6h (community-contributed; replay verification pending)
```
or
```
ventd matched board profile lenovo-thinkpad-t14-gen3-amd (verified 2026-XX-XX)
```

**3. Diagnostic bundle** carries the verified flag and verification timestamp (if any) so support requests include the context.

### What disclosure does NOT do

- No separate "use at your own risk" toggle. ventd does not ask the user to acknowledge the verification status.
- No restriction on which features are available on unverified boards.
- No banner in the main dashboard. Verification info lives on the board info card; users have to actively look.
- No automated downgrade ("we noticed this is unverified, switching to a safer profile") — there is no "safer profile" concept, and inventing one would create the very gating dynamic this spec rejects.

---

## §4 — Replay assertions

A captured bundle passes CI replay if and only if all of the following hold. spec-14 implements the assertions.

### A1: Fingerprint round-trip
The bundle's stated fingerprint (the 16-hex hash) recomputes correctly from the bundle's stated DMI/DT input fields. Catches bundles with tampered/synthetic fingerprints.

### A2: Matcher dispatch
Feeding the bundle's DMI/DT input into the matcher returns the bundle's stated board id. The board YAML in the catalog must match the same way the bundle reports.

### A3: Driver references resolve
The matched board's `primary_controller.chip:` and every `additional_controllers[].chip:` reference resolves to a driver YAML in `internal/hwdb/catalog/drivers/`. No dangling references.

### A4: Override consistency
The bundle's recorded `overrides:` block is a subset of, or equal to, the catalog board's `overrides:` block. Bundles cannot introduce override fields not declared in the catalog (catches contributors trying to extend the schema via captures).

### A5: Capability coverage
The bundle's recorded calibration succeeded (all channels reported PWM/RPM data). A bundle from a board where calibration failed does NOT verify the board profile — it verifies that calibration noticed the failure.

### A6: Schema strictness
The bundle parses cleanly under the current schema version with `KnownFields(true)` strict-decode. Bundles with unknown fields fail.

### A7: Anonymisation completeness
The bundle contains no PII patterns matching the redactor's known signatures. Catches captures that bypassed anonymisation (shouldn't be possible given spec-03 capture pipeline, but defense in depth).

**Failure handling**: any assertion failure blocks the verification PR. CI bot comments on the contribution PR explaining which assertion failed. Contributor can re-capture and resubmit.

---

## §5 — Capture submission UX

This section is informational; the actual submission flow is P5-PROF-03 (future spec). spec-13 specifies what the submission must produce.

### Minimum viable submission

A contributor:
1. Runs ventd on their hardware. ventd captures a pending profile to `/var/lib/ventd/profiles-pending/<fingerprint>.yaml`.
2. Reviews the file (mode 0640, plain YAML, anonymised).
3. Opens a PR adding the file to `testdata/captures/<board-id>/<their-handle>.yaml`.

The PR template provides the file path convention and asks the contributor to confirm:
- They ran calibration successfully (no fan stuck, no crash).
- They observed the fan responding to ventd's PWM writes during calibration.
- They reviewed the YAML and didn't spot residual PII.

### What the contributor doesn't have to do

- Run any tests. CI does that.
- Edit the catalog board YAML. CI's verification PR does that.
- Understand Go. The submission is YAML-only.
- Have a GitHub account if a non-PR submission flow is later added (out of scope for spec-13).

---

## §6 — Edge cases

### E1: Captured bundle for an unknown board

A contributor captures a bundle on hardware ventd dispatched via tier-3 generic fallback (no specific board profile matched). The bundle's stated board id is `generic-coretemp-only` or similar.

Replay passes assertion A2 (matcher returns the same generic id). But this doesn't verify any specific board profile — generic profiles are not subject to the verified flag (they're chip-family fallbacks, not boards).

**Resolution**: the bundle is still useful as a generic-coverage data point. Stored under `testdata/captures/_generic/`. Does not flip any board's verified flag.

### E2: BIOS revision conflict

A contributor captures Legion 5 15ACH6H on BIOS GKCN65WW. The catalog's bios_version glob is `GKCN*` (matches all GKCN revisions). Replay passes.

The board flips to `verified: true`. **The verification asserts the family, not the specific revision.** This is the right scope — the catalog entry itself is family-scoped via glob. If a future BIOS revision in the GKCN family breaks something, that's a regression bug filed against the catalog, not a re-verification request.

### E3: Re-capture after schema bump

Schema v1.1 adds bios_version. A bundle captured under schema v1.0 doesn't have the field. Replay assertion A6 (strict-decode) fails because the bundle has the bumped schema_version field but missing v1.1 expected fields.

**Resolution**: schema migration runs on the bundle BEFORE replay. PR 1's migrate.go chain handles v1.0 → v1.1 transparently. Replay sees the migrated form. spec-14 wires the migration in.

### E4: Captured bundle disagrees with catalog

A contributor's bundle records `overrides.requires_watchdog: false` for ThinkPad T490. The catalog says `true`. Replay assertion A4 fails.

This is the common-and-good case. CI surfaces the disagreement in PR comments. Phoenix or the contributor investigates: is the catalog wrong, the bundle wrong, or is there a per-revision difference? The investigation produces either a catalog correction PR or a closing comment.

**Important: A4 disagreements DO NOT auto-flip the verified flag.** Verification PRs require all assertions clean.

### E5: Multiple captures, conflicting

Two contributors capture the same board id with mutually inconsistent overrides. CI fails both bundles' replay (one matches catalog, the other doesn't). Phoenix manually decides which is correct.

This is a feature, not a bug — the system surfaces real-world hardware variance instead of papering over it.

---

## §7 — Out of scope for spec-13

### S1: Verification of curve quality
The verified flag has nothing to do with whether the autocurve generated for the board is "good." Curve quality is spec-04 / spec-05's domain.

### S2: Versioned verification
A board verified in 2026 with capture C1 stays verified after schema v1.2 ships. Re-verification is not automatic on schema bumps. If a schema bump breaks a verified board's replay, that's a separate bug.

### S3: Per-distribution verification
A bundle captured on Ubuntu 24.04 verifies the board, not the distribution. Distro-specific issues (modprobe.d quirks, AppArmor profiles) are install-contract concerns (spec-06), not catalog verification.

### S4: Negative verification
There is no `verified: false-after-failure` state. A failed replay doesn't downgrade an already-verified board. It produces a CI-failed PR that someone investigates.

### S5: Trust hierarchy among contributors
All contributors are equal. Phoenix's captures and a stranger's captures get the same replay treatment. CI is the trust mechanism, not contributor identity.

### S6: Encrypted bundle storage
Captures are anonymised, plain-text YAML, committed to the public repo. Anyone can read them. This is by design — auditability requires public artifacts.

---

## §8 — Acceptance criteria for spec-13 itself

This spec is "done" when:
- [ ] Phoenix has read it and signed off on the §1–§6 design.
- [ ] §1's three assertions are agreed on.
- [ ] §2's CI authority model is agreed on.
- [ ] §3's "no functional gating" stance is agreed on.
- [ ] §4's seven assertions form the spec for spec-14.
- [ ] §6's edge cases don't surface a missed scenario.

**No code lands for spec-13 itself.** spec-14 is the implementation.

---

## §9 — Dependency chain

| Step | Status | Blocks |
|---|---|---|
| spec-03 schema v1.1 | Drafted, CC prompt ready | Catalog scope-C seed (depends on bios_version + dt_fingerprint) |
| spec-03 catalog scope-C seed | Drafted, CC prompt ready | Larger catalog → more verification value |
| spec-03 capture pipeline | Drafted, CC prompt ready | spec-13 verification flow (no bundles → nothing to replay) |
| spec-13 (this doc) | Drafted | spec-14 implementation |
| spec-14 catalog smoke harness | Not yet drafted | Verification PRs (CI machinery to run replay) |
| First verification PR | After spec-14 ships | Catalog growth |

**Critical path:** capture pipeline → spec-14 → first replay-PASS captures → first `verified: true` flips.

The chain implies the first board flips from `verified: false → true` only after capture pipeline + spec-14 both ship + at least one contributor donates a passing bundle. This may take weeks of real time post-spec-14 ship. **That's fine.** The catalog grows without verification in the meantime; runtime behavior is unaffected.

---

## §10 — Anticipated questions

### Q: What if no contributor ever captures a Legion bundle?

Then Legion entries stay `verified: false` forever. That's fine. They work at runtime. Phoenix can capture them himself if/when he acquires HIL access. The system tolerates indefinite `verified: false`.

### Q: What if a contributor captures a Legion bundle but ventd's autocurve crashes their fan?

That's a calibration bug, not a verification process bug. Capture pipeline only runs after successful calibration (spec-03 capture pipeline §post-calibration hook); a crashed calibration produces no bundle. The contributor opens an issue, ventd fixes the calibration crash, contributor re-captures.

### Q: Why not use signed bundles?

Out of scope. Anonymisation strips PII; that's the privacy goal. Authenticity is a distinct concern (preventing forged bundles). Signed bundles add infrastructure complexity (key distribution, revocation) for marginal benefit when CI replay catches the structural assertions anyway. If a future supply-chain threat materializes, revisit then.

### Q: What about boards Phoenix can capture himself?

Phoenix's captures go through the same flow. He opens a PR with his bundle in `testdata/captures/<board-id>/phoenix.yaml`. CI runs replay. If passed, CI bot opens the verification PR. Phoenix merges his own verification PR.

This deliberately removes any "Phoenix bypass" — the workflow is the same for everyone, including Phoenix.

### Q: What if Phoenix wants to flip a board verified manually?

He can. He just edits the catalog YAML. But there's no audit trail — no captured bundle that verifies the claim. Future reviewers see `verified: true` with no `testdata/captures/<board-id>/` evidence and rightfully ask "what's the basis?"

The honest path is: capture, replay, verification PR. The dishonest path is technically open but socially discouraged.

### Q: Does verified status ever expire?

No. A bundle captured today verifies the board against the schema as of today. Schema bumps don't auto-invalidate. If a schema change makes old bundles unreplayable, that's noted in the migration; affected boards may need re-verification but the existing `verified: true` flag stays until explicit downgrade.

---

## §11 — Cross-references

- `specs/spec-03-profile-library.md` — base profile schema, where `verified` field originated.
- `specs/spec-03-amendment-schema-v1.1.md` — schema v1.1 with bios_version, dt_fingerprint, unsupported override.
- `cc-prompt-spec03-capture-pipeline.md` — capture pipeline implementation.
- `cc-prompt-spec03-schema-v1.1.md` — schema v1.1 implementation.
- `cc-prompt-spec03-catalog-seed-scope-c.md` — scope-C catalog with 17 unverified entries.
- `specs/spec-14-catalog-smoke-harness.md` — implementation of replay assertions §4.
- `internal/diag/redactor/` — anonymisation primitives (already shipped in PR 2c).

---

## §12 — Summary

`verified: true` means a real captured bundle from real hardware passed all 7 CI replay assertions. CI flips the flag, Phoenix merges the resulting PR. Runtime behavior is identical regardless of flag value. Disclosure is honest and quiet — a badge on the board info card, one log line at startup. The path from `false → true` requires no manual per-board work from Phoenix; community contributors with hardware access drive verification through bundle donations.

The catalog can grow indefinitely with `verified: false` entries; the system is designed to tolerate that. Verification is additive, not gating.
