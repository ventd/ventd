package controller

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
)

// TestReadAllSensors_LowTempDisconnectedFlaggedAsSentinel is the regression
// guard for RULE-CTRL-LOWTEMP-DISCONNECT: a temp control sensor reading below
// the ambient floor (a disconnected pin at ~8.5°C) must be classified as
// data-loss (sentinel) — not trusted as a genuinely cold chip, which would
// make the curve compute minimum PWM and under-cool. A healthy temp and a low
// non-temp (voltage) reading must be unaffected.
func TestReadAllSensors_LowTempDisconnectedFlaggedAsSentinel(t *testing.T) {
	dir := t.TempDir()
	mk := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	disconnected := mk("temp1_input", "8500") // 8.5°C — classic unplugged pin
	healthy := mk("temp2_input", "45000")     // 45°C — a real chip
	lowVoltage := mk("in0_input", "3300")     // 3.3V — low number, NOT a temp

	sensors := []config.Sensor{
		{Name: "cpu_disconnected", Type: "hwmon", Path: disconnected},
		{Name: "cpu_ok", Type: "hwmon", Path: healthy},
		{Name: "vcore", Type: "hwmon", Path: lowVoltage},
	}
	dst := map[string]float64{}
	sentinel := map[string]bool{}
	readAllSensors(slog.Default(), sensors, dst, sentinel, nil, time.Time{})

	if !sentinel["cpu_disconnected"] {
		t.Error("disconnected temp sensor (8.5°C) must be flagged as data-loss, not trusted as a cold chip")
	}
	if _, ok := dst["cpu_disconnected"]; ok {
		t.Error("disconnected temp value must not reach the curve (skipped, like a sentinel)")
	}
	if sentinel["cpu_ok"] {
		t.Error("a healthy 45°C reading must not be flagged")
	}
	if dst["cpu_ok"] != 45 {
		t.Errorf("healthy sensor value = %v, want 45", dst["cpu_ok"])
	}
	if sentinel["vcore"] {
		t.Error("a low voltage reading (non-temp path) must not be misclassified as a disconnected temp")
	}
}
