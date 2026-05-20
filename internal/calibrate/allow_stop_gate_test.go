// Copyright the ventd authors.
// SPDX-License-Identifier: GPL-3.0-or-later

package calibrate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// TestVerifyAllowStopGate covers the #600 safety gate: allow_stop=true
// requires both StartPWM>0 and StopPWM>0 in the persisted calibration
// record. Cases:
//
//  1. No AllowStop fans → pass without reading the file.
//  2. AllowStop=true but no calibration file → refused.
//  3. AllowStop=true with no record for this PWMPath → refused.
//  4. AllowStop=true with record missing StartPWM → refused (clear message).
//  5. AllowStop=true with record missing StopPWM → refused (clear message).
//  6. AllowStop=true with both StartPWM and StopPWM measured → pass.
//  7. nvidia fans with AllowStop=true skipped (NVML floor is non-zero).
func TestVerifyAllowStopGate(t *testing.T) {
	hwmonPath := "/sys/class/hwmon/hwmon0/pwm1"

	t.Run("no allow_stop fans → vacuous pass", func(t *testing.T) {
		cfg := &config.Config{
			Fans: []config.Fan{
				{Name: "cpu", Type: "hwmon", PWMPath: hwmonPath, MinPWM: 80, AllowStop: false},
			},
		}
		if err := VerifyAllowStopGate(cfg, "/nonexistent/calibration.json"); err != nil {
			t.Errorf("unexpected refusal: %v", err)
		}
	})

	t.Run("allow_stop with missing calibration file → refused", func(t *testing.T) {
		cfg := &config.Config{
			Fans: []config.Fan{
				{Name: "case", Type: "hwmon", PWMPath: hwmonPath, MinPWM: 0, AllowStop: true},
			},
		}
		err := VerifyAllowStopGate(cfg, "/nonexistent/calibration.json")
		if !errors.Is(err, ErrAllowStopNoCalibration) {
			t.Errorf("want ErrAllowStopNoCalibration, got %v", err)
		}
	})

	t.Run("allow_stop with no record for this fan → refused", func(t *testing.T) {
		calPath := writeCalibration(t, map[string]Result{
			"/sys/class/hwmon/hwmon0/pwm2": {StartPWM: 60, StopPWM: 30},
		})
		cfg := &config.Config{
			Fans: []config.Fan{
				{Name: "case", Type: "hwmon", PWMPath: hwmonPath, MinPWM: 0, AllowStop: true},
			},
		}
		err := VerifyAllowStopGate(cfg, calPath)
		if !errors.Is(err, ErrAllowStopNoCalibration) {
			t.Fatalf("want ErrAllowStopNoCalibration, got %v", err)
		}
		if !strings.Contains(err.Error(), "no calibration record") {
			t.Errorf("error message lacks 'no calibration record': %v", err)
		}
	})

	t.Run("allow_stop with missing pwm_min_start → refused", func(t *testing.T) {
		calPath := writeCalibration(t, map[string]Result{
			hwmonPath: {StartPWM: 0, StopPWM: 30}, // start not measured
		})
		cfg := &config.Config{
			Fans: []config.Fan{
				{Name: "case", Type: "hwmon", PWMPath: hwmonPath, MinPWM: 0, AllowStop: true},
			},
		}
		err := VerifyAllowStopGate(cfg, calPath)
		if !errors.Is(err, ErrAllowStopNoCalibration) {
			t.Fatalf("want ErrAllowStopNoCalibration, got %v", err)
		}
		if !strings.Contains(err.Error(), "pwm_min_start") {
			t.Errorf("error message lacks 'pwm_min_start': %v", err)
		}
	})

	t.Run("allow_stop with missing pwm_min_running → refused", func(t *testing.T) {
		calPath := writeCalibration(t, map[string]Result{
			hwmonPath: {StartPWM: 60, StopPWM: 0}, // stall not measured
		})
		cfg := &config.Config{
			Fans: []config.Fan{
				{Name: "case", Type: "hwmon", PWMPath: hwmonPath, MinPWM: 0, AllowStop: true},
			},
		}
		err := VerifyAllowStopGate(cfg, calPath)
		if !errors.Is(err, ErrAllowStopNoCalibration) {
			t.Fatalf("want ErrAllowStopNoCalibration, got %v", err)
		}
		if !strings.Contains(err.Error(), "pwm_min_running") {
			t.Errorf("error message lacks 'pwm_min_running': %v", err)
		}
	})

	t.Run("allow_stop with both measured → pass", func(t *testing.T) {
		calPath := writeCalibration(t, map[string]Result{
			hwmonPath: {StartPWM: 60, StopPWM: 30},
		})
		cfg := &config.Config{
			Fans: []config.Fan{
				{Name: "case", Type: "hwmon", PWMPath: hwmonPath, MinPWM: 0, AllowStop: true},
			},
		}
		if err := VerifyAllowStopGate(cfg, calPath); err != nil {
			t.Errorf("unexpected refusal with both measurements: %v", err)
		}
	})

	t.Run("nvidia fans skipped", func(t *testing.T) {
		cfg := &config.Config{
			Fans: []config.Fan{
				{Name: "gpu0", Type: "nvidia", PWMPath: "0", MinPWM: 0, AllowStop: true},
			},
		}
		// No calibration file; NVIDIA fans with AllowStop should still pass.
		if err := VerifyAllowStopGate(cfg, "/nonexistent/calibration.json"); err != nil {
			t.Errorf("nvidia fan AllowStop should be skipped, got: %v", err)
		}
	})

	t.Run("legacy bare-map calibration envelope is honoured", func(t *testing.T) {
		dir := t.TempDir()
		calPath := filepath.Join(dir, "calibration.json")
		// Pre-v3 envelope: bare map, no schema_version.
		bare := map[string]Result{
			hwmonPath: {StartPWM: 50, StopPWM: 25},
		}
		data, _ := json.Marshal(bare)
		if err := os.WriteFile(calPath, data, 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		cfg := &config.Config{
			Fans: []config.Fan{
				{Name: "case", Type: "hwmon", PWMPath: hwmonPath, MinPWM: 0, AllowStop: true},
			},
		}
		if err := VerifyAllowStopGate(cfg, calPath); err != nil {
			t.Errorf("legacy envelope should parse and pass: %v", err)
		}
	})
}

// writeCalibration seeds a v3-envelope calibration.json under
// t.TempDir() and returns its path.
func writeCalibration(t *testing.T, results map[string]Result) string {
	t.Helper()
	dir := t.TempDir()
	calPath := filepath.Join(dir, "calibration.json")
	env := onDiskEnvelope{
		SchemaVersion: SchemaVersion,
		Results:       results,
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(calPath, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return calPath
}
