# spec-09 — NBFC backend for laptop EC fan control

**Status:** draft. Targets v0.8.0 ship, work-tree opens after v0.6.0 smart-mode lands.
**Predecessor:** spec-03 PR 2b (validity probe, polarity), spec-10 (doctor surface), spec-12 amendment (OOT driver install pattern — pkexec-elevated one-prompt). Sits next to spec-02 (Corsair HID-direct) and spec-IPMI (BMC) as the third userspace EC-class backend.
**Bound spec sections:** §1 motivation, §2 honest framing, §3 PR breakdown, §4 trigger conditions, §5 invariant bindings (RULE-NBFC-*), §6 PR-by-PR file map.
**Upstream:** `github.com/nbfc-linux/nbfc-linux` v0.5.2 (2026-05-13), GPL-3.0, native C, 311 JSON configs. The schema reference this spec binds to is `doc/nbfc_service.json.5.md` at the same tag.
**Why now (and not v0.6.0):** v0.5.x → v0.6.0 is closing the smart-mode wiring gaps that issue #1024 / #1033 / #1035 surfaced — Layer A `Observe`, Layer B `Update`, Layer C `OnObservation`, signature manifest load, calibration-complete callback, and so on. Opening a new backend mid-cycle splits attention. spec-09 opens *after* `Snapshot.WarmingUp` reliably clears on Phoenix's MSI Z690-A and the smart-mode controller proves its first-user contract on the hardware that already works.

---

## §1 — Why this spec exists

`ventd` today controls fans through three classes of writable interface: `hwmon` sysfs (motherboard Super-I/O chips, AMD/Intel GPU integrated controllers), NVML (NVIDIA GPUs), and IPMI (server BMCs). Every other class of hardware that ships a fan and a temperature sensor falls into the **EC-locked-laptop** bucket: HP Pavilion / Victus / EliteBook, ASUS VivoBook / Zenbook, Acer Aspire / Nitro, Lenovo ThinkPad (non-`thinkpad_acpi`-cooperative SKUs), Lenovo IdeaPad, MSI gaming laptops, Toshiba, Gigabyte Aero, Framework, plus the long tail. `internal/doctor/detectors/ec_locked_laptop_d.go` (`RULE-DOCTOR-DETECTOR-ECLOCKEDLAPTOP`) surfaces these today as informational only: "your hardware works as designed; fan control is firmware-owned." That's the truthful diagnostic — but for ~300 specific laptop models the embedded controller is *documented and writable* via direct port I/O, and the open-source community has spent a decade reverse-engineering and curating the per-model register maps that make that I/O safe. That curated catalog is `nbfc-linux/nbfc-linux`.

`hwdb` already references this fact in five places:

1. `internal/hal/gpu/nvml/probe.go` raises `ErrLaptopDgpuRequiresEC` with a message pointing at "spec-09 NBFC backend" (`RULE-GPU-PR2D-06`).
2. `internal/doctor/detectors/userspace_conflict_d.go` already detects `nbfc_service.service` as a *conflicting* userspace daemon (`RULE-DOCTOR-DETECTOR-USERSPACECONFLICT`) — when this spec ships we'll be the daemon that needs to refuse to start if a foreign `nbfc_service` is running, same as we already do for `fancontrol` and `thinkfan`.
3. `(research note in git history)` §B.13 carries a half-drafted driver-tier profile entry — `family: nbfc-linux`, `pwm_unit: percentage_0_100`, `conflicts_with_userspace: [nbfc_service]`, citation pinned at `nbfc_service.json.5.md`.
4. `(research note in git history)` §10.4 declares the integration model: *"per-model config files, never assume a generic EC layout"*.
5. `specs/spec-12-amendment-oot-driver-install.md`: "Framework laptops via NBFC (deferred to spec-09)".

This spec turns those references into shipping code.

---

## §2 — Honest framing

What this spec **does not** do:

- It does not replace `nbfc-linux` upstream — we vendor their JSON catalogue and write a pure-Go backend against the same schema, but the catalogue itself is curated upstream and we pull updates rather than fork. Per-laptop new-model contributions go upstream first; we sync.
- It does not embed a Lua runtime. The nbfc-linux JSON schema permits Lua-driven control via `ReadLuaCode` / `WriteLuaCode` / `LuaLibraries`; 0/311 catalog configs currently use it. Lua-using configs are refused with `ErrNBFCConfigNeedsLuaRuntime`. If the upstream catalogue starts using Lua, we revisit.
- It does not auto-install `ec_sys.write_support=1` or `acpi_call`. The wizard's existing modprobe-options-write endpoint (`RULE-MODPROBE-OPTIONS-01`) gains the allowlist entry `ec_sys → write_support=1`. The DKMS install pipeline that already ships `nct6687d` and `legion_laptop` gains a new catalogue row for `acpi_call`. Operators click through the same one-prompt UI that already handles `thinkpad_acpi fan_control=1`.
- It does not run on battery. `RULE-IDLE-02` already hard-refuses; no plumbing needed.
- It does not run inside a container. `RULE-IDLE-03` already hard-refuses; same.
- It does not "discover" EC registers. ventd never probes registers that aren't named in the matched config. The catalog is the source of truth; an unrecognised laptop falls through to monitor-only mode and the operator is invited to contribute a config upstream.

What it **does** do:

- Vendors the 311-config catalogue at the upstream-v0.5.2 tag under `internal/hwdb/nbfc/configs/`, embeds via `embed.FS`, refreshable via `make sync-nbfc-configs`.
- Matches the live DMI fingerprint against config `NotebookModel` strings using `hwdb.Fingerprint` parity (`RULE-DOCTOR-05`) — same matcher the daemon already uses for hwmon boards.
- Wires a new `internal/hal/nbfc/` backend behind the existing `hal.FanBackend` interface: `Enumerate / Read / Write / Restore / Close / Name`. Everything downstream (watchdog restore-on-exit, calibration polarity probe, smart-mode confidence aggregator, doctor card) Just Works because the contract is the contract.
- Adds a pure-Go EC transport at `internal/ec/`: primary `/sys/kernel/debug/ec/ec0/io` (the `ec_sys` debugfs path, the same one nbfc-linux prefers), fallback `/dev/port` at 0x62/0x66. No CGO; the `CGO_ENABLED=0` invariant holds.

Coverage targets:

| Tier | Config count | Coverage after v0.8.0 |
|---|---|---|
| Register-only (8-bit) | 279 | **100% supported** (PR B2) |
| Register-only (16-bit, `ReadWriteWords`) | 26 | **100% supported** (PR B2) |
| ACPI-method (writable via firmware) | 7 | **100% supported** (PR B3 — via `acpi_call` DKMS module, same pipeline as `nct6687d`) |
| Lua-driven | 0 | **structurally refused**, slot reserved for a future revisit |
| **Total** | **311** | **311 / 311 = 100%** |

---

## §3 — Scope (four PRs, all in v0.8.0)

### PR A — catalog vendoring + matcher + doctor card (read-only)

**Goal:** every operator on a laptop with an upstream nbfc config sees in the Doctor surface that ventd recognises their hardware, names what control becomes possible in a later release, and points at the upstream contribution path if their model isn't yet listed. Zero EC writes; zero new privileged code paths.

**Files (new):**

- `internal/hwdb/nbfc/configs/` — 311 JSON files vendored from `nbfc-linux/nbfc-linux@v0.5.2`. License: GPL-3.0 (compatible). Provenance: tracked in `internal/hwdb/nbfc/UPSTREAM` (commit SHA + tag + sync date).
- `internal/hwdb/nbfc/schema.go` — Go structs for `ModelConfig`, `FanConfiguration`, `TemperatureThreshold`, `FanSpeedPercentageOverride`, `RegisterWriteConfiguration` per the upstream `nbfc_service.json.5.md` schema. Tag-mirror only — every field present in the v0.5.2 schema is mirrored, even fields we don't consume in this PR (forward-compat for B1/B2).
- `internal/hwdb/nbfc/embed.go` — `//go:embed configs/*.json` populates an `fs.FS`; `LoadCatalog(fs.FS) (*Catalog, error)` parses every file once at daemon start, dedupes by `NotebookModel`, classifies each config's *control mode* (`ControlModeRegister` / `ControlModeRegister16` / `ControlModeACPI` / `ControlModeLua` / `ControlModeMixed`).
- `internal/hwdb/nbfc/match.go` — `Match(dmi DMI) (*Config, MatchTier, error)`. Tier 1: exact `NotebookModel` == `dmi.ProductName`. Tier 2: glob match (nbfc-linux upstream uses substring; we match that for parity). Tier 3: family-prefix (`HP Pavilion 15-*`). Returns `(nil, MatchNone, nil)` on no match — that is the not-an-error case.
- `internal/hwdb/nbfc/sync.go` — `make sync-nbfc-configs` driver: `gh release download v0.5.2 -R nbfc-linux/nbfc-linux`, vendor under `configs/`, rewrite `UPSTREAM`. No-op when SHAs match.
- `internal/doctor/detectors/nbfc_match_d.go` — new detector. Reads DMI via `hwdb.ReadDMI` (re-use, do not fork — `RULE-DOCTOR-05`); calls `nbfc.Match`; emits one Doctor fact per state: `OK` "this model has a known NBFC config (control deferred to v0.8.0)", `Warning` "this laptop is not in the NBFC catalogue — contribute via $url".
- `internal/doctor/detectors/nbfc_match_d_test.go` — table-driven: HP Pavilion 15-cs0xxx (register-only, fact = OK), HP 250 G8 (ACPI-only, fact = OK with "ACPI deferred"), arbitrary uncatalogued laptop (fact = Warning).
- `docs/rules/RULE-NBFC-A.md` — PR A invariants. Per `docs/rules/README.md`, `<!-- rulelint:allow-orphan -->` markers on every `Bound:` line until B1/B2 implement.
- `docs/nbfc.md` — operator-facing: what NBFC support means, why we vendor not link, how upstream contribution works.

**Files (modified):**

- `internal/doctor/runner.go` — register the new detector after `RULE-DOCTOR-DETECTOR-ECLOCKEDLAPTOP`; that earlier detector's "control unavailable" message is now a fallback, fired only when nbfc.Match returns no result.
- `internal/doctor/detectors/ec_locked_laptop_d.go` — gain a peer-detector check: don't fire the v0.6 platform_profile-selector hint when nbfc_match_d already surfaced a fact for the same DMI.
- `internal/hal/gpu/nvml/probe.go` — `ErrLaptopDgpuRequiresEC`'s detail string upgrades from "spec-09 NBFC backend" to "this model has a known NBFC config — fan control deferred to v0.8.0" / "this model is not in the NBFC catalogue".
- `Makefile` — add `sync-nbfc-configs` target.
- `CHANGELOG.md` — v0.8.0-A entry.

**Files (out of scope, not touched):**

- All `internal/hal/*` backends — A is data + diagnostic only.
- All `internal/controller/*`, `internal/watchdog/*` — unchanged.
- The wizard — A is invisible to first-boot; doctor card surface only.

### PR B1 — pure-Go EC transport (`internal/ec/`)

**Goal:** read and write embedded-controller registers via either `ec_sys` debugfs or `/dev/port`. No CGO; no DKMS module shipped by ventd; no kernel patches. The transport is the foundation; the HAL backend in B2 sits on top.

**Files (new):**

- `internal/ec/transport.go` — `Transport` interface: `Read(reg uint8) (uint8, error)`, `Write(reg uint8, val uint8) error`, `Read16(reg uint8) (uint16, error)`, `Write16(reg uint8, val uint16) error`, `Close() error`. Returns `ErrECBusy` on retryable in-flight transitions; the busy-wait protocol mirrors `nbfc-linux`'s `ec_sys.c` exactly.
- `internal/ec/ec_sys.go` — debugfs implementation: `os.OpenFile("/sys/kernel/debug/ec/ec0/io", O_RDWR, 0)`, `io.ReadAt(buf, int64(reg))` / `io.WriteAt(buf, int64(reg))`. Refuses if `read_support` / `write_support` aren't set (parses `/sys/module/ec_sys/parameters/{read,write}_support` once at Open).
- `internal/ec/dev_port.go` — `/dev/port` implementation: open `O_RDWR`, seek to `0x62` (data) / `0x66` (command), implement the EC handshake byte-poke sequence per ACPI 4.0 §12.3. Bit 0 of `0x66` ≙ OBF (output buffer full); bit 1 ≙ IBF (input buffer full). Wait-with-timeout (`ECPollIntervalDefault = 10 µs`, `ECPollTimeoutDefault = 1 ms`) before each read/write.
- `internal/ec/preflight.go` — `Available(logger) (Transport, error)`: tries `ec_sys` first, falls back to `/dev/port`, returns explicit `ErrECNotAvailable` with the chain of failures. Used by both the doctor detector and the HAL backend.
- `internal/ec/safety.go` — `Refuse(reg uint8) bool` — refuse any read/write to a register not declared in the active config (the `nbfc.Config.AllRegistersUsed()` list). Prevents a corrupt config from doing a wild write; also prevents test scaffolding from racing the live system.
- `internal/ec/transport_test.go`, `ec_sys_test.go`, `dev_port_test.go` — `fakeec` test fixture: backing `map[uint8]uint8` indexed by register, exercised via the `Transport` interface. The transport-impl tests stub at the syscall boundary (`os.OpenFile` seam).
- `internal/preflight/checks/ec_sys.go` — preflight check parallel to the `thinkpad_acpi fan_control=1` pattern. Detects missing `ec_sys.write_support=1`; AutoFix dispatches via the existing `/api/hwdiag/modprobe-options-write` endpoint (extended allowlist).
- `internal/preflight/checks/ec_sys_test.go`.
- `docs/rules/RULE-NBFC-B1.md` — EC-transport invariants.

**Files (modified):**

- `internal/hwmon/modprobe_options.go` — extend `allowlist` to include `("ec_sys", "write_support=1")`.
- `internal/hwmon/modprobe_options_test.go` — table-row extension; binds the new allowlist entry to `RULE-MODPROBE-OPTIONS-01`.
- `cmd/ventd/preflight.go` — wire the new check into the orchestrator's catalogue.
- `CHANGELOG.md` — v0.8.0-B1 entry.

**Out of scope:** anything HAL-shaped. B1 is purely the kernel-side I/O primitive.

### PR B2 — `internal/hal/nbfc/` backend

**Goal:** the HAL backend that closes the loop. Sits on `internal/ec.Transport` + `internal/hwdb/nbfc.Config`. Plumbs into watchdog, calibration, smart-mode aggregator, doctor — all unchanged on their side because `hal.FanBackend` is the contract.

**Files (new):**

- `internal/hal/nbfc/backend.go` — `Backend` struct + `hal.FanBackend` implementation:
  - `Enumerate`: one `hal.Channel` per `FanConfiguration` entry; `ID` = sanitised `FanDisplayName`; `Role` = inferred from name (`CPU Fan` → `RoleCPU`, etc.); `Caps` = `CapRead | CapWritePWM | CapRestore`.
  - `Read`: `transport.Read(cfg.ReadRegister)` (or `Read16` when `ReadWriteWords=true`); scale to `Reading.PWM` via the `Min/MaxSpeedValueRead` (or `Min/MaxSpeedValue` when not independent); RPM unreadable for nbfc (no fan-tach register in the schema), so `Reading.RPM = 0` and `Reading.OK = true` only when the PWM read succeeded.
  - `Write`: clamp `pwm` to `[MinSpeedValue, MaxSpeedValue]`, scale linearly, `transport.Write(cfg.WriteRegister, val)`. Plus: apply every `RegisterWriteConfiguration` whose `WriteOccasion = OnWriteFanSpeed` in order, with the correct `WriteMode` semantics (`Set` = direct write, `And` = read-mask-and-write, `Or` = read-mask-or-write).
  - `Restore`: for every fan with `ResetRequired=true`, write `FanSpeedResetValue` to `WriteRegister`. For every `RegisterWriteConfiguration` with `ResetRequired=true`, write `ResetValue` (or apply `ResetWriteMode` semantics). Tag the channels with origin in `hal.Channel.Opaque` so the watchdog's existing `RestoreOne` dispatch wires through unchanged.
- `internal/hal/nbfc/backend_test.go` — full happy-path: Enumerate, Read, Write at min/max boundaries, Restore including `RegisterWriteConfigurations`, Close. `fakeec` fixture from B1.
- `internal/hal/nbfc/register_write_config.go` — `WriteMode` enum + `Set` / `And` / `Or` / `Call` / `Lua` dispatch (`Call` and `Lua` return `ErrNBFCConfigNeedsAcpiBridge` / `ErrNBFCConfigNeedsLuaRuntime`).
- `internal/hal/nbfc/register_write_config_test.go`.
- `internal/hal/nbfc/init_writes.go` — apply every `RegisterWriteConfiguration` with `WriteOccasion = OnInitialization` on first `Enumerate`. Idempotent (re-applying `Set`/`And`/`Or` is a no-op when the byte already matches the result).
- `internal/hal/nbfc/scaling.go` — `pwmToRegister(pwm uint8, cfg FanConfiguration) uint16` and inverse. Honours `FanSpeedPercentageOverrides` (sparse mappings, e.g. percentage 0 maps to a specific non-zero "fan off" register byte on some HP Omens).
- `internal/hal/nbfc/probe.go` — `Probe(ctx, dmi, ec) (*Backend, error)`: matches DMI → loads catalog config → opens EC transport → constructs `Backend`. Returns `nil, ErrNBFCNoMatch` if `nbfc.Match` returns no result; that is the not-an-error path.
- `cmd/ventd/nbfc_watchdog.go` — analogue of `cmd/ventd/ipmi_watchdog.go::registerIPMIWatchdogEntries`. Walks the NBFC backend's channels and registers each with the watchdog via `RegisterIPMI`-style helper (the existing helper is generic — `RULE-WD-IPMI-ROUTING` already abstracted this).
- `docs/rules/RULE-NBFC-B2.md` — backend-contract invariants.

**Files (modified):**

- `cmd/ventd/main.go` — register the new backend with `hal.Register("nbfc", backend)` when `Probe` succeeds. Otherwise the daemon proceeds without — same pattern as NVML graceful-absent (`RULE-GPU-PR2D-03`).
- `internal/hal/registry.go` — no changes needed; the existing `Register` accepts any name.
- `internal/preflight/checks/conflicts.go` — append `nbfc_service.service` to the userspace-conflict list (already there for the doctor surface; the preflight version uses a similar list). Two writers on the same EC corrupt state — refuse to start.
- `internal/doctor/detectors/userspace_conflict_d.go` — refuse-to-start propagates here unchanged (the rule already names `nbfc_service`).
- `internal/doctor/detectors/nbfc_match_d.go` — fact tier upgrades when the backend is live: from "control deferred to v0.8.0" to "controlled".

### PR B3 — ACPI method bridge (`internal/acpi/` + `acpi_call` DKMS row) — ships in v0.8.0

**Goal:** the 7 catalogue configs that drive fans through ACPI methods rather than direct EC registers (Acer TravelMate P253, ASUSTeK X551CA, HP 250 G8 Notebook PC, HP Pavilion 17 Notebook PC, Acer Aspire E1-570G, plus two others) gain full fan control at v0.8.0 GA. Trust boundary is identical to B2's register path: we only invoke method paths *named in the upstream-vetted nbfc-linux config* — never arbitrary firmware code.

**Mechanism:** ACPI methods can't be invoked from userspace through the mainline kernel alone (`/sys/firmware/acpi/tables/` is read-only). The accepted Linux solution is the `acpi_call` out-of-tree module (GPL-2.0+, mature since 2013, packaged by Arch / Fedora COPR / Debian non-free; the `tuxedo-keyboard` and `corectrl` projects both rely on it). It exposes `/proc/acpi/call`: write a method path (e.g. `\_SB.PCI0.LPCB.EC0.SFNV`) plus arguments, read the result. nbfc-linux's `ec_acpi.c` mirrors exactly this protocol, so our Go port is a line-for-line translation.

**Files (new):**

- `internal/acpi/call.go` — `Call(method string, args ...uint64) (uint64, error)`. Opens `/proc/acpi/call`, writes the request, reads response. Parses both legacy-decimal and `0x`-prefixed-hex response formats (`acpi_call` evolved over years). Refuses methods not in an allowlist derived from the active nbfc config (`Config.ACPIMethodsUsed()`) — same closed-set discipline as `internal/ec.Refuse` (`RULE-NBFC-EC-02`).
- `internal/acpi/preflight.go` — `Available() (bool, error)`: checks `/proc/acpi/call` exists, attempts a no-op call (`\_SB`), reports `ErrACPICallNotLoaded` with the wrapping `RULE-DOCTOR-DETECTOR-DKMSSTATUS`-shaped remediation when missing.
- `internal/acpi/call_test.go` — `fakeacpi` fixture (a `bytes.Buffer`-backed `io.ReadWriteSeeker` masquerading as `/proc/acpi/call`); both response formats covered.
- `internal/preflight/checks/acpi_call.go` — preflight check parallel to `ec_sys`. Detects missing module + missing DKMS install; AutoFix dispatches via the existing DKMS install pipeline (same shape as the `nct6687d` row at `internal/preflight/checks/secure_boot.go` consumes).
- `internal/preflight/checks/acpi_call_test.go`.
- `internal/hwdb/profiles-v1.yaml` — new driver_profile entry: `module: acpi_call`, `family: acpi-bridge`, `capability: rw_full`, `pwm_unit: -` (passthrough), `dkms_required: true`, `kernel_module_source: github.com/nix-community/acpi_call`, `conflicts_with_userspace: []`, `runtime_conflict_detection_supported: true`.
- `docs/rules/RULE-NBFC-B3.md` — ACPI-bridge invariants.

**Files (modified):**

- `internal/hal/nbfc/register_write_config.go` — `WriteMode = Call` dispatches to `acpi.Call(method)` instead of returning `ErrNBFCConfigNeedsAcpiBridge`. The error stays as the "module not loaded" fallback only — surfaced when `acpi.Available()` returned false but a Call config matched.
- `internal/hal/nbfc/backend.go` — `Read` / `Write` / `Restore` handle `ReadAcpiMethod` / `WriteAcpiMethod` / `ResetAcpiMethod` fields by routing to `acpi.Call`. The percentage-to-byte scaling path (`scaling.go`) is unchanged — ACPI methods take a uint8 argument the same way registers do.
- `internal/hal/nbfc/probe.go` — `ControlModeACPI` and `ControlModeMixed` no longer return `ErrNBFCConfigNeedsAcpiBridge`; they construct the backend with `requireACPI=true`, which probes `acpi.Available()` and surfaces the DKMS install path on miss.
- `internal/hwdb/nbfc/embed.go` — control-mode classification stays exhaustive (`RULE-NBFC-CATALOG-03`), but ACPI is now a supported tier rather than a refusal tier.
- `internal/doctor/detectors/nbfc_match_d.go` — fact text no longer says "ACPI deferred" for the 7 affected models; they now report identically to register-only matches.
- `internal/hal/gpu/nvml/probe.go` — `ErrLaptopDgpuRequiresEC` message no longer differentiates "ACPI deferred" from "register controlled."

**Out of scope:**

- Lua. Still 0 catalogue configs; remains structurally refused with `ErrNBFCConfigNeedsLuaRuntime`. A pure-Go embedding (`yuin/gopher-lua`) is the obvious path *if* the catalogue ever grows a Lua config — we revisit then, not now.
- `acpi_call` packaging for distros that don't carry it. The DKMS pipeline handles build-from-source against the running kernel; we ship the catalogue row + the build trigger. Operators on immutable-rootfs distros (Silverblue, NixOS) hit `RULE-PREFLIGHT-SYS-03` (lib_modules read-only) the same way they already do for `nct6687d` — same remediation card.

**Trust note:** `acpi_call` allows *any* `/proc/acpi/call` writer to invoke *any* method. ventd's discipline is that the backend only ever passes method paths drawn from the matched nbfc config's `AcpiMethod` fields (closed set, vetted upstream). The path string is *not* operator-controlled — there is no config surface, CLI flag, or API endpoint that accepts a method path. The doctor card lists exactly which method names will be invoked on this hardware so operators can audit before enabling.

---

## §4 — Trigger conditions for the NBFC backend

The HAL backend's `Probe` activates if and only if:

1. **DMI matches a catalog config.** `nbfc.Match(dmi)` returns a non-nil `*Config`. No catalog match → backend doesn't construct; daemon proceeds without NBFC.
2. **Catalog config is not Lua-driven.** `cfg.ControlMode ∈ {ControlModeRegister, ControlModeRegister16, ControlModeACPI, ControlModeMixed}`. Lua-using configs (currently 0/311) return `ErrNBFCConfigNeedsLuaRuntime` from `Probe`. ACPI and Mixed configs require additional preconditions (next two predicates).
3. **EC transport opens cleanly.** Required for all register-touching configs. `internal/ec.Available()` returns a non-nil `Transport` — either `ec_sys` with `write_support=1` or `/dev/port` accessible. Pure-ACPI configs (read + write + reset *all* via `AcpiMethod`) skip this check.
4. **ACPI bridge available** if the config uses any ACPI methods. `internal/acpi.Available()` returns nil (the `acpi_call` module loaded). Pure-register configs skip this check.
5. **No conflicting userspace daemon.** `nbfc_service.service` and `nbfc-linux.service` are not running. `RULE-PREFLIGHT-CONFL-03` already covers the policy; we just extend the list.
6. **Not on battery** (`RULE-IDLE-02`).
7. **Not in a container** (`RULE-IDLE-03`).

Any predicate false → the NBFC backend doesn't register; the Doctor surface explains which gate refused and what remediation is available.

---

## §5 — Invariant bindings (RULE-NBFC-*)

The full rule family lands across `RULE-NBFC-A.md`, `RULE-NBFC-B1.md`, `RULE-NBFC-B2.md`, `RULE-NBFC-B3.md`. Drafted skeleton:

- **RULE-NBFC-CATALOG-01** — `embed.FS` catalog parses cleanly at daemon start; a malformed config aborts daemon-start with the offending file named. Bound in B-A.
- **RULE-NBFC-CATALOG-02** — `Match(dmi)` is pure and deterministic; same DMI → same result across calls. Bound in B-A.
- **RULE-NBFC-CATALOG-03** — control-mode classification is exhaustive across the 311-config catalogue; a new config that introduces a fourth control mode fails the catalogue load (forward-compat fence). Bound in B-A.
- **RULE-NBFC-DOCTOR-01** — doctor detector emits exactly one Fact per matched DMI; emits Warning on no-match; emits OK with mode-name detail on match. Bound in B-A.
- **RULE-NBFC-EC-01** — `internal/ec.Available()` selects `ec_sys` before `/dev/port`; surfaces both failure causes when both fail. Bound in B-B1.
- **RULE-NBFC-EC-02** — every `Transport.Read`/`Write` validates `reg` against the active config's register set; rejects unknowns with `ErrECRegisterNotInConfig`. Bound in B-B1.
- **RULE-NBFC-EC-03** — `/dev/port` transport honours OBF/IBF handshake with the ACPI 4.0 §12.3 poll interval; doesn't busy-spin without a timeout. Bound in B-B1.
- **RULE-NBFC-EC-04** — `ec_sys` transport refuses if `write_support` is unset and surfaces the modprobe-options-write remediation. Bound in B-B1.
- **RULE-NBFC-HAL-01** — backend satisfies the full `hal.FanBackend` contract (`RULE-HAL-001..008`). Bound in B-B2 via reuse of `internal/hal/contract_test.go`.
- **RULE-NBFC-HAL-02** — `Write` clamps to `[MinSpeedValue, MaxSpeedValue]` and applies every `RegisterWriteConfiguration` with `OnWriteFanSpeed` in declaration order. Bound in B-B2.
- **RULE-NBFC-HAL-03** — `Restore` writes `FanSpeedResetValue` for every fan with `ResetRequired=true`, and applies every `RegisterWriteConfiguration` with `ResetRequired=true` using its `ResetValue` / `ResetWriteMode`. Bound in B-B2.
- **RULE-NBFC-HAL-04** — `WriteMode = Lua` returns `ErrNBFCConfigNeedsLuaRuntime`; never silently no-op. `WriteMode = Call` routes to `acpi.Call` (B3); returns `ErrNBFCConfigNeedsAcpiBridge` only as the module-not-loaded fallback. Bound in B-B2 + B-B3.
- **RULE-NBFC-HAL-05** — `ReadWriteWords=true` triggers `Read16`/`Write16` (two consecutive bytes); `ReadWriteWords=false` triggers `Read`/`Write`. Bound in B-B2.
- **RULE-NBFC-HAL-06** — `OnInitialization` register writes apply exactly once at first `Enumerate`; replayed `Enumerate` is idempotent. Bound in B-B2.
- **RULE-NBFC-CONFLICT-01** — daemon refuses to start when `nbfc_service.service` is active (preflight + doctor parity). Bound in B-B2; reuses the existing userspace-conflict surface.
- **RULE-NBFC-WD-01** — every NBFC channel registers with the watchdog; `RULE-WD-RESTORE-EXIT` covers the cross-cutting contract. Bound in B-B2 via `RULE-WD-IPMI-ROUTING` parity.
- **RULE-NBFC-ACPI-01** — `internal/acpi.Call(method, args...)` refuses any method path not present in the active config's `AcpiMethodsUsed()` allowlist; closed-set discipline mirrors `RULE-NBFC-EC-02`. Bound in B-B3.
- **RULE-NBFC-ACPI-02** — the `acpi_call` response parser handles both legacy-decimal and `0x`-prefixed-hex response formats and returns wrapped errors when neither parses. Bound in B-B3.
- **RULE-NBFC-ACPI-03** — `Available()` distinguishes "module not loaded" (ENOENT on `/proc/acpi/call`) from "no-op invocation failed" (write succeeded, response unparseable); each surfaces a distinct doctor remediation. Bound in B-B3.
- **RULE-NBFC-ACPI-04** — backend `Probe` activates an ACPI-touching config only when `acpi.Available()` returns nil; otherwise refuses with `ErrNBFCConfigNeedsAcpiBridge` and surfaces the DKMS install path. Bound in B-B3.
- **RULE-NBFC-ACPI-05** — the doctor card for an ACPI-touching match lists every method path that will be invoked (read from `cfg.AcpiMethodsUsed()`), so operators can audit before enabling. Bound in B-B3.

Each rule lands with `<!-- rulelint:allow-orphan -->` until its bound subtest is implemented in the same PR.

---

## §6 — File map summary

```
ventd/
├── internal/
│   ├── ec/                                  [NEW — PR B1]
│   │   ├── transport.go
│   │   ├── ec_sys.go
│   │   ├── dev_port.go
│   │   ├── preflight.go
│   │   ├── safety.go
│   │   └── *_test.go
│   ├── acpi/                                [NEW — PR B3]
│   │   ├── call.go
│   │   ├── preflight.go
│   │   └── call_test.go
│   ├── hal/nbfc/                            [NEW — PR B2 + B3]
│   │   ├── backend.go
│   │   ├── register_write_config.go
│   │   ├── init_writes.go
│   │   ├── scaling.go
│   │   ├── probe.go
│   │   └── *_test.go
│   ├── hwdb/nbfc/                           [NEW — PR A]
│   │   ├── configs/*.json                   (311 files vendored from upstream v0.5.2)
│   │   ├── UPSTREAM
│   │   ├── schema.go
│   │   ├── embed.go
│   │   ├── match.go
│   │   ├── sync.go
│   │   └── *_test.go
│   ├── doctor/detectors/
│   │   ├── nbfc_match_d.go                  [NEW — PR A]
│   │   ├── nbfc_match_d_test.go             [NEW — PR A]
│   │   ├── ec_locked_laptop_d.go            [MODIFIED — PR A: peer-detector check]
│   │   └── userspace_conflict_d.go          [MODIFIED — PR B2: nbfc_service already named, gain refuse-to-start arm]
│   ├── preflight/checks/
│   │   ├── ec_sys.go                        [NEW — PR B1]
│   │   ├── ec_sys_test.go                   [NEW — PR B1]
│   │   ├── acpi_call.go                     [NEW — PR B3]
│   │   ├── acpi_call_test.go                [NEW — PR B3]
│   │   └── conflicts.go                     [MODIFIED — PR B2]
│   ├── hwmon/
│   │   ├── modprobe_options.go              [MODIFIED — PR B1: add ec_sys allowlist entry]
│   │   └── modprobe_options_test.go         [MODIFIED — PR B1]
│   └── hal/gpu/nvml/
│       └── probe.go                         [MODIFIED — PR A: improve ErrLaptopDgpuRequiresEC message]
├── cmd/ventd/
│   ├── main.go                              [MODIFIED — PR B2: hal.Register("nbfc", ...)]
│   ├── nbfc_watchdog.go                     [NEW — PR B2]
│   └── preflight.go                         [MODIFIED — PR B1: wire ec_sys check]
├── specs/
│   └── spec-09-nbfc-backend.md              (this file)
├── docs/
│   └── nbfc.md                              [NEW — PR A]
├── docs/rules/
│   ├── RULE-NBFC-A.md                       [NEW — PR A]
│   ├── RULE-NBFC-B1.md                      [NEW — PR B1]
│   ├── RULE-NBFC-B2.md                      [NEW — PR B2]
│   └── RULE-NBFC-B3.md                      [NEW — PR B3]
├── Makefile                                 [MODIFIED — PR A: sync-nbfc-configs target]
└── CHANGELOG.md                             [MODIFIED — each PR]
```

---

## §7 — Citations

- **Upstream repository:** `https://github.com/nbfc-linux/nbfc-linux` (v0.5.2, GPL-3.0, 2026-05-13)
- **Schema reference:** `nbfc-linux/nbfc-linux/blob/v0.5.2/doc/nbfc_service.json.5.md`
- **EC port-I/O protocol:** ACPI 4.0 specification §12.3 (Embedded Controller Interface)
- **`ec_sys` kernel module documentation:** `kernel.org/doc/Documentation/acpi/ec_sys.txt`
- **`/dev/port` access pattern:** nbfc-linux source `src/ec_linux.c` (the C reference we mirror in pure Go)
- **`acpi_call` reference implementation:** `github.com/nix-community/acpi_call` (community-maintained fork of the original `mkottman/acpi_call`); the response-format evolution is documented in their CHANGELOG
- **nbfc-linux ACPI dispatcher:** `src/ec_acpi.c` (the C reference our `internal/acpi/call.go` mirrors)
- **Catalog license:** GPL-3.0 (same as ventd; vendoring is license-compatible)
- **Predecessor research:**
  - `(research note in git history)` §10.4 "Notebook FanControl Linux (NBFC)"
  - `(research note in git history)` §B.13 "NBFC-Linux userspace EC"
  - `(research note in git history)` §3.4 "nbfc-linux"
  - `(research note in git history)`
