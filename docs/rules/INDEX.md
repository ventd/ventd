# Rule Index

Canonical map of `docs/rules/*.md`.
Read this file first; open a specific rule file only when the full text is needed.
Regenerate with: `go run ./tools/rule-index`

---

## RULE-SETUP

| File | Bound subtest | Summary |
|------|---------------|---------|
| RULE-SETUP-REPROBE-01.md | TestReProber_FiresAfterLoadModule | Setup Manager re-runs the daemon-level probe after a successful driver install or kernel-module load. |

## RULE-WIZARD

| File | Bound subtest | Summary |
|------|---------------|---------|
| wizard-gate-calibrate-acoustic.md | TestRULE_WIZARD_GATE_CALIBRATE_ACOUSTIC_01 | calibrate_acoustic PhaseGate is opt-in, non-fatal, and cleans up its own temp files. |

## Free-form files

| File | Summary |
|------|---------|
| INDEX.md | Rule Index |
| RULE-HAL-LEGION.md | HAL legion backend rules — platform_profile + powermode state-switcher |
| RULE-HAL-LENOVO-IDEAPAD.md | HAL lenovoideapad backend rules — platform_profile state-switcher for IdeaPad-class hosts |
| RULE-HAL-THINKPAD.md | HAL thinkpad backend rules — /proc/acpi/ibm/fan procfs surface |
| RULE-NBFC-A.md | NBFC catalog + matcher + doctor card rules — spec-09 PR A |
| RULE-NBFC-B1.md | NBFC EC transport rules — spec-09 PR B1 |
| RULE-NBFC-B2.md | NBFC HAL backend rules — spec-09 PR B2 |
| RULE-NBFC-B3.md | NBFC ACPI bridge rules — spec-09 PR B3 |
| acoustic-capture.md | Acoustic capture — R30 mic-calibration primitives |
| acoustic-proxy.md | Acoustic proxy — R33 no-mic loudness estimator |
| acoustic-stall.md | Acoustic stall detector — R31 (advisory only) |
| blended-controller.md | Blended IMC-PI controller rules — v0.5.9 PR-A.3 |
| calibrate-persist.md | Calibration Result Persistence Rules |
| calibration-safety.md | Calibration Safety Rules |
| calibration.md | Calibration validity-probe rules (PR-2b) |
| ci-action-pinning.md | CI Action Pinning Invariants |
| confidence-aggregator.md | Confidence aggregator rules — v0.5.9 PR-A.2 |
| confidence-layer-a.md | Layer-A confidence (`conf_A`) rules — v0.5.9 PR-A sub-component |
| coupling.md | Layer-B thermal coupling rules (v0.5.7) |
| diag.md | Diagnostic-bundle rules (PR-2c) |
| doctor.md | Doctor rules — v0.5.10 |
| envelope.md | Envelope probing rules |
| experimental.md | Experimental-feature flag rules |
| go-conventions.md | Go Conventions — ventd |
| gpu.md | GPU HAL backend rules (spec-03 PR 2d) |
| hal-contract.md | HAL Backend Contract |
| hidraw-safety.md | hidraw-safety — invariant bindings for pure-Go Linux hidraw substrate |
| hwdb-schema.md | HWDB schema invariants |
| hwdb.md | Hardware database rules (PR-2 schema, fingerprint matcher, capture, overrides) |
| hwmon-safety.md | Hardware Safety Rules |
| hwmon-sentinel.md | hwmon Sentinel Acceptance Invariants |
| idle.md | Idle-gate rules |
| install-contract.md | Install Contract Rules |
| install-pipeline.md | Controlled install pipeline rules — v0.5.9 PR-D |
| iox.md | iox atomic-write helper rules — v0.5.11 R28 Stage 1.5 PR-3 |
| ipmi-safety.md | IPMI Safety Rules |
| liquid-safety.md | liquid-safety — invariant bindings for the Corsair AIO backend |
| marginal.md | Layer-C marginal-benefit rules (v0.5.8) |
| modprobe-options-write.md | Modprobe-options-write endpoint rules — v0.5.11 R28 Stage 1 |
| nvml-helper.md | NVML helper rules — SUID-root write-helper for unprivileged ventd |
| observation.md | Observation Log Rules |
| opportunistic.md | Opportunistic active probing rules (v0.5.5) |
| polarity.md | Polarity probe + write-path rules |
| preflight-comprehensive.md | Comprehensive preflight rules — v0.5.9 PR-D |
| preflight-orchestrator.md | Preflight orchestrator rules — v0.5.11 |
| probe.md | Daemon-startup probe rules |
| setup.md | Setup wizard rules |
| signature.md | Workload signature learning rules (v0.5.6) |
| signguard.md | Sign-guard rules (v0.5.8) |
| smart-mode-wiring-1035.md | Smart-mode pipeline wiring rules — issue #1035 |
| smart-preset.md | Smart-mode preset config rules — v0.5.9 PR-A.4 |
| state.md | State store rules |
| sysclass.md | System-class detection rules |
| ui.md | Web UI Invariants (spec-12) |
| usability.md | Usability — Universal Linux Compatibility |
| watchdog-safety.md | Watchdog Safety Rules |
| web-ui.md | Web UI Rules |
| wizard-gates.md | Wizard PhaseGate machinery rules — v0.5.9 PR-D |
| wizard-recovery.md | Wizard recovery classifier rules — v0.5.9 PR-C (#800) |

