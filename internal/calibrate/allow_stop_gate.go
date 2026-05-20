// Copyright the ventd authors.
// SPDX-License-Identifier: GPL-3.0-or-later

package calibrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ventd/ventd/internal/config"
)

// ErrAllowStopNoCalibration is returned by VerifyAllowStopGate when a
// fan with allow_stop=true has no stall-calibration backing it: the
// runtime needs both StartPWM (lowest PWM that spins from standstill)
// and StopPWM (lowest PWM that keeps a spinning fan spinning) measured
// before PWM=0 writes are safe. Issue #600 elevated this from an
// implicit assumption to an explicit refusal — without calibration
// data, allow_stop=true is asking the daemon to write PWM=0 on hope.
var ErrAllowStopNoCalibration = errors.New("calibrate: allow_stop=true requires stall calibration (pwm_min_start + pwm_min_running) measured first")

// VerifyAllowStopGate inspects every fan with AllowStop=true and refuses
// to start the daemon unless the persisted calibration record for that
// fan carries both StartPWM>0 and StopPWM>0 (i.e. the sweep observed
// the spin-up and stall PWM thresholds). Hand-edited YAMLs that flip
// allow_stop=true without running the wizard first will fail here with
// a per-fan error pointing at the missing measurement.
//
// Pre-calibration (calPath missing) returns nil if no fan has
// AllowStop=true — fresh installs with the default wizard config
// (MinPWM=80, AllowStop=false) pass vacuously. A missing calibration
// file with AllowStop=true fans is treated as a refusal because the
// user has asked for a behaviour the daemon can't verify is safe.
//
// nvidia fans are skipped — they have no PWM=0 surface (NVML's percent
// floor is non-zero by API contract) and their calibration records are
// PWM-readback proxies, not RPM measurements.
//
// (#600, RULE-CAL-ALLOW-STOP-GATED)
func VerifyAllowStopGate(cfg *config.Config, calPath string) error {
	if cfg == nil {
		return nil
	}
	// Fast-path: no AllowStop fans → no gate check needed at all.
	anyAllowStop := false
	for _, f := range cfg.Fans {
		if f.AllowStop && f.Type != "nvidia" {
			anyAllowStop = true
			break
		}
	}
	if !anyAllowStop {
		return nil
	}

	records, err := loadAllowStopRecords(calPath)
	if err != nil {
		return fmt.Errorf("%w: calibration file %q unreadable: %v",
			ErrAllowStopNoCalibration, calPath, err)
	}

	for _, f := range cfg.Fans {
		if !f.AllowStop || f.Type == "nvidia" {
			continue
		}
		rec, ok := records[f.PWMPath]
		if !ok {
			return fmt.Errorf("%w: fan %q (pwm_path=%s) — no calibration record. Run the setup wizard or `ventd --calibrate-probe` before enabling allow_stop, or raise min_pwm above 0",
				ErrAllowStopNoCalibration, f.Name, f.PWMPath)
		}
		if rec.StartPWM == 0 {
			return fmt.Errorf("%w: fan %q (pwm_path=%s) — calibration recorded no pwm_min_start (sweep never observed the fan spinning up from standstill). Recalibrate before enabling allow_stop",
				ErrAllowStopNoCalibration, f.Name, f.PWMPath)
		}
		if rec.StopPWM == 0 {
			return fmt.Errorf("%w: fan %q (pwm_path=%s) — calibration recorded no pwm_min_running (sweep never observed the fan stalling). Without this, the daemon cannot verify PWM=0 is a safe stop signal; recalibrate before enabling allow_stop",
				ErrAllowStopNoCalibration, f.Name, f.PWMPath)
		}
	}
	return nil
}

// loadAllowStopRecords reads only the fields VerifyAllowStopGate needs
// from the calibration envelope. Avoids importing the full Manager
// machinery so the gate works when calibrate is built without its
// HAL/watchdog dependencies (e.g. a future cfg-validator subcommand).
func loadAllowStopRecords(path string) (map[string]Result, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("calibration file not found")
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	// Probe envelope shape: v3+ wraps results under "results"; legacy
	// is a bare map keyed by pwm_path.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("envelope parse: %w", err)
	}
	if _, hasSchema := probe["schema_version"]; !hasSchema {
		var bare map[string]Result
		if err := json.Unmarshal(data, &bare); err != nil {
			return nil, fmt.Errorf("legacy parse: %w", err)
		}
		return bare, nil
	}
	var env struct {
		Results map[string]Result `json:"results"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("v3 parse: %w", err)
	}
	if env.Results == nil {
		env.Results = map[string]Result{}
	}
	return env.Results, nil
}
