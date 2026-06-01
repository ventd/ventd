package controller

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/massstall"
	"github.com/ventd/ventd/internal/testfixture/faultbackend"
	"github.com/ventd/ventd/internal/watchdog"
)

// newStallFan stands up an independent fan (its own fakehwmon tree + Controller)
// whose committed-tick stall reports feed the shared tracker, plus a setter for
// its tach. Temp is parked at 85 °C so the curve commands ~200 (above the
// stiction floor), and the fan starts spinning.
func newStallFan(t *testing.T, tracker *massstall.Tracker, name string) (*Controller, func(rpm string)) {
	t.Helper()
	ff := newFakeFan(t)
	chipDir := filepath.Dir(ff.pwmPath)
	tachPath := filepath.Join(chipDir, "fan1_input")
	writeTempAttr(t, chipDir, "temp1_input", "85000")
	setTach := func(rpm string) {
		t.Helper()
		if err := os.WriteFile(tachPath, []byte(rpm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setTach("1500") // start spinning
	cfg := makeLinearCurveCfg(ff, name, name+"_curve", 40, 200)
	logger := silentLogger()
	wd := watchdog.New(logger)
	c := New(name, name+"_curve", ff.pwmPath, "hwmon", cfgAtomicPtr(cfg), wd, &stubCal{}, logger,
		WithStallReporter(ff.pwmPath, func(ch string, pwm uint8, rpm int32, now time.Time) {
			tracker.Report(ch, pwm, rpm, now)
		}))
	return c, setTach
}

// TestMultiFan_MassStallTripsAtThresholdEndToEnd drives THREE independent
// controllers, each reading its own real fan*_input through the real hwmon
// backend, into one shared massstall.Tracker (minChannels=2). It pins the
// system-wide mass-stall gate end to end: one stalled fan is below threshold,
// a second concurrently-stalled fan trips it, and recovering one drops back
// below. massstall_test.go covers the Tracker in isolation (direct Report
// calls); this covers the real controller → tracker integration across fans.
func TestMultiFan_MassStallTripsAtThresholdEndToEnd(t *testing.T) {
	tracker := massstall.New(time.Minute, 2) // 2 of 3 fans stalled = "mass"
	c1, tach1 := newStallFan(t, tracker, "fan1")
	c2, tach2 := newStallFan(t, tracker, "fan2")
	c3, _ := newStallFan(t, tracker, "fan3")

	tickAll := func() { c1.tick(); c2.tick(); c3.tick() }

	// All spinning → nothing stalled.
	tickAll()
	if tracker.MassStalled(time.Now()) {
		t.Fatal("mass-stall flagged with all three fans spinning")
	}

	// Fan1 stalls — 1 of 3, below the threshold of 2.
	tach1("0")
	c1.tick()
	if tracker.MassStalled(time.Now()) {
		t.Errorf("mass-stall flagged with only 1 fan stalled (threshold is 2)")
	}

	// Fan2 also stalls — 2 of 3 → trips.
	tach2("0")
	c2.tick()
	if !tracker.MassStalled(time.Now()) {
		t.Errorf("2 of 3 fans stalled but mass-stall did not trip (threshold 2)")
	}

	// Fan1 recovers — back to 1 stalled, below threshold.
	tach1("1500")
	c1.tick()
	if tracker.MassStalled(time.Now()) {
		t.Errorf("mass-stall still flagged after a fan recovered (1 < threshold 2)")
	}
}

// TestMultiFan_StuckFanDoesNotDisruptHealthySibling pins fault isolation across
// fans: one fan in a sustained EBUSY storm (fault-injecting backend) hands back
// every tick, while a sibling fan on a healthy backend keeps committing its
// curve duty undisturbed. A stuck/contested fan must never take its neighbours
// down with it.
func TestMultiFan_StuckFanDoesNotDisruptHealthySibling(t *testing.T) {
	// Healthy fan: real hwmon backend over a fakehwmon tree.
	healthy, _ := newStallFan(t, massstall.New(time.Minute, 1), "healthy")
	// Re-park at 60 °C so the committed duty is a clean curve value (128).
	writeTempAttr(t, filepath.Dir(healthy.pwmPath), "temp1_input", "60000")

	// Stuck fan: fault backend that always returns EBUSY on write.
	fb := faultbackend.New("fault", faultbackend.Channel("stuck"))
	fb.WritePolicy = faultbackend.AlwaysFail(syscall.EBUSY)
	stuck, _ := faultControllerWithLog(t, fb)

	for i := 0; i < 4; i++ {
		stuck.tick()
		healthy.tick()
	}

	// The stuck fan stormed: every Write attempt errored, none committed.
	for _, w := range fb.Writes {
		if w.Err != syscall.EBUSY {
			t.Errorf("stuck fan Write returned %v, want EBUSY throughout the storm", w.Err)
		}
	}
	// The healthy sibling kept committing its real duty (128) the whole time.
	if got := readPWMByte(t, healthy.pwmPath); got != 128 {
		t.Errorf("healthy fan PWM = %d, want 128 — a sibling's EBUSY storm disrupted it", got)
	}
}
