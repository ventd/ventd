package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/recovery"
)

// DefaultConfigPath is the production location for ventd's config.
// ApplyPhase writes here unless an operator-supplied path override
// is wired in via the bridge.
const DefaultConfigPath = "/etc/ventd/config.yaml"

// ApplyArtifact is the structured result of the ApplyPhase. Carries
// the path the config was written to and a snapshot of what was
// configured so the wizard UI can render a "what just happened" panel.
type ApplyArtifact struct {
	ConfigPath  string `json:"config_path"`
	Fans        int    `json:"fans"`
	MonitorOnly bool   `json:"monitor_only"`

	// MonitorReason is a short machine-readable explanation of why
	// the apply landed in monitor-only mode despite the wizard's
	// initial_outcome being control_mode. Empty in the happy case
	// (active control achieved). The wizard UI surfaces this on
	// the "Wizard complete" screen so an operator who expected
	// active control isn't left guessing.
	//
	// Known values:
	//   no_cpu_sensor       — DiscoverCPUSensor returned no path
	//   no_admitted_fans    — every probed fan was excluded
	//                         (phantom polarity / calibrate Phantom /
	//                         calibration skipped / probe found zero)
	//   no_controls_built   — guard token for a buildConfig regression
	//                         that produced Fans without Controls
	MonitorReason string `json:"monitor_reason,omitempty"`

	// EnableRestored counts how many excluded channels had their
	// pwm<N>_enable restored to the probe-time value. Surfaced for
	// the doctor page so operators can see whether the cleanup ran.
	EnableRestored int `json:"enable_restored,omitempty"`
}

// ApplyPhase writes the daemon's config.yaml from prior phases'
// artifacts. This is the orchestrator's terminal phase — once
// ApplyPhase succeeds the wizard is done and the daemon can take
// control on next reload.
//
// Full-control contract: consumes ProbeArtifact + PolarityArtifact +
// CalibrateArtifact + a discovered CPU sensor to build a working
// ACTIVE-CONTROL config with Sensors + Fans + Curves + Controls. The
// daemon loads this and starts driving fans immediately — Goal 4 of
// the rework is met the moment Apply succeeds.
//
// Fallback to monitor-only: when no CPU sensor is discoverable, or
// when every probed fan is excluded (phantom polarity / phantom from
// calibrate's sustained-spin check), ApplyPhase writes a config with
// Fans listed but no Controls. The daemon still loads it and reports
// temps/RPMs in the dashboard; operators add curves later via the
// web UI.
//
// Fan exclusion rules:
//   - polarity == "phantom"          → exclude (no PWM surface)
//   - calibrate Phantom              → exclude (sustained-spin check
//     saw zero RPM AND the sweep
//     itself measured MaxRPM == 0)
//   - calibration skipped/failed     → include with safe defaults
//   - polarity == "unknown"          → include; daemon's polarity-
//     aware WritePWM refuses to
//     drive until polarity resolves
type ApplyPhase struct {
	// ConfigPath overrides DefaultConfigPath. Used by tests to write
	// to t.TempDir().
	ConfigPath string

	// HwmonRoot overrides "/sys/class/hwmon" for sensor discovery.
	// Tests inject a fixture root.
	HwmonRoot string
}

// Name identifies this phase in the checkpoint store and the wizard UI.
func (ApplyPhase) Name() string { return "apply" }

// Execute reads prior artifacts and writes config.yaml atomically.
func (p ApplyPhase) Execute(_ context.Context, rc *RunContext) Outcome {
	path := p.ConfigPath
	if path == "" {
		path = DefaultConfigPath
	}

	probeArt, err := loadProbeArtifact(rc)
	if err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "load probe artifact: " + err.Error(),
		}
	}

	polarityArt, polErr := loadPolarityArtifact(rc)
	// polarity is best-effort — if it didn't run, treat every fan as
	// "unknown" polarity and let the controller's WritePWM refuse
	// writes safely. The wizard will still write a usable config.
	if polErr != nil {
		rc.Log().Warn("apply: no polarity artifact, defaulting all fans to unknown polarity",
			"err", polErr)
		polarityArt = PolarityArtifact{}
	}
	polByPath := make(map[string]string, len(polarityArt.Results))
	for _, r := range polarityArt.Results {
		polByPath[r.PWMPath] = r.Polarity
	}

	// CalibrateArtifact is best-effort: missing/failed → use safe
	// heuristic PWM bounds for every fan rather than failing apply.
	// CalibrateFanResult.Phantom (set by the sweep's sustained-spin
	// check) now carries what loadVerifyArtifact previously did — a
	// fan flagged Phantom is excluded from the applied config.
	calByPath := map[string]CalibrateFanResult{}
	if calArt, calErr := loadCalibrateArtifact(rc); calErr == nil {
		for _, r := range calArt.Results {
			calByPath[r.PWMPath] = r
		}
	}

	// RPMDetectArtifact is best-effort: missing → ApplyPhase uses
	// ProbeArtifact's same-index pairing without overrides.
	rpmOverrides := map[string]string{}
	if rpmArt, rpmErr := loadRPMDetectArtifact(rc); rpmErr == nil {
		for _, r := range rpmArt.Results {
			if r.Improved && r.ResolvedRPM != "" {
				rpmOverrides[r.PWMPath] = r.ResolvedRPM
			}
		}
	}

	// NVMLArtifact is best-effort: missing or empty → no NVIDIA
	// fans in the config (non-NVIDIA host or no GPU fans visible).
	var nvmlFans []NVMLGPUFan
	if nvmlArt, nvmlErr := loadNVMLArtifact(rc); nvmlErr == nil {
		nvmlFans = nvmlArt.Fans
	}

	// Sensor discovery: a working active-control config needs at
	// least one temperature source. No sensor → fall back to
	// monitor-only (Controls remains empty).
	hwmonRoot := p.HwmonRoot
	if hwmonRoot == "" {
		hwmonRoot = rc.HwmonRoot
	}
	if hwmonRoot == "" {
		hwmonRoot = "/sys/class/hwmon"
	}
	cpuSensor := DiscoverCPUSensor(hwmonRoot)

	cfg := buildConfig(probeArt, polByPath, calByPath, rpmOverrides, nvmlFans, cpuSensor)
	monitorOnly := len(cfg.Controls) == 0

	// Diagnose the monitor-only path so the artifact carries a
	// reason field the UI + doctor can surface. The two distinct
	// causes have very different operator implications: missing
	// CPU sensor is a hwmon discovery gap; no admitted fans is
	// usually a calibrate-phantom cascade (every probed fan failed
	// both the sweep and the sustained-spin check).
	monitorReason := ""
	if monitorOnly {
		switch {
		case cpuSensor.Path == "":
			monitorReason = "no_cpu_sensor"
		case len(cfg.Fans) == 0:
			monitorReason = "no_admitted_fans"
		default:
			// cfg.Fans>0 but cfg.Controls==0 — shouldn't happen
			// with the current buildConfig (Controls always
			// follows Fans when both prerequisites are met), but
			// be explicit so a future regression surfaces a
			// recognisable token rather than empty.
			monitorReason = "no_controls_built"
		}
	}

	if err := writeConfigAtomic(path, cfg); err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "write config: " + err.Error(),
		}
	}

	// Restore pwm<N>_enable on every channel that's NOT in the
	// applied config — phantom-classified, calibrate-phantom, and
	// (when we demoted to monitor-only) every probed channel.
	// Without this, the wizard would leave channels at pwm_enable=1
	// (manual) with whatever PWM the calibrate sweep left last, and
	// neither ventd (monitor-only) nor BIOS (manual mode active)
	// would drive the fan under load — a thermal safety regression
	// observed on Dell SMM after the calibrate→apply cascade.
	includedPWM := map[string]bool{}
	for _, f := range cfg.Fans {
		includedPWM[f.PWMPath] = true
	}
	enableRestored := 0
	for _, fan := range probeArt.Fans {
		// Monitor-only demotion: restore every probed channel,
		// regardless of whether it would have been included. The
		// daemon won't drive any of them so firmware control is
		// the safe default for all.
		if !monitorOnly && includedPWM[fan.PWMPath] {
			continue
		}
		if fan.EnablePath == "" || fan.InitialEnable == 0 {
			continue
		}
		if err := os.WriteFile(fan.EnablePath, []byte(strconv.Itoa(int(fan.InitialEnable))), 0o644); err != nil {
			rc.Log().Warn("apply: restore pwm_enable failed",
				"enable_path", fan.EnablePath,
				"target", fan.InitialEnable,
				"err", err)
			continue
		}
		enableRestored++
	}

	art := ApplyArtifact{
		ConfigPath:     path,
		Fans:           len(cfg.Fans),
		MonitorOnly:    monitorOnly,
		MonitorReason:  monitorReason,
		EnableRestored: enableRestored,
	}
	raw, _ := EncodeArtifact(art)

	rc.Sink().Emit("info", "apply",
		fmt.Sprintf("config written to %s (%d fan(s); monitor-only=%v)",
			path, art.Fans, art.MonitorOnly))
	if monitorOnly {
		rc.Log().Warn("apply complete: monitor-only fallback",
			"path", path,
			"fans", art.Fans,
			"reason", monitorReason,
			"pwm_enable_restored", enableRestored)
	} else {
		rc.Log().Info("apply complete",
			"path", path,
			"fans", art.Fans,
			"monitor_only", art.MonitorOnly,
			"pwm_enable_restored", enableRestored)
	}

	return Outcome{Status: StatusSuccess, Artifact: raw}
}

// buildConfig assembles the daemon-loadable config from every prior
// phase's artifact. When all prerequisites are met (probe fans,
// polarity resolved, calibration data, CPU sensor discovered), the
// resulting config has Sensors + Fans + Curves + Controls and the
// daemon starts active control on next reload — meeting Goal 4 of
// the rework.
//
// Degraded paths:
//   - No CPU sensor              → no Curves/Controls (monitor-only)
//   - Calibration missing        → fan included with safe heuristic PWM
//     bounds (StartPWM=80, MaxPWM=255), curve still wired
//   - Polarity phantom OR        → fan excluded entirely
//     calibrate-flagged Phantom
//     (sustained-spin check)
func buildConfig(
	probeArt ProbeArtifact,
	polByPath map[string]string,
	calByPath map[string]CalibrateFanResult,
	rpmOverrides map[string]string,
	nvmlFans []NVMLGPUFan,
	cpuSensor DiscoveredSensor,
) *config.Config {
	cfg := &config.Config{
		Version:      1,
		PollInterval: config.Duration{Duration: 2 * time.Second},
		Web: config.Web{
			Listen:     "0.0.0.0:9999",
			SessionTTL: config.Duration{Duration: 24 * time.Hour},
		},
	}

	if cpuSensor.Path != "" {
		cfg.Sensors = append(cfg.Sensors, config.Sensor{
			Name:        "cpu_temp",
			Type:        "hwmon",
			Path:        cpuSensor.Path,
			ChipName:    cpuSensor.ChipName,
			HwmonDevice: resolveStableHwmonDevice(cpuSensor.Path),
		})
	}

	for _, fan := range probeArt.Fans {
		if polByPath[fan.PWMPath] == "phantom" {
			continue
		}
		if cal, ok := calByPath[fan.PWMPath]; ok && cal.Phantom {
			continue
		}

		minPWM := uint8(80)
		if cal, ok := calByPath[fan.PWMPath]; ok && cal.StartPWM > 0 {
			minPWM = cal.StartPWM
		}
		isPump := false
		if cal, ok := calByPath[fan.PWMPath]; ok {
			isPump = cal.IsPump
		}

		// Prefer RPMDetectPhase's detected path when ProbePhase
		// couldn't pair a same-index tach (split-chip fan/tach
		// configurations on some AMD boards).
		rpmPath := fan.RPMPath
		if rpmPath == "" {
			if override, ok := rpmOverrides[fan.PWMPath]; ok {
				rpmPath = override
			}
		}

		cfg.Fans = append(cfg.Fans, config.Fan{
			Name:        fan.LabelHint,
			Type:        "hwmon",
			PWMPath:     fan.PWMPath,
			RPMPath:     rpmPath,
			ChipName:    fan.ChipName,
			HwmonDevice: resolveStableHwmonDevice(fan.PWMPath),
			MinPWM:      minPWM,
			MaxPWM:      255,
			IsPump:      isPump,
		})
	}

	// Append NVIDIA GPU fans (PWMPath is the GPU index encoded as
	// a string; the daemon's nvidia HAL backend uses it to address
	// the right GPU via NVML).
	for _, gf := range nvmlFans {
		cfg.Fans = append(cfg.Fans, config.Fan{
			Name:    gf.Label,
			Type:    "nvidia",
			PWMPath: fmt.Sprintf("%d", gf.Index),
			MinPWM:  30,  // GPU fans default min is safer than CPU fans (avoid stall)
			MaxPWM:  100, // NVML uses percent, not byte
		})
	}

	// Active control only when we have BOTH a sensor and at least
	// one fan. Otherwise stay monitor-only.
	if cpuSensor.Path == "" || len(cfg.Fans) == 0 {
		return cfg
	}

	// Default curve: hardware-aware. The chip's reported tempN_crit
	// (TjMax — Intel/AMD shutdown threshold) caps the curve's ramp
	// so fans hit 100% with safe thermal headroom. Falls back to a
	// conservative 95°C ceiling when the chip doesn't report it
	// (rare; coretemp / k10temp / k8temp all populate _crit).
	//
	// Curve shape: linear from MinTemp (idle baseline) → 20% to
	// MaxTemp (TjMax - 10°C safety margin) → 100%. Operators reshape
	// in the web UI; this is the "works on day one" default.
	maxTemp := cpuSensor.CritC - 10
	if maxTemp <= 0 {
		maxTemp = 95 // fallback when _crit isn't reported
	}
	minTemp := 40.0 // idle baseline for most CPUs; refined per-chip in a future PR
	midTemp := (minTemp + maxTemp) / 2
	cfg.Curves = []config.CurveConfig{
		{
			Name:    "default",
			Type:    "linear",
			Sensor:  "cpu_temp",
			MinTemp: minTemp,
			MaxTemp: maxTemp,
			Points: []config.CurvePoint{
				{Temp: minTemp, PWMPct: ptrU8(20)},
				{Temp: midTemp, PWMPct: ptrU8(50)},
				{Temp: maxTemp, PWMPct: ptrU8(100)},
			},
		},
	}

	for _, f := range cfg.Fans {
		cfg.Controls = append(cfg.Controls, config.Control{
			Fan:   f.Name,
			Curve: "default",
		})
	}

	return cfg
}

func ptrU8(v uint8) *uint8 { return &v }

// resolveStableHwmonDevice returns the canonical /sys/devices/... path
// for the hwmon chip whose pwm/temp file lives at sysfsPath. The result
// is the chip dir's `device` symlink resolved via EvalSymlinks. Used by
// config.ResolveHwmonPaths to disambiguate between two hwmonN entries
// that share a chip_name — common on boards where the in-kernel
// nct6683 and the OOT nct6687 both bind and both report `name=nct6687`.
//
// Best-effort: any failure (synthetic test path, virtual chip without
// a `device` symlink such as acpitz, partially-enumerated sysfs at cold
// boot) returns "". The daemon's resolver treats empty HwmonDevice as
// "fall back to chip_name alone", which is the pre-disambiguation
// behaviour — strictly weaker than failing the write.
var resolveStableHwmonDevice = func(sysfsPath string) string {
	if sysfsPath == "" {
		return ""
	}
	target, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(sysfsPath), "device"))
	if err != nil {
		return ""
	}
	return target
}

// writeConfigAtomic marshals cfg as YAML and writes to path via a
// tmp+fsync+rename so a crash mid-write never leaves a half-written
// file the daemon's loader would reject.
func writeConfigAtomic(path string, cfg *config.Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", tmp, err)
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// loadPolarityArtifact reads the PolarityPhase's checkpoint. Returns
// an error if the phase didn't run. The error is recoverable —
// ApplyPhase falls back to "unknown polarity for all fans" rather
// than failing.
func loadPolarityArtifact(rc *RunContext) (PolarityArtifact, error) {
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		return PolarityArtifact{}, err
	}
	prior, ok := state.Outcomes[(PolarityPhase{}).Name()]
	if !ok {
		return PolarityArtifact{}, errors.New("PolarityPhase has not run")
	}
	if prior.Status != StatusSuccess && prior.Status != StatusSkipped {
		return PolarityArtifact{}, fmt.Errorf(
			"PolarityPhase did not succeed (status=%q)", prior.Status)
	}
	if len(prior.Artifact) == 0 {
		return PolarityArtifact{}, nil // skipped → empty results, not an error
	}
	var art PolarityArtifact
	if err := json.Unmarshal(prior.Artifact, &art); err != nil {
		return PolarityArtifact{}, fmt.Errorf("decode PolarityArtifact: %w", err)
	}
	return art, nil
}
