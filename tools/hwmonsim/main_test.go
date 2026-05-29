package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/hwdb"
)

func baseCfg() config {
	return config{fans: 3, temps: 2, chip: "nct6687", maxRPM: 2200, minRPM: 500,
		stopPWM: 25, startPWM: 40, model: "spinup", tick: time.Millisecond}
}

// firstBoardWithControllableChip returns a real catalog board id whose primary
// controller chip is one hwmonsim would simulate (not unknown/empty/nvidia),
// plus that chip — so the board test tracks the catalog instead of hard-coding.
func firstBoardWithControllableChip(t *testing.T) (id, chip string) {
	t.Helper()
	entries, err := hwdb.LoadBoardCatalog()
	if err != nil {
		t.Fatalf("load board catalog: %v", err)
	}
	for _, e := range entries {
		c := e.PrimaryController.Chip
		if c != "" && c != "unknown" && c != "nvidia" {
			return e.ID, c
		}
	}
	t.Skip("no catalog board with a controllable primary chip")
	return "", ""
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
	if err := materialise(dev, "nct6687", 3, 2); err != nil {
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

func TestBuildDevices_DefaultSingle(t *testing.T) {
	cfg := baseCfg()
	cfg.out = t.TempDir()
	cfg.chip = "it87"
	devs, err := buildDevices(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0].chip != "it87" {
		t.Fatalf("default buildDevices = %+v, want one it87 device", devs)
	}
}

func TestBuildDevices_FromBoardSeedsChips(t *testing.T) {
	// Pick a real catalog board with a known primary chip, via the same
	// loader buildDevices uses, so the test tracks the catalog.
	id, chip := firstBoardWithControllableChip(t)
	cfg := baseCfg()
	cfg.out = t.TempDir()
	cfg.board = id
	devs, err := buildDevices(cfg)
	if err != nil {
		t.Fatalf("buildDevices(board=%q): %v", id, err)
	}
	if len(devs) == 0 {
		t.Fatalf("board %q produced no devices", id)
	}
	if devs[0].chip != chip {
		t.Errorf("board %q primary chip = %q, want %q", id, devs[0].chip, chip)
	}
	// hwmonN dirs must be contiguous from hwmon0.
	for i, d := range devs {
		if filepath.Base(d.dir) != "hwmon"+strconv.Itoa(i) {
			t.Errorf("device %d dir = %q, want hwmon%d", i, d.dir, i)
		}
	}
}

func TestBuildDevices_UnknownBoard(t *testing.T) {
	cfg := baseCfg()
	cfg.out = t.TempDir()
	cfg.board = "definitely-not-a-real-board-xyz"
	if _, err := buildDevices(cfg); err == nil {
		t.Fatal("expected error for unknown board id")
	}
}
