# spec-v0_6_0-split-daemon — separating control-loop hardening from wizard-time root operations

**Status**: draft (2026-05-02 overnight)
**Issue**: #787
**Closes**: many of the v0.5.8.1 wave (#777, #778, #780, #781, #783, #785, #786, #796).
**Replaces**: the single-unit ventd.service architecture introduced before v0.5.0.

## Problem statement

ventd's setup wizard performs operations that are fundamentally incompatible with the unprivileged-control-loop daemon model:

- `apt-get install` (root, distro packages)
- `modprobe` / `init_module(2)` (root, CAP_SYS_MODULE)
- `dkms add / build / install` (root, /usr/src + /var/lib/dkms writes)
- `make install` (root, /lib/modules writes)
- module persistence (root, /etc/modules-load.d writes)
- NVML writes (root effective uid)
- bootloader regen on ACPI conflict (root, grub-mkconfig / update-grub)
- `sensors-detect` (root, /etc/modprobe.d writes)
- arbitrary `/sys/devices/...` enumeration during hardware probe

Two days of HIL on Phoenix's Z690-A and minipc demonstrated that every layered-elevation patch (sudoers drop-in, SUID NVML helper, AppArmor Ux transitions, ReadWritePaths additions, capability grants) closes one symptom while uncovering the next. Each new code path the wizard happens to visit produces a fresh "permission denied" / EROFS / DENIED. The single-unit model can't maintain coverage as the codebase grows.

v0.5.8.1's response was the **root flip**: ship ventd as `User=root`, drop all the elevation theatre. Pragmatic but dilutes the unprivileged-daemon promise. The split-daemon model recovers the promise.

## Proposed architecture

Three units:

```
┌────────────────────────────────────┐  ┌────────────────────────────────────┐
│ ventd.service                      │  │ ventd-setup.service                │
│   User=ventd                       │  │   User=root                        │
│   NoNewPrivileges=yes              │  │   no NNP, no profile               │
│   ProtectKernelModules=yes         │  │   no ReadWritePaths constraint     │
│   ProtectSystem=strict             │  │   Type=oneshot (start on demand)   │
│   ReadWritePaths=                  │  │   ExecStart=/usr/local/sbin/       │
│     /etc/ventd /run/ventd          │  │     ventd-setup --request /run/    │
│     /sys/class/hwmon               │  │     ventd/setup-request.json       │
│   AppArmorProfile=ventd            │  │                                    │
│   capability_bounds=               │  │   Triggers: install kernel headers │
│   AmbientCapabilities=             │  │   build OOT module, dkms register, │
│                                    │  │   modprobe, write modules-load.d,  │
│   Steady-state control loop:       │  │   patch bootloader, run            │
│     - read PWM/RPM/temp            │  │   sensors-detect, NVML writes      │
│     - write PWM (udev g+w)         │  │                                    │
│     - serve web UI                 │  │   Exits when request completes;    │
│     - drive wizard logic           │  │   audit log to /var/log/ventd/     │
│                                    │  │   setup-audit.log                  │
└──────────────┬─────────────────────┘  └────────────────────────────────────┘
               │                                        ▲
               │  systemctl start ventd-setup.service   │
               │  on wizard-step-needs-root events      │
               └────────────────────────────────────────┘

                      Optional third unit:
                ┌────────────────────────────────────┐
                │ ventd-nvml-helper                  │
                │   SUID-root binary                 │
                │   (already shipped in v0.5.8)      │
                │                                    │
                │   Used by ventd.service when       │
                │   NVML writes are needed during    │
                │   normal operation (post-wizard)   │
                │   without invoking ventd-setup.    │
                └────────────────────────────────────┘
```

## Why this works

1. **The control loop never needs root**. Sysfs PWM writes go through DAC permissions granted by the udev rule (`/lib/udev/rules.d/90-ventd-hwmon.rules` chgrps `/sys/class/hwmon/*/pwm[N]` to the `ventd` group). The control loop reads sensors and writes PWMs — all available to a non-root user with the right group membership. Existing v0.5.x code paths.

2. **All privileged operations are wizard-step transitions**. The wizard's state machine already has discrete phases: `installing_driver`, `loading_module`, `applying_kernel_param`, etc. Each is a one-shot operation. Mapping each to a `ventd-setup` invocation is straightforward; the wizard waits for `ventd-setup.service` to enter `inactive(dead)` state then reads the result from `/run/ventd/setup-result.json`.

3. **Audit trail is simpler, not more complex**. Currently every privileged operation logs through ventd's slog → journald. With ventd-setup as a separate unit, the audit log is the systemd journal of `ventd-setup.service` — narrowly scoped, readable via `journalctl -u ventd-setup`. No mixing of control-loop noise with privileged-operation events.

4. **Failure modes are local**. If ventd-setup hangs or crashes mid-install, ventd.service keeps running. The wizard surfaces "setup operation failed" with the failed step's name; the operator can retry without restarting the control loop. Today's single-unit model couples them: a wizard install crash takes the whole daemon down through the OnFailure handler.

## ventd-setup.service request format

Wizard writes `/run/ventd/setup-request.json` then `systemctl start ventd-setup.service`:

```json
{
  "schema_version": 1,
  "operation": "install_oot_driver",
  "params": {
    "chip_key": "nct6687d",
    "kernel_version": "6.8.0-111-generic",
    "ack_secure_boot": false
  },
  "audit": {
    "wizard_session_id": "abc123",
    "requested_by": "phoenix@desktop",
    "client_ip": "192.168.7.10"
  }
}
```

Supported operations (v0.6.0 initial set):

- `install_oot_driver` — apt-get install kernel headers, download driver source, build, dkms register, modprobe
- `install_dependency` — apt-get install \<package list\> with whitelist
- `load_module` — modprobe \<module\> + write /etc/modules-load.d/ventd-\<module\>.conf
- `unload_module` — modprobe -r \<module\> + remove modules-load.d entry
- `patch_kernel_param` — append to /etc/default/grub or systemd-boot loader entries + regen bootloader
- `nvml_write` — NVML fan-speed / fan-policy writes (replaces SUID helper for non-realtime cases)
- `run_sensors_detect` — sensors-detect --auto + capture output

Each operation has a strict input schema; deserialisation rejects unknown fields. Output goes to `/run/ventd/setup-result.json` with `{ok: bool, error: string, audit_summary: string}`.

## Migration from v0.5.8.1 root daemon

Two phases:

**Phase A (v0.6.0-rc.1)** — ship ventd-setup.service alongside the root ventd.service. ventd.service still runs as root; ventd-setup is shipped but not used. Smoke test on the HIL grid that ventd-setup builds + installs the same OOT drivers the v0.5.8.1 root ventd does.

**Phase B (v0.6.0-rc.2)** — flip ventd.service to `User=ventd` + restore the full sandbox. Wizard's privileged operations route through ventd-setup. HIL re-test on the full grid (Z690-A + minipc + Proxmox host + NAS/TerraMaster).

**Phase C (v0.6.0)** — release tag.

Rollback path: a config flag `Setup.UseLegacyRootDaemon: true` keeps Phase A behaviour for one cycle. Operators who hit unexpected breakage in Phase B can flip back; ventd-setup stays unused. The flag is removed in v0.7.0.

## Code surface

**New packages:**
- `internal/setupbroker/` — request schema, dispatch table, audit log writer
- `cmd/ventd-setup/` — main + dispatch loop, per-operation handlers

**Modified:**
- `internal/setup/setup.go` — when wizard reaches a privileged step, emit a request to `/run/ventd` and call `systemctl start ventd-setup`; wait + parse result
- `deploy/ventd.service` — flip back to `User=ventd` + restore hardening
- `deploy/ventd-setup.service` — new unit (oneshot, `User=root`, no sandbox)
- `scripts/postinstall.sh` — install both units, run sysusers
- `internal/hwmon/install.go` — strip the inline `runLogDirRoot` / `rootArgv` plumbing; routes go through the broker
- `.goreleaser.yml` — add `cmd/ventd-setup` to builds, ship the new unit in nfpms.contents

**Removed:**
- `/etc/sudoers.d/ventd` (no longer needed; restored from removal in v0.5.8.1)
- AppArmor `Ux` transitions for sudo / dkms / modprobe etc. (ventd no longer execs them; ventd-setup does, unconfined)

**New rules:**
- `RULE-SETUP-BROKER-01..N` — request schema, dispatch whitelist, audit log requirements

## Why not just systemctl-start the privileged operations directly from ventd?

`systemctl start <unit>` requires `org.freedesktop.systemd1.manage-units` polkit permission. An unprivileged user gets it via PolicyKit rules. We could go that route — but the broker's request-file model gives stronger isolation (the wizard never crosses uid=0, even via DBUS) and makes the audit trail authoritative (the request file IS the privileged-operation contract).

## Why not run the wizard as root?

That's v0.5.8.1's solution. It works but couples the wizard's heavy code (large, evolving) with the control loop's tight code (small, audited). Every wizard refactor risks introducing a privilege escalation. The split keeps the wizard architecture flexible and pushes the privileged surface into a tiny, easy-to-audit binary.

## Open questions

1. **Timing**. Does the wizard block synchronously waiting for ventd-setup, or does it poll? Polling is simpler; synchronous is faster. Probably synchronous with a 5-min timeout.
2. **Concurrent requests**. Two operators racing the same wizard? `flock` on /run/ventd/setup-request.json.
3. **Reboot-required operations** (kernel param patch). Today's wizard surfaces `ErrRebootRequired`; ventd-setup needs to communicate the same condition through the result file.
4. **NVML helper retirement?** Keep it for the runtime-NVML-write case (post-wizard). Reconsider in v0.7+.

## Acceptance criteria

- HIL on all four target machines: install via .deb, complete the wizard end-to-end, see ventd.service running as User=ventd with `NoNewPrivs: 1`. Zero "permission denied" / EROFS / DENIED log lines.
- Unit test: `internal/setupbroker/` request validation rejects unknown operations, malformed input, oversized payloads.
- Audit-log invariant test: every privileged operation produces a /var/log/ventd/setup-audit.log entry with timestamp, operation name, client_ip, success/failure, summary.
- Rollback test: flip `UseLegacyRootDaemon: true`, confirm Phase A behaviour returns.

## Estimated scope

- Spec drafting (this doc): done.
- Broker + setup binary: ~250 LOC Go + 100 LOC tests.
- Wizard plumbing: ~80 LOC Go + 40 LOC tests.
- Unit + .goreleaser.yml updates: ~30 LOC.
- Total: ~500 LOC + tests. ~3-5 days for a focused engineer.

## Cross-refs

- #787 — issue that motivated this spec
- #788 — v0.6.0 product roadmap umbrella
- v0.5.8.1 wave — #777..#786, #796 (the issues the split closes)
