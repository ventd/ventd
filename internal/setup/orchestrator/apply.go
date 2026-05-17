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
// First-cut monitor-only contract (PR#B3): consumes ProbeArtifact +
// PolarityArtifact to build a minimal config with Fans listed but no
// Controls + no Curves. The daemon loads this and operates in
// monitor-only mode (no PWM writes) until the operator adds Curves
// + Controls via the web UI or the next-PR CalibratePhase produces
// a fuller config.
//
// Fans with polarity == "phantom" are excluded (they're not safely
// writable). Fans with polarity == "unknown" are included but
// flagged in the artifact — the daemon's polarity-aware WritePWM
// refuses to write to "unknown" channels until polarity is resolved.
type ApplyPhase struct {
	// ConfigPath overrides DefaultConfigPath. Used by tests to write
	// to t.TempDir().
	ConfigPath string
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

	cfg := buildMonitorOnlyConfig(probeArt, polByPath)
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

// buildMonitorOnlyConfig produces the minimum config that the daemon
// can load. Fans are listed (so they show up in the dashboard) but
// no Curves / Controls are written — operators add those via the
// web UI when they're ready to enable active control.
//
// Phantom fans are excluded because they have no usable PWM surface.
func buildMonitorOnlyConfig(probeArt ProbeArtifact, polByPath map[string]string) *config.Config {
	cfg := &config.Config{
		Version:      1,
		PollInterval: config.Duration{Duration: 2 * time.Second},
		Web: config.Web{
			Listen:     "0.0.0.0:9999",
			SessionTTL: config.Duration{Duration: 24 * time.Hour},
		},
	}

	for _, fan := range probeArt.Fans {
		polarity := polByPath[fan.PWMPath]
		if polarity == "phantom" {
			continue
		}
		cfg.Fans = append(cfg.Fans, config.Fan{
			Name:     fan.LabelHint,
			Type:     "hwmon",
			PWMPath:  fan.PWMPath,
			RPMPath:  fan.RPMPath,
			ChipName: fan.ChipName,
			MinPWM:   80, // safe default; controller refuses 0 unless AllowStop
			MaxPWM:   255,
		})
	}

	return cfg
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
