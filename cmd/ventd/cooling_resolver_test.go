package main

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// TestNewCoolingResolver_HasSignalWhenCalibrateAndRAPLPresent wires
// a fixture orchestrator state.json + a RAPL sysfs fixture and
// verifies the resolver returns the capacity_w + cpu_tdp_w pair.
// (#1285)
func TestNewCoolingResolver_HasSignalWhenCalibrateAndRAPLPresent(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	// Two 120 mm fans at 1500 RPM = 60 W cooling capacity.
	body := []byte(`{
		"outcomes": {
			"calibrate": {
				"status": "success",
				"artifact": {
					"results": [
						{"pwm_path": "/sys/hwmon0/pwm1", "max_rpm": 1500},
						{"pwm_path": "/sys/hwmon0/pwm2", "max_rpm": 1500}
					]
				}
			}
		}
	}`)
	if err := os.WriteFile(statePath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	raplDir := filepath.Join(dir, "intel-rapl:0")
	if err := os.MkdirAll(raplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raplPath := filepath.Join(raplDir, "constraint_0_power_limit_uw")
	if err := os.WriteFile(raplPath, []byte("125000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(&config.Config{
		Fans: []config.Fan{
			{Name: "case_top", Type: "hwmon", PWMPath: "/sys/hwmon0/pwm1"},
			{Name: "case_bottom", Type: "hwmon", PWMPath: "/sys/hwmon0/pwm2"},
		},
	})

	resolver := newCoolingResolver(cfgPtr, statePath, []string{raplPath})
	out := resolver()
	if !out.HasSignal {
		t.Errorf("HasSignal = false; want true (calibrate + RAPL both present)")
	}
	if out.CapacityW <= 0 {
		t.Errorf("CapacityW = %v; want > 0", out.CapacityW)
	}
	if out.CPUTDPW != 125 {
		t.Errorf("CPUTDPW = %d; want 125", out.CPUTDPW)
	}
}

// TestNewCoolingResolver_NoCalibrateFile returns a zero-signal
// response: pre-wizard hosts shouldn't surface capacity numbers.
// (#1285)
func TestNewCoolingResolver_NoCalibrateFile(t *testing.T) {
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(&config.Config{})
	resolver := newCoolingResolver(cfgPtr, filepath.Join(t.TempDir(), "missing.json"), []string{})
	out := resolver()
	if out.HasSignal {
		t.Errorf("HasSignal = true; want false on missing calibrate")
	}
	if !out.Adequate {
		t.Errorf("Adequate = false; want true on no-signal (UI hides panel)")
	}
}

// TestNewCoolingResolver_PathologicalRAPLReturnsZero — malformed
// uw file falls through to 0 W TDP rather than crashing. (#1285)
func TestNewCoolingResolver_PathologicalRAPLReturnsZero(t *testing.T) {
	dir := t.TempDir()
	raplPath := filepath.Join(dir, "uw")
	if err := os.WriteFile(raplPath, []byte("not-a-number\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readRAPLTDPWFromPaths([]string{raplPath}); got != 0 {
		t.Errorf("malformed uw = %d; want 0", got)
	}
}
