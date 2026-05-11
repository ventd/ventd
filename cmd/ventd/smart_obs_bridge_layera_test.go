package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/controller"
)

// TestSmartObsBridge_LayerAObserveFiresEveryTick pins
// RULE-CONFA-WIRING-01: the bridge calls layer_a.Estimator.Observe
// on every controller tick (not skipped on first tick, not skipped
// on empty sensor map), and every Observe advances the per-channel
// bin histogram so coverage rises.
//
// Without this wiring conf_A stays at 0 forever and the aggregator's
// min-collapse pins w_pred = 0 system-wide (issue #1035 row 1).
func TestSmartObsBridge_LayerAObserveFiresEveryTick(t *testing.T) {
	ch := "/sys/class/hwmon/hwmon0/pwm1"
	w, couplingRT, marginalRT, _ := newBridgeRuntimes(t, ch)

	est, err := layer_a.New(layer_a.Config{})
	if err != nil {
		t.Fatalf("layer_a.New: %v", err)
	}
	now := time.Now()
	if err := est.Admit(ch, 0, layer_a.DefaultNoiseFloor, now); err != nil {
		t.Fatalf("Admit: %v", err)
	}

	bridge := buildSmartObsBridge(w, couplingRT, marginalRT, est)

	// Three ticks at the same PWM bin (PWM=100 → bin 6) — that bin
	// must reach MinObsPerBinForCoverage = 3, raising coverage to
	// exactly 1/NumBins. Snapshot.Coverage > 0 is the load-bearing
	// signal that Observe actually fired.
	bridge(makeObsRecord(now, ch, 100, "sig-1", 50.0))
	bridge(makeObsRecord(now.Add(2*time.Second), ch, 100, "sig-1", 52.5))
	bridge(makeObsRecord(now.Add(4*time.Second), ch, 100, "sig-1", 55.0))

	snap := est.Read(ch)
	if snap == nil {
		t.Fatal("layer_a snapshot nil after three Observe calls")
	}
	wantCoverage := 1.0 / float64(layer_a.NumBins)
	if snap.Coverage < wantCoverage-1e-9 || snap.Coverage > wantCoverage+1e-9 {
		t.Errorf("Coverage = %v after 3 ticks in same bin; want %v",
			snap.Coverage, wantCoverage)
	}
	if snap.ConfA <= 0 {
		t.Errorf("ConfA = %v after coverage advanced; want > 0", snap.ConfA)
	}
}

// TestSmartObsBridge_LayerANilIsNoOp asserts the bridge's no-op-on-nil
// Layer-A contract. When the estimator is nil (monitor-only systems
// where no controllable channels exist, or test scaffolding), the
// closure MUST still persist + feed Layer-B/C without panicking.
func TestSmartObsBridge_LayerANilIsNoOp(t *testing.T) {
	ch := "/sys/class/hwmon/hwmon0/pwm1"
	w, couplingRT, marginalRT, _ := newBridgeRuntimes(t, ch)

	bridge := buildSmartObsBridge(w, couplingRT, marginalRT, nil)
	now := time.Now()
	// No panic, no error — Layer-B should still see the second tick.
	bridge(makeObsRecord(now, ch, 100, "sig-1", 50.0))
	bridge(makeObsRecord(now.Add(2*time.Second), ch, 120, "sig-1", 52.5))

	if got := couplingRT.Shard(ch).Read().NSamples; got != 1 {
		t.Errorf("Layer-B NSamples = %d with nil LayerA; want 1", got)
	}
}

// TestSmartObsBridge_LayerAObservesEvenWhenSensorMapEmpty pins the
// sensor-independent contract: Layer-A's Observe fires even when the
// thermal map is empty (the bridge short-circuits Layer-B/C but
// MUST NOT short-circuit Layer-A). A fresh-install controller with
// no thermal sources still grows bin coverage from PWM writes alone.
func TestSmartObsBridge_LayerAObservesEvenWhenSensorMapEmpty(t *testing.T) {
	ch := "/sys/class/hwmon/hwmon0/pwm1"
	w, couplingRT, marginalRT, _ := newBridgeRuntimes(t, ch)

	est, err := layer_a.New(layer_a.Config{})
	if err != nil {
		t.Fatalf("layer_a.New: %v", err)
	}
	if err := est.Admit(ch, 0, layer_a.DefaultNoiseFloor, time.Now()); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	bridge := buildSmartObsBridge(w, couplingRT, marginalRT, est)

	// Tick with empty SensorReadings — Layer-B/C short-circuit.
	now := time.Now()
	for i := 0; i < 3; i++ {
		rec := &controller.ObsRecord{
			Ts:             now.Add(time.Duration(i) * time.Second).UnixMicro(),
			PWMPath:        ch,
			PWMWritten:     100,
			RPM:            -1,
			SignatureLabel: "sig-1",
			SensorReadings: nil, // empty — triggers maxTempReading NaN path
		}
		bridge(rec)
	}

	snap := est.Read(ch)
	if snap == nil || snap.Coverage <= 0 {
		t.Errorf("Layer-A Coverage did not advance under empty sensor map; got snap=%+v", snap)
	}
}

// _ silences the path import even when this file is the only consumer
// of filepath in cmd/ventd_test.
var _ = filepath.Join
