package massstall

import (
	"testing"
	"time"
)

// TestMassStall_TripsAtThreshold binds RULE-MASSTALL-TRIP-01: MassStalled
// trips only when >= MinChannels distinct channels are stalled within Window.
func TestMassStall_TripsAtThreshold(t *testing.T) {
	tr := New(3*time.Minute, 2)
	now := time.Unix(1000, 0)

	// One stalled channel: below the threshold of 2.
	tr.Report("fan1", 200, 0, now)
	if tr.MassStalled(now) {
		t.Fatalf("one stalled channel must not trip mass-stall (min=2)")
	}

	// Second stalled channel: trips.
	tr.Report("fan2", 200, 0, now)
	if !tr.MassStalled(now) {
		t.Fatalf("two stalled channels must trip mass-stall (min=2)")
	}

	cnt, ids := tr.Snapshot(now)
	if cnt != 2 || len(ids) != 2 || ids[0] != "fan1" || ids[1] != "fan2" {
		t.Fatalf("snapshot = %d %v; want 2 [fan1 fan2]", cnt, ids)
	}
}

// TestMassStall_FloorAndTachless binds RULE-MASSTALL-FLOOR-01: a stall
// requires commandedPWM >= StallPWMFloor AND observedRPM == 0; a
// tach-less read (-1) never counts.
func TestMassStall_FloorAndTachless(t *testing.T) {
	tr := New(3*time.Minute, 2)
	now := time.Unix(2000, 0)

	// Below the stiction floor with RPM 0: working as intended, not a stall.
	tr.Report("fan1", StallPWMFloor-1, 0, now)
	// Above the floor but tach-less (-1): cannot be judged, not a stall.
	tr.Report("fan2", 255, -1, now)
	if cnt, _ := tr.Snapshot(now); cnt != 0 {
		t.Fatalf("below-floor + tach-less must not count; got %d", cnt)
	}

	// Above the floor, RPM exactly 0 with a working tach: a genuine stall.
	tr.Report("fan1", StallPWMFloor, 0, now)
	tr.Report("fan2", 255, 0, now)
	if !tr.MassStalled(now) {
		t.Fatalf("two genuine stalls (pwm>=floor, rpm==0) must trip")
	}
}

// TestMassStall_RecoveryAndExpiry binds RULE-MASSTALL-WINDOW-01: a channel
// that recovers (non-stall report) clears immediately; a channel that stops
// reporting expires after Window.
func TestMassStall_RecoveryAndExpiry(t *testing.T) {
	tr := New(3*time.Minute, 2)
	now := time.Unix(3000, 0)

	tr.Report("fan1", 200, 0, now)
	tr.Report("fan2", 200, 0, now)
	if !tr.MassStalled(now) {
		t.Fatalf("two stalls must trip")
	}

	// fan1 recovers (RPM > 0): cleared immediately, count drops below threshold.
	tr.Report("fan1", 200, 900, now)
	if tr.MassStalled(now) {
		t.Fatalf("after one fan recovers, count must drop below threshold")
	}
	if cnt, _ := tr.Snapshot(now); cnt != 1 {
		t.Fatalf("only fan2 should remain stalled; got %d", cnt)
	}

	// fan2 goes silent (no further reports). After Window it expires.
	later := now.Add(3*time.Minute + time.Second)
	if cnt, _ := tr.Snapshot(later); cnt != 0 {
		t.Fatalf("a stale stall must expire after Window; got %d", cnt)
	}
}
