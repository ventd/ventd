package watchdog

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/hal"
)

type fakeRestoreBackend struct {
	name         string
	chID         string
	restoredID   atomic.Value // string
	restoreCalls atomic.Int32
}

func (f *fakeRestoreBackend) Name() string { return f.name }
func (f *fakeRestoreBackend) Close() error { return nil }
func (f *fakeRestoreBackend) Enumerate(context.Context) ([]hal.Channel, error) {
	return []hal.Channel{{ID: f.chID, Role: hal.RoleGPU, Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore}}, nil
}
func (f *fakeRestoreBackend) Read(hal.Channel) (hal.Reading, error) {
	return hal.Reading{OK: false}, nil
}
func (f *fakeRestoreBackend) Write(hal.Channel, uint8) error { return nil }
func (f *fakeRestoreBackend) Restore(ch hal.Channel) error {
	f.restoreCalls.Add(1)
	f.restoredID.Store(ch.ID)
	return nil
}

// TestRestore_RegistryBackendRestoredViaOwnBackend is the regression guard for
// RULE-WD-RESTORE-REGISTRY-BACKEND: a non-hwmon registry-backed channel
// (amdgpu, corsair, thinkpad, …) is restored through its OWN HAL backend
// (matched by channel ID via Enumerate), not silently routed to the hwmon
// backend — which would target a nonexistent pwm_enable and leave the fan in
// manual mode on exit.
func TestRestore_RegistryBackendRestoredViaOwnBackend(t *testing.T) {
	hal.Reset()
	t.Cleanup(hal.Reset)
	fb := &fakeRestoreBackend{name: "faketest", chID: "fakechan"}
	hal.Register(fb.name, fb)

	wd := New(slog.Default())
	wd.Register("fakechan", "faketest")
	wd.Restore()

	if got := fb.restoreCalls.Load(); got != 1 {
		t.Fatalf("fake backend Restore called %d times, want 1 (registry channel must restore via its own backend)", got)
	}
	if got, _ := fb.restoredID.Load().(string); got != "fakechan" {
		t.Errorf("restored channel ID = %q, want %q", got, "fakechan")
	}
}
