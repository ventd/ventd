# Framework Laptop fan control — backend design tradeoff memo

**Status:** Research memo. NO catalog entries delivered in this scope.
Decision required before scope-D wave can include Framework laptops.

**Decision belongs to:** spec-09 NBFC integration chat (forward
reference) OR a new spec-XX ec_command backend chat.

**Bound spec sections:** spec-03 §11 (driver catalog — Framework will
need a new driver YAML once approach is decided), spec-09 NBFC
integration (potential alternative path).

---

## TL;DR

Framework laptops have **no kernel hwmon path for fan PWM control** as
of mainline kernel 6.13. Fan control requires direct EC ioctl commands
through `/dev/cros_ec`. This is materially different from every other
hardware family in the ventd catalog — it's a **new backend** (similar
in shape to IPMI or USB-HID Corsair AIO), not a driver-catalog entry.

Two viable paths forward:
1. **Bundle into spec-09 NBFC integration** — use existing NBFC
   community Framework profile, accept NBFC's runtime as our backend.
2. **Native ventd ec_command backend** — implement a Go port of the
   relevant ChromiumOS ectool commands.

Recommendation: defer to spec-09 chat. NBFC route is materially
cheaper and gets ventd Framework + Acer + Sony VAIO + a long tail of
other consumer laptops simultaneously.

---

## Why no hwmon path on Framework

Framework laptops use a custom EC firmware derived from ChromiumOS.
The Linux kernel exposes the EC through this driver stack:

```
cros_ec_lpcs       (LPC bus shim — module: cros_ec_lpcs)
  └── cros_ec_dev  (chardev /dev/cros_ec — module: cros_ec_dev)
        └── cros_ec_sensorhub (motion sensors — module: cros_ec_sensorhub)
        └── EC commands (fan control via ec_command() ioctls)
```

This stack provides:
- `/dev/cros_ec` chardev with `EC_DEV_IOCXCMD` ioctl
- Sensor reads via cros_ec_sensorhub
- Battery info, button events, thermal sensor reads via cros_ec_dev

What it does NOT provide:
- Any hwmon-class registration for fan PWM as a writable channel.
- Any `/sys/class/hwmon/hwmonX/pwm1` node mapped to the EC fan.

The ChromiumOS `ectool` userspace utility uses `EC_CMD_PWM_SET_FAN_DUTY`
(0x0024) and `EC_CMD_PWM_GET_FAN_RPM_DUTY` (0x0025) ioctl commands to
control the fan. There is no equivalent kernel hwmon abstraction.

This has been discussed in upstream platform/x86 mailing list a few
times; consensus is that adding hwmon would require Framework-specific
EC firmware feature negotiation that isn't in mainline cros_ec_dev's
abstraction layer. Active patchsets have not landed as of 2026-04.

---

## Path A — bundle into spec-09 NBFC integration

NBFC (NoteBook FanControl) is an existing cross-platform fan-control
tool with a substantial community profile library. Framework laptops
have well-tested NBFC profiles for 11th/12th/13th gen Intel and AMD
Phoenix Framework 13/16 chassis.

**How spec-09 brings Framework support:**
- spec-09 (drafted, not yet implemented) integrates NBFC profiles as
  a fallback backend in ventd.
- Framework profile flows through that integration "for free" as soon
  as spec-09 ships.
- ventd UX: user with Framework boots ventd, ventd detects no DMI
  match, falls through to NBFC profile lookup, finds Framework profile,
  uses NBFC backend for fan reads/writes.

**Pros:**
- Zero new ventd backend code.
- Picks up Acer Aspire, Sony VAIO, and ~100+ other consumer laptops
  simultaneously.
- NBFC profile community is active; new Framework chassis variants
  are typically profiled within months of release.

**Cons:**
- NBFC is a Mono/.NET application historically; the Linux
  reimplementation is a Python service. Either embeds a runtime
  dependency in ventd's distribution path (memory previously noted
  spec-09 chat 4 retargeted NBFC from v1.0α to v0.8.0, and that
  spec-09 is AGPL-3 NOT Apache — pattern-only integration, not direct
  code bundling).
- NBFC profiles are XML/text declarative; ventd cannot extend them
  with predictive features. Framework remains "frozen at NBFC's
  capabilities" until/unless ventd writes a real backend later.
- Framework-specific quirks (battery-check fan override, temp sensor
  selection) may not be cleanly expressible in NBFC's profile schema.

**Estimated cost:** ZERO additional cost beyond what spec-09 already
budgets. Framework support is a free side-effect.

---

## Path B — native ventd ec_command backend

Implement a Go port of the relevant ChromiumOS EC commands using
`golang.org/x/sys/unix.IoctlSetInt` (or syscall.Syscall) against
`/dev/cros_ec`.

**Required EC commands:**

| Command | Purpose | Hex |
|---|---|---|
| `EC_CMD_PWM_GET_FAN_TARGET_RPM` | Read current fan RPM target | 0x0020 |
| `EC_CMD_PWM_SET_FAN_TARGET_RPM` | Write fan RPM target | 0x0021 |
| `EC_CMD_PWM_GET_FAN_RPM_DUTY` | Read current fan PWM duty | 0x0025 |
| `EC_CMD_PWM_SET_FAN_DUTY` | Write fan PWM duty | 0x0024 |
| `EC_CMD_THERMAL_GET_THRESHOLD` | Read temp threshold config | 0x0050 |
| `EC_CMD_THERMAL_AUTO_FAN_CTRL` | Re-enable EC auto fan control | 0x0052 |
| `EC_CMD_TEMP_SENSOR_GET_INFO` | Enumerate temp sensors | 0x0070 |

(There are more. Framework's ChromiumOS EC fork adds Framework-specific
commands too — battery-extender, backlight, etc., not relevant here.)

**Wrapper approach:**
- Define Go struct mirroring `struct cros_ec_command` from kernel uapi
  (`linux/include/uapi/linux/cros_ec_dev.h`).
- Open `/dev/cros_ec`, ioctl-call EC_DEV_IOCXCMD with command + payload.
- Wrap PWM read/write into ventd's existing fan-channel abstraction.
- Add `chip: "cros_ec_fan"` to driver catalog.

**Pros:**
- Native Go, no runtime dependencies.
- Full predictive-thermal support (spec-05) can extend to Framework.
- Framework users get the same ventd polish as homelab users.

**Cons:**
- Net-new backend = full subsystem scope. Per scope-cost-calibration
  doc, new backends are $20-40 PRs in CC budget.
- Per-chassis quirks (Framework 11th/12th/13th gen Intel vs Phoenix
  AMD vs upcoming Strix Halo) need testing — Phoenix has no Framework
  HIL access (gap from box matrix).
- ChromiumOS EC ABI evolves slowly but is not promised stable across
  Framework EC firmware updates. Maintenance burden.

**Estimated cost:** $25-40 first PR + $10-20 per additional chassis
generation. Likely 3-4 PRs to v1.0.

---

## Path comparison

| Dimension | Path A: NBFC bridge | Path B: native ec_command |
|---|---|---|
| New ventd code | Minimal (NBFC adapter) | New backend (~600 LOC Go) |
| Cost | $0 (folded into spec-09) | $25-40 first PR |
| Time to first Framework support | When spec-09 ships (v0.8.0) | Whenever spec-XX ships |
| Predictive thermal capability | NO (frozen at NBFC profile schema) | YES |
| Maintenance burden | Low (NBFC community owns profiles) | Medium (Framework EC ABI tracking) |
| HIL needed | NO (NBFC profiles validated by community) | YES (Framework laptop required) |
| Captures other laptops simultaneously | YES (Acer, Sony, 100+) | NO |

---

## Recommendation

**Defer to spec-09 NBFC integration chat.** Path A is materially
cheaper for v1.0 timeline, and the consumer-laptop tier has too many
stakeholders for ventd-native backends to be a viable scaling
strategy. Path B can become a v1.x revisit IF Framework users
specifically demand predictive features that NBFC cannot deliver.

When spec-09 chat happens, that chat should:
1. Audit the NBFC Framework profile schema for completeness.
2. Confirm AGPL-3 boundary (pattern-only integration per memory anchor,
   not code reuse).
3. Define the runtime-bridge mechanism (subprocess vs in-process
   port).
4. Confirm Framework chassis coverage in NBFC's profile library.

---

## Out of scope for this memo

- Framework Strix Halo (16-inch AMD, late 2025 / 2026). New EC firmware
  generation; needs separate scoping when hardware is publicly
  available.
- Framework 12-inch (announced but not shipping at time of writing).
  Likely shares EC firmware family with 13-inch.
- Other ChromiumOS-EC laptops (System76 some models, MNT Reform). Same
  shape as Framework — would benefit from whichever path ventd picks.

---

## References

- ChromiumOS EC commands header (canonical EC_CMD_* enum):
  https://chromium.googlesource.com/chromiumos/platform/ec/+/refs/heads/main/include/ec_commands.h
- ChromiumOS ectool source (userspace reference impl):
  https://chromium.googlesource.com/chromiumos/platform/ec/+/refs/heads/main/util/ectool.cc
- Framework EmbeddedController fork (Framework-specific EC commands):
  https://github.com/FrameworkComputer/EmbeddedController
- Linux cros_ec_dev kernel driver:
  https://github.com/torvalds/linux/blob/master/drivers/platform/chrome/cros_ec_chardev.c
- NBFC Linux reimplementation (referenced but not endorsed; check
  current state at spec-09 chat time):
  Hwell-known fork: https://github.com/nbfc-linux/nbfc-linux
