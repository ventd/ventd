package controller

import (
	"bytes"
	"log/slog"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/testfixture/faultbackend"
	"github.com/ventd/ventd/internal/watchdog"
)

// faultControllerWithLog wires a controller around a fault-injecting backend and
// returns it plus a log buffer. fanType stays "hwmon" so tick() builds the
// channel inline and dispatches through c.backend (the injected fake), exactly
// like the production write path — only the syscall result is scripted.
func faultControllerWithLog(t *testing.T, fb *faultbackend.Backend) (*Controller, *bytes.Buffer) {
	t.Helper()
	ff := newFakeFan(t) // temp1_input pre-set to 60 °C → curve wants 128
	cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 40, 200)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	wd := watchdog.New(logger)
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(cfg)
	c := New("cpu fan", "cpu_curve", ff.pwmPath, "hwmon", cfgPtr, wd, &stubCal{}, logger)
	c.backend = fb
	return c, &buf
}

// TestController_TransientWriteErrorRecoversOnRetry pins the controller's
// resilience to a single sysfs hiccup (EIO): the first Write errors, the
// retry succeeds, the fan is NOT handed back to firmware. A file-backed sim
// cannot make a write return EIO, so this needs the injecting backend. Without
// the retry path a transient errno would strand the fan on its last duty.
func TestController_TransientWriteErrorRecoversOnRetry(t *testing.T) {
	fb := faultbackend.New("fault", faultbackend.Channel("cpu"))
	fb.WriteErrs = []error{syscall.EIO} // one transient failure, then success
	c, buf := faultControllerWithLog(t, fb)

	c.tick()

	if got := len(fb.Writes); got != 2 {
		t.Fatalf("Write attempts = %d, want 2 (initial EIO + successful retry)", got)
	}
	log := buf.String()
	if !strings.Contains(log, "write_retry_succeeded") {
		t.Errorf("expected write_retry_succeeded event; log:\n%s", log)
	}
	if strings.Contains(log, "write_failed_restore_triggered") {
		t.Error("fan was handed back to firmware after a transient EIO that the retry recovered")
	}
	// The retry (second attempt) must have committed the curve duty (60 °C = 128)
	// with no error. Inspect the record directly — the controller keys the
	// channel by its pwm path, not the fake's configured channel name.
	retry := fb.Writes[1]
	if retry.Err != nil || retry.PWM != 128 {
		t.Errorf("retry write = {pwm:%d err:%v}, want {pwm:128 err:<nil>} (recovered duty committed)", retry.PWM, retry.Err)
	}
}

// TestController_PersistentEBUSYStormHandsBackAndSurvives pins behaviour under a
// sustained EBUSY storm — a BIOS contesting manual mode the backend can't
// reclaim. Every tick both the initial Write and the retry return EBUSY, so
// every tick triggers a hand-back to firmware; across many ticks the controller
// must keep doing exactly that without crashing or wedging. EBUSY is a write
// *syscall* error, impossible to express with tools/hwmonsim's files.
func TestController_PersistentEBUSYStormHandsBackAndSurvives(t *testing.T) {
	fb := faultbackend.New("fault", faultbackend.Channel("cpu"))
	fb.WritePolicy = faultbackend.AlwaysFail(syscall.EBUSY) // never clears
	c, buf := faultControllerWithLog(t, fb)

	const ticks = 4
	for range [ticks]struct{}{} {
		c.tick()
	}

	// Two Write attempts per tick (initial + one retry), both EBUSY.
	if got := len(fb.Writes); got != 2*ticks {
		t.Errorf("Write attempts = %d, want %d (2 per tick across %d ticks)", got, 2*ticks, ticks)
	}
	for _, w := range fb.Writes {
		if w.Err != syscall.EBUSY {
			t.Errorf("Write returned %v, want EBUSY on every attempt during the storm", w.Err)
		}
	}
	// Every tick must trigger the restore (hand-back), not just the first.
	if n := strings.Count(buf.String(), "write_failed_restore_triggered"); n != ticks {
		t.Errorf("restore-triggered events = %d, want %d (one per stormed tick)", n, ticks)
	}
	// No duty ever committed — the storm never let a write land (every record
	// carries the injected error; none is nil).
	for i, w := range fb.Writes {
		if w.Err == nil {
			t.Errorf("Write record %d committed (err=nil) during a total EBUSY storm; none should succeed", i)
		}
	}
}
