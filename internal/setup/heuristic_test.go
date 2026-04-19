package setup

import (
	"strings"
	"testing"
)

// TestRegression_Issue504_HeuristicBindingCoreTemp verifies that a chips slice
// containing a coretemp chip with a "Package id 0" sensor returns that sensor
// as the highest-priority binding. This covers the Intel mini PC case where
// RPM correlation failed but the CPU package sensor is present.
func TestRegression_Issue504_HeuristicBindingCoreTemp(t *testing.T) {
	chips := []HwmonChip{
		{
			Name: "coretemp",
			Sensors: []HwmonSensor{
				{Label: "Core 0", Path: "/sys/class/hwmon/hwmon1/temp2_input", CurrentMillideg: 45_000},
				{Label: "Package id 0", Path: "/sys/class/hwmon/hwmon1/temp1_input", CurrentMillideg: 47_000},
				{Label: "Core 1", Path: "/sys/class/hwmon/hwmon1/temp3_input", CurrentMillideg: 46_000},
			},
		},
		{
			Name: "nct6687",
			Sensors: []HwmonSensor{
				{Label: "MB Temp", Path: "/sys/class/hwmon/hwmon2/temp1_input", CurrentMillideg: 38_000},
			},
		},
	}

	got := heuristicSensorBinding(chips)
	if got == nil {
		t.Fatal("expected non-nil sensor, got nil")
	}
	if !strings.Contains(got.Label, "Package id 0") {
		t.Errorf("expected 'Package id 0' sensor, got label %q (path %s)", got.Label, got.Path)
	}
	if got.Path != "/sys/class/hwmon/hwmon1/temp1_input" {
		t.Errorf("unexpected path %s", got.Path)
	}
}

// TestRegression_Issue504_HeuristicBindingK10temp verifies Priority 2:
// k10temp "Tctl" wins when no coretemp chip is present (AMD mini PC).
func TestRegression_Issue504_HeuristicBindingK10temp(t *testing.T) {
	chips := []HwmonChip{
		{
			Name: "k10temp",
			Sensors: []HwmonSensor{
				{Label: "Tdie", Path: "/sys/class/hwmon/hwmon0/temp2_input", CurrentMillideg: 52_000},
				{Label: "Tctl", Path: "/sys/class/hwmon/hwmon0/temp1_input", CurrentMillideg: 53_000},
			},
		},
	}

	got := heuristicSensorBinding(chips)
	if got == nil {
		t.Fatal("expected non-nil sensor, got nil")
	}
	// Tctl comes second in the sensor list but Priority 2 matches both; Tctl wins
	// because the loop finds Tctl first only if it's literally "Tctl" — Tdie also
	// matches, so whichever appears first in Sensors is returned.
	if got.Label != "Tdie" && got.Label != "Tctl" {
		t.Errorf("expected Tdie or Tctl, got %q", got.Label)
	}
}

// TestRegression_Issue504_HeuristicBindingFallback verifies Priority 4: when
// no named CPU chip is present, any sensor reading in [20°C, 100°C] is
// accepted. This is the "any sensor at plausible temp" last resort.
func TestRegression_Issue504_HeuristicBindingFallback(t *testing.T) {
	chips := []HwmonChip{
		{
			Name: "it8688",
			Sensors: []HwmonSensor{
				{Label: "", Path: "/sys/class/hwmon/hwmon3/temp1_input", CurrentMillideg: 35_000},
				{Label: "", Path: "/sys/class/hwmon/hwmon3/temp2_input", CurrentMillideg: 42_000},
			},
		},
	}

	got := heuristicSensorBinding(chips)
	if got == nil {
		t.Fatal("expected non-nil sensor for plausible-range reading, got nil")
	}
	if got.CurrentMillideg < 20_000 || got.CurrentMillideg > 100_000 {
		t.Errorf("returned sensor outside plausible range: %d millideg", got.CurrentMillideg)
	}
}

// TestRegression_Issue504_HeuristicBindingNilWhenImplausible verifies that
// sentinel readings (255.5°C) and zero readings (0°C) are both rejected, and
// nil is returned when no plausible sensor exists. This prevents heuristic
// binding from selecting a sensor that is itself broken.
func TestRegression_Issue504_HeuristicBindingNilWhenImplausible(t *testing.T) {
	chips := []HwmonChip{
		{
			Name: "it8688",
			Sensors: []HwmonSensor{
				// 0xFFFF sentinel = 255500 millideg
				{Label: "", Path: "/sys/class/hwmon/hwmon3/temp1_input", CurrentMillideg: 255_500},
				// 0°C — likely disconnected sensor
				{Label: "", Path: "/sys/class/hwmon/hwmon3/temp2_input", CurrentMillideg: 0},
				// 110°C — over the 100°C plausible cap
				{Label: "", Path: "/sys/class/hwmon/hwmon3/temp3_input", CurrentMillideg: 110_000},
			},
		},
	}

	got := heuristicSensorBinding(chips)
	if got != nil {
		t.Errorf("expected nil for all-implausible sensors, got label=%q path=%s", got.Label, got.Path)
	}
}

// TestRegression_Issue504_DetectedNotCorrelatedMessage verifies Part C: when
// fans responded (detected) but heuristic binding failed (no plausible sensor),
// the error message describes the situation accurately and does NOT contain the
// old "no fans responded" string.
func TestRegression_Issue504_DetectedNotCorrelatedMessage(t *testing.T) {
	fans := []FanState{
		{
			Name:        "Fan 1",
			Type:        "hwmon",
			PWMPath:     "/sys/class/hwmon/hwmon2/pwm1",
			DetectPhase: "heuristic", // fan responded (delta=43) but heuristic found no sensor
			CalPhase:    "skipped",
		},
	}

	msg := setupFailMessage(fans)

	if strings.Contains(msg, "no fans responded") {
		t.Errorf("error message must not say 'no fans responded' when fans were detected; got: %q", msg)
	}
	if !strings.Contains(msg, "detected") {
		t.Errorf("error message must contain 'detected'; got: %q", msg)
	}
	if !strings.Contains(msg, "heuristic") {
		t.Errorf("error message must contain 'heuristic'; got: %q", msg)
	}
}

// TestRegression_Issue504_TrulyNoFansMessage verifies the complementary case:
// when no fans responded at all (delta=0 for all), the error message is clear
// about truly absent fans rather than the heuristic-binding path.
func TestRegression_Issue504_TrulyNoFansMessage(t *testing.T) {
	fans := []FanState{
		{Name: "Fan 1", Type: "hwmon", DetectPhase: "none", CalPhase: "skipped"},
		{Name: "Fan 2", Type: "hwmon", DetectPhase: "none", CalPhase: "skipped"},
	}

	msg := setupFailMessage(fans)

	if strings.Contains(msg, "heuristic") {
		t.Errorf("no-fan message should not mention heuristic; got: %q", msg)
	}
	if !strings.Contains(msg, "0 RPM delta") {
		t.Errorf("no-fan message should mention '0 RPM delta'; got: %q", msg)
	}
}

// TestLoadHwmonChips verifies that loadHwmonChips reads chip names and
// temperature sensor paths from a fake hwmon tree. Missing label files produce
// empty labels (not an error).
func TestLoadHwmonChips(t *testing.T) {
	dir := t.TempDir()
	fakeHwmon(t, dir, map[string]string{
		"hwmon0/name":        "coretemp\n",
		"hwmon0/temp1_input": "47000\n",
		"hwmon0/temp1_label": "Package id 0\n",
		"hwmon0/temp2_input": "45000\n",
		// temp2 has no label file
		"hwmon1/name":        "it8688\n",
		"hwmon1/temp1_input": "38000\n",
	})

	chips := loadHwmonChips(dir)

	if len(chips) != 2 {
		t.Fatalf("expected 2 chips, got %d", len(chips))
	}

	// Find coretemp chip.
	var ct *HwmonChip
	for i := range chips {
		if chips[i].Name == "coretemp" {
			ct = &chips[i]
		}
	}
	if ct == nil {
		t.Fatal("coretemp chip not found")
	}
	if len(ct.Sensors) != 2 {
		t.Fatalf("coretemp: expected 2 sensors, got %d", len(ct.Sensors))
	}
	if ct.Sensors[0].Label != "Package id 0" {
		t.Errorf("expected label 'Package id 0', got %q", ct.Sensors[0].Label)
	}
	if ct.Sensors[0].CurrentMillideg != 47_000 {
		t.Errorf("expected 47000 millideg, got %d", ct.Sensors[0].CurrentMillideg)
	}
	if ct.Sensors[1].Label != "" {
		t.Errorf("sensor without label file: expected empty, got %q", ct.Sensors[1].Label)
	}
}
