# Doctor rules — v0.5.10

These invariants govern the v0.5.10 doctor surface — the post-install
runtime equivalent of the wizard recovery classifier (#800/#810).

The patch spec is `specs/spec-10-doctor.md` (predates v0.5.0; the
v0.5.10 implementation incorporates the R13 pivot — three-surface
Doctor page that replaces Health, 60 s detector cadence, KV-backed
suppression at namespace `doctor/suppression`, NO per-board BIOS
denylist per RULE-PROBE-10).

Each rule below is bound to one or more subtests in
`internal/doctor/`. `tools/rulelint` blocks the merge if a rule
lacks its bound test.

## Foundation

## RULE-DOCTOR-SEVERITY-01: Severity exit codes are 0=OK / 1=Warning / 2=Blocker / 3=Error per spec-10's RULE-DOCTOR-02.

The CLI's exit code is the Report-level severity rolled up across
emitted Facts. Pinned so a future Severity reordering can't silently
break the spec-11 wizard's branching contract.

Bound: internal/doctor/severity_test.go:TestSeverity_String
Bound: internal/doctor/severity_test.go:TestSeverity_ExitCode
Bound: internal/doctor/severity_test.go:TestSeverity_Worse

## RULE-DOCTOR-SUPPRESSION-01: KV-backed suppression at namespace `doctor/suppression`; round-trip + auto-expiry on time advance.

The store keys by `<detector_name>:<entity_hash>` and serialises a
`SuppressionEntry` with `until_unix`, `reason`, `acknowledged_at_unix`.
`IsSuppressed` reports true only when the current clock is before
`until`. Auto-expiry per the test's clock-advance pattern.

Bound: internal/doctor/suppression_test.go:TestRULE_DOCTOR_SUPPRESSION_RoundTrip
Bound: internal/doctor/suppression_test.go:TestRULE_DOCTOR_SUPPRESSION_AcknowledgeForever
Bound: internal/doctor/suppression_test.go:TestRULE_DOCTOR_SUPPRESSION_Unsuppress
Bound: internal/doctor/suppression_test.go:TestSuppressionStore_NilSafe
Bound: internal/doctor/suppression_test.go:TestSuppressionStore_List

## RULE-DOCTOR-RUNNER-01: RunOnce aggregates Facts across detectors with stable order, severity rollup, and skip/only filtering.

The runner sorts facts by `(detector_name, entity_hash)` so successive
reports diff cleanly for the SSE-driven web UI; severity rollup picks
the worst per fact. Skip wins over Only on conflict.

Bound: internal/doctor/runner_test.go:TestRULE_DOCTOR_RUNNER_RunOnceAggregatesFacts
Bound: internal/doctor/runner_test.go:TestRULE_DOCTOR_RUNNER_SkipExcludesDetector
Bound: internal/doctor/runner_test.go:TestRULE_DOCTOR_RUNNER_OnlyIncludesNamed
Bound: internal/doctor/runner_test.go:TestRULE_DOCTOR_RUNNER_SeverityRollup

## RULE-DOCTOR-RUNNER-02: Per-detector timeout caps Probe at 200 ms (configurable); panic recovery surfaces as DetectorError, not Fact.

RULE-DOCTOR-09 budgets each detector at 200 ms with the runner
total <2 s. Panic in one detector becomes a DetectorError; other
detectors still run. ctx cancellation before any detector starts
returns wrapped context.Canceled.

Bound: internal/doctor/runner_test.go:TestRULE_DOCTOR_RUNNER_PerDetectorTimeout
Bound: internal/doctor/runner_test.go:TestRULE_DOCTOR_RUNNER_PanicSurfacesAsDetectorError
Bound: internal/doctor/runner_test.go:TestRULE_DOCTOR_RUNNER_RespectsContextCancel

## RULE-DOCTOR-RUNNER-03: Suppressed Facts are filtered at emission time before they reach Report.Facts.

The runner consults `SuppressionStore.IsSuppressed(name, entity_hash)`
on every emitted Fact. Suppression auto-expires; nil store means
"never suppressed".

Bound: internal/doctor/runner_test.go:TestRULE_DOCTOR_RUNNER_SuppressionFiltersFacts

## Detectors — reuse-wired

## RULE-DOCTOR-DETECTOR-PREFLIGHT: PreflightSubsetDetector wraps PR-D's PreflightOOT chain; maps each Reason to (Severity, FailureClass).

Surfaces "system was healthy at install but a precondition regressed
since" cases. Hard blockers (Containerised, NoSudoNoRoot,
LibModulesReadOnly, AnotherWizardRunning, InTreeDriverConflict,
StaleDKMSState, DiskFull) → Blocker; install-time-only prereq absences
(GCC, Make, sign-file, mokutil, headers, DKMS, KernelTooNew,
SecureBoot, AptLockHeld) → Warning.

Bound: internal/doctor/detectors/preflight_subset_d_test.go:TestRULE_DOCTOR_DETECTOR_PreflightSubset_OKEmitsNoFacts
Bound: internal/doctor/detectors/preflight_subset_d_test.go:TestRULE_DOCTOR_DETECTOR_PreflightSubset_BlockerOnContainer
Bound: internal/doctor/detectors/preflight_subset_d_test.go:TestRULE_DOCTOR_DETECTOR_PreflightSubset_WarningOnGCCMissing
Bound: internal/doctor/detectors/preflight_subset_d_test.go:TestRULE_DOCTOR_DETECTOR_PreflightSubset_RespectsContextCancel
Bound: internal/doctor/detectors/preflight_subset_d_test.go:TestPreflightSubset_EntityHashStableAcrossCalls

## RULE-DOCTOR-DETECTOR-POLARITYFLIP: PolarityFlipDetector wraps signguard.Detector.Confirmed; emits Warning per channel whose polarity vote hasn't stabilised.

Catches the re-cabled-fan-mid-deployment case (RULE-SGD-CONT-01).
Warning, not Blocker — the controller's polarity.WritePWM
(RULE-POLARITY-05) already inverts based on persisted polarity
classification; the signguard snapshot is advisory.

Bound: internal/doctor/detectors/polarity_flip_d_test.go:TestRULE_DOCTOR_DETECTOR_PolarityFlip_AllConfirmedNoFacts
Bound: internal/doctor/detectors/polarity_flip_d_test.go:TestRULE_DOCTOR_DETECTOR_PolarityFlip_OneUnconfirmedYieldsWarning
Bound: internal/doctor/detectors/polarity_flip_d_test.go:TestRULE_DOCTOR_DETECTOR_PolarityFlip_NilSignguardEmitsNothing
Bound: internal/doctor/detectors/polarity_flip_d_test.go:TestRULE_DOCTOR_DETECTOR_PolarityFlip_RespectsContextCancel
Bound: internal/doctor/detectors/polarity_flip_d_test.go:TestPolarityFlip_EntityHashUniqueAcrossChannels
Bound: internal/doctor/detectors/polarity_flip_d_test.go:TestPolarityFlip_NoStateAcrossProbeCalls
Bound: internal/doctor/detectors/polarity_flip_d_test.go:TestTimeNowFromDeps_NilFallback

## Detectors — runtime

## RULE-DOCTOR-DETECTOR-DKMSSTATUS: DKMSStatusDetector parses `dkms status` for failed entries; surfaces as Blocker via ClassDKMSBuildFailed.

Recognises both DKMS 2.x (comma-separated) and 3.x (slash-separated)
line formats; "broken" alias from some forks; tolerates "failed
(config: ...)" prefix variants. dkms-not-on-PATH gracefully degrades
(preflight detector covers absence).

Bound: internal/doctor/detectors/dkms_status_d_test.go:TestRULE_DOCTOR_DETECTOR_DKMSStatus_HappyAllInstalled
Bound: internal/doctor/detectors/dkms_status_d_test.go:TestRULE_DOCTOR_DETECTOR_DKMSStatus_FailureSurfacesAsBlocker
Bound: internal/doctor/detectors/dkms_status_d_test.go:TestRULE_DOCTOR_DETECTOR_DKMSStatus_BrokenAlsoFails
Bound: internal/doctor/detectors/dkms_status_d_test.go:TestRULE_DOCTOR_DETECTOR_DKMSStatus_DKMSAbsentEmitsNothing
Bound: internal/doctor/detectors/dkms_status_d_test.go:TestRULE_DOCTOR_DETECTOR_DKMSStatus_RespectsContextCancel
Bound: internal/doctor/detectors/dkms_status_d_test.go:TestParseDKMSStatusLine
Bound: internal/doctor/detectors/dkms_status_d_test.go:TestIsDKMSFailureStatus

## RULE-DOCTOR-DETECTOR-USERSPACECONFLICT: Queries systemctl is-active for known fan daemons; emits Blocker per active conflict.

Default unit set: fancontrol, thinkfan, nbfc_service, coolercontrold,
liquidctl. Non-systemd hosts (Alpine OpenRC) gracefully degrade. The
"failed" state is NOT treated as conflict — the unit isn't actively
writing fans.

Bound: internal/doctor/detectors/userspace_conflict_d_test.go:TestRULE_DOCTOR_DETECTOR_UserspaceConflict_AllInactiveNoFacts
Bound: internal/doctor/detectors/userspace_conflict_d_test.go:TestRULE_DOCTOR_DETECTOR_UserspaceConflict_ActiveSurfacesAsBlocker
Bound: internal/doctor/detectors/userspace_conflict_d_test.go:TestRULE_DOCTOR_DETECTOR_UserspaceConflict_MultipleActiveYieldsMultipleFacts
Bound: internal/doctor/detectors/userspace_conflict_d_test.go:TestRULE_DOCTOR_DETECTOR_UserspaceConflict_FailedStateNotTreatedAsConflict
Bound: internal/doctor/detectors/userspace_conflict_d_test.go:TestRULE_DOCTOR_DETECTOR_UserspaceConflict_NonSystemdGracefullyDegrades
Bound: internal/doctor/detectors/userspace_conflict_d_test.go:TestRULE_DOCTOR_DETECTOR_UserspaceConflict_RespectsContextCancel
Bound: internal/doctor/detectors/userspace_conflict_d_test.go:TestUserspaceConflict_UnitListOverride

## RULE-DOCTOR-DETECTOR-MODULESLOAD: Verifies /etc/modules-load.d/ventd-<mod>.conf still exists with content naming the module.

Three failure modes: file missing, content drifted (no non-comment
line names module), I/O error other than not-exist (RULE-DOCTOR-04
graceful-degrade with Warning). Token-bounded matching avoids
"nct6687d" matching "nct6687".

Bound: internal/doctor/detectors/modules_load_d_test.go:TestRULE_DOCTOR_DETECTOR_ModulesLoad_AllPresentAndCorrect
Bound: internal/doctor/detectors/modules_load_d_test.go:TestRULE_DOCTOR_DETECTOR_ModulesLoad_MissingFileSurfacesAsWarning
Bound: internal/doctor/detectors/modules_load_d_test.go:TestRULE_DOCTOR_DETECTOR_ModulesLoad_DriftedContentSurfacesAsWarning
Bound: internal/doctor/detectors/modules_load_d_test.go:TestRULE_DOCTOR_DETECTOR_ModulesLoad_PermissionDeniedSurfaces
Bound: internal/doctor/detectors/modules_load_d_test.go:TestRULE_DOCTOR_DETECTOR_ModulesLoad_RespectsContextCancel
Bound: internal/doctor/detectors/modules_load_d_test.go:TestContentMentionsModule_SubstringNotFalseMatch
Bound: internal/doctor/detectors/modules_load_d_test.go:TestModulesLoadConfPath_FormatStable

## RULE-DOCTOR-DETECTOR-BATTERY: AC offline AND battery Discharging emits a Warning; AND-gate prevents desktop-empty-AC-slot false positive.

RULE-IDLE-02 hard-refuses calibration on battery; this surfaces the
runtime case (laptop unplugged mid-run). "Not charging" status is
NOT treated as Discharging.

Bound: internal/doctor/detectors/battery_transition_d_test.go:TestRULE_DOCTOR_DETECTOR_BatteryTransition_OnACNoFacts
Bound: internal/doctor/detectors/battery_transition_d_test.go:TestRULE_DOCTOR_DETECTOR_BatteryTransition_OnBatteryYieldsWarning
Bound: internal/doctor/detectors/battery_transition_d_test.go:TestRULE_DOCTOR_DETECTOR_BatteryTransition_DesktopWithEmptyACSlotNoFalsePositive
Bound: internal/doctor/detectors/battery_transition_d_test.go:TestRULE_DOCTOR_DETECTOR_BatteryTransition_NoPowerSupplyDirIsNoOp
Bound: internal/doctor/detectors/battery_transition_d_test.go:TestRULE_DOCTOR_DETECTOR_BatteryTransition_ChargingNotDischargingNoFact
Bound: internal/doctor/detectors/battery_transition_d_test.go:TestRULE_DOCTOR_DETECTOR_BatteryTransition_RespectsContextCancel
Bound: internal/doctor/detectors/battery_transition_d_test.go:TestReadAcOnline_ParseValues
Bound: internal/doctor/detectors/battery_transition_d_test.go:TestBatteryTransition_FastPath

## RULE-DOCTOR-DETECTOR-APPARMORDRIFT: Compares the running ventd profile's mode against the daemon-start baseline.

Three drift cases: appeared-since-start (operator attached), disappeared
(parser reload didn't preserve attach), mode-flipped (enforce↔complain).
Empty ExpectedMode = no baseline = no-op. AppArmor-absent gracefully
degrades.

Bound: internal/doctor/detectors/apparmor_profile_drift_d_test.go:TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_AppearedSinceStart
Bound: internal/doctor/detectors/apparmor_profile_drift_d_test.go:TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_DisappearedSinceStart
Bound: internal/doctor/detectors/apparmor_profile_drift_d_test.go:TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_ModeChanged
Bound: internal/doctor/detectors/apparmor_profile_drift_d_test.go:TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_StableModeNoFacts
Bound: internal/doctor/detectors/apparmor_profile_drift_d_test.go:TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_NoBaselineNoOp
Bound: internal/doctor/detectors/apparmor_profile_drift_d_test.go:TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_NoAppArmorGracefulDegrade
Bound: internal/doctor/detectors/apparmor_profile_drift_d_test.go:TestRULE_DOCTOR_DETECTOR_AppArmorProfileDrift_RespectsContextCancel
Bound: internal/doctor/detectors/apparmor_profile_drift_d_test.go:TestLookupAppArmorProfile_ParseFormats

## RULE-DOCTOR-DETECTOR-KMODLOADED: Verifies expected modules appear in /sys/class/hwmon/hwmonN/name; emits Blocker per missing.

Distinct from preflight_subset's "OOT module needs reinstall after
kernel update" — this fires when the module is supposed to be
available right now and isn't. ExpectedModules set populated by the
catalog match resolution. Hwmon-root-absent still emits
("Blocker: module not loaded because we can't read /sys").

Bound: internal/doctor/detectors/kmod_loaded_d_test.go:TestRULE_DOCTOR_DETECTOR_KmodLoaded_AllExpectedPresent
Bound: internal/doctor/detectors/kmod_loaded_d_test.go:TestRULE_DOCTOR_DETECTOR_KmodLoaded_MissingModuleSurfacesAsBlocker
Bound: internal/doctor/detectors/kmod_loaded_d_test.go:TestRULE_DOCTOR_DETECTOR_KmodLoaded_MultipleMissing
Bound: internal/doctor/detectors/kmod_loaded_d_test.go:TestRULE_DOCTOR_DETECTOR_KmodLoaded_HwmonRootAbsentNoFacts
Bound: internal/doctor/detectors/kmod_loaded_d_test.go:TestRULE_DOCTOR_DETECTOR_KmodLoaded_NoExpectedModulesNoOp
Bound: internal/doctor/detectors/kmod_loaded_d_test.go:TestRULE_DOCTOR_DETECTOR_KmodLoaded_RespectsContextCancel
Bound: internal/doctor/detectors/kmod_loaded_d_test.go:TestLoadedHwmonNames_DedupsAcrossChips
Bound: internal/doctor/detectors/kmod_loaded_d_test.go:TestSortedKeys_DeterministicOrder

## RULE-DOCTOR-DETECTOR-EXPERIMENTALFLAGS: Reads hwdiag.Store filtered on ComponentExperimental; emits one OK Fact per active flag (RULE-DOCTOR-10).

Severity OK (not Warning) — flags are operator-opt-in; surface for
visibility, not for dismissal. Filter pinned so non-experimental
hwdiag entries never leak into doctor output.

Bound: internal/doctor/detectors/experimental_flags_d_test.go:TestRULE_DOCTOR_DETECTOR_ExperimentalFlags_NoActiveNoFacts
Bound: internal/doctor/detectors/experimental_flags_d_test.go:TestRULE_DOCTOR_DETECTOR_ExperimentalFlags_ActiveFlagYieldsOK
Bound: internal/doctor/detectors/experimental_flags_d_test.go:TestRULE_DOCTOR_DETECTOR_ExperimentalFlags_OnlyExperimentalComponent
Bound: internal/doctor/detectors/experimental_flags_d_test.go:TestRULE_DOCTOR_DETECTOR_ExperimentalFlags_NilStoreNoOp
Bound: internal/doctor/detectors/experimental_flags_d_test.go:TestRULE_DOCTOR_DETECTOR_ExperimentalFlags_RespectsContextCancel

## RULE-DOCTOR-DETECTOR-CONTAINERPOSTBOOT: Four-signal container detection with two-source confirmation per RULE-PROBE-03.

Mirrors the wizard's preflight container probe. Single signal alone
(stale /.dockerenv on a reinstalled bare-metal system) does NOT
fire. Cgroup-v2 docker is caught via the /.dockerenv + overlay combo.

Bound: internal/doctor/detectors/container_postboot_d_test.go:TestRULE_DOCTOR_DETECTOR_ContainerPostboot_BareMetalNoFacts
Bound: internal/doctor/detectors/container_postboot_d_test.go:TestRULE_DOCTOR_DETECTOR_ContainerPostboot_DockerWithCgroupV2
Bound: internal/doctor/detectors/container_postboot_d_test.go:TestRULE_DOCTOR_DETECTOR_ContainerPostboot_LXC
Bound: internal/doctor/detectors/container_postboot_d_test.go:TestRULE_DOCTOR_DETECTOR_ContainerPostboot_SingleSignalNoFalsePositive
Bound: internal/doctor/detectors/container_postboot_d_test.go:TestRULE_DOCTOR_DETECTOR_ContainerPostboot_KubernetesPod
Bound: internal/doctor/detectors/container_postboot_d_test.go:TestRULE_DOCTOR_DETECTOR_ContainerPostboot_RespectsContextCancel
Bound: internal/doctor/detectors/container_postboot_d_test.go:TestScoreContainerSignals_AllKeywordsRecognised

## RULE-DOCTOR-DETECTOR-CALIBRATIONFRESHNESS: Three failure modes against persisted CalibrationRun — absent (Warning), BIOS-stale (Blocker per RULE-HWDB-PR2-09), too-old (Warning).

The 6-month freshness threshold is operator-overridable via
FreshnessWindow. Loader-error case Warning ("cannot verify"). Nil
loader = no-op.

Bound: internal/doctor/detectors/calibration_freshness_d_test.go:TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_PresentAndFreshNoFacts
Bound: internal/doctor/detectors/calibration_freshness_d_test.go:TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_AbsentRecordWarning
Bound: internal/doctor/detectors/calibration_freshness_d_test.go:TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_BIOSDriftBlocker
Bound: internal/doctor/detectors/calibration_freshness_d_test.go:TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_OldButCurrentBIOSWarning
Bound: internal/doctor/detectors/calibration_freshness_d_test.go:TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_LoaderErrorWarns
Bound: internal/doctor/detectors/calibration_freshness_d_test.go:TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_NilLoaderNoOp
Bound: internal/doctor/detectors/calibration_freshness_d_test.go:TestRULE_DOCTOR_DETECTOR_CalibrationFreshness_RespectsContextCancel

## RULE-DOCTOR-DETECTOR-HWMONSWAP: Compares the (chip-name → hwmonN dir) map against the daemon-start baseline; emits Blocker per shifted chip.

Runtime watchdog for RULE-HWMON-INDEX-UNSTABLE. Baseline is captured
by the wiring layer at daemon start; the controller's cached PWM
paths reference the baseline-time dirs. Defensive copy at
construction prevents post-construction map mutation.

Bound: internal/doctor/detectors/hwmon_swap_d_test.go:TestRULE_DOCTOR_DETECTOR_HwmonSwap_NoChangeNoFacts
Bound: internal/doctor/detectors/hwmon_swap_d_test.go:TestRULE_DOCTOR_DETECTOR_HwmonSwap_IndexFlipBlocker
Bound: internal/doctor/detectors/hwmon_swap_d_test.go:TestRULE_DOCTOR_DETECTOR_HwmonSwap_DisappearedChipBlocker
Bound: internal/doctor/detectors/hwmon_swap_d_test.go:TestRULE_DOCTOR_DETECTOR_HwmonSwap_EmptyBaselineNoOp
Bound: internal/doctor/detectors/hwmon_swap_d_test.go:TestRULE_DOCTOR_DETECTOR_HwmonSwap_BaselineCopiedNotShared
Bound: internal/doctor/detectors/hwmon_swap_d_test.go:TestRULE_DOCTOR_DETECTOR_HwmonSwap_RespectsContextCancel
Bound: internal/doctor/detectors/hwmon_swap_d_test.go:TestTrimNL_TrailingWhitespace

## RULE-DOCTOR-DETECTOR-DMIFINGERPRINT: Reads DMI via hwdb.ReadDMI + hwdb.Fingerprint — same code paths the daemon uses (RULE-DOCTOR-05 catalog match parity).

Matched=true → OK Fact (informational); Matched=false → Warning
("not in catalog; running in generic mode"); ReadDMI error →
Warning. EntityHash includes the fingerprint hash so suppressions
stay stable across reboots.

Bound: internal/doctor/detectors/dmi_fingerprint_d_test.go:TestRULE_DOCTOR_DETECTOR_DMIFingerprint_MatchedYieldsOK
Bound: internal/doctor/detectors/dmi_fingerprint_d_test.go:TestRULE_DOCTOR_DETECTOR_DMIFingerprint_NoMatchYieldsWarning
Bound: internal/doctor/detectors/dmi_fingerprint_d_test.go:TestRULE_DOCTOR_DETECTOR_DMIFingerprint_EmptyFingerprintNoOp
Bound: internal/doctor/detectors/dmi_fingerprint_d_test.go:TestRULE_DOCTOR_DETECTOR_DMIFingerprint_RespectsContextCancel
Bound: internal/doctor/detectors/dmi_fingerprint_d_test.go:TestDMIFingerprint_HashStableAcrossInvocations

## RULE-DOCTOR-DETECTOR-PERMISSIONS: ventd user/group + /var/lib/ventd mode audit per RULE-INSTALL-01 + RULE-STATE-09; permission-denied gracefully degrades per RULE-DOCTOR-04.

Surfaces: missing user/group (Warning, sysusers.d drop-in re-creates
on next install), state-dir mode != 0755 (Warning, RULE-STATE-09),
state.yaml mode != 0640 (Warning), Stat permission-denied (Warning,
"rerun as root for full check"). Missing dir is benign per
RULE-STATE-10 first-boot.

Bound: internal/doctor/detectors/permissions_d_test.go:TestRULE_DOCTOR_DETECTOR_Permissions_HappyPathNoFacts
Bound: internal/doctor/detectors/permissions_d_test.go:TestRULE_DOCTOR_DETECTOR_Permissions_MissingUserSurfaces
Bound: internal/doctor/detectors/permissions_d_test.go:TestRULE_DOCTOR_DETECTOR_Permissions_MissingGroupSurfaces
Bound: internal/doctor/detectors/permissions_d_test.go:TestRULE_DOCTOR_DETECTOR_Permissions_DirModeDriftSurfaces
Bound: internal/doctor/detectors/permissions_d_test.go:TestRULE_DOCTOR_DETECTOR_Permissions_FileModeDriftSurfaces
Bound: internal/doctor/detectors/permissions_d_test.go:TestRULE_DOCTOR_DETECTOR_Permissions_MissingDirIsBenign
Bound: internal/doctor/detectors/permissions_d_test.go:TestRULE_DOCTOR_DETECTOR_Permissions_StatPermissionDeniedSurfaces
Bound: internal/doctor/detectors/permissions_d_test.go:TestRULE_DOCTOR_DETECTOR_Permissions_RespectsContextCancel
Bound: internal/doctor/detectors/permissions_d_test.go:TestLooksUpAccount_PassFormat

## RULE-DOCTOR-DETECTOR-GPUREADINESS: NVIDIA driver R<515 → Blocker (RULE-POLARITY-06); NVML lib missing → Warning; AMD card without fan iface → Warning.

Pure filesystem reads (no NVML/amdgpu library deps in the detector
itself). No-GPU systems emit zero facts. Tolerates malformed
/proc/driver/nvidia/version content via 0-return parser.

Bound: internal/doctor/detectors/gpu_readiness_d_test.go:TestRULE_DOCTOR_DETECTOR_GPUReadiness_NoGPUNoFacts
Bound: internal/doctor/detectors/gpu_readiness_d_test.go:TestRULE_DOCTOR_DETECTOR_GPUReadiness_OldNVIDIASurfacesAsBlocker
Bound: internal/doctor/detectors/gpu_readiness_d_test.go:TestRULE_DOCTOR_DETECTOR_GPUReadiness_NewNVIDIANoFacts
Bound: internal/doctor/detectors/gpu_readiness_d_test.go:TestRULE_DOCTOR_DETECTOR_GPUReadiness_NVMLLibMissingWarning
Bound: internal/doctor/detectors/gpu_readiness_d_test.go:TestRULE_DOCTOR_DETECTOR_GPUReadiness_AMDGPUWithoutFanInterfaceWarns
Bound: internal/doctor/detectors/gpu_readiness_d_test.go:TestRULE_DOCTOR_DETECTOR_GPUReadiness_AMDGPUWithPWMNoFacts
Bound: internal/doctor/detectors/gpu_readiness_d_test.go:TestRULE_DOCTOR_DETECTOR_GPUReadiness_RespectsContextCancel
Bound: internal/doctor/detectors/gpu_readiness_d_test.go:TestParseNvidiaDriverMajor_Variants

## RULE-DOCTOR-DETECTOR-KERNELUPDATE: Compares running kernel vs persisted last-seen baseline; warns on transition (correlates with cold-start re-warmup).

Pure read per RULE-DOCTOR-01 — the wiring layer persists
last_kernel after a clean RunOnce with no Blockers; the detector
just reads it. Empty baseline = first daemon run = no-op.
Unreadable /proc gracefully degrades.

Bound: internal/doctor/detectors/kernel_update_d_test.go:TestRULE_DOCTOR_DETECTOR_KernelUpdate_SameKernelNoFact
Bound: internal/doctor/detectors/kernel_update_d_test.go:TestRULE_DOCTOR_DETECTOR_KernelUpdate_NewKernelWarning
Bound: internal/doctor/detectors/kernel_update_d_test.go:TestRULE_DOCTOR_DETECTOR_KernelUpdate_FirstRunNoOp
Bound: internal/doctor/detectors/kernel_update_d_test.go:TestRULE_DOCTOR_DETECTOR_KernelUpdate_UnreadableProcNoFact
Bound: internal/doctor/detectors/kernel_update_d_test.go:TestRULE_DOCTOR_DETECTOR_KernelUpdate_RespectsContextCancel
Bound: internal/doctor/detectors/kernel_update_d_test.go:TestKernelUpdate_EntityHashChangesAcrossTransitions

## RULE-DOCTOR-DETECTOR-ECLOCKEDLAPTOP: EC-locked laptops with platform_profile but no controllable channels surface as OK-severity (informational), naming current value + choices and pointing at issue #872 (v0.6 platform_profile selector mode).

Common on consumer HP / Dell / Lenovo / ASUS laptops where the embedded
controller owns fan actuation entirely — userspace gets the ACPI
`platform_profile` enum (low-power / balanced / performance) but no
`pwm*` duty-cycle write file and no `fan*_input` tach. The probe
correctly classifies these as `monitor_only` per RULE-PROBE-04, but
without a doctor card the operator gets no diagnostic — empty
dashboard with no path forward.

Fires when ALL of:
- `/sys/firmware/acpi/platform_profile` exists with non-empty value;
- `/sys/firmware/acpi/platform_profile_choices` lists ≥ 2 enum values;
- The probe's controllable-channel count is zero.

Quiet when any condition fails:
- Desktops have controllable channels → smart_mode owns the surface.
- Servers / embedded hosts without platform_profile → other detectors handle the monitor-only case.
- Single-value `platform_profile_choices` is degenerate; surfacing a
  card would promise control the hardware can't deliver.

Severity: OK. The hardware works as designed; this is informational,
not a warning (mirrors `experimental_flags`'s "surface for visibility,
not for dismissal" pattern). Detail names the active profile +
available choices and points at issue #872 (v0.6 platform_profile
selector mode) so operators on EC-locked hardware know follow-up work
is scoped.

EntityHash includes the joined choices string so HP-style 3-choice and
Dell-style 4-choice enums hash to distinct keys — operators on each
platform can suppress independently.

Bound: internal/doctor/detectors/ec_locked_laptop_d_test.go:TestRULE_DOCTOR_DETECTOR_ECLockedLaptop_HPPavilionPatternEmitsInfo
Bound: internal/doctor/detectors/ec_locked_laptop_d_test.go:TestRULE_DOCTOR_DETECTOR_ECLockedLaptop_DesktopWithChannelsNoFact
Bound: internal/doctor/detectors/ec_locked_laptop_d_test.go:TestRULE_DOCTOR_DETECTOR_ECLockedLaptop_NoPlatformProfileNoFact
Bound: internal/doctor/detectors/ec_locked_laptop_d_test.go:TestRULE_DOCTOR_DETECTOR_ECLockedLaptop_SingleChoiceEnumNoFact
Bound: internal/doctor/detectors/ec_locked_laptop_d_test.go:TestRULE_DOCTOR_DETECTOR_ECLockedLaptop_RespectsContextCancel
Bound: internal/doctor/detectors/ec_locked_laptop_d_test.go:TestECLockedLaptop_EntityHashStableAcrossProbes
Bound: internal/doctor/detectors/ec_locked_laptop_d_test.go:TestECLockedLaptop_EntityHashDistinguishesChoicesShape
