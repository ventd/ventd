You are Claude Code, working on the ventd repository.

## Task
ID: P2-CROSEC-02
Track: CROSEC
Goal: Extend Wave 1's Framework/Chromebook EC backend with support for three more laptop EC families: ThinkPad (`thinkpad_acpi`), Dell (`dell-smm-hwmon` + `i8k` legacy fallback), and HP (`hp-wmi`). Each family has a different interface; this task abstracts them behind the existing `crosec` backend via vendor detection at construction time.

## Care level
Medium. These EC interfaces are narrower and better-documented than cros_ec; most of the risk is already known from the Wave 1 work. Main risk: on HP laptops, `hp-wmi` has historically had kernel-level issues that can freeze the machine on write; MUST gate HP writes behind a warning label and allow read-only mode as default.

## Context you should read first

- `internal/hal/crosec/backend.go` — Wave 1 backend. This task either extends it or adds sibling packages.
- `internal/hwdb/profiles.yaml` — has entries for Framework and ThinkPad; check DMI gating pattern.
- Kernel docs:
  - ThinkPad: `Documentation/laptops/thinkpad-acpi.rst` — `/proc/acpi/ibm/fan` and `/sys/bus/platform/drivers/thinkpad_hwmon/`.
  - Dell: `drivers/hwmon/dell-smm-hwmon.c` — `/proc/i8k` (legacy) and `/sys/class/hwmon/hwmonN/` when driver loads.
  - HP: `drivers/platform/x86/hp-wmi.c` — WMI method invocation via `/sys/devices/platform/hp-wmi/`.

## What to do

1. Restructure `internal/hal/crosec/backend.go` or add new vendor modules alongside it:
   - Option A: keep the package name `crosec` but broaden the backend to cover thinkpad/dell/hp (cleaner for main.go registration).
   - Option B: rename Wave 1's package to `internal/hal/laptop` and make `crosec`, `thinkpad`, `dell`, `hp` sub-packages.
   - Pick Option B. The name "crosec" was always Framework-specific; laptop EC is a bigger family.

2. Migrate P2-CROSEC-01's code to `internal/hal/laptop/crosec/`. Update main.go registration.

3. Create `internal/hal/laptop/thinkpad/backend.go`:
   - Detect: DMI `sys_vendor` contains "LENOVO" AND `/proc/acpi/ibm/fan` exists.
   - Read: parse `/proc/acpi/ibm/fan` for `speed: N` line.
   - Write: `echo "level auto"` or `echo "level N"` (0-7) to `/proc/acpi/ibm/fan` via sysfs.
   - Restore: `echo "level auto"`.

4. Create `internal/hal/laptop/dell/backend.go`:
   - Detect: DMI vendor contains "Dell" AND hwmon chip name is `dell_smm`.
   - Read/Write: route through the standard hwmon backend (dell_smm presents as a hwmon device). This is a thin wrapper that just scopes hwmon channels to the dell_smm chip.
   - No special restore (normal hwmon semantics).

5. Create `internal/hal/laptop/hp/backend.go`:
   - Detect: DMI vendor contains "HP" AND `/sys/devices/platform/hp-wmi/` exists.
   - Read: parse `/sys/devices/platform/hp-wmi/fan_temp*` (read-only by default).
   - Write: GATED. Only enabled if `config.yaml` has `backends.hp.allow_fan_write: true`. Default is false because hp-wmi writes have historically caused kernel hangs on specific HP models.
   - When write is disabled: the channel's Caps field does NOT include CapWritePWM.

6. Main laptop backend (`internal/hal/laptop/backend.go`): dispatches to the right sub-backend based on DMI detection at construction. Enumerate aggregates across whichever sub-backend detected. One laptop has exactly one vendor detected at a time.

7. Unit tests per sub-package plus an aggregate test for the dispatch:
   - `TestDispatch_LenovoDMI_UsesThinkPadBackend`.
   - `TestDispatch_HPDMI_NoHPWriteCaps`.
   - `TestDispatch_DellDMI_ScopesToHwmonChip`.
   - `TestDispatch_UnknownVendor_EmptyEnumeration`.

8. Update `internal/hwdb/profiles.yaml` to add ThinkPad X-series, Dell XPS 13/15, HP EliteBook 840/1040 entries with their appropriate module loads.

9. Build/vet/lint/test clean.

## Definition of done

- `internal/hal/laptop/` package with crosec, thinkpad, dell, hp sub-packages.
- Aggregate dispatch based on DMI.
- HP write gated behind config flag.
- CHANGELOG entry.
- Updated hwdb entries for common laptops in each family.

## Out of scope

- ASUS laptops (zenbook-class with `asus_wmi`) — could be Wave 3.
- Razer laptops (custom EC) — no public docs, skip.
- Real-hardware HIL tests.

## Branch and PR

- Branch: `claude/P2-CROSEC-02-laptop-ecs`
- Title: `feat(hal/laptop): ThinkPad + Dell + HP EC backends (P2-CROSEC-02)`

## Constraints

- Depends on P2-CROSEC-01 being merged. Its code gets migrated, not copied.
- Files: `internal/hal/laptop/**` (new), `internal/hwdb/profiles.yaml`, `cmd/ventd/main.go`, `CHANGELOG.md`.
- No new dependencies.
- HP write default: OFF.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS.
- Additional field: VENDOR_MATRIX — which vendors support read, write, and what the default write gate is per vendor.
