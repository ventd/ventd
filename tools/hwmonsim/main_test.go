package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func baseCfg() config {
	return config{fans: 3, temps: 2, chip: "nct6687", maxRPM: 2200, minRPM: 500,
		stopPWM: 25, startPWM: 40, model: "spinup", tick: time.Millisecond}
}

func TestRpmFor_Linear(t *testing.T) {
	cfg := baseCfg()
	cfg.model = "linear"
	var sp bool
	if r := rpmFor(0, 1, cfg, &sp); r != 0 {
		t.Errorf("pwm=0 linear: rpm=%d, want 0", r)
	}
	if r := rpmFor(255, 1, cfg, &sp); r != cfg.maxRPM {
		t.Errorf("pwm=255 linear: rpm=%d, want %d", r, cfg.maxRPM)
	}
	// Monotonic non-decreasing.
	prev := -1
	for p := 0; p <= 255; p++ {
		r := rpmFor(uint8(p), 1, cfg, &sp)
		if r < prev {
			t.Fatalf("linear not monotonic at pwm=%d: %d < %d", p, r, prev)
		}
		prev = r
	}
}

func TestRpmFor_SpinupHysteresis(t *testing.T) {
	cfg := baseCfg()
	var sp bool // starts stalled
	// Below startPWM while stalled → stays stalled.
	if r := rpmFor(30, 1, cfg, &sp); r != 0 || sp {
		t.Errorf("stalled fan at pwm=30 (< startPWM=40): rpm=%d spinning=%v, want 0/false", r, sp)
	}
	// At/above startPWM → spins.
	if r := rpmFor(40, 1, cfg, &sp); r <= 0 || !sp {
		t.Errorf("pwm=40 (== startPWM): rpm=%d spinning=%v, want >0/true", r, sp)
	}
	// Now spinning: drop to between stop and start → keeps spinning (hysteresis).
	if r := rpmFor(30, 1, cfg, &sp); r <= 0 || !sp {
		t.Errorf("spinning fan at pwm=30 (> stopPWM=25): rpm=%d spinning=%v, want >0/true", r, sp)
	}
	// Drop to/below stopPWM → stalls.
	if r := rpmFor(25, 1, cfg, &sp); r != 0 || sp {
		t.Errorf("spinning fan at pwm=25 (== stopPWM): rpm=%d spinning=%v, want 0/false", r, sp)
	}
	if r := rpmFor(255, 1, cfg, &sp); r != cfg.maxRPM {
		t.Errorf("pwm=255 spinup: rpm=%d, want %d", r, cfg.maxRPM)
	}
}

func TestRpmFor_AutoModeIgnoresDuty(t *testing.T) {
	cfg := baseCfg()
	var sp bool
	// enable != 1 (firmware/auto): a baseline regardless of duty byte.
	r0 := rpmFor(0, 2, cfg, &sp)
	r255 := rpmFor(255, 2, cfg, &sp)
	if r0 == 0 || r0 != r255 {
		t.Errorf("auto-mode rpm should be a nonzero constant: r0=%d r255=%d", r0, r255)
	}
}

func TestTempMilliC_MonotonicCooling(t *testing.T) {
	hot := tempMilliC(0)
	cool := tempMilliC(1)
	if hot <= cool {
		t.Fatalf("more airflow must be cooler: hot=%d cool=%d", hot, cool)
	}
	if hot != 75000 || cool != 35000 {
		t.Errorf("temp bounds: hot=%d cool=%d, want 75000/35000", hot, cool)
	}
}

func TestMaterialise_FaithfulShape(t *testing.T) {
	root := t.TempDir()
	dev := filepath.Join(root, "hwmon0")
	cfg := baseCfg()
	if err := materialise(dev, cfg); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"name", "pwm1", "pwm1_enable", "fan1_input", "pwm3", "fan3_input", "temp1_input", "temp2_input"} {
		if _, err := os.Stat(filepath.Join(dev, f)); err != nil {
			t.Errorf("missing synthetic file %s: %v", f, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dev, "name"))
	if got := string(b); got != "nct6687\n" {
		t.Errorf("name = %q, want nct6687", got)
	}
}
