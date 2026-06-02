package sensorfreeze

import (
	"testing"
	"time"
)

// base is an arbitrary fixed epoch; all tests advance from it deterministically.
var base = time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

// feed reports value for sensor over span, one sample every step, starting at
// start, and returns the time just after the last sample.
func feed(t *Tracker, name string, value float64, start time.Time, span, step time.Duration) time.Time {
	now := start
	for elapsed := time.Duration(0); elapsed <= span; elapsed += step {
		now = start.Add(elapsed)
		t.Observe(name, value, now)
	}
	return now
}

func names(s []Stuck) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.Name
	}
	return out
}

func TestStuck_FrozenSensorWhileSiblingRises_Flagged(t *testing.T) {
	tr := New()
	step := 10 * time.Second
	span := StuckMinDuration + time.Minute

	// "vrm" sits frozen at 42.0 the whole time.
	feed(tr, "vrm", 42.0, base, span, step)

	// "cpu" climbs 35 -> 65 over the same window — a clear 30 °C swing.
	var now time.Time
	for i := 0; ; i++ {
		now = base.Add(time.Duration(i) * step)
		if now.Sub(base) > span {
			break
		}
		temp := 35.0 + float64(i)*0.8
		if temp > 65 {
			temp = 65
		}
		tr.Observe("cpu", temp, now)
	}

	got := tr.Stuck(now)
	if len(got) != 1 || got[0].Name != "vrm" {
		t.Fatalf("expected vrm flagged stuck, got %v", names(got))
	}
	if got[0].ValueC != 42.0 {
		t.Errorf("ValueC = %v, want 42.0", got[0].ValueC)
	}
	if got[0].FrozenSeconds < int(StuckMinDuration.Seconds()) {
		t.Errorf("FrozenSeconds = %d, want >= %d", got[0].FrozenSeconds, int(StuckMinDuration.Seconds()))
	}
	if got[0].ReferenceRiseC < RiseThresholdC {
		t.Errorf("ReferenceRiseC = %v, want >= %v", got[0].ReferenceRiseC, RiseThresholdC)
	}
}

// The core false-positive guard: an idle machine where every sensor
// legitimately sits flat must NOT flag any sensor as stuck.
func TestStuck_AllFlatIdle_NotFlagged(t *testing.T) {
	tr := New()
	step := 10 * time.Second
	span := StuckMinDuration + time.Minute

	feed(tr, "vrm", 42.0, base, span, step)
	feed(tr, "cpu", 38.0, base, span, step)
	now := feed(tr, "board", 30.0, base, span, step)

	if got := tr.Stuck(now); len(got) != 0 {
		t.Fatalf("idle steady-state must not flag any sensor, got %v", names(got))
	}
}

func TestStuck_MovingSensor_NotFlagged(t *testing.T) {
	tr := New()
	step := 10 * time.Second
	span := StuckMinDuration + time.Minute

	// cpu rises (the activity reference); board oscillates a few degrees so
	// it is never "frozen" even though it's not the active one.
	var now time.Time
	for i := 0; ; i++ {
		now = base.Add(time.Duration(i) * step)
		if now.Sub(base) > span {
			break
		}
		tr.Observe("cpu", 35.0+float64(i)*0.8, now)
		board := 40.0
		if i%2 == 0 {
			board = 41.0
		}
		tr.Observe("board", board, now)
	}
	for _, s := range tr.Stuck(now) {
		if s.Name == "board" {
			t.Fatalf("oscillating sensor must not be flagged frozen")
		}
	}
}

func TestStuck_FrozenButTooBrief_NotFlagged(t *testing.T) {
	tr := New()
	step := 10 * time.Second
	span := StuckMinDuration - time.Minute // short of the threshold

	feed(tr, "vrm", 42.0, base, span, step)
	var now time.Time
	for i := 0; ; i++ {
		now = base.Add(time.Duration(i) * step)
		if now.Sub(base) > span {
			break
		}
		tr.Observe("cpu", 35.0+float64(i)*1.5, now)
	}
	if got := tr.Stuck(now); len(got) != 0 {
		t.Fatalf("sensor frozen for less than %v must not flag, got %v", StuckMinDuration, names(got))
	}
}

func TestStuck_StaleSensorDropped(t *testing.T) {
	tr := New()
	step := 10 * time.Second
	span := StuckMinDuration + time.Minute

	feed(tr, "vrm", 42.0, base, span, step)
	var last time.Time
	for i := 0; ; i++ {
		last = base.Add(time.Duration(i) * step)
		if last.Sub(base) > span {
			break
		}
		tr.Observe("cpu", 35.0+float64(i)*0.8, last)
	}
	// Query far in the future: both sensors are now stale (unreported for
	// well over staleTimeout) and must not be flagged.
	future := last.Add(staleTimeout + time.Minute)
	if got := tr.Stuck(future); len(got) != 0 {
		t.Fatalf("stale sensors must be dropped, got %v", names(got))
	}
}

func TestStuck_TwoFrozenSensors_SortedDeterministically(t *testing.T) {
	tr := New()
	step := 10 * time.Second
	span := StuckMinDuration + time.Minute

	feed(tr, "vrm", 42.0, base, span, step)
	feed(tr, "aux", 50.0, base, span, step)
	var now time.Time
	for i := 0; ; i++ {
		now = base.Add(time.Duration(i) * step)
		if now.Sub(base) > span {
			break
		}
		tr.Observe("cpu", 35.0+float64(i)*0.8, now)
	}
	got := names(tr.Stuck(now))
	want := []string{"aux", "vrm"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v (sorted)", got, want)
	}
}

func TestObserveAndStuck_NilSafe(t *testing.T) {
	var tr *Tracker
	tr.Observe("x", 40, base) // must not panic
	if got := tr.Stuck(base); got != nil {
		t.Fatalf("nil tracker Stuck = %v, want nil", got)
	}
}
