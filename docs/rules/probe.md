# Daemon-startup probe rules

These invariants govern the daemon-startup probe in
`internal/probe/`: the read-only enumeration of channels, thermal
sources, and virtualisation / containerisation signals, the
catalog-overlay step, the three-state outcome (refuse /
monitor-only / control), and the persistence of the wizard's
initial outcome to the KV store. The probe is the first thing
that runs on daemon start; its outcome gates whether the wizard
takes the install / calibration / monitor-only path.

Each rule below binds 1:1 to a subtest. If a rule text is edited,
update the binding subtest in the same PR; if a new rule lands,
it must ship with a matching subtest or `tools/rulelint` blocks
the merge.

## RULE-PROBE-01: Probe MUST be entirely read-only — no PWM writes, no IPMI commands, no EC commands.

`Prober.Probe()` reads hardware state from injected `fs.FS`
values (SysFS, ProcFS, RootFS) and from external commands via
`ExecFn`. The only write-adjacent operation is the
`WriteChecker`, which opens a sysfs PWM path `O_WRONLY` and
immediately closes it — no data bytes are written. In tests, a
stub `WriteChecker` is injected so no real file descriptors are
opened. All other I/O is `fs.ReadFile` / `fs.ReadDir` on the
injected FS or trimmed stdout from `ExecFn`. A Probe that writes
a PWM value or issues an IPMI command could alter fan state
before the operator has consented to ventd taking control.

Bound: internal/probe/probe_test.go:RULE-PROBE-01_read_only

## RULE-PROBE-02: Virtualisation detection requires ≥3 independent sources before setting Virtualised=true.

`detectEnvironment` scores **four** independent virt signals and
sets `RuntimeEnvironment.Virtualised` only when the score
reaches 3: (1) DMI `sys_vendor` / `product_name` substring match
against `virtVendors`; (2) `systemd-detect-virt --vm` exits 0
with output other than `"none"` or `""`; (3) existence of
`/sys/hypervisor`; (4) the cpuid `hypervisor` flag in
`/proc/cpuinfo` flag list. The 4th source was added 2026-05-03
to close the MicroVM/Firecracker recall gap — those hosts can
fire only the cpuid hypervisor bit (no DMI strings, no
`/sys/hypervisor`, no systemd-detect-virt on minimal images) and
would otherwise score ≤2 and pass as bare-metal.

The threshold stays at 3 (now of 4) — widening recall without
lowering the precision bar. With ≤2 sources the field remains
false. False-positive virt detection on bare-metal hardware
would cause ventd to refuse installation on real systems; the
≥3-of-4 threshold trades recall (missing a novel hypervisor)
for precision (never refusing a valid bare-metal host).

Bound: internal/probe/probe_test.go:RULE-PROBE-02_virt_requires_3_sources

## RULE-PROBE-03: Containerisation detection requires ≥2 independent sources before setting Containerised=true.

`detectEnvironment` scores four independent container signals
and sets `RuntimeEnvironment.Containerised` only when the score
reaches 2: (1) existence of `/.dockerenv`; (2) `/proc/1/cgroup`
content containing a container runtime keyword (`docker`,
`lxc`, `kubepods`, `garden`); (3) `systemd-detect-virt
--container` exits 0 with output other than `"none"` or `""`;
(4) `/proc/mounts` contains a line with mount-point `/` and
filesystem type `overlay` (Docker on cgroup v2 hosts — Ubuntu
22.04+, Debian 12+ — where `/proc/1/cgroup` shows only `"0::/"`
with no container keyword). With ≤1 source the field remains
false. A single-source false positive (e.g., a stale
`.dockerenv` on a reinstalled system) would incorrectly refuse
installation. Two-source confirmation makes accidental refusal
essentially impossible on real hardware.

Bound: internal/probe/probe_test.go:RULE-PROBE-03_container_requires_2_sources

## RULE-PROBE-04: ClassifyOutcome follows the §3.2 algorithm exactly — virt/container → refuse; no sensors → refuse; sensors only → monitor_only; sensors + channels → control.

`ClassifyOutcome(r *ProbeResult) Outcome` applies four rules in
priority order:
1. `Virtualised || Containerised` → `OutcomeRefuse` ("refused").
2. `len(ThermalSources) == 0` → `OutcomeRefuse`.
3. `len(ControllableChannels) == 0` → `OutcomeMonitorOnly`
   ("monitor_only").
4. Otherwise → `OutcomeControl` ("control_mode").

The function is pure and does not read `CatalogMatch`. The
three-state outcome drives the setup wizard fork: refuse aborts
the install flow, monitor_only enters a read-only dashboard,
and control enters the full calibration pipeline.

Bound: internal/probe/probe_test.go:RULE-PROBE-04_classify_outcome

## RULE-PROBE-05: No downstream code branches on CatalogMatch==nil vs non-nil — channels are enumerated the same way regardless.

`ClassifyOutcome` MUST return the same `Outcome` for two
`ProbeResult` values that have identical `ThermalSources` and
`ControllableChannels` but differ only in whether `CatalogMatch`
is nil. The catalog overlay adds `CapabilityHint` annotations
to existing channels and may set `OverlayApplied` on
`CatalogMatch`, but it MUST NOT create or remove
`ControllableChannel` entries. Downstream code that reads
`ControllableChannels` must work identically whether the catalog
matched or not — a channel's presence in the slice is determined
solely by hwmon sysfs enumeration, not by catalog knowledge.

Bound: internal/probe/probe_test.go:RULE-PROBE-05_channels_uniform_regardless_of_catalog_match

## RULE-PROBE-06: ControllableChannel.Polarity MUST be drawn from the closed set {"unknown", "normal", "inverted", "phantom"}.

`ControllableChannel.Polarity` MUST be a value from the closed
set `{"unknown", "normal", "inverted", "phantom"}`. No code path
may produce a value outside this set. The probe layer
(spec-v0_5_1) sets every channel to `"unknown"`. The polarity
probe (spec-v0_5_2) resolves each channel to one of the other
three values. A value outside this set — including the empty
string — is invalid: empty string is indistinguishable from a
missing field in JSON serialisation and would cause downstream
migration logic to misclassify the channel's probe state.

Bound: internal/probe/probe_test.go:RULE-PROBE-06_polarity_always_unknown

## RULE-PROBE-07: PersistOutcome writes schema_version, last_run, result (probe namespace) and initial_outcome, outcome_reason, outcome_timestamp (wizard namespace) atomically.

`PersistOutcome(db *state.KVDB, r *ProbeResult)` MUST use
`db.WithTransaction` to set all six keys in a single atomic
commit: `probe.schema_version` (uint16 SchemaVersion),
`probe.last_run` (RFC3339 timestamp), `probe.result`
(JSON-encoded ProbeResult), `wizard.initial_outcome` (outcome
string), `wizard.outcome_reason`, and `wizard.outcome_timestamp`.
A partial write (transaction failure mid-way) leaves the store
unchanged. This ensures the wizard fork decision and the full
probe result are always consistent — a daemon that reads
`wizard.initial_outcome` can trust that `probe.result` reflects
the same run.

Bound: internal/probe/probe_test.go:RULE-PROBE-07_persist_outcome_writes_kv_keys

## RULE-PROBE-08: Daemon start consults wizard.initial_outcome KV key; LoadWizardOutcome returns the correct Outcome enum value.

`LoadWizardOutcome(db *state.KVDB) (Outcome, bool, error)` reads
`wizard.initial_outcome` and maps its string value to the
`Outcome` enum: `"control_mode"` → `OutcomeControl`;
`"monitor_only"` → `OutcomeMonitorOnly`; `"refused"` →
`OutcomeRefuse`. When the key is absent, it returns
`(OutcomeControl, false, nil)` — a missing key means "never
probed", not "refused". The daemon startup path calls
`LoadWizardOutcome` after `state.Open` and before starting the
control loop, gating entry to full control mode on
`OutcomeControl` and refusing start on `OutcomeRefuse`.

Bound: internal/probe/probe_test.go:RULE-PROBE-08_load_wizard_outcome

## RULE-PROBE-09: "Reset to initial setup" wipes both wizard and probe KV namespaces atomically; LoadWizardOutcome returns ok=false afterward.

`WipeNamespaces(db *state.KVDB)` enumerates all keys in the
`wizard` and `probe` namespaces via `db.List`, then deletes them
all inside a single `db.WithTransaction` call. After a successful
wipe, `db.List("wizard")` and `db.List("probe")` MUST return
empty maps, and `LoadWizardOutcome` MUST return `ok=false`. The
web handler for "Reset to initial setup" calls `WipeNamespaces`
after removing the config file, ensuring the next daemon start
treats the system as freshly installed and runs the full probe
again before entering the wizard. A partial wipe (wizard cleared,
probe left) could cause the wizard to start from scratch while
the old probe result remains, producing contradictory state.

Bound: internal/probe/probe_test.go:RULE-PROBE-09_wipe_namespaces_empties_both

## RULE-PROBE-10: internal/hwdb/bios_known_bad.go MUST NOT exist.

No per-board BIOS-version denylist is permitted in the hwdb
package. A hardcoded `bios_known_bad.go` or equivalent file
would require constant maintenance, create a false sense of
security (list is always incomplete), and couple the probe's
refuse decision to knowledge that the catalog overlay and
precondition checks already handle through
`overrides.unsupported` and `experimental:` schema fields. The
probe uses read-only hwmon enumeration for channel discovery
and catalog overlay for capability hints; it does not need a
BIOS version allowlist or denylist. The test asserts the file
does not exist in the module tree.

Bound: internal/probe/probe_test.go:RULE-PROBE-10_no_bios_known_bad_file

## RULE-PROBE-11: Daemon MUST NOT exit fatally on a persisted OutcomeRefuse from probe.LoadWizardOutcome.

The ventd daemon MUST NOT exit fatally on a persisted
`OutcomeRefuse` from `probe.LoadWizardOutcome`. Refuse is the
contract under which the first-run wizard explains why control
is unavailable; exiting bypasses that surface and leaves the
operator with no diagnostic UI.

On startup-time refuse, the daemon MUST:
- Log the refuse outcome and reason at WARN level.
- Continue startup so the web server binds and serves the
  setup / dashboard surfaces.

This rule complements RULE-PROBE-08 (which gates *wizard
behaviour* on the outcome). RULE-PROBE-08 governs the wizard;
RULE-PROBE-11 governs the daemon.

Bound: internal/probe/persist_test.go:TestRULE_PROBE_11_RefuseDoesNotBlockStartup
