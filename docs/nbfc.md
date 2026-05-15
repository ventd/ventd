# NBFC backend — laptop EC fan control

ventd's NBFC backend (spec-09) extends fan control to laptops whose
embedded controller (EC) the kernel doesn't expose a writable `hwmon`
surface for. Coverage rides on the upstream **`nbfc-linux/nbfc-linux`**
community catalogue — 311 laptop-model configurations curated over a
decade of community reverse-engineering, vendored at upstream tag
`0.5.2` under `internal/hwdb/nbfc/configs/`.

## What works at v0.8.0 GA

| Tier | Configs | Status |
|---|---|---|
| Register-only (8-bit EC ports) | 279 | Controlled (B2) |
| Register-only (16-bit, `ReadWriteWords`) | 26 | Controlled (B2) |
| ACPI-method (firmware-managed) | 7 | Controlled via `acpi_call` DKMS (B3) |
| Lua-driven | 0 | Refused — no Lua runtime |
| **Total** | **311** | **100% catalogue coverage** |

## What this does for an operator

When ventd starts on a laptop, the first-boot probe enumerates
writable PWM channels. If it finds none (the common case for HP /
Dell / Lenovo / ASUS / Acer / MSI / Framework / Toshiba consumer
laptops), v0.5.x ventd previously went monitor-only with a generic
"fan control is owned by the EC" doctor card and no path forward.

v0.6.0+ consults the embedded `nbfc-linux` catalogue using your DMI
identity (`/sys/class/dmi/id/{product_name,board_name,sys_vendor}`).
Three outcomes:

- **Exact / glob / substring match → fan control is available.** The
  doctor surface names the upstream `NotebookModel`, the source file
  the config came from, and the control mode (register / ACPI). The
  NBFC backend registers automatically and drives fans without
  further operator opt-in. The closed-set register allowlist (only
  registers named in the matched config are writable), the upstream-
  vetted catalogue, and the existing battery / container / idle
  refuses (RULE-IDLE-02 / 03) are the safety mechanism.
- **Lua-driven match → monitor-only, refused.** No catalogue entry
  currently uses Lua, but the slot exists for forward compatibility.
- **No match → monitor-only with a contribution invite.** The doctor
  card includes the upstream Configuration HowTo URL and the
  `ec_probe` walk you'll need to produce a new config.

## Safety mechanism

ventd never writes to a register the matched upstream config didn't
declare — `internal/ec.WithAllowlist` wraps the EC transport in a
closed-set gate sourced from `Config.RegistersUsed()`. Same closed-
set discipline applies to ACPI method paths via `Config.AcpiMethodsUsed()`.
The watchdog's restore-on-exit contract (RULE-WD-RESTORE-EXIT) hands
every channel back to firmware-managed mode on daemon exit. Battery,
container, and scrub-active states refuse the daemon entirely
(RULE-IDLE-02, RULE-IDLE-03). These are the same protections that
have been load-bearing in nbfc-linux's upstream daemon for years.

## EC transport

Two transport options, tried in order:

1. **`ec_sys` debugfs** at `/sys/kernel/debug/ec/ec0/io` — the
   in-tree `ec_sys` kernel module exposes EC read/write here when
   loaded with `ec_sys.write_support=1`. ventd's preflight detects
   the missing option and offers a one-prompt fix via the existing
   modprobe-options-write endpoint.
2. **`/dev/port` direct I/O** at command port `0x66` and data port
   `0x62`, with the ACPI 4.0 §12.3 OBF/IBF handshake. Fallback when
   `ec_sys` isn't available; same kernel access path, just lower
   level. Requires `CAP_SYS_RAWIO` (which ventd has as `root`).

## ACPI bridge

The 7 catalogue configs that drive fans through firmware-defined
ACPI methods (HP 250 G8 Notebook PC, HP Pavilion 17 Notebook PC,
Acer TravelMate P253, ASUSTeK X551CA, plus three others) require the
out-of-tree `acpi_call` kernel module (DKMS, GPL-2.0+). ventd's
existing DKMS pipeline (the same shape that installs `nct6687d` and
`legion_laptop`) handles install + signing + Secure Boot enrolment.

Trust boundary: ventd only invokes ACPI method paths that appear in
the matched upstream config. The catalogue is the closed set. There
is no operator-facing CLI / API surface that takes an arbitrary
method path.

## Upstream sync

Periodically re-sync from the upstream nbfc-linux release:

```sh
make sync-nbfc-configs   # bumps internal/hwdb/nbfc/configs/ + UPSTREAM
```

The catalogue ships under GPL-3.0 (same as ventd); the vendor copy
preserves the upstream `LICENSE` and tracks the synced commit SHA +
tag in `internal/hwdb/nbfc/UPSTREAM`.

To contribute a new laptop config (or fix one that misclassifies),
work upstream: <https://github.com/nbfc-linux/nbfc-linux/blob/main/doc/Configuration%20HowTo.md>.
The next `make sync-nbfc-configs` picks up new contributions.

## References

- Upstream: <https://github.com/nbfc-linux/nbfc-linux>
- Schema: <https://github.com/nbfc-linux/nbfc-linux/blob/main/doc/nbfc_service.json.5.md>
- ACPI 4.0 §12.3 (Embedded Controller Interface)
- `ec_sys` documentation: kernel.org/doc/Documentation/acpi/ec_sys.txt
- spec-09 work-tree: `specs/spec-09-nbfc-backend.md`
