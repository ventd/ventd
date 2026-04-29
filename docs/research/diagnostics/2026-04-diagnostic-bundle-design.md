# 2026-04 Diagnostic Bundle Prior Art

**Status:** research doc, informs spec-03 PR 2c diagnostic bundle design and `internal/diag` package.
**Scope:** survey of established diagnostic-bundle tools in the Linux ecosystem to identify proven structures, redaction strategies, and items worth capturing for a fan-controller-specific bundle.
**Out of scope:** novel bundle designs not yet shipped, Windows-specific tools, full system-snapshot tools beyond the diagnostic use case.
**Cross-cuts:** privacy threat model (Phase 2 #6, next), spec-05-prep trace harness (`internal/ndjson` substrate shared).

---

## 1. Why study prior art

ventd's diagnostic bundle has three distinct callers:

1. **User filing a bug report** — wants a one-command, redacted, attachable archive.
2. **ventd's own automated calibration recovery** — wants structured machine-parseable state for replay.
3. **spec-05 model trainers** — want fixtures for the predictive thermal trace harness (separate from diag bundle but shares `internal/ndjson` substrate).

Each existing tool (sosreport, hwloc, lstopo, `liquidctl --debug`, `journalctl --user-unit`, OpenZFS `zpool events`, kdump) has solved a slice of this. Stealing structure where it works is cheaper than inventing.

---

## 2. sosreport — the gold standard for human-attached bundles

### 2.1 Provenance and scope

sos (formerly sosreport) is the canonical Linux diagnostic-bundle tool. Originated at Red Hat ca. 2009, written in Python, modular plugin architecture. Now `sos report` (RHEL 8+ syntax). Open source, primary reference: https://github.com/sosreport/sos.

### 2.2 Structural lessons applicable to ventd

**Lesson 1: top-level symlinks for fast triage.** sosreport's tarball expands to a directory containing both a Linux-rootfs-mirror (`./etc`, `./proc`, `./sys`, `./var`, `./boot`) AND top-level symlinks pointing into `sos_commands/<plugin>/<command>` for the most-needed outputs:

```
./uname        -> sos_commands/general/uname
./dmidecode    -> sos_commands/hardware/dmidecode
./hostname     -> sos_commands/general/hostname
./lspci        -> sos_commands/general/lspci
./free         -> sos_commands/memory/free
./installed-rpms -> sos_commands/rpm/...
```

A support engineer opens `cat dmidecode | head` and immediately knows the hardware. They don't have to know the plugin layout.

ventd-equivalent symlinks:
```
./hwmon       -> commands/hwmon/sensors_-u
./gpu         -> commands/gpu/lspci_filtered
./calibration -> state/calibration/current.json
./profile     -> state/hwdb/effective_profile.json
./journal     -> commands/journal/ventd-tail.txt
./version     -> commands/ventd/version
```

**Lesson 2: filesystem-shaped capture for raw paths.** sosreport copies `/etc/<some-config>` to `./etc/<some-config>` verbatim. This preserves provenance — the bug filer can grep `./etc/...` and the path mirrors what's on the live system.

ventd applies this to `/sys/class/hwmon/*/`, `/sys/class/drm/card*/device/hwmon/*/`, `/etc/ventd/*`, `/var/lib/ventd/calibration/*`. Bundle `./sys/class/hwmon/hwmon0/pwm1` contains the value `pwm1` had at capture time. Easy to diff between two captures.

**Lesson 3: command-output capture for ephemera.** Things that aren't files (process state, NVML query results, lspci formatted output) go in `sos_commands/<category>/<command_with_args_in_filename>`. Filenames preserve the exact command run, so reproducibility is one `bash -c <filename>` away.

ventd applies this to `commands/nvml/nvmlDeviceGetCount`, `commands/amdgpu/amdgpu_pm_info`, `commands/userspace/processes_with_hidraw_open`, `commands/hwmon/sensors_-u`, etc.

**Lesson 4: explicit "this came from this command" provenance.** sosreport never paraphrases. If `lspci -vvv` was run, the file is named after the command, not "pci_devices". Reduces ambiguity. Catches "did this come from the running system or some helper computed it?" questions instantly.

ventd does the same — bundle filenames always preserve the command + args, never editorialize.

### 2.3 Redaction lessons (carries into Phase 2 #6)

`sos clean` (formerly `soscleaner`) ships separately from `sos report`. Default-on in some distros, default-off in others. Captures these classes by default:
- IPv4 (with CIDR notation and dash-separated DHCP hostname forms — see [sosreport/sos#3388](https://github.com/sosreport/sos/issues/3388))
- IPv6
- MAC addresses
- Hostnames (system FQDN + subdomains)
- Domain names (user-specified)
- Usernames
- User-supplied keywords

Output is *consistent obfuscation* — same input → same output across the bundle. Maintains analytical utility (you can still see "host1 talks to host2" without seeing real names). Mapping table written to `/etc/sos/cleaner/default_mapping`, kept private to bundle generator.

Disable list: `--disable-parsers <hostname,ip,ipv6,mac,keyword,username>`.

ventd's redactor inherits this consistent-mapping pattern. spec-03 amendment §16 already mandates redaction; this corroborates the approach.

### 2.4 Plugin model — applicable to ventd v0.5+?

sosreport plugins are independently registered, can be enabled/disabled (`--enable-plugins`, `--skip-plugins`). Each plugin declares which files/commands it captures.

**ventd v0.4.x is far too small for a plugin model.** spec-03 PR 2c hard-codes the capture list. If ventd grows to where third parties want to register diag plugins (community-contributed AIO drivers, exotic ARM SoC support), plugin model becomes appropriate. Defer to v1.x.

### 2.5 Anti-patterns to avoid

**A1: 23-MB+ bundles.** sosreport defaults capture so much that bundles exceed 250 MB on stuffed servers, triggering Red Hat's "use file upload not email" guidance. ventd's bundle should be sub-MB by default — fan controller diagnostics don't need full `journalctl` history.

**A2: Python startup cost / dependency sprawl.** sosreport pulls in `sos.collector`, `sos.report`, plugin tree, takes seconds to start. ventd bundle generation must be fast (sub-second to start, total runtime under 5s for a no-stress capture) — Go binary, no Python.

**A3: Implicit redaction.** sos's `--clean` is opt-in by default. Several public Red Hat support cases have been filed where customers attached non-cleaned bundles by mistake. **ventd defaults to redacted** with explicit `--no-redact` for the user's own debug runs. This matches CoolerControl's default-conservative ethos.

---

## 3. liquidctl `--debug` — the AIO/HID-device idiom

### 3.1 What liquidctl captures

`liquidctl --debug <subcommand>` prints a structured trace to stderr containing:
- script path, version, platform (Linux-X.Y-Z-arch)
- Python interpreter version and key dependency versions (hidapi, pyusb, pillow, smbus, crcmod)
- per-bus probe results (HidapiBus, PyUsbBus, LinuxI2c)
- per-device VID:PID, hidraw path, instanced driver
- raw HID write/read packets (hex-dump) for each report exchange
- keyval store path (`/run/user/<uid>/liquidctl/vidNNNN_pidMMMM/locK`)

Every issue report on liquidctl's GitHub includes `liquidctl --debug` output. The format has been stable for years and proven sufficient for triage.

### 3.2 Lessons applicable to ventd

**L1: Capture the entire wire trace.** When a Corsair AIO behaves erratically, knowing which feature reports were sent in what order is essential. ventd-equivalent: when calibration fails or a write doesn't take, capture the sysfs read-back-after-write sequence.

For NVML: capture each NVML call, its return code, and timestamp. ventd's nvml wrapper logs this at trace level; PR 2c bundle includes a configurable trace tail.

For hwmon: each `pwm1=X` write paired with the immediate read-back and a 200ms-later read-back. This is exactly the "BIOS overridden" detection from spec-03 amendment §15. PR 2c bundle just dumps the rolling buffer.

**L2: Dependency version capture.** liquidctl logs the version of every linked library. ventd-equivalent: kernel version, kernel build options for hwmon, loaded hwmon kernel modules with versions (`modinfo nct6775`), Go runtime version, libnvidia-ml.so version, every binary's build tag and date.

**L3: Per-device structured key (VID:PID, hidraw path).** liquidctl uses the hidraw path as the canonical identifier. ventd uses `/sys/class/hwmon/hwmonN/name + dmi_serial + pci_slot_name` (per spec-03 amendment §11 inheritance keying). Bundle records this key for every captured device.

### 3.3 What liquidctl gets wrong (avoid)

**A1: Output is human-formatted, not machine-readable.** Hex dumps with colons, mixed prose and data. Hard to grep, hard to parse, hard to diff between captures. ventd uses NDJSON for the wire trace inside the bundle — one event per line, fields named explicitly.

**A2: No redaction.** liquidctl debug output leaks hostname, USB physical paths, sometimes serial numbers (`Firmware version: ...`). For ventd bundles, redactor strips DMI serials, USB physical paths sanitized to topology-only, hostnames replaced with consistent token.

**A3: No version history.** Each `--debug` call is a fresh snapshot. Useful for "right now" but useless for "was this device misbehaving 2 minutes ago when the fan stopped?" ventd's bundle captures a rolling buffer of the last N events, which liquidctl doesn't.

---

## 4. CoolerControl's diagnostic export

### 4.1 What it captures

CoolerControl ships a diagnostic export (per docs at https://docs.coolercontrol.org/) that includes:
- daemon log
- detected devices with full sysfs paths
- profile config
- channel state at capture time

### 4.2 Notable choice: profile + state in same bundle

CC's bundle co-locates the *configuration* (what the user wanted) with the *state* (what actually happened). This is invaluable for "I set 60°C → 50%, but the fan stays at 30%" type bugs.

ventd-equivalent: bundle includes
- `state/calibration/current.json` (matched profile, calibration result, schema version)
- `state/runtime/current_setpoints.ndjson` (last N controller decisions)
- `state/runtime/active_profile.yaml` (what the user configured)

Three artifacts, one place. Engineer can immediately see config-vs-actual divergence.

### 4.3 What CC misses that ventd should not

CC's bundle is GUI-driven (export button in the application). For a daemon-without-GUI like ventd, the equivalent is `ventd diag bundle` CLI. Same output, but invocable from a support context where the user has SSH only.

---

## 5. hwloc / lstopo — the topology-snapshot model

### 5.1 What hwloc does

`hwloc-gather-topology` (part of the hwloc package) captures a complete platform topology snapshot: CPU sockets, NUMA nodes, cache hierarchy, PCI tree, all hwloc-visible devices. Output is XML (machine-parseable) plus a human-readable summary.

The pattern: take a snapshot of a complex platform model, store it in a way that lets the same hwloc tools render it later as if the original system were live.

### 5.2 Lesson for ventd: replay-capable snapshots

ventd bundle's `state/hwdb/effective_profile.json` is the equivalent of an hwloc snapshot. Given that JSON, `internal/hwdb` should be able to reconstruct the matched profile *as if the original system were present* — same matcher tier, same precedence resolution, same effective fields.

This is critical for support: an engineer with a bundle in hand can run ventd in "replay mode" against the bundle and see exactly what the daemon saw. spec-05-prep trace harness already requires replay; spec-03 PR 2c bundle inherits the requirement.

Implementation: bundle's hwdb snapshot lives in NDJSON (per spec-05-prep §4.4 schema), parseable by `internal/trace.Parse`. Same Go package handles both diag bundles and trace harness fixtures.

---

## 6. journalctl + systemd-cat — the structured-log model

### 6.1 Why it matters

ventd is a systemd service. Its primary log channel is the journal. `journalctl -u ventd` is the diagnostic source of truth.

But journalctl output verbatim is too noisy for a bundle (multi-day retention, all priorities). The bundle wants:
- last N entries (e.g., 1000 lines or 24 hours, whichever is smaller)
- only `_SYSTEMD_UNIT=ventd.service` 
- structured fields preserved (`_PID`, `_BOOT_ID`, custom `VENTD_EVENT=` fields)

### 6.2 Capture pattern

```
journalctl -u ventd.service --since "24 hours ago" \
  -o json --output-fields=MESSAGE,PRIORITY,VENTD_EVENT,VENTD_DEVICE,_PID
```

NDJSON output. Each line a record with structured fields. Bundle stores as `commands/journal/ventd-tail.ndjson`. Parseable, redactable per-field.

### 6.3 Custom journal fields ventd should emit

Per ventd-daily-rules.md and existing logging, ventd already emits some structured fields. PR 2c bundle work formalizes this — every diag-relevant log line carries:

- `VENTD_EVENT` — high-level event tag (`calibration_start`, `pwm_write`, `bios_override_detected`, etc.)
- `VENTD_DEVICE` — hwmon-name + chip identifier (e.g., `nct6798:fan2`)
- `VENTD_BACKEND` — `hwmon`, `nvml`, `amdgpu`, `corsair_aio_hidraw`, `legion_ec`

Engineer with the bundle can `grep VENTD_EVENT=bios_override_detected` and immediately find every spec-03 amendment §15 trip.

### 6.4 PII in journal

Journal entries can contain hostnames in syslog identifier, user names if a user-side ventd-cli was invoked, paths under `/home/$USER` if the user passed a profile path. Redactor must process the journal NDJSON the same way it processes other bundle content.

---

## 7. /sys/class/hwmon walk — the structural reference

### 7.1 What lm_sensors `sensors -u` produces

`sensors -u` (machine-readable mode) walks every `/sys/class/hwmon/hwmon*/` and emits per-chip blocks:

```
nct6798-isa-0290
Adapter: ISA adapter
in0:
  in0_input: 0.880
  in0_min: 0.000
  in0_max: 1.744
fan1:
  fan1_input: 1085.000
  fan1_min: 0.000
  fan1_pulses: 2.000
temp1:
  temp1_input: 30.000
  temp1_max: 80.000
  temp1_crit: 95.000
```

Format: chip-header line, then `<sensor>:` block with key-value indented pairs.

### 7.2 Why ventd captures this verbatim

`sensors -u` output is what every Linux user knows how to read. Bundle includes it under `commands/hwmon/sensors_-u`. Even if every other ventd-specific file confuses the engineer, this file is universally interpretable.

### 7.3 What ventd captures BEYOND sensors -u

sensors only reads the hwmon-exposed attributes. ventd's bundle additionally captures:
- `pwm*_enable` (current control mode per spec-03 amendment §1)
- `pwm*_mode` (DC vs PWM per Q4 below)
- `*_pulses` (RPM scaling factor, often misconfigured)
- driver-specific debugfs entries when readable (e.g., `/sys/kernel/debug/dri/<N>/amdgpu_pm_info`)
- module parameters at `/sys/module/<name>/parameters/*`

**Q4 cross-cut:** DC vs PWM `pwm_mode` matters for spec-04 PI tuning. DC fans have different transient response than PWM fans. Bundle must capture this so support-engineer + spec-04 work both have the data.

---

## 8. nvidia-bug-report.sh — vendor-specific deep capture

### 8.1 Provenance

`nvidia-bug-report.sh` ships with the proprietary NVIDIA driver. When users report driver bugs, NVIDIA asks for this bundle. Single shell script, ~3000 lines, captures:

- `dmesg` (full)
- NVIDIA kernel module info (`/proc/driver/nvidia/{version,registry,gpus/*/information}`)
- Xorg log (relevant on display systems)
- nvidia-smi `-q` (full XML query)
- nvidia-settings query (display-related)
- relevant `/var/log/messages` excerpts
- GPU PCIe config space hex dump

### 8.2 Lessons for ventd

**L1: Vendor-specific files are worth dedicated capture paths.** ventd shouldn't try to be a generic system diagnostic. It should deeply capture the fan-control surface and rely on `sosreport`/`nvidia-bug-report.sh` for the broader system context if needed. Bundle README explicitly says "if you also have a system-wide issue, consider also attaching `sosreport` output."

**L2: Per-vendor capture is keyed off detection.** nvidia-bug-report.sh detects whether the proprietary driver is loaded; if not, doesn't try to query it. ventd does the same — `commands/nvml/*` only populated if libnvidia-ml.so opens; `commands/amdgpu/*` only if `/sys/class/drm/card*` has `amdgpu` driver bound.

### 8.3 What NOT to copy

nvidia-bug-report.sh captures the entire dmesg and full Xorg log. Most of it is irrelevant to the bug. ventd's bundle filters: only ventd's own dmesg lines (`dmesg | grep -E 'nct6|amdgpu|nvidia|ventd'`), only journal entries from ventd's unit, only PCI devices in the relevant device classes.

---

## 9. kdump / crash dumps — the on-failure-only capture

### 9.1 The kdump model

When the kernel panics, `kdump` captures a core dump to a pre-configured path (`/var/crash/`). The dump is ON-FAILURE-ONLY. No periodic capture, no rolling buffer. The disk space cost is acceptable because failure is rare.

### 9.2 ventd-equivalent: on-failure auto-bundle

ventd already has the auto-recovery primitive (per ventd masterplan). When auto-recovery triggers, ventd should write a "minimal failure bundle" to `/var/lib/ventd/diag-bundles/auto-<timestamp>/`:

- the trigger event (which sensor, which threshold, which decision)
- last 60 seconds of NDJSON trace
- current `state/calibration/current.json`
- snapshot of relevant `/sys/class/hwmon/...` paths

This is a SUBSET of what `ventd diag bundle` produces interactively. Same writer code, smaller scope, no human in the loop.

PR 2c ships the interactive `ventd diag bundle` first. Auto-bundle on failure can land in a follow-up PR (probably v0.5.x or with spec-07 scheduler hardening).

---

## 10. Bundle structure synthesis (recommendation for PR 2c)

Based on §2-9 lessons, ventd's bundle structure:

```
ventd-diag-<hostname-redacted>-<timestamp>.tar.gz
├── README.md                          # human-readable index, see §11
├── manifest.json                      # machine index: file → schema version
├── ventd                              # symlinks to top-level useful files
├── hwmon                              # symlink to commands/hwmon/sensors_-u
├── gpu                                # symlink to commands/gpu/lspci_class_03
├── journal                            # symlink to commands/journal/ventd-tail.ndjson
├── calibration                        # symlink to state/calibration/current.json
├── profile                            # symlink to state/hwdb/effective_profile.json
│
├── commands/                          # everything that is command output
│   ├── ventd/
│   │   ├── version                    # `ventd --version`
│   │   ├── help                       # `ventd diag bundle --help`  (for reproducibility)
│   │   └── status                     # `ventd status` snapshot
│   ├── hwmon/
│   │   ├── sensors_-u                 # `sensors -u`
│   │   ├── sensors_-u_-A              # without adapter prefix (alt format)
│   │   └── lm_sensors_version         # `sensors -v`
│   ├── gpu/
│   │   ├── lspci_class_03             # `lspci -nn -d ::0300`  (display class)
│   │   ├── nvml_query                 # NVML enumeration NDJSON (if NVML present)
│   │   ├── amdgpu_pm_info             # if AMDGPU present
│   │   └── xe_hwmon_walk              # if Intel xe present
│   ├── journal/
│   │   ├── ventd-tail.ndjson          # journalctl -u ventd -o json
│   │   └── dmesg-filtered.txt         # `dmesg | grep -E '<our-modules>'`
│   ├── userspace/
│   │   ├── processes_with_hidraw_open # for runtime conflict detection
│   │   ├── lsof_dev_dri               # for GPU driver conflicts
│   │   ├── ppfeaturemask              # /sys/module/amdgpu/parameters/ppfeaturemask
│   │   └── tainted                    # /proc/sys/kernel/tainted
│   ├── system/
│   │   ├── uname_-a
│   │   ├── lsb_release                # or /etc/os-release
│   │   ├── dmidecode_-t_baseboard_-t_chassis_-t_processor  # filtered, no UUID
│   │   ├── modules_loaded             # lsmod | grep <fan-related>
│   │   └── kernel_cmdline             # /proc/cmdline (redacted)
│   └── corsair_aio/                   # if HID Corsair detected
│       ├── hidraw_devinfo
│       └── recent_packets.ndjson      # ring buffer of last N HID exchanges
│
├── sys/                               # filesystem mirror, raw paths
│   └── class/
│       ├── hwmon/hwmon0/...
│       └── drm/card0/device/hwmon/...
│
├── etc/                               # ventd's config files
│   └── ventd/...
│
├── state/                             # ventd's runtime state
│   ├── calibration/
│   │   └── current.json               # spec-03 amendment §15 schema
│   ├── hwdb/
│   │   ├── effective_profile.json     # post-resolve EffectiveControllerProfile
│   │   └── match_diagnostics.json     # which tier matched, fields populated
│   └── runtime/
│       ├── current_setpoints.ndjson   # last N controller decisions
│       └── active_profile.yaml        # user-configured profile
│
├── trace/                             # rolling-buffer NDJSON
│   ├── pwm_writes.ndjson              # per pwm-write event with read-back
│   ├── nvml_calls.ndjson              # if NVML in use
│   └── recovery_events.ndjson         # any auto-recovery triggers
│
└── REDACTION_REPORT.json              # what redactor changed (§11.3)
```

**Default size target: <500 KB**. Most bundles will land at 50–200 KB. Single-host capture, gzip compressed, tar archive.

---

## 11. Bundle README and manifest (machine + human dual)

### 11.1 README.md (human-readable index)

Top of the bundle expansion. Tells a support engineer what to look at first.

```markdown
# ventd diagnostic bundle

Captured: 2026-04-26T18:30:00Z
Hostname: [redacted to obf_host_1]
ventd version: 0.4.1
Kernel: 6.8.0
Schema version: ventd-diag-bundle-v1

## What ventd thought was happening

See: `./profile` (effective controller profile)
See: `./calibration` (current calibration result)

## What was actually on the wire (last N events)

See: `./trace/pwm_writes.ndjson` (rolling buffer)
See: `./journal` (filtered systemd journal tail)

## Hardware ground truth

See: `./hwmon` (lm_sensors snapshot)
See: `./gpu` (PCI display devices)

## To reproduce on this bundle

ventd replay --bundle <this-directory>

## Redaction

This bundle was captured with default redaction. See REDACTION_REPORT.json
for what was changed.

## If you also have a system-wide issue

Consider also attaching `sos report --batch --clean` output.
```

### 11.2 manifest.json (machine-readable index)

```json
{
  "schema_version": "ventd-diag-bundle-v1",
  "ventd_version": "0.4.1",
  "captured_at": "2026-04-26T18:30:00Z",
  "host_id_redacted": "obf_host_1",
  "files": {
    "state/calibration/current.json": {
      "schema": "ventd-calibration-v1",
      "size_bytes": 4123,
      "sha256": "..."
    },
    "trace/pwm_writes.ndjson": {
      "schema": "ventd-pwm-write-v1",
      "events": 247,
      "first_ts": "2026-04-26T18:25:00Z",
      "last_ts": "2026-04-26T18:30:00Z"
    },
    ...
  }
}
```

Per-file schema version means a future ventd version can ingest old bundles without rebuilding the bundle structure.

### 11.3 REDACTION_REPORT.json (audit trail)

```json
{
  "redactor_version": "1",
  "redactor_profile": "default-conservative",
  "redactions_by_class": {
    "hostname": 12,
    "username": 0,
    "ipv4": 3,
    "ipv6": 0,
    "mac_address": 8,
    "dmi_serial": 4,
    "usb_physical_path": 2,
    "user_path_home": 1
  },
  "redactions_skipped_classes": [],
  "non_redacted_files": [
    "manifest.json",
    "REDACTION_REPORT.json",
    "commands/ventd/version"
  ],
  "redaction_consistent": true
}
```

User can verify nothing sensitive was retained without trusting the redactor blindly.

---

## 12. Detection items — what gets captured (cross-ref to controllability map §10 and survey §8)

This list is the union of hwmon controllability-map §10 (~40 items), userspace-survey §8 (~10 items), and GPU vendor catalog §5.7 (~10 GPU items). Rolled up into PR 2c capture categories:

### 12.1 Platform identity (§commands/system/)
1. DMI baseboard manufacturer + product (no UUID, no serial)
2. DMI chassis type (laptop/desktop/server discriminator)
3. CPU brand string (`/proc/cpuinfo` model name)
4. Memory total (`free`)
5. Kernel version (`uname -a`)
6. Distro identifier (`/etc/os-release` ID + VERSION_ID, no PRETTY_NAME if it contains hostname)
7. Boot cmdline (`/proc/cmdline`, with hostname/uuid args redacted)
8. Tainted state (`/proc/sys/kernel/tainted`)

### 12.2 hwmon ground truth (§commands/hwmon/, §sys/class/hwmon/)
9. `sensors -u` output for every chip
10. Per-hwmon: name, prefix, path, every pwm/fan/temp attribute readable
11. Per-pwm: `pwm_enable`, `pwm_mode` (DC/PWM), pulses
12. driver-specific module params (`/sys/module/<name>/parameters/*`)
13. udev attributes for the hwmon device (subsystem, driver)

### 12.3 Userspace conflicts (§commands/userspace/)
14. Processes with any `/dev/hidraw*` open (`lsof | grep hidraw`)
15. Processes with `/dev/dri/card*` open
16. Running fan-related services (`systemctl list-units --state=running | grep -iE 'fan|liquid|cool'`)
17. Userspace tool fingerprint (per survey §8): if `liquidctl`, `coolercontrol-liqctld`, `lact`, `corectrl`, `fancontrol`, `nbfc-linux`, `thinkfan`, `fan2go` daemons are running, capture version + active config
18. amdgpu OverDrive bit state (ppfeaturemask AND 0x4000)

### 12.4 GPU detection (§commands/gpu/) — from spec-03b GPU catalog §5.7
19. PCI vendor/device for every drm card
20. NVIDIA driver version (NVML), library version, per-device UUID + name + arch + vBIOS version
21. AMDGPU module version (`modinfo amdgpu`), `gpu_metrics` snapshot per card
22. Intel xe vs i915 driver per card
23. Hybrid laptop detection (chassis_type ∈ laptop AND multiple GPUs)

### 12.5 Corsair AIO (§commands/corsair_aio/) — only if VID 0x1b1c HID device present
24. hidraw devinfo (vendor, product, physical-path-redacted)
25. last N HID exchanges (request + response, ring buffer)
26. firmware version reported by AIO
27. detected device class (Core / Core XT / Core ST per v0.4.x spec-02)

### 12.6 ventd's own state (§state/)
28. effective_profile.json (post-resolve, all 13 v1.0 fields)
29. match_diagnostics.json (which tier, which fields populated, why fallback)
30. current calibration record (BIOS version, polarity, stall_pwm, min_responsive_pwm, phantom flags)
31. user-active profile YAML
32. last N controller decisions (setpoint, error, output, mode)

### 12.7 ventd's own logs (§commands/journal/)
33. journalctl -u ventd.service --since "24 hours ago" -o json (NDJSON)
34. dmesg filtered to fan-related modules (NDJSON via `dmesg --json` if available, plain otherwise)

### 12.8 Cross-cuts to spec-04 / spec-05
35. Recovery event log (rolling buffer of last 30 days of recovery triggers, if any)
36. Calibration history (last N calibrations, schema-versioned)
37. Active stress engine fingerprint (set by spec-05-prep harness if running)

**Total: ~37 distinct capture items.** Comfortably implementable in PR 2c at $30-50 budget.

---

## 13. Format choices summary

| Concern | Choice | Rationale |
|---|---|---|
| Compression | gzip (`.tar.gz`) | universal, no metadata leak, sufficient for sub-MB bundles |
| Wire format | NDJSON for events, plain for command output | NDJSON streamable + grep-able; command output preserves verbatim form |
| Schema versioning | strict-major (`v1`, `v2`) per spec-05-prep §4.4 | strict-decode catches drift early |
| Top-level layout | mirror sosreport's filesystem-shaped + symlinks pattern | proven, learnable, tooling-friendly |
| Manifest | manifest.json at root | machine reading and inventory |
| README | README.md at root | human triage entry point |
| Redaction audit | REDACTION_REPORT.json | bundle is auditable without trusting the redactor blindly |
| Default size | <500 KB | order of magnitude below sosreport, fits any chat/bug-tracker upload |

---

## 14. Substrate sharing with spec-05-prep

`internal/ndjson/{writer,reader,schema}.go` lives in PR 2c. spec-05-prep PR 1 imports it for the trace harness. Same writer for diag bundle's `trace/*.ndjson` and harness's `traces/*.ndjson.gz`.

Schema differences are per-event-type: `ventd-pwm-write-v1` differs from `ventd-thermal-trace-v1`. Both share the strict-major schema header convention (`{"schema_version":"...","ts":"..."}` first field). One reader, many event schemas.

PR 2c MUST land before spec-05-prep PR 1, or spec-05-prep PR 1 imports a phantom package. Sequencing matters; document in chat 2 handoff.

---

## 15. Out of scope for PR 2c

- **Auto-bundle on failure** (per §9.2): land in v0.5.x follow-up.
- **Plugin model** (per §2.4): defer to v1.x.
- **Continuous trace collection at all times** (per spec-05-prep): trace harness is a separate binary (`ventd-trace`); diag bundle just dumps the rolling buffer, doesn't itself collect long-running traces.
- **GUI export button**: ventd has no GUI. CLI only.
- **Bundle upload to a service**: ventd doesn't phone home. User attaches manually wherever.
- **Diff-mode capture** (compare two bundles): a useful future tool, but not part of v0.4.x.

---

## 16. Open questions for chat 3 / Phoenix

- **Q1:** Is `nvidia-bug-report.sh`-style reliance on `dmesg` filtering acceptable, or does ventd want its own kernel-side ring buffer? **Recommendation: filter dmesg in v1; consider a kernel-side trace_event in post-v1.0 if needed.**
- **Q2:** Compress with gzip or zstd? gzip is universal, zstd is faster and smaller. **Recommendation: gzip for v1, zstd as opt-in via `--compress=zstd`.**
- **Q3:** Bundle output location default — `/var/lib/ventd/diag-bundles/<timestamp>/` or `$XDG_STATE_HOME/ventd/diag-bundles/`? **Recommendation: per spec-05-prep §13 by-uid pattern: root → /var/lib/ventd/, user → XDG_STATE_HOME.**
- **Q4:** Default ring-buffer size for trace events — last 60s, last 1000 events, last 1MB? **Recommendation: 1000 events default, configurable.**

---

**End of diagnostic bundle prior art research.**
