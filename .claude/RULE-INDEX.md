# Rule Index

Canonical map of `.claude/rules/*.md`.
Read this file first; open a specific rule file only when the full text is needed.
Regenerate with: `go run ./tools/rule-index`

---

## RULE-CALIB

| File | Bound subtest | Summary |
|------|---------------|---------|
| calibration-pr2b-01.md | TestPR2B_Rules/polarity_normal_detected | Polarity probe classifies normal polarity when RPM delta ≥ 200 RPM at 80% vs 20% PWM. |
| calibration-pr2b-02.md | TestPR2B_Rules/polarity_inverted_detected | Polarity probe classifies inverted polarity when rpmAtLow − rpmAtHigh ≥ 200 RPM. |
| calibration-pr2b-03.md | TestPR2B_Rules/phantom_marked_from_ambiguous_polarity | Polarity probe returns PolarityAmbiguous when |rpmAtHigh − rpmAtLow| < 200; channel is marked phantom. |
| calibration-pr2b-04.md | TestPR2B_Rules/stall_pwm_detected_duty_0_255 | Stall PWM is detected for duty_0_255 channels via a descending sweep with step size 16. |
| calibration-pr2b-05.md | TestPR2B_Rules/min_responsive_pwm_detected | MinResponsivePWM is set to the sweep step immediately above the detected stall point. |
| calibration-pr2b-06.md | TestPR2B_Rules/bios_override_detected | BIOS override is detected when the first readback matches the write but the second readback (≈200ms later) does not. |
| calibration-pr2b-07.md | TestWriteWithRetry_RefusesPhantom | ShouldApplyCurve returns ErrPhantom for phantom channels; writes are unconditionally refused. |
| calibration-pr2b-10.md | TestPR2B_Rules/step_0N_stall_binary_search | step_0_N stall detection uses binary search; convergence in ≤ ceil(log2(N+1)) + 1 samples. |
| calibration-pr2b-11.md | TestPR2B_Rules/calibration_result_json_roundtrip | CalibrationRun JSON round-trips without data loss; schema_version=1 is preserved. |
| calibration-pr2b-12.md | TestPR2B_Rules/store_filename_format | Store.Filename produces "<dmi_fingerprint>-<bios_version_safe>.json"; non-alphanumeric chars in the BIOS version are ... |

## RULE-DIAG

| File | Bound subtest | Summary |
|------|---------------|---------|
| diag-pr2c-01.md | TestRuleDiagPR2C_01/default_profile_is_conservative | Default redaction profile is default-conservative. |
| diag-pr2c-02.md | TestRuleDiagPR2C_02/self_check_detects_hostname_leak | Self-check pass detects un-redacted hostname strings in the final bundle. |
| diag-pr2c-03.md | TestRuleDiagPR2C_03/self_check_failure_is_fatal | Self-check failure is fatal unless --allow-redaction-failures is passed. |
| diag-pr2c-04.md | TestRuleDiagPR2C_04/mapping_file_mode_0600 | Redactor mapping file is created with mode 0600. |
| diag-pr2c-05.md | TestRuleDiagPR2C_05/off_requires_confirm_or_flag | --redact=off requires interactive confirmation or --i-understand-this-is-not-redacted. |
| diag-pr2c-06.md | TestRuleDiagPR2C_06/denylist_paths_never_captured | Architecturally-excluded paths are never captured, even with --redact=off. |
| diag-pr2c-07.md | TestRuleDiagPR2C_07/redaction_report_always_present | REDACTION_REPORT.json is generated for every bundle including --redact=off. |
| diag-pr2c-08.md | TestRuleDiagPR2C_08/mapping_consistent_within_bundle | Redaction mapping is consistent within a bundle (same input → same output). |
| diag-pr2c-09.md | TestRuleDiagPR2C_09/foreign_mapping_file_graceful | Reading a mapping file from another machine must not crash (graceful schema-mismatch). |
| diag-pr2c-10.md | TestRuleDiagPR2C_10/output_dir_and_file_modes | Bundle output directory has mode 0o700; bundle file has mode 0o600. Both verified post-write. |

## RULE-ENVELOPE

| File | Bound subtest | Summary |
|------|---------------|---------|
| RULE-ENVELOPE-01.md | TestRULE_ENVELOPE_01_WritePWMViaHelper | All PWM writes during envelope probing MUST go through polarity.WritePWM — never direct sysfs writes. |
| RULE-ENVELOPE-02.md | TestRULE_ENVELOPE_02_BaselineRestoreAllExitPaths | Baseline PWM is captured before the first step write and restored on every exit path via defer. |
| RULE-ENVELOPE-03.md | TestRULE_ENVELOPE_03_ClassThresholdLookup | ClassThresholds lookup returns the correct Thresholds struct for every SystemClass including ClassUnknown. |
| RULE-ENVELOPE-04.md | TestRULE_ENVELOPE_04_DTDtTripBoundary | dT/dt thermal abort fires when temperature rise rate exceeds DTDtAbortCPerSec for the class. |
| RULE-ENVELOPE-05.md | TestRULE_ENVELOPE_05_TAbsTripBoundary | Absolute temperature abort fires when any sensor exceeds Tjmax minus TAbsOffsetBelowTjmax. |
| RULE-ENVELOPE-06.md | TestRULE_ENVELOPE_06_AmbientHeadroomPrecondition | Ambient headroom precondition refuses Envelope C when ambient ≥ (Tjmax − AmbientHeadroomMin). |
| RULE-ENVELOPE-07.md | TestRULE_ENVELOPE_07_AbortCToProbeD_OrderingPersist | Envelope C thermal abort transitions to Envelope D; KV state reflects the ordering and abort reason. |
| RULE-ENVELOPE-08.md | TestRULE_ENVELOPE_08_EnvelopeDRefusesBelowBaseline | Envelope D (ramp-up) only writes PWM values ≥ baseline; writes below baseline are refused. |
| RULE-ENVELOPE-09.md | TestRULE_ENVELOPE_09_StepLevelResumability | Probe is resumable from the last completed step after a daemon restart. |
| RULE-ENVELOPE-10.md | TestRULE_ENVELOPE_10_LogStoreSchemaConformance | Every probe step event is appended to the LogStore as a msgpack-encoded StepEvent with schema_version=1. |
| RULE-ENVELOPE-11.md | TestRULE_ENVELOPE_11_SequentialChannelsNoParallel | Channels are probed sequentially — never concurrently. |
| RULE-ENVELOPE-12.md | TestRULE_ENVELOPE_12_PausedStateReruns_StartupGate | A channel in state "paused_*" re-runs the idle.StartupGate before resuming the probe. |
| RULE-ENVELOPE-13.md | TestRULE_ENVELOPE_13_UniversalDInsufficient_WizardFallback | When Envelope D cannot produce a safe curve (all steps below baseline), the wizard falls back to monitor-only mode. |
| RULE-ENVELOPE-14.md | TestRULE_ENVELOPE_14_PWMReadbackVerification | PWM readback after each step write must match the written value within ±2 LSB. |

## RULE-EXPERIMENTAL

| File | Bound subtest | Summary |
|------|---------------|---------|
| experimental-amd-overdrive-01.md | TestAMDGPU_WriteRefusesWhenOverdriveFlagFalse | All AMD GPU HAL write paths return ErrAMDOverdriveDisabled when AMDOverdrive flag is false. |
| experimental-amd-overdrive-02.md | TestAMDOverdrive_PreconditionFailsActionableWhenBitUnset | Precondition check parses /proc/cmdline for the OverDrive bit (0x4000) and returns an actionable detail when unset. |
| experimental-amd-overdrive-03.md | TestDoctor_AMDOverdrive_ReportsActiveStateAndMask | Doctor check reports active state and ppfeaturemask value in the status line. |
| experimental-amd-overdrive-04.md | TestAMDGPU_RDNA4RefusesOnKernelBelow615 | RDNA4 (Navi 48, PCI 0x7550) fan_curve writes are refused on kernel < 6.15. |
| experimental-framework-01.md | TestMerge_PrecedenceCLIOverConfig | CLI flags override config-file values; OR-merge satisfies CLI > config > default for additive boolean flags. |
| experimental-framework-02.md | TestExperimental_HwdiagEntryPublished | Publish sets one hwdiag entry per active flag under ComponentExperimental. |
| experimental-framework-03.md | TestDiag_SnapshotIncludesActiveAndPreconditions | Snapshot encodes active flags and all-flags precondition status for the diagnostic bundle. |
| experimental-framework-04.md | TestStartupLog_FirstRunEmits | LogActiveFlagsOnce emits at most one INFO log per 24h; no log when no flags are active. |
| experimental-merge-01.md | TestMatcher_ExperimentalEligibility_OrsBoardAndGPU | CatalogMatch.ExperimentalEligibility OR-merges experimental flags from board and driver profiles. |
| experimental-schema-01.md | TestSchemaValidator_ExperimentalBlock_AcceptsRecognizedKeys | Recognized experimental key with bool value is accepted and parsed into ExperimentalBlock. |
| experimental-schema-02.md | TestSchemaValidator_ExperimentalBlock_RejectsNonBoolValue | Recognized experimental key with non-bool value is rejected with a typed error. |
| experimental-schema-03.md | TestSchemaValidator_ExperimentalBlock_RejectsTypoWithSuggestion | Unknown experimental key with Levenshtein distance ≤ 2 from a known key is rejected as a likely typo with a suggest... |
| experimental-schema-04.md | TestSchemaValidator_ExperimentalBlock_WarnsUnknownKeyOnce | Unknown experimental key with Levenshtein distance > 2 is accepted with a one-shot WARN; subsequent occurrences of th... |
| experimental-schema-05.md | TestSchemaValidator_ExperimentalBlockAbsent_BehavesAsV1_1 | Absent experimental block behaves identically to an all-false ExperimentalBlock (v1.1 behavior preserved). |

## RULE-FINGERPRINT

| File | Bound subtest | Summary |
|------|---------------|---------|
| fingerprint-04.md | TestMatcher_BiosVersionGlob_Matches | Matcher matches DMI `bios_version` glob when field is present on a board profile. |
| fingerprint-05.md | TestMatcher_BiosVersionAbsent_BehavesAsV1 | Fingerprint without `bios_version` field matches any live BIOS version (v1 behavior unchanged). |
| fingerprint-06.md | TestMatcher_DTCompatibleGlob_Matches | Matcher matches device-tree `compatible` list glob when DMI is absent and `dt_fingerprint.compatible` is set. |
| fingerprint-07.md | TestMatcher_DTModelGlob_Matches | Matcher matches device-tree `model` string glob when DMI is absent and `dt_fingerprint.model` is set. |

## RULE-GPU

| File | Bound subtest | Summary |
|------|---------------|---------|
| gpu-pr2d-01.md | TestGPU_WriteGated | All GPU writes are gated behind --enable-gpu-write flag AND per-device capability probe success. |
| gpu-pr2d-02.md | TestNVML_NoCGO | NVML wrapper in internal/hal/gpu/nvml/ uses purego only — no CGO. |
| gpu-pr2d-03.md | TestNVML_GracefulMissingLib | libnvidia-ml.so.1 absence is graceful — no panic, daemon continues. |
| gpu-pr2d-04.md | TestHWDB_GPUEntriesV1Compatible | Schema v1.0 unchanged — new GPU driver YAMLs validate against existing profile_v1.go schema with no new fields. |
| gpu-pr2d-05.md | TestGPU_NoHwmonNumbersHardcoded | hwmon path resolution by name only — no hwmonN number literals in non-test code under internal/hal/gpu/. |
| gpu-pr2d-06.md | TestNVML_LaptopDgpuRequiresEC | Laptop dGPU detection is conservative — DMI chassis_type in laptop set marks dGPU as requires_userspace_ec. |
| gpu-pr2d-07.md | TestAMD_RDNA3UsesFanCurve | RDNA3+ AMD GPU writes use gpu_od/fan_ctrl/fan_curve interface only — direct pwm1 writes are refused. |
| gpu-pr2d-08.md | TestXE_ReadOnly | Intel xe/i915 backend has no write code paths — os.OpenFile with write flags must not appear. |

## RULE-HWDB

| File | Bound subtest | Summary |
|------|---------------|---------|
| hwdb-capture-01.md | TestRuleHwdbCapture_01_PendingDirOnly | Capture writes go to `/var/lib/ventd/profiles-pending/` (or `$XDG_STATE_HOME/ventd/profiles-pending/` in user mode) o... |
| hwdb-capture-02.md | TestRuleHwdbCapture_02_FailClosedOnAnonymise | Capture cannot run if the anonymiser fails. The capture function returns an error and writes nothing — fail closed. |
| hwdb-capture-03.md | TestRuleHwdbCapture_03_AllowlistedFieldsOnly | A captured profile YAML never contains a field outside the schema v1.0 allowlist. |
| hwdb-pr2-01.md | TestRuleHwdbPR2_01 | Every driver_profile MUST declare all fields in §2-§12. Missing field = matcher refuses to load profile DB. |
| hwdb-pr2-02.md | TestRuleHwdbPR2_02 | chip_profile.inherits_driver MUST resolve to a known driver_profile.module. |
| hwdb-pr2-03.md | TestRuleHwdbPR2_03 | board_profile.primary_controller.chip MUST resolve to a known chip_profile.name. |
| hwdb-pr2-04.md | TestRuleHwdbPR2_04 | pwm_unit_max MUST be set when pwm_unit ∈ {step_0_N, cooling_level}. |
| hwdb-pr2-05.md | TestRuleHwdbPR2_05 | pwm_enable_modes MUST contain a manual entry when capability ∈ {rw_full, rw_quirk, rw_step}. |
| hwdb-pr2-06.md | TestRuleHwdbPR2_06 | recommended_alternative_driver MUST be non-null when capability == ro_pending_oot. |
| hwdb-pr2-07.md | TestRuleHwdbPR2_07 | fan_control_capable: false profiles MUST install in monitor-only mode (no calibration probe runs). |
| hwdb-pr2-08.md | TestWriteWithRetry_RefusesBIOSOverridden | Calibration result bios_overridden: true MUST cause apply path to refuse curve writes for that channel. |
| hwdb-pr2-09.md | TestRuleHwdbPR2_09 | DMI BIOS version mismatch between calibration record and current firmware MUST trigger recalibration. |
| hwdb-pr2-10.md | TestRuleHwdbPR2_10 | Layer precedence (board > chip > driver, calibration > all for runtime fields) MUST be enforced by the resolver. Inva... |
| hwdb-pr2-11.md | TestRuleHwdbPR2_11 | PR 1 → PR 2 migration: a PR 1 pwm_control: <string> MUST resolve via the chip-name fallback path with a logged warn... |
| hwdb-pr2-12.md | TestRuleHwdbPR2_12 | The matcher MUST refuse to match a profile that violates any of RULE-HWDB-PR2-01..05. Test fixture: invalid profile, ... |
| hwdb-pr2-13.md | TestRuleHwdbPR2_13 | Every driver_profile MUST declare exit_behaviour from the §12.1 enum. Missing/unknown value = matcher refuses to loa... |
| hwdb-pr2-14.md | TestRuleHwdbPR2_14 | Every driver_profile MUST declare runtime_conflict_detection_supported boolean. Field is consumed by post-PR-2 sanity... |

## RULE-IDLE

| File | Bound subtest | Summary |
|------|---------------|---------|
| RULE-IDLE-01.md | TestRULE_IDLE_01_StartupGate_DurabilityRequired | StartupGate requires the idle predicate to be TRUE for ≥ 300 s (durability window) before returning ok=true. |
| RULE-IDLE-02.md | TestRULE_IDLE_02_BatteryRefusal | Battery-powered operation (AC offline or BAT discharging) is a hard refusal — AllowOverride has no effect. |
| RULE-IDLE-03.md | TestRULE_IDLE_03_ContainerRefusal | Container environment is a hard refusal — AllowOverride has no effect. |
| RULE-IDLE-04.md | TestRULE_IDLE_04_PSIPrimaryFallback | PSI is the primary load signal when /proc/pressure/ is available; /proc/loadavg is the fallback. |
| RULE-IDLE-05.md | TestRULE_IDLE_05_LoadAvgDirectRead | /proc/loadavg is read via direct file read, not getloadavg(3); no CGO is permitted. |
| RULE-IDLE-06.md | TestRULE_IDLE_06_ProcessBlocklist | Process blocklist includes canonical R5 §7.1 entries and is extensible via SetExtraBlocklist. |
| RULE-IDLE-07.md | TestRULE_IDLE_07_RuntimeCheckBaselineDelta | RuntimeCheck computes a delta from the baseline snapshot; baseline-resident blocked processes do not cause refusal. |
| RULE-IDLE-08.md | TestRULE_IDLE_08_BackoffFormula | Backoff delay follows min(60×2^n, 3600) ± 20% jitter, with daily cap at n=12. |
| RULE-IDLE-09.md | TestRULE_IDLE_09_OverrideNeverSkipsBatteryContainer | AllowOverride=true skips storage-maintenance refusal but never skips battery or container refusal. |
| RULE-IDLE-10.md | TestRULE_IDLE_10_StartupGateReturnsSnapshot | StartupGate returns a non-nil, populated Snapshot on success; snap.Timestamp is non-zero. |

## RULE-OVERRIDE

| File | Bound subtest | Summary |
|------|---------------|---------|
| override-unsupported-01.md | TestMatcher_UnsupportedEmitsLogOnce | Matcher with `overrides.unsupported: true` emits the INFO log exactly once per ventd lifetime per board ID. |
| override-unsupported-02.md | TestCalibration_UnsupportedSkipsAutocurve | Calibration phase skips autocurve generation when the resolved profile has `overrides.unsupported: true`. |

## RULE-POLARITY

| File | Bound subtest | Summary |
|------|---------------|---------|
| RULE-POLARITY-01.md | TestPolarityRules/RULE-POLARITY-01_midpoint_write | Midpoint write is exactly 128 for hwmon, 50% for NVML, and vendor-specific for IPMI. |
| RULE-POLARITY-02.md | TestPolarityRules/RULE-POLARITY-02_hold_time_3s | Hold time after the midpoint write MUST be exactly HoldDuration (3s) ± 200ms across all backends. |
| RULE-POLARITY-03.md | TestPolarityRules/RULE-POLARITY-03_threshold_boundary | Classification thresholds — hwmon |delta| < 150 RPM → phantom; NVML |delta| < 10 pct → phantom. |
| RULE-POLARITY-04.md | TestPolarityRules/RULE-POLARITY-04_restore_on_all_paths | Baseline PWM is restored on every exit path — write failure, context cancel, and normal return. |
| RULE-POLARITY-05.md | TestPolarityRules/RULE-POLARITY-05_write_helper_refuses_phantom_unknown | WritePWM refuses writes to phantom channels (ErrChannelNotControllable) and unknown channels (ErrPolarityNotResolved). |
| RULE-POLARITY-06.md | TestPolarityRules/RULE-POLARITY-06_nvml_driver_version_gate | NVML polarity probe refuses channels whose driver version is below R515 (major < 515). |
| RULE-POLARITY-07.md | TestPolarityRules/RULE-POLARITY-07_ipmi_vendor_probe_interface | IPMI polarity probe uses a vendor-dispatch interface; Dell firmware-locked and HPE profile-only channels are permanen... |
| RULE-POLARITY-08.md | TestPolarityRules/RULE-POLARITY-08_daemon_start_match | On daemon start, ApplyOnStart matches persisted polarity results to live channels by PWMPath; unmatched channels rema... |
| RULE-POLARITY-09.md | TestPolarityRules/RULE-POLARITY-09_reset_wipes_calibration_namespace | "Reset to initial setup" wipes the calibration KV namespace atomically via WipeNamespaces. |
| RULE-POLARITY-10.md | TestPolarityRules/RULE-POLARITY-10_phantom_not_writable | All phantom reason codes are writable via WritePWM and return ErrChannelNotControllable. |

## RULE-PROBE

| File | Bound subtest | Summary |
|------|---------------|---------|
| RULE-PROBE-01.md | TestProbe_Rules/RULE-PROBE-01_read_only | Probe MUST be entirely read-only — no PWM writes, no IPMI commands, no EC commands. |
| RULE-PROBE-02.md | TestProbe_Rules/RULE-PROBE-02_virt_requires_3_sources | Virtualisation detection requires ≥3 independent sources before setting Virtualised=true. |
| RULE-PROBE-03.md | TestProbe_Rules/RULE-PROBE-03_container_requires_2_sources | Containerisation detection requires ≥2 independent sources before setting Containerised=true. |
| RULE-PROBE-04.md | TestProbe_Rules/RULE-PROBE-04_classify_outcome | ClassifyOutcome follows the §3.2 algorithm exactly — virt/container → refuse; no sensors → refuse; sensors onl... |
| RULE-PROBE-05.md | TestProbe_Rules/RULE-PROBE-05_channels_uniform_regardless_of_catalog_match | No downstream code branches on CatalogMatch==nil vs non-nil — channels are enumerated the same way regardless. |
| RULE-PROBE-06.md | TestProbe_Rules/RULE-PROBE-06_polarity_always_unknown | ControllableChannel.Polarity MUST be drawn from the closed set {"unknown", "normal", "inverted", "phantom"}. |
| RULE-PROBE-07.md | TestProbe_Rules/RULE-PROBE-07_persist_outcome_writes_kv_keys | PersistOutcome writes schema_version, last_run, result (probe namespace) and initial_outcome, outcome_reason, outcome... |
| RULE-PROBE-08.md | TestProbe_Rules/RULE-PROBE-08_load_wizard_outcome | Daemon start consults wizard.initial_outcome KV key; LoadWizardOutcome returns the correct Outcome enum value. |
| RULE-PROBE-09.md | TestProbe_Rules/RULE-PROBE-09_wipe_namespaces_empties_both | "Reset to initial setup" wipes both wizard and probe KV namespaces atomically; LoadWizardOutcome returns ok=false aft... |
| RULE-PROBE-10.md | TestProbe_Rules/RULE-PROBE-10_no_bios_known_bad_file | internal/hwdb/bios_known_bad.go MUST NOT exist. |
| RULE-PROBE-11.md | TestRULE_PROBE_11_RefuseDoesNotBlockStartup |  |

## RULE-SCHEMA

| File | Bound subtest | Summary |
|------|---------------|---------|
| schema-08.md | TestSchemaValidator_RejectsBothFingerprintTypes | Board catalog loader rejects a profile with both `dmi_fingerprint` and `dt_fingerprint` set. |

## RULE-STATE

| File | Bound subtest | Summary |
|------|---------------|---------|
| RULE-STATE-01.md | TestRULE_STATE_01_AtomicWrite | KV store writes MUST use tempfile + rename + fsync semantics. Direct overwrite is forbidden. |
| RULE-STATE-02.md | TestRULE_STATE_02_BlobSHA256Verification | Blob store reads MUST verify magic, length, and SHA256. Mismatch MUST result in found=false returned to consumer; con... |
| RULE-STATE-03.md | TestRULE_STATE_03_LogOAppendODsync | Log store appends MUST use `O_APPEND | O_DSYNC`. Buffered writes are forbidden for log primitive. |
| RULE-STATE-04.md | TestRULE_STATE_04_LogTornRecordSkip | Log store iteration MUST tolerate torn records (length-prefix-overrun) and CRC-mismatched records (skip and continue). |
| RULE-STATE-05.md | TestRULE_STATE_05_SchemaVersionCheck | Schema version on read MUST be checked. Y > X (downgrade) MUST refuse start with diagnostic. Y < X (upgrade) MUST run... |
| RULE-STATE-06.md | TestRULE_STATE_06_PIDFileMultiProcess | Multiple ventd processes against the same state directory MUST be detected via PID file; second process MUST exit wit... |
| RULE-STATE-07.md | TestRULE_STATE_07_TransactionAtomicCommit | KV `WithTransaction` MUST serialise to a single atomic write at commit. Partial commits across failure are forbidden. |
| RULE-STATE-08.md | TestRULE_STATE_08_LogRotationNoRecordLoss | Log rotation MUST NOT lose in-flight records. Atomic rename + new file creation, no append-after-rename window. |
| RULE-STATE-09.md | TestRULE_STATE_09_FileModeRepair | All state files MUST be created with mode `0640 ventd ventd`; directories `0755 ventd ventd`. Mode mismatches on read... |
| RULE-STATE-10.md | TestRULE_STATE_10_DirectoryBootstrap | The state directory `/var/lib/ventd/` MUST exist after first daemon start; absence triggers initialisation, not failure. |

## RULE-SYSCLASS

| File | Bound subtest | Summary |
|------|---------------|---------|
| RULE-SYSCLASS-01.md | TestRULE_SYSCLASS_01_PrecedenceOrder | System class precedence order is NAS > MiniPC > Laptop > Server > HEDT > MidDesktop > Unknown. |
| RULE-SYSCLASS-02.md | TestRULE_SYSCLASS_02_KVWriteBeforeEnvelopeC | System class and evidence are written to KV store before Envelope C begins. |
| RULE-SYSCLASS-03.md | TestRULE_SYSCLASS_03_AmbientFallbackChain | Ambient sensor identification uses a three-step fallback chain: labeled → lowest-at-idle → 25°C constant. |
| RULE-SYSCLASS-04.md | TestRULE_SYSCLASS_04_AmbientBoundsRefusal | Ambient reading outside [10, 50]°C is rejected as implausible before Envelope C starts. |
| RULE-SYSCLASS-05.md | TestRULE_SYSCLASS_05_ServerBMCGate | ClassServer + BMC-present systems require --allow-server-probe to proceed with Envelope C. |
| RULE-SYSCLASS-06.md | TestRULE_SYSCLASS_06_LaptopECHandshake | Laptop EC handshake succeeds when RPM changes within 5s; fails cleanly on context cancel. |
| RULE-SYSCLASS-07.md | TestRULE_SYSCLASS_07_EvidenceCompleteness | Every system class produces at least one evidence string in Detection.Evidence. |

## Free-form files

| File | Summary |
|------|---------|
| attribution.md | Attribution & Commit Identity |
| calibrate-persist.md | Calibration Result Persistence Rules |
| calibration-safety.md | Calibration Safety Rules |
| ci-action-pinning.md | CI Action Pinning Invariants |
| collaboration.md | Collaboration Rules |
| coupling.md | Layer-B thermal coupling rules (v0.5.7) |
| go-conventions.md | Go Conventions — ventd |
| hal-contract.md | HAL Backend Contract |
| hidraw-safety.md | hidraw-safety — invariant bindings for pure-Go Linux hidraw substrate |
| hwdb-schema.md | HWDB schema invariants |
| hwmon-safety.md | Hardware Safety Rules |
| hwmon-sentinel.md | hwmon Sentinel Acceptance Invariants |
| install-contract.md | Install Contract Rules |
| ipmi-safety.md | IPMI Safety Rules |
| liquid-safety.md | liquid-safety — invariant bindings for the Corsair AIO backend |
| marginal.md | Layer-C marginal-benefit rules (v0.5.8) |
| observation.md | Observation Log Rules |
| opportunistic.md | Opportunistic active probing rules (v0.5.5) |
| signature.md | Workload signature learning rules (v0.5.6) |
| signguard.md | Sign-guard rules (v0.5.8) |
| ui.md | Web UI Invariants (spec-12) |
| usability.md | Usability — Universal Linux Compatibility |
| watchdog-safety.md | Watchdog Safety Rules |
| web-ui.md | Web UI Rules |

