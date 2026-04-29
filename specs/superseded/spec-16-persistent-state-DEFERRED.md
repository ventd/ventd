# spec-16 — Persistent runtime state (DEFERRED, design pending)

**Status:** DEFERRED 2026-04-27. Drafted as concept during spec-15 F1 design chat. To be designed standalone in dedicated session.

**Why deferred:** F1 amd_overdrive originally needed flag-toggle detection (was-flag-on-last-run?), which would have required persistence. Bolting persistence onto F1 would have meant F1 owned the framework's first persistence implementation — wrong shape, narrow design. Persistence has 7+ candidate consumers and likely needs more than one storage shape; designing it against one consumer warps it.

**F1 ships without persistence.** Documented as known UX gap: toggling `--enable-amd-overdrive` off does not surface "GPU still has the curve from previous run" warning. User must reboot or echo defaults to reset.

---

## Candidate consumers

State that ventd needs to persist across daemon restarts:

| Consumer | Shape | Why |
|---|---|---|
| Calibration cache | KV (firmware_version-keyed) | Already exists per spec-03; recals on BIOS update |
| Spec-05 thermal model coefficients | Binary serialization | VFF-RLS coefficients are numerical; re-learning every boot defeats the predictive value |
| Doctor last-clean-run timestamp | KV | "X has been broken for Y days" reporting |
| Experimental flag transitions | KV per flag | Surface "you turned off amd_overdrive but GPU still has custom curve" — F1's original use-case |
| spec-12 setup wizard completion | KV | First-run vs returning-user UI |
| spec-13 telemetry consent + last-submission | KV + timestamp | Consent state must not be re-prompted each run |
| spec-09 NBFC init state | KV | Config installed but not yet validated by user |

**Multi-shape implication.** One KV store doesn't fit all: thermal model wants binary, telemetry wants append-only log. Likely 2-3 stores: KV state file (`/var/lib/ventd/state.yaml`), binary model file (`/var/lib/ventd/thermal.dat`), append-only telemetry log (`/var/lib/ventd/telemetry.log`).

## Design questions to resolve in spec-16

1. Single store vs multi-store. Probably multi-store given shape diversity.
2. Schema versioning. How does ventd handle a state file written by an older ventd?
3. Atomic write semantics. Tempfile + rename, or fsync, or sqlite?
4. Lock contention. Multiple ventd processes shouldn't happen but file format must not corrupt if it does.
5. Permissions. `/var/lib/ventd/` ownership, sysusers integration with the existing `ventd` system user.
6. Migration story when format changes between minor versions.
7. Test coverage for: pre-existing file, missing file, corrupt file, partial write, schema mismatch, permission denied.

## Estimated cost

- spec-16 draft (chat): $0
- Persistence framework PR (Sonnet, single PR, well-spec'd): $15-25
- F1 toggle-detection backfill PR (Sonnet): $3-5
- Doctor last-clean-run backfill PR (Sonnet): $3-5
- Wizard completion backfill PR (Sonnet): $3-5
- Other backfills as features land

**Total persistence rollout:** $25-45 across 4-5 PRs, vs. ~$15-25 if bolted into F1 but with worse design and unaddressed consumer shapes.

## When to draft

After spec-15 F1 ships and v0.6.0 is in flight. Persistence is **infrastructure**, not a release blocker. Likely v0.7.0 timing alongside spec-15a (ilo4_unlocked) or as a v0.6.x point release.

## Open with

```
spec-16 design session. Survey existing on-disk state in the codebase
(internal/calibration/cache, internal/diag/, anywhere we touch
/var/lib/ventd or write files). Define persistence interface against
the 7 consumers in this doc. Decide single-store vs multi-store. Draft
spec-16-persistent-state.md against the findings.
```

---

## References

- Chat 2026-04-27 spec-15 F1 design session — F1 prompt design surfaced this need.
- specs/spec-15-experimental-features.md §4.1 — F1 use-case origin.
- specs/spec-05-predictive-thermal.md — thermal model coefficient persistence requirement.
- specs/spec-13-verification-workflow.md — telemetry consent persistence.
