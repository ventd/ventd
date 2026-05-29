package controller

import (
	"testing"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/probe"
)

// TestRuleApplyShadow01_ControllerSuppressesWrites pins RULE-APPLY-SHADOW-01:
// when apply.shadow is true the controller's PWM write funnel
// (writePWMViaPolarity, reached via writeWithRetry) short-circuits and
// NEVER reaches backend.Write — even for a fully controllable,
// normal-polarity channel that would otherwise be written. The control
// sub-test proves the gate (not some other refusal) is what suppresses
// the write by flipping shadow off on an identical setup and observing
// the backend receive exactly one write.
//
// Bound: internal/controller/shadow_test.go:shadow_mode_suppresses_backend_write
// Bound: internal/controller/shadow_test.go:shadow_off_writes_through
func TestRuleApplyShadow01_ControllerSuppressesWrites(t *testing.T) {
	t.Run("shadow_mode_suppresses_backend_write", func(t *testing.T) {
		ff := newFakeFan(t)
		cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 0, 255)
		cfg.Apply.Shadow = true
		c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
		// A perfectly controllable, normal-polarity channel: nothing else
		// would refuse this write, so a zero call count isolates the
		// shadow gate as the cause.
		c.polarityCh = &probe.ControllableChannel{
			PWMPath:  ff.pwmPath,
			Polarity: "normal",
		}
		fb := &fakeErrBackend{}
		c.backend = fb

		ch := hal.Channel{ID: ff.pwmPath}
		if err := c.writeWithRetry(ch, 100, ff.pwmPath, "curve"); err != nil {
			t.Fatalf("shadow write should be a successful skip, got %v", err)
		}
		if fb.writeCalls != 0 {
			t.Errorf("shadow mode: backend.Write called %d times, want 0 (no hardware write)", fb.writeCalls)
		}
	})

	t.Run("shadow_off_writes_through", func(t *testing.T) {
		ff := newFakeFan(t)
		cfg := makeLinearCurveCfg(ff, "cpu fan", "cpu_curve", 0, 255)
		// Shadow explicitly off — identical to the suppression case
		// except for the one flag.
		cfg.Apply.Shadow = false
		c := newTestController(t, ff, cfg, &stubCal{}, "cpu fan", "cpu_curve")
		c.polarityCh = &probe.ControllableChannel{
			PWMPath:  ff.pwmPath,
			Polarity: "normal",
		}
		fb := &fakeErrBackend{}
		c.backend = fb

		ch := hal.Channel{ID: ff.pwmPath}
		if err := c.writeWithRetry(ch, 100, ff.pwmPath, "curve"); err != nil {
			t.Fatalf("writeWithRetry returned unexpected error: %v", err)
		}
		if fb.writeCalls != 1 {
			t.Errorf("shadow off: backend.Write called %d times, want 1", fb.writeCalls)
		}
		if fb.lastWritten != 100 {
			t.Errorf("shadow off: backend got %d, want 100 (normal polarity pass-through)", fb.lastWritten)
		}
	})
}
