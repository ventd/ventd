package main

import (
	"testing"

	"github.com/ventd/ventd/internal/observation"
)

// TestConvertSensorReadings_HappyPath pins the v0.5.8.1 type bridge:
// controller's map[string]float64 (sensor name → °C) translates into
// the observation log's map[uint16]int16 (SensorID → centi-celsius)
// without loss in the realistic operating range. Each input key/value
// pair must round-trip through SensorID + (°C × 100) cleanly.
func TestConvertSensorReadings_HappyPath(t *testing.T) {
	in := map[string]float64{
		"cpu_temp":    52.0,
		"gpu_temp":    74.5,
		"motherboard": 38.0,
	}
	out := convertSensorReadings(in)
	if out == nil {
		t.Fatal("convertSensorReadings returned nil for non-empty input")
	}
	if got, want := len(out), len(in); got != want {
		t.Fatalf("output size: got %d, want %d (no entries should be dropped at sensible values)", got, want)
	}
	for name, celsius := range in {
		id := observation.SensorID(name)
		got, ok := out[id]
		if !ok {
			t.Errorf("missing key for sensor %q (id %d)", name, id)
			continue
		}
		want := int16(celsius * 100)
		if got != want {
			t.Errorf("convert %q: got %d centi-°C, want %d", name, got, want)
		}
	}
}

// TestConvertSensorReadings_FiltersImplausible verifies the defensive
// plausibility band — values outside [-150, 150]°C are dropped. This
// is belt-and-braces; the controller's read path already filters
// sentinels, but a stale or future-format reading must not enter the
// persisted observation log under any path.
func TestConvertSensorReadings_FiltersImplausible(t *testing.T) {
	in := map[string]float64{
		"good_sensor":      50.0,
		"sentinel_high":    255.5, // 0xFFFF/1000 sentinel after scale
		"impossible_low":   -200.0,
		"boundary_at_150":  150.0, // strictly outside (rule uses < / >)
		"boundary_at_n150": -150.0,
	}
	out := convertSensorReadings(in)
	if got, ok := out[observation.SensorID("good_sensor")]; !ok || got != 5000 {
		t.Errorf("good_sensor: got %d ok=%v, want 5000 ok=true", got, ok)
	}
	if _, ok := out[observation.SensorID("sentinel_high")]; ok {
		t.Error("sentinel_high (255.5°C) leaked into output — must be filtered")
	}
	if _, ok := out[observation.SensorID("impossible_low")]; ok {
		t.Error("impossible_low (-200°C) leaked into output — must be filtered")
	}
	// Boundary points: implementation choice is to keep ±150°C inclusive
	// (`celsius < -150 || celsius > 150` rejects strictly outside the band).
	if _, ok := out[observation.SensorID("boundary_at_150")]; !ok {
		t.Error("boundary_at_150 dropped; rule is inclusive at exactly ±150°C")
	}
	if _, ok := out[observation.SensorID("boundary_at_n150")]; !ok {
		t.Error("boundary_at_n150 dropped; rule is inclusive at exactly ±150°C")
	}
}

// TestConvertSensorReadings_NilOnEmpty asserts the converter returns
// a nil map (not an empty allocation) when there is no input. Keeps
// the persisted record's SensorReadings nil — msgpack omits nil maps
// without writing the empty-map sentinel.
func TestConvertSensorReadings_NilOnEmpty(t *testing.T) {
	if got := convertSensorReadings(nil); got != nil {
		t.Errorf("convertSensorReadings(nil) = %v, want nil", got)
	}
	if got := convertSensorReadings(map[string]float64{}); got != nil {
		t.Errorf("convertSensorReadings(empty) = %v, want nil", got)
	}
	allFiltered := map[string]float64{"bad": 999.9}
	if got := convertSensorReadings(allFiltered); got != nil {
		t.Errorf("convertSensorReadings(all-filtered) = %v, want nil", got)
	}
}
