package web

import (
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/sensorfreeze"
)

func stuckSensorFact(r doctor.Report) *doctor.Fact {
	for i := range r.Facts {
		if r.Facts[i].Detector == "stuck_sensor" {
			return &r.Facts[i]
		}
	}
	return nil
}

// seedStuck feeds a tracker a history (ending at end) in which "vrm" is frozen
// at 42 °C while "cpu" climbs 35→65 °C — the canonical stuck-sensor scenario.
// Observe takes an explicit timestamp, so the history is laid down relative to
// the real clock the doctor reads with.
func seedStuck(tr *sensorfreeze.Tracker, end time.Time) {
	span := sensorfreeze.StuckMinDuration + time.Minute
	step := 10 * time.Second
	start := end.Add(-span)
	for i := 0; ; i++ {
		at := start.Add(time.Duration(i) * step)
		if at.After(end) {
			break
		}
		tr.Observe("vrm", 42.0, at)
		cpu := 35.0 + float64(i)*0.8
		if cpu > 65 {
			cpu = 65
		}
		tr.Observe("cpu", cpu, at)
	}
}

// TestDoctorReport_SurfacesStuckSensor is the end-to-end wire for the
// stuck-sensor detector: once a tracker holding a frozen-while-active history is
// set on the server, GET /api/v1/doctor surfaces a Warning stuck_sensor fact
// naming the frozen sensor. Proves the web closure (s.stuckSensors →
// stuck_sensor detector) the unit tests could not reach.
func TestDoctorReport_SurfacesStuckSensor(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	tr := sensorfreeze.New()
	seedStuck(tr, time.Now())
	srv.SetStuckSensorTracker(tr)

	got := stuckSensorFact(runDoctor(t, srv))
	if got == nil {
		t.Fatal("stuck_sensor fact missing from doctor report despite a frozen sensor")
	}
	if got.Severity.String() != "warning" {
		t.Errorf("stuck_sensor severity = %q, want warning", got.Severity.String())
	}
	if !strings.Contains(got.Detail, "vrm") {
		t.Errorf("stuck_sensor detail should name the frozen sensor; got: %s", got.Detail)
	}
}

// TestDoctorReport_NoStuckSensorWhenQuiet pins the silent side: no tracker wired
// (monitor-only) and an idle box where every sensor sits flat both emit no
// stuck_sensor fact. A fresh server each time so the report cache doesn't carry
// a verdict over.
func TestDoctorReport_NoStuckSensorWhenQuiet(t *testing.T) {
	t.Run("no tracker", func(t *testing.T) {
		srv, _, cancel := newHandlerHarness(t)
		defer cancel()
		if f := stuckSensorFact(runDoctor(t, srv)); f != nil {
			t.Errorf("stuck_sensor fact present with no tracker wired: %+v", f)
		}
	})
	t.Run("idle all-flat", func(t *testing.T) {
		srv, _, cancel := newHandlerHarness(t)
		defer cancel()
		tr := sensorfreeze.New()
		end := time.Now()
		start := end.Add(-(sensorfreeze.StuckMinDuration + time.Minute))
		for i := 0; ; i++ {
			at := start.Add(time.Duration(i) * 10 * time.Second)
			if at.After(end) {
				break
			}
			tr.Observe("vrm", 42.0, at)
			tr.Observe("cpu", 38.0, at)
		}
		srv.SetStuckSensorTracker(tr)
		if f := stuckSensorFact(runDoctor(t, srv)); f != nil {
			t.Errorf("stuck_sensor fact present on an idle all-flat box: %+v", f)
		}
	})
}
