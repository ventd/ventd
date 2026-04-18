# P2-ASAHI-01 — Apple Silicon Asahi backend

**Model:** Opus 4.7. Asahi's fan-control story is evolving rapidly upstream; Asahi docs + current `macsmc_hwmon` driver are the only source of truth. Protocol correctness is essential — this is the first fan controller on Apple Silicon Linux.
**Care level:** HIGH. Greenfield market; this PR establishes how ventd looks on Apple Silicon.

## Task

- **ID:** P2-ASAHI-01
- **Track:** ASAHI (Phase 2)
- **Goal:** Apple Silicon fan backend on Asahi Linux. Detect via `/proc/device-tree/compatible` matching `apple,t800[23]` or `apple,m1*` or `apple,m2*`. Wrap the `macsmc_hwmon` driver (which exposes fans as standard hwmon chips when loaded) with role-classification hints (pump / CPU fan / GPU fan) derived from the DT compatible and hwmon label.

## Context

1. `ventdmasterplan.mkd` §8 P2-ASAHI-01 entry.
2. Asahi Linux fan driver: `drivers/hwmon/macsmc-hwmon.c` in Asahi kernel tree (upstream merge pending).
3. `/proc/device-tree/compatible` entries for Apple Silicon: apple,j274 (MacBook Air M1), apple,j313 (MacBook Air M2), apple,j293 (MacBook Pro 13 M1), apple,j314s (MacBook Pro 14 M1 Pro), apple,j316s (MacBook Pro 16 M1 Pro) — many more; match on the "apple,t800"/"apple,t801"/"apple,t602"/"apple,m1"/"apple,m2" prefixes.
4. `internal/hal/hwmon/backend.go` — ASAHI builds on top of hwmon, reusing its sysfs walk but adding classification.

## What to do

1. `internal/hal/asahi/asahi.go`:
   - Detection: Read `/proc/device-tree/compatible` at Enumerate-time. If no Apple Silicon prefix matches, return `[]` silently.
   - Wrap `hwmon.Backend` or directly walk `/sys/class/hwmon/*/name` looking for `macsmc_hwmon`.
   - Classify each channel via the hwmon label (macsmc exposes labels like "Fan 1", "Exhaust", "Intake"): Role = RolePump for "Pump"; RoleGPU for "GPU"; otherwise RoleCaseFan.
   - Read/Write/Restore delegate to hwmon backend (macsmc implements the standard interface).
   - Caps: CapRead | CapWritePWM | CapRestore (same as hwmon) — but if the driver reports write-unsupported (some machines without full support yet), downgrade to CapRead only.
2. `internal/testfixture/fakedt/fakedt.go` (new): tiny helper to stub `/proc/device-tree/compatible` content via test tempdir override.
3. `internal/hal/asahi/asahi_test.go`:
   - Non-Apple DT → empty Enumerate.
   - Apple DT + no macsmc hwmon → empty Enumerate (driver not loaded).
   - Apple DT + macsmc hwmon → correct role classification via labels.
   - Write-unsupported hardware variant → Caps has no CapWrite.
4. Wire into `cmd/ventd/main.go` registry, before generic hwmon (so ASAHI's classification wins on Apple Silicon; hwmon's generic classification is the fallback on non-Apple).
5. CHANGELOG line.

## Definition of done

- CGO-off build clean.
- `-race` tests pass.
- Non-Apple Linux unaffected: `/proc/device-tree/compatible` absent or non-Apple → Enumerate returns empty and no errors.
- macsmc present but unclassifiable label → falls back to RoleUnknown (never panic).
- CHANGELOG one-line.
- vet/fmt clean.

## Out of scope

- Writing via direct SMC calls (Linux ventd does NOT talk to the SMC; it goes through `macsmc_hwmon`).
- Fan control on machines where the upstream driver is still read-only; surface as CapRead-only.
- macOS (that's P6-MAC-01, different code path entirely).
- Tests outside the scope this task targets per the testplan catalogue.

## Branch and PR

- Branch: `claude/P2-ASAHI-01-apple-silicon`.
- Title: `feat(hal/asahi): Apple Silicon Linux (Asahi) fan backend (P2-ASAHI-01)`.

## Allowlist

- `internal/hal/asahi/**` (new)
- `internal/testfixture/fakedt/**` (new)
- `cmd/ventd/main.go` (registry line)
- `CHANGELOG.md`

## Reporting

Standard block.
