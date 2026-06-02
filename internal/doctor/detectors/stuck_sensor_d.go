package detectors

import (
	"context"
	"fmt"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// StuckSensor is one sensor the freeze tracker currently believes is frozen,
// already filtered to "frozen long enough while the system is thermally active"
// by the source.
type StuckSensor struct {
	// Name is the configured sensor name.
	Name string
	// ValueC is the value the sensor has been frozen at.
	ValueC float64
	// FrozenSeconds is how long the value has been held.
	FrozenSeconds int
	// ReferenceRiseC is the largest recent swing on another sensor — the
	// evidence that the box was thermally active while this one sat still.
	ReferenceRiseC float64
}

// StuckSensorStatusFn returns the sensors currently judged stuck. Production
// wires it to the shared sensorfreeze.Tracker every controller reports into via
// the sensor-read hook; tests inject a stub. A function seam keeps the detectors
// package decoupled from internal/sensorfreeze and the daemon wiring.
type StuckSensorStatusFn func() []StuckSensor

// StuckSensorDetector surfaces a temperature sensor that has gone *stuck*: its
// reading is plausible and so passes every per-sample check (it is not a
// sentinel and not implausibly low — those are caught by
// RULE-CTRL-LOWTEMP-DISCONNECT and the sentinel guard), yet it has not moved for
// minutes while another sensor clearly has. A frozen control sensor is
// dangerous in slow motion: the curve it feeds stops responding to real heat,
// so the fan never ramps even as the chip climbs. ventd cannot safely
// auto-correct — it has no second source of truth for that channel — so this is
// observability-only (RULE-DOCTOR-DETECTOR-STUCK-SENSOR): a Warning card telling
// the operator to check the sensor / reseat the header, never a control action.
type StuckSensorDetector struct {
	status StuckSensorStatusFn
}

// NewStuckSensorDetector constructs the detector. A nil status fn is a no-op
// (zero facts) — e.g. monitor-only hosts that never wired a freeze tracker.
func NewStuckSensorDetector(fn StuckSensorStatusFn) *StuckSensorDetector {
	return &StuckSensorDetector{status: fn}
}

// Name returns the stable detector ID.
func (d *StuckSensorDetector) Name() string { return "stuck_sensor" }

// Probe reads the stuck-sensor snapshot and emits one Warning Fact per frozen
// sensor. Pure read through the seam; never touches sysfs — the freeze judgement
// is made over time in the tracker the control loop already feeds.
func (d *StuckSensorDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.status == nil {
		return nil, nil
	}
	now := time.Now
	if deps.Now != nil {
		now = deps.Now
	}
	var facts []doctor.Fact
	for _, s := range d.status() {
		facts = append(facts, doctor.Fact{
			Detector:   d.Name(),
			Severity:   doctor.SeverityWarning,
			Class:      recovery.ClassUnknown,
			Title:      "A temperature sensor appears stuck",
			Detail:     stuckSensorBody(s),
			EntityHash: doctor.HashEntity("stuck_sensor", s.Name),
			Observed:   now(),
		})
	}
	return facts, nil
}

// stuckSensorBody renders the operator-facing explanation + remediation for one
// frozen sensor.
func stuckSensorBody(s StuckSensor) string {
	return fmt.Sprintf(
		"Sensor %q has reported the same temperature (%.1f °C) for %d minutes, while another "+
			"sensor on this machine swung by %.0f °C over the same period. A reading that never "+
			"moves while the rest of the box heats up and cools down is almost always a frozen or "+
			"disconnected sensor, not a genuinely steady chip. If a fan curve is driven from this "+
			"sensor, that fan has stopped responding to real temperature changes. ventd keeps using "+
			"the reading because it is in a plausible range and ventd has no second source to check "+
			"it against — so this needs a human. Check the sensor in your motherboard's hardware "+
			"monitor, reseat the relevant header/cable, and consider pointing the affected curve at a "+
			"sensor known to track load (the CPU package temperature is usually safest).",
		s.Name, s.ValueC, s.FrozenSeconds/60, s.ReferenceRiseC)
}
