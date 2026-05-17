package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
}

// ApplyPhase writes the daemon's config.yaml from prior phases'
// artifacts. This is the orchestrator's terminal phase — once
// ApplyPhase succeeds the wizard is done and the daemon can take
// control on next reload.
//
// Full-control contract (PR#B4): consumes ProbeArtifact +
// PolarityArtifact + CalibrateArtifact + VerifyArtifact + a
// discovered CPU sensor to build a working ACTIVE-CONTROL config
// with Sensors + Fans + Curves + Controls. The daemon loads this
// and starts driving fans immediately — Goal 4 of the rework is met
// the moment Apply succeeds.
//
// Fallback to monitor-only: when no CPU sensor is discoverable, or
// when CalibrateArtifact is missing entirely, ApplyPhase writes a
// config with Fans listed but no Controls. The daemon still loads
// it and reports temps/RPMs in the dashboard; operators add curves
// later via the web UI.
//
// Fan exclusion rules:
//   - polarity == "phantom"                  → exclude (no PWM surface)
//   - verify reclassified as phantom         → exclude (post-cal proof)
//   - calibration skipped/failed             → include with safe defaults
//   - polarity == "unknown"                  → include; daemon's polarity-
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
	calByPath := map[string]CalibrateFanResult{}
	if calArt, calErr := loadCalibrateArtifact(rc); calErr == nil {
		for _, r := range calArt.Results {
			calByPath[r.PWMPath] = r
		}
	}

	// VerifyArtifact is best-effort: missing → no fans are
	// reclassified as phantom by verify.
	verifyPhantom := map[string]bool{}
	if verifyArt, verifyErr := loadVerifyArtifact(rc); verifyErr == nil {
		for _, r := range verifyArt.Results {
			if r.Phantom {
				verifyPhantom[r.PWMPath] = true
			}
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

	cfg := buildConfig(probeArt, polByPath, calByPath, verifyPhantom, rpmOverrides, nvmlFans, cpuSensor)
	monitorOnly := len(cfg.Controls) == 0

	if err := writeConfigAtomic(path, cfg); err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "write config: " + err.Error(),
		}
	}

	art := ApplyArtifact{
		ConfigPath:  path,
		Fans:        len(cfg.Fans),
		MonitorOnly: monitorOnly,
	}
	raw, _ := EncodeArtifact(art)

	rc.Sink().Emit("info", "apply",
		fmt.Sprintf("config written to %s (%d fan(s); monitor-only=%v)",
			path, art.Fans, art.MonitorOnly))
	rc.Log().Info("apply complete",
		"path", path, "fans", art.Fans, "monitor_only", art.MonitorOnly)

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
//   - No CPU sensor          → no Curves/Controls (monitor-only)
//   - Calibration missing    → fan included with safe heuristic PWM bounds
//     (StartPWM=80, MaxPWM=255), curve still wired
//   - Polarity phantom OR    → fan excluded entirely
//     verify phantom
func buildConfig(
	probeArt ProbeArtifact,
	polByPath map[string]string,
	calByPath map[string]CalibrateFanResult,
	verifyPhantom map[string]bool,
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
			Name:     "cpu_temp",
			Type:     "hwmon",
			Path:     cpuSensor.Path,
			ChipName: cpuSensor.ChipName,
		})
	}

	for _, fan := range probeArt.Fans {
		if polByPath[fan.PWMPath] == "phantom" || verifyPhantom[fan.PWMPath] {
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
			Name:     fan.LabelHint,
			Type:     "hwmon",
			PWMPath:  fan.PWMPath,
			RPMPath:  rpmPath,
			ChipName: fan.ChipName,
			MinPWM:   minPWM,
			MaxPWM:   255,
			IsPump:   isPump,
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
