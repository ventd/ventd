package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/hal"
	halnbfc "github.com/ventd/ventd/internal/hal/nbfc"
	"github.com/ventd/ventd/internal/watchdog"
)

// TestRegisterNBFCBackend_EmptyDMIIsBenign exercises the no-match branch
// of registerNBFCBackend: an empty sysRoot reads zero-value DMI fields,
// nbfc.Probe returns ErrNBFCNoMatch, the helper logs INFO and does NOT
// register a backend. The contract is "every Probe sentinel is benign".
func TestRegisterNBFCBackend_EmptyDMIIsBenign(t *testing.T) {
	t.Cleanup(hal.Reset)

	root := t.TempDir()
	if err := os.MkdirAll(root+"/sys/class/dmi/id", 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.MkdirAll(root+"/proc", 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(root+"/proc/cpuinfo", []byte("model name\t: stub\nprocessor\t: 0\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registerNBFCBackend(logger, root)

	if _, ok := hal.Backend(halnbfc.BackendName); ok {
		t.Fatalf("expected no backend registered on empty-DMI no-match path")
	}
}

// TestRegisterNBFCWatchdogEntries_NoBackendIsNoOp covers the short-circuit
// when the NBFC backend hasn't been registered (the common case on
// non-laptop hosts). The function MUST NOT panic.
func TestRegisterNBFCWatchdogEntries_NoBackendIsNoOp(t *testing.T) {
	t.Cleanup(hal.Reset)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wd := watchdog.New(logger)
	registerNBFCWatchdogEntries(wd, logger)
}

// TestRegisterNBFCWatchdogEntries_RegistersEachChannel verifies the happy
// path: with an NBFC backend pre-registered exposing N channels, the
// helper wires N watchdog entries (one closure per channel). Triggering
// wd.Restore drives each closure into the backend's Restore method, and
// the counter confirms every channel was visited.
func TestRegisterNBFCWatchdogEntries_RegistersEachChannel(t *testing.T) {
	t.Cleanup(hal.Reset)

	fake := &fakeNBFCBackend{
		channels: []hal.Channel{
			{ID: "/sys/fake/fan1", Role: hal.RoleCase},
			{ID: "/sys/fake/fan2", Role: hal.RoleCPU},
		},
	}
	hal.Register(halnbfc.BackendName, fake)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wd := watchdog.New(logger)
	registerNBFCWatchdogEntries(wd, logger)

	wd.Restore()
	if got := fake.restoreCalls.Load(); got != 2 {
		t.Fatalf("expected 2 Restore calls (one per channel); got %d", got)
	}
}

// fakeNBFCBackend is a minimal hal.FanBackend stub used by the watchdog
// wiring tests. Only Enumerate + Restore are exercised; Read/Write/Close
// are present to satisfy the interface.
type fakeNBFCBackend struct {
	channels     []hal.Channel
	restoreCalls atomic.Int32
}

func (f *fakeNBFCBackend) Name() string                                       { return halnbfc.BackendName }
func (f *fakeNBFCBackend) Enumerate(_ context.Context) ([]hal.Channel, error) { return f.channels, nil }
func (f *fakeNBFCBackend) Read(_ hal.Channel) (hal.Reading, error) {
	return hal.Reading{OK: false}, nil
}
func (f *fakeNBFCBackend) Write(_ hal.Channel, _ uint8) error { return nil }
func (f *fakeNBFCBackend) Restore(_ hal.Channel) error {
	f.restoreCalls.Add(1)
	return nil
}
func (f *fakeNBFCBackend) Close() error { return nil }
