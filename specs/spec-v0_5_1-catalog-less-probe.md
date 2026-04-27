# spec-v0_5_1 — Catalog-less probe + Tier-2 detection + 3-state wizard fork

**Status:** DESIGN. Drafted 2026-04-27.
**Ships as:** v0.5.1 (first smart-mode behaviour patch, after v0.5.0.1
spec-16 foundation).
**Depends on:** v0.5.0.1 spec-16 persistent state must be merged first.
**Consumed by:** v0.5.2 polarity disambiguation, v0.5.3 Envelope C/D
probe — both build on the probe layer this patch establishes.
**References:** `spec-smart-mode.md` §2, §3, §4 (design of record).

---

## 1. Why this is the first smart-mode behaviour patch

v0.5.0.1 ships infrastructure (persistence) but no behavioural change
to ventd. v0.5.1 is the first patch users would notice — it changes
what happens when ventd is installed:

- Probe runs catalog-independently.
- Three-state wizard outcome (control / monitor-only / graceful exit)
  replaces the previous "calibrate or fail" flow.
- Tier-2 hardware classes (VMs, containers) refuse install with clear
  diagnostic instead of attempting calibration on inappropriate
  hardware.

Everything downstream in the smart-mode sequence builds on this
probe layer. Polarity disambiguation (v0.5.2) and Envelope C/D probe
(v0.5.3) operate on the channel set produced here. Layer A/B/C
learning (v0.5.4-v0.5.8) operates on the system this probe declared
viable.

---

## 2. Scope

### 2.1 In scope

- Catalog-less probe layer that enumerates thermal sources and
  controllable channels from sysfs + DMI + NVML + IPMI + EC ACPI
  without depending on the hardware catalog.
- Catalog overlay path: when catalog matches, hints are applied on
  top of probe output without changing downstream code paths.
- Tier-2 detection (virtualised guest, containerised) at install time
  with graceful exit + diagnostic.
- Three-state wizard outcome:
  - Control mode (≥1 sensor + ≥1 controllable channel).
  - Monitor-only mode (≥1 sensor, zero channels) — wizard fork:
    keep-as-dashboard / uninstall / contribute-tickbox.
  - Graceful exit (zero sensors).
- Persistent storage of probe outcome via spec-16 KV store.
- Removal of `bios_known_bad.go` enumeration (replaced behaviourally
  in this patch's probe layer for the runtime-environment subset; full
  BIOS-override behavioural detection lands in later patches).
- Removal of catalog-as-prerequisite assumption — every code path
  downstream of probe sees the same probe output shape regardless of
  catalog match.

### 2.2 Out of scope

- **Polarity disambiguation.** Probe records `polarity: unknown` on
  channels; v0.5.2 disambiguates.
- **Any PWM writes.** Probe is read-only. No calibration in this
  patch. v0.5.3 introduces Envelope C/D probe writes.
- **Workload signature learning.** v0.5.6 territory.
- **Confidence indicators in UI.** v0.5.9 territory. v0.5.1's UI
  shows runtime-state mode (control/monitor) without learning
  confidence yet.
- **Steam Deck firmware-owns-fans behavioural detection.** This patch
  detects virtualisation and containers; full firmware-owns-fans
  behavioural detection lands when control writes are introduced
  (v0.5.3+).
- **NBFC integration.** spec-09 territory; userspace EC drivers are
  detected as `requires_userspace_ec` in probe output but no install
  flow change in this patch.
- **Full hardware catalog rework.** Catalog continues to ship as-is
  (schema v1.2). Demoted from prerequisite to overlay; not removed.

---

## 3. The probe contract

### 3.1 Probe layer interface

```go
package probe

type ProbeResult struct {
    SchemaVersion       uint16
    RuntimeEnvironment  RuntimeEnvironment
    ThermalSources      []ThermalSource
    ControllableChannels []ControllableChannel
    CatalogMatch        *CatalogMatch  // nil when no match
    Diagnostics         []Diagnostic
}

type RuntimeEnvironment struct {
    Virtualised        bool
    VirtType           string  // "kvm" | "vmware" | "hyperv" | "qemu" | ""
    Containerised      bool
    ContainerRuntime   string  // "docker" | "lxc" | "kubepods" | ""
    DetectedVia        []string  // sources that detected the environment
}

type ThermalSource struct {
    SourceID    string  // "hwmon0" | "thermal_zone0" | "nvml:0" | etc.
    Driver      string
    Sensors     []SensorChannel
}

type SensorChannel struct {
    Name        string  // "temp1_input" | "package" | etc.
    Path        string
    Label       string  // from `*_label` if available
    InitialRead float64  // °C; zero if read failed
    ReadOK      bool
}

type ControllableChannel struct {
    SourceID         string  // "hwmon3"
    PWMPath          string  // sysfs PWM file
    TachPath         string  // sysfs RPM file (may be empty for tach-less fans)
    Driver           string
    Polarity         string  // always "unknown" in v0.5.1 (set by v0.5.2)
    InitialPWM       int     // current PWM value (0-255)
    InitialRPM       int     // current RPM (0 if no tach)
    CapabilityHint   string  // from catalog overlay; empty if no match
    Notes            []string
}

type CatalogMatch struct {
    Matched          bool
    Fingerprint      string
    OverlayApplied   []string  // names of profiles overlaid
}

type Diagnostic struct {
    Severity  string  // "info" | "warning" | "error"
    Code      string  // structured code: "PROBE-VIRT-DETECTED", etc.
    Message   string
    Context   map[string]string
}

type Prober interface {
    Probe(ctx context.Context) (*ProbeResult, error)
}
```

### 3.2 Probe outcome states

ventd derives runtime state from `ProbeResult`:

```go
func ClassifyOutcome(r *ProbeResult) Outcome {
    if r.RuntimeEnvironment.Virtualised || r.RuntimeEnvironment.Containerised {
        return OutcomeRefuse
    }
    if len(r.ThermalSources) == 0 {
        return OutcomeRefuse
    }
    if len(r.ControllableChannels) == 0 {
        return OutcomeMonitorOnly
    }
    return OutcomeControl
}

type Outcome int
const (
    OutcomeControl Outcome = iota
    OutcomeMonitorOnly
    OutcomeRefuse
)
```

---

## 4. Discovery sources (read-only)

In probe order:

### 4.1 Runtime environment detection (FIRST)

- `/.dockerenv` exists → containerised.
- `/proc/1/cgroup` mentions `docker`, `lxc`, `kubepods`, `garden` →
  containerised.
- `systemd-detect-virt --container` returns non-`none` → containerised
  (run via os/exec, tolerate command-not-found).
- `systemd-detect-virt --vm` returns non-`none` → virtualised.
- DMI fields (`/sys/class/dmi/id/sys_vendor`, `product_name`) match
  known virt vendors (`KVM`, `VMware`, `Microsoft Corporation`,
  `QEMU`, `innotek GmbH`, `Xen`, `Parallels`).

If virtualised or containerised → probe stops immediately, populates
`RuntimeEnvironment`, returns. No further discovery runs.

### 4.2 Thermal source enumeration

For each `/sys/class/hwmon/hwmon*/`:
- Read `name` file → driver name.
- Enumerate `temp*_input` files → sensors.
- Read each sensor's initial value (°C, divided by 1000 from millidegrees).
- Read `temp*_label` if present.
- Sensors with read failures recorded with `ReadOK: false` but not
  excluded — diag bundle uses them.

For each `/sys/class/thermal/thermal_zone*/`:
- Read `type` → zone type (e.g. `x86_pkg_temp`).
- Read `temp` → initial value.
- Skip zones already covered by hwmon (deduplication by sysfs link
  inspection).

NVML enumeration (purego, read-only):
- For each device: `nvmlDeviceGetTemperature` for sensor.
- For each device: `nvmlDeviceGetFanSpeed` for fan readback (control
  capability resolved later).

IPMI sensor enumeration:
- `ipmi_si` module presence check.
- If present: read `/dev/ipmi0` SDR sensor list, classify by sensor
  type (temperature, fan).
- Read-only — no IPMI commands beyond sensor read.

EC ACPI methods:
- `/proc/acpi/ibm/thermal` for ThinkPad EC.
- `/sys/devices/virtual/dmi/id/board_name` matched against known
  exposing-EC boards (Framework, Legion).
- This patch only enumerates exposed temps from EC; does not claim
  control capability.

### 4.3 Controllable channel enumeration

For each `/sys/class/hwmon/hwmon*/`:
- For each `pwm[N]` file present:
  - PWM is writable: required `write_ok` heuristic (open O_WRONLY,
    immediately close — no actual write).
  - Companion `pwm[N]_enable` exists: required.
  - Companion `fan[N]_input` may or may not exist (tach-less fans
    are valid controllable channels; just RPM=0).
- For each candidate, populate `ControllableChannel` struct with
  `Polarity: "unknown"`, `CapabilityHint: ""` initially.

NVML controllable fan enumeration:
- For each device: `nvmlDeviceGetNumFans`. Each fan candidate becomes
  a controllable channel via the NVML backend (driver R515+ required
  for actual writes; this patch only enumerates).

IPMI controllable fans:
- For each detected fan sensor: candidate channel via IPMI backend.
- Read-only enumeration; control gating per existing IPMI backend
  rules.

EC controllable fans:
- For ThinkPad: `/proc/acpi/ibm/fan` write capability via existing
  ThinkPad backend.
- For Framework/Legion: detection only; control gating via spec-09
  NBFC integration (post-v0.5.1).

### 4.4 Catalog overlay (last)

After probe enumerates discovery sources:
- Build hardware fingerprint from DMI (per existing schema v1.2).
- Look up fingerprint in catalog.
- On match: overlay capability hints, polarity priors, quirk flags
  onto matched channels. Set `ProbeResult.CatalogMatch`.
- On no match: leave channels with default-paranoid defaults
  (`Polarity: "unknown"`, no quirk flags). `ProbeResult.CatalogMatch
  = nil`.

The overlay does not change channel set, sensor set, or runtime
state determination. It only annotates channels with hints that
later patches (v0.5.2 polarity probe) may use to skip work.

---

## 5. Three-state wizard fork

### 5.1 Wizard flow

```
Probe runs → ProbeResult → ClassifyOutcome →
  ├── OutcomeRefuse + virt/container reason →
  │     Show diagnostic, exit, do not install.
  │
  ├── OutcomeRefuse + no thermal sources →
  │     Show "ventd cannot find any thermal sensors on this hardware"
  │     diagnostic + contribute-profile link + uninstall.
  │
  ├── OutcomeMonitorOnly →
  │     Show wizard fork:
  │     - Keep ventd as monitoring dashboard
  │     - Uninstall
  │     [optional tickbox: Contribute anonymised profile]
  │     User selects → install in monitor-only mode OR uninstall.
  │
  └── OutcomeControl →
        Continue to existing wizard flow (calibration, presets, etc.).
        v0.5.1 retains the existing wizard from spec-12; subsequent
        patches modify it.
```

### 5.2 Persistent state

Probe outcome persists in spec-16 KV store under `wizard` namespace:

```yaml
wizard:
  initial_outcome: "control_mode" | "monitor_only" | "refused"
  outcome_reason: "..."  # for refused: "virtualised", "containerised", "no_sensors"
  outcome_timestamp: "2026-04-27T..."
  user_choice: "install" | "uninstall" | null  # for monitor-only fork
  contribute_tickbox: true | false
```

This state is consulted at every daemon start to:

- Detect "user previously chose monitor-only, do not attempt
  calibration."
- Detect "outcome was control mode, current probe shows monitor-only
  — hardware change?" → surface in doctor.
- Detect "outcome was refused, daemon is being asked to start
  anyway" → refuse start with diagnostic.

### 5.3 Reset path

Settings page exposes "Reset ventd to initial setup":
- Wipe `wizard` namespace from KV store.
- Wipe `probe` namespace (per §6.2).
- On next daemon start, full probe + wizard re-runs.

This is a deliberate user-initiated action. Hot-plug detection
(post-v0.5.1) handles automatic re-probing for incremental hardware
changes.

---

## 6. Catalog overlay handling

### 6.1 Demotion semantics

"Catalog as prerequisite" is removed. Concretely:

- **Before v0.5.1:** if no catalog match, daemon refused to control
  (existing `bios_known_bad.go` and related catalog-gated logic).
- **After v0.5.1:** catalog match is informational only. No-match
  systems still reach control mode if probe finds controllable
  channels.

Catalog match accelerates and refines but never gates.

### 6.2 Probe outcome storage

Probe outcome (full ProbeResult) persists in spec-16 KV store under
`probe` namespace:

```yaml
probe:
  schema_version: 1
  last_run: "2026-04-27T..."
  result:
    runtime_environment: { ... }
    thermal_sources: [ ... ]
    controllable_channels: [ ... ]
    catalog_match:
      matched: true
      fingerprint: "..."
      overlay_applied: ["nct6798_chip_profile"]
    diagnostics: [ ... ]
```

Storage is convenient for diag bundle generation and for later
patches' "compare current probe to last probe" drift detection.

### 6.3 Removal of bios_known_bad.go

The existing `bios_known_bad.go` enumeration is removed. Its only
caller (the install-time refuse path) is replaced by §3.2's
classifier. Tests that referenced bios_known_bad are removed or
rewritten against §3.2.

Behavioural BIOS-override detection (the deeper job that
bios_known_bad was a primitive enumeration of) lands in v0.5.3+ when
PWM writes begin. v0.5.1 only handles runtime-environment refuse cases
(virt/container) and absence-of-resources cases.

---

## 7. UI changes (minimal)

This patch is mostly daemon-internal. Web UI changes are limited:

### 7.1 Wizard

- Existing spec-12 wizard PR 1 (#661) base is reworked to handle the
  three-state outcome.
- New screen for "monitor-only fork" with three options: keep /
  uninstall / contribute tickbox.
- New screen for "graceful refuse" cases with diagnostic content +
  contribute link.
- Existing spec-12 mockups do not show these screens — that's the
  spec-12 amendment work (separate from this patch).

### 7.2 Dashboard

Dashboard runtime state pill (top of page):
- Control mode → green "Smart-mode active" pill (cosmetic — full
  confidence indicators land in v0.5.9).
- Monitor-only mode → blue "Monitoring only" pill with tooltip
  explaining no controllable fans detected.
- Refused → daemon does not start; not visible.

### 7.3 Settings

New "Reset to initial setup" button under existing Settings page.
Confirmation modal warns:

> Resetting will wipe ventd's learned state and re-detect your
> hardware. Calibration data, learned curves, and workload signatures
> will be discarded. This cannot be undone.
>
> Continue?

---

## 8. Invariant bindings

| Rule ID | Statement |
|---|---|
| `RULE-PROBE-01` | Probe MUST be read-only. No PWM writes, no IPMI commands beyond sensor read, no EC commands beyond temp read. Subtest verifies no write syscalls during probe via syscall trace fixture. |
| `RULE-PROBE-02` | Probe MUST detect virtualisation via at least three independent sources (DMI, systemd-detect-virt, /sys/hypervisor) before declaring `virtualised: true`. Single-source detection is recorded in diagnostics but does not trigger refuse. |
| `RULE-PROBE-03` | Probe MUST detect containerisation via at least two of (`/.dockerenv`, `/proc/1/cgroup`, systemd-detect-virt --container). |
| `RULE-PROBE-04` | Probe outcome classification MUST follow §3.2 algorithm exactly. Refuse-virt/container, refuse-no-sensors, monitor-only, control are the only four outcomes. |
| `RULE-PROBE-05` | No code path downstream of probe MAY branch on `CatalogMatch == nil` vs non-nil for behaviour purposes. Catalog overlay only annotates channels; downstream code reads the annotated channels uniformly. |
| `RULE-PROBE-06` | All `ControllableChannel.Polarity` MUST be `"unknown"` in v0.5.1 output. v0.5.2 introduces disambiguation. |
| `RULE-PROBE-07` | Probe MUST persist outcome to spec-16 KV store on completion. Persistent state used for "compare current probe to last probe" by later patches. |
| `RULE-PROBE-08` | Three-state wizard outcome MUST be reflected in `wizard.initial_outcome` KV state. Daemon start MUST consult this state and refuse to enter control mode if previous outcome was monitor-only or refused. |
| `RULE-PROBE-09` | "Reset to initial setup" MUST wipe both `wizard` and `probe` KV namespaces. Subsequent start MUST re-run full probe. |
| `RULE-PROBE-10` | bios_known_bad.go and its callers MUST be removed in this PR. Subtest enforces file does not exist in tree post-merge. |

Each rule maps 1:1 to a Go subtest. `tools/rulelint` enforces the
binding.

---

## 9. Deletions

This PR removes:

- `internal/hwdb/bios_known_bad.go` (and any `bios_known_bad*` test
  files).
- Catalog-gated install-refuse logic in the existing wizard path.
- The "catalog match required" branches in install scripts (per
  spec-06 install contract — verify these don't exist in deploy
  scripts; if they do, remove).
- Test fixtures asserting "no catalog match → install refused."
  Replaced by tests asserting §3.2 classifier outcomes.

The hardware catalog itself is NOT removed. It continues to ship as
overlay data per §6.

---

## 10. Failure modes enumerated

1. **Probe fails to read a sensor.** Sensor recorded with
   `ReadOK: false`. Not excluded from `ThermalSources`. Other sensors
   continue. Diagnostic emitted.

2. **`systemd-detect-virt` not installed.** ENOENT tolerated.
   Falls back to DMI + cgroup detection. Recorded in diagnostics.

3. **DMI fields unreadable (chrooted environment, weird containers).**
   Treated as detection signal — likely containerised. Refuse with
   container-detected diagnostic.

4. **NVML init fails (no NVIDIA driver, libnvidia-ml absent).**
   Skipped silently. NVML sources/channels empty. Other sources still
   probed.

5. **IPMI present but `/dev/ipmi0` not accessible (permissions).**
   IPMI module presence recorded; sensor enumeration empty;
   diagnostic surfaces "IPMI driver loaded but device inaccessible —
   check ventd's group membership."

6. **Probe runs on Steam Deck.** Detects jupiter EC. Channels enumerate
   normally. v0.5.1 does not yet detect `firmware_owns_fans`
   behaviourally — Steam Deck enters control mode by probe outcome.
   Default control attempts at v0.5.3+ will detect firmware-revert
   behaviour and fall back appropriately. v0.5.1 user experience on
   Steam Deck: enters control mode but does not yet have
   `firmware_owns_fans` flag set — this is acceptable because no PWM
   writes happen until v0.5.3.

7. **User runs `ventd` in a Docker container with `--privileged
   --pid=host`.** Container detection still triggers refuse (correct
   — ventd should not run in a container, even privileged).
   `--allow-container` opt-in flag (defined in spec-smart-mode §4.2)
   is **not** implemented in v0.5.1; deferred.

8. **Probe persistence write fails (disk full, permissions).** Probe
   completes in-memory and proceeds with installation; persistence
   failure recorded in diagnostics. Daemon does not start until
   storage issue resolved (RULE-STATE-01 covers).

9. **Hardware catalog file corrupt.** Catalog overlay step skipped;
   `CatalogMatch = nil`; probe completes without catalog hints.
   Diagnostic surfaces catalog read failure for diag bundle.

10. **Two daemons race-launched, both reach probe step.** spec-16
    PID file detection prevents — second daemon exits before reaching
    probe. RULE-STATE-06 covers.

---

## 11. Validation criteria

### 11.1 Synthetic CI tests

Required, all must pass on every PR:

- Virtualisation detection: three synthetic fixtures
  (DMI=KVM/VMware/Hyper-V), each must produce `virtualised: true`.
- Container detection: two synthetic fixtures
  (`/.dockerenv` present, `/proc/1/cgroup` mentioning docker), each
  must produce `containerised: true`.
- Outcome classification: synthetic ProbeResult fixtures for each of
  the four outcome cases, classifier must produce expected outcome.
- Catalog overlay: matched fixture must populate
  `CatalogMatch.OverlayApplied`; no-match fixture must produce
  `CatalogMatch = nil`; downstream code paths must produce identical
  channel-handling output for both.
- bios_known_bad removed: file system test asserts file not present.
- Probe persistence: probe runs, KV state contains expected
  `probe.last_run`, `wizard.initial_outcome`.
- Reset path: KV state populated, "Reset to initial setup" called,
  KV namespaces `wizard` and `probe` are empty after.

### 11.2 Behavioural HIL

**Fleet members required:**

1. **Proxmox VM HIL:** install ventd in a guest VM. Probe detects
   `virtualised: true`. Wizard refuses install with VM diagnostic.
2. **Proxmox host (5800X + RTX 3060):** install ventd on the host.
   Probe enumerates hwmon + NVML sources + channels. Outcome:
   control mode. Wizard reaches existing post-probe flow.
3. **MiniPC (Celeron):** install ventd. Probe enumerates whatever
   hwmon exists. Verify outcome matches expected (control mode if
   PWM channels exist, monitor-only if sensors-only). If
   monitor-only: verify wizard fork presents three options correctly.
4. **Container test:** Phoenix runs `docker run --rm -it
   ventd-test:latest`. Probe detects containerised, refuses install,
   diagnostic mentions host-install recommendation.

### 11.3 Time-bound metric

**Probe completion time:** full probe must complete within **3
seconds** on Proxmox host, **5 seconds** on MiniPC. Including all
discovery sources + catalog overlay + persistence write.

This bound is generous (probe is mostly sysfs reads + a few exec
calls); exceeding it suggests pathological I/O or implementation
inefficiency worth surfacing.

---

## 12. Estimated cost

- Spec drafting (this document): $0 (chat).
- CC implementation (Sonnet, single PR): **$15-25 estimate**.
  - New probe package: moderate scope but well-spec'd.
  - Wizard rework: small touch on existing spec-12 PR 1 base.
  - Removal of bios_known_bad: mechanical.
  - Persistence wiring: trivial against spec-16 primitive.
  - RULE-PROBE-* bindings: 10 rules, 1:1 subtests.
  - Synthetic test fixtures: 7+ fixtures across detection, outcome,
    overlay, persistence.

Pad +50% if container detection has portability issues across
ventd's CI matrix.

---

## 13. PR sequencing

Single PR. Splitting into "probe primitive" + "wizard fork" + "remove
bios_known_bad" introduces dependency on PRs that ship empty
(probe primitive without classifier integration is not shippable; the
classifier needs the wizard fork to exercise; the wizard fork needs
bios_known_bad gone to compile correctly without parallel paths).

---

## 14. References

- `spec-smart-mode.md` §2, §3, §4 — design of record.
- `specs/spec-16-persistent-state.md` — KV store consumed here.
- `specs/spec-03-amendment-schema-v1_2.md` — catalog overlay shape
  (used as-is, no changes).
- `specs/spec-12-ui-redesign.md` — base wizard reworked here.
- `specs/spec-06-install-contract.md` — sysusers / AppArmor baseline.

---

**End of spec.**
