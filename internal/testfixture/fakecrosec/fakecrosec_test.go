package fakecrosec_test

import (
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"

	"go.uber.org/goleak"

	"github.com/ventd/ventd/internal/testfixture/fakecrosec"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// ── constructor / DevicePath ─────────────────────────────────────────────────

func TestNew_NilOpts(t *testing.T) {
	f := fakecrosec.New(t, nil)
	if f == nil {
		t.Fatal("New returned nil")
	}
}

func TestNew_EmptyOpts(t *testing.T) {
	f := fakecrosec.New(t, &fakecrosec.Options{})
	if f == nil {
		t.Fatal("New returned nil")
	}
}

func TestDevicePath_NonEmpty(t *testing.T) {
	f := fakecrosec.New(t, nil)
	if f.DevicePath() == "" {
		t.Fatal("DevicePath returned empty string")
	}
}

func TestDevicePath_FileExists(t *testing.T) {
	f := fakecrosec.New(t, nil)
	if _, err := os.Stat(f.DevicePath()); err != nil {
		t.Fatalf("DevicePath file does not exist: %v", err)
	}
}

func TestDevicePath_DistinctPerFixture(t *testing.T) {
	f1 := fakecrosec.New(t, nil)
	f2 := fakecrosec.New(t, nil)
	if f1.DevicePath() == f2.DevicePath() {
		t.Error("distinct fixtures must have distinct device paths")
	}
}

// ── nil opts: no defaults ────────────────────────────────────────────────────

func TestSend_NilOpts_NoHandlerReturnsError(t *testing.T) {
	f := fakecrosec.New(t, nil)
	err := f.Send(0x0001, 0, make([]byte, 4), make([]byte, 4))
	if err == nil {
		t.Fatal("expected error for unregistered cmd, got nil")
	}
}

// ── non-nil opts: built-in defaults ─────────────────────────────────────────

func TestSend_EmptyOpts_HelloDefaultResponds(t *testing.T) {
	f := fakecrosec.New(t, &fakecrosec.Options{})

	// EC_CMD_HELLO: send cookie 0xEC000001, expect cookie + 0x01020304.
	var cookie uint32 = 0xEC000001
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, cookie)

	in := make([]byte, 4)
	if err := f.Send(0x0001, 0, out, in); err != nil {
		t.Fatalf("HELLO send error: %v", err)
	}
	got := binary.LittleEndian.Uint32(in)
	want := cookie + 0x01020304
	if got != want {
		t.Errorf("HELLO response = 0x%08x, want 0x%08x", got, want)
	}
}

func TestSend_EmptyOpts_GetVersionDefaultResponds(t *testing.T) {
	f := fakecrosec.New(t, &fakecrosec.Options{})
	in := make([]byte, 16)
	if err := f.Send(0x0002, 0, nil, in); err != nil {
		t.Fatalf("GET_VERSION send error: %v", err)
	}
	if in[0] == 0 {
		t.Error("GET_VERSION default response was all-zero (expected version string)")
	}
}

func TestSend_EmptyOpts_SetDutyDefaultRecords(t *testing.T) {
	f := fakecrosec.New(t, &fakecrosec.Options{})

	out := []byte{50, 0, 0, 0} // percent = 50
	if err := f.Send(0x0024, 0, out, nil); err != nil {
		t.Fatalf("SET_FAN_DUTY send error: %v", err)
	}
	ws := f.Writes()
	if len(ws) != 1 || ws[0] != 50 {
		t.Errorf("Writes() = %v, want [50]", ws)
	}
}

// ── Handle override ──────────────────────────────────────────────────────────

func TestHandle_OverridesDefault(t *testing.T) {
	f := fakecrosec.New(t, &fakecrosec.Options{})

	called := false
	f.Handle(0x0001, func(cmd, ver uint32, out []byte) ([]byte, error) {
		called = true
		return []byte{0xAA, 0xBB, 0xCC, 0xDD}, nil
	})

	in := make([]byte, 4)
	if err := f.Send(0x0001, 0, make([]byte, 4), in); err != nil {
		t.Fatalf("Send error: %v", err)
	}
	if !called {
		t.Error("Handle override was not invoked")
	}
	if in[0] != 0xAA {
		t.Errorf("response[0] = 0x%02X, want 0xAA", in[0])
	}
}

// ── FailOn ───────────────────────────────────────────────────────────────────

func TestFailOn_ReturnsErrorForCommand(t *testing.T) {
	wantErr := errors.New("EC refused")
	f := fakecrosec.New(t, &fakecrosec.Options{
		FailOn: map[uint32]error{0x0001: wantErr},
	})
	err := f.Send(0x0001, 0, make([]byte, 4), make([]byte, 4))
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

func TestFailOn_UnaffectedCommandSucceeds(t *testing.T) {
	f := fakecrosec.New(t, &fakecrosec.Options{
		FailOn: map[uint32]error{0x0001: errors.New("fail HELLO")},
	})
	// GET_VERSION (0x0002) is not in FailOn, should use the built-in default.
	if err := f.Send(0x0002, 0, nil, make([]byte, 16)); err != nil {
		t.Fatalf("GET_VERSION: unexpected error: %v", err)
	}
}

// ── CommandResponses ─────────────────────────────────────────────────────────

func TestCommandResponses_Override(t *testing.T) {
	custom := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	f := fakecrosec.New(t, &fakecrosec.Options{
		CommandResponses: map[uint32][]byte{0x0001: custom},
	})
	in := make([]byte, 4)
	if err := f.Send(0x0001, 0, make([]byte, 4), in); err != nil {
		t.Fatalf("Send error: %v", err)
	}
	for i, b := range custom {
		if in[i] != b {
			t.Errorf("in[%d] = 0x%02X, want 0x%02X", i, in[i], b)
		}
	}
}

func TestCommandResponses_OverridesFailOn(t *testing.T) {
	// CommandResponses takes priority over FailOn for same cmd.
	f := fakecrosec.New(t, &fakecrosec.Options{
		FailOn:           map[uint32]error{0x0001: errors.New("should not fire")},
		CommandResponses: map[uint32][]byte{0x0001: {0x01, 0x02, 0x03, 0x04}},
	})
	in := make([]byte, 4)
	if err := f.Send(0x0001, 0, make([]byte, 4), in); err != nil {
		t.Fatalf("CommandResponses should override FailOn, got error: %v", err)
	}
}

// ── LockoutAfter ─────────────────────────────────────────────────────────────

func TestLockoutAfter_SucceedsUpToThreshold(t *testing.T) {
	f := fakecrosec.New(t, &fakecrosec.Options{LockoutAfter: 3})

	for i := range 3 {
		if err := f.Send(0x0001, 0, make([]byte, 4), make([]byte, 4)); err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
	}
}

func TestLockoutAfter_EPERMAfterThreshold(t *testing.T) {
	f := fakecrosec.New(t, &fakecrosec.Options{LockoutAfter: 2})

	for range 2 {
		f.Send(0x0001, 0, make([]byte, 4), make([]byte, 4)) //nolint:errcheck
	}

	err := f.Send(0x0001, 0, make([]byte, 4), make([]byte, 4))
	if !errors.Is(err, syscall.EPERM) {
		t.Errorf("call after lockout: err = %v, want EPERM", err)
	}
}

func TestLockoutAfter_ZeroDisablesLockout(t *testing.T) {
	f := fakecrosec.New(t, &fakecrosec.Options{LockoutAfter: 0})

	for i := range 10 {
		if err := f.Send(0x0001, 0, make([]byte, 4), make([]byte, 4)); err != nil {
			t.Fatalf("call %d: unexpected error with LockoutAfter=0: %v", i+1, err)
		}
	}
}

// ── Writes ───────────────────────────────────────────────────────────────────

func TestWrites_RecordsMultipleDutyValues(t *testing.T) {
	f := fakecrosec.New(t, &fakecrosec.Options{})

	for _, pct := range []uint32{25, 50, 75, 100} {
		out := []byte{byte(pct), 0, 0, 0}
		if err := f.Send(0x0024, 0, out, nil); err != nil {
			t.Fatalf("SET_FAN_DUTY(%d): %v", pct, err)
		}
	}

	ws := f.Writes()
	if len(ws) != 4 {
		t.Fatalf("Writes() len = %d, want 4", len(ws))
	}
	for i, want := range []uint32{25, 50, 75, 100} {
		if ws[i] != want {
			t.Errorf("Writes()[%d] = %d, want %d", i, ws[i], want)
		}
	}
}

func TestWrites_NilOptsNotPopulated(t *testing.T) {
	f := fakecrosec.New(t, nil)
	// No default handler, so there's nothing to record — Writes should be empty.
	ws := f.Writes()
	if len(ws) != 0 {
		t.Errorf("Writes() = %v, want empty (nil opts)", ws)
	}
}

// ── concurrent safety ────────────────────────────────────────────────────────

func TestSend_ConcurrentSafe(t *testing.T) {
	f := fakecrosec.New(t, &fakecrosec.Options{})
	const workers = 20
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			f.Send(0x0001, 0, make([]byte, 4), make([]byte, 4)) //nolint:errcheck
		}()
	}
	wg.Wait()
}

func TestHandle_ConcurrentSafe(t *testing.T) {
	f := fakecrosec.New(t, nil)
	var wg sync.WaitGroup
	const workers = 10
	wg.Add(workers * 2)
	for range workers {
		go func() {
			defer wg.Done()
			f.Handle(0x0001, fakecrosec.HelloHandler())
		}()
		go func() {
			defer wg.Done()
			f.Send(0x0001, 0, make([]byte, 4), make([]byte, 4)) //nolint:errcheck
		}()
	}
	wg.Wait()
}
