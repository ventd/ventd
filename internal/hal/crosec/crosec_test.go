package crosec_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hal/crosec"
	"github.com/ventd/ventd/internal/testfixture/fakecrosec"
)

// --- HELLO gate ---

func TestEnumerate_HelloGateFails_EmptyResult(t *testing.T) {
	f := fakecrosec.New(t)
	f.Handle(0x0001, fakecrosec.ErrorHandler(errors.New("hello refused")))

	b := crosec.NewBackendForTest(nil, f.Send)
	chs, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chs) != 0 {
		t.Fatalf("expected 0 channels when HELLO fails, got %d", len(chs))
	}
}

func TestEnumerate_HelloBadMagic_EmptyResult(t *testing.T) {
	f := fakecrosec.New(t)
	// Return a bad magic value.
	f.Handle(0x0001, func(cmd, ver uint32, out []byte) ([]byte, error) {
		resp := make([]byte, 4)
		resp[0] = 0xFF // definitely not in_data + 0x01020304
		return resp, nil
	})

	b := crosec.NewBackendForTest(nil, f.Send)
	chs, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chs) != 0 {
		t.Fatalf("expected 0 channels on bad HELLO magic, got %d", len(chs))
	}
}

func TestEnumerate_NoHandler_EmptyResult(t *testing.T) {
	// Simulate no device: sender returns an error for every call.
	f := fakecrosec.New(t) // no handlers registered

	b := crosec.NewBackendForTest(nil, f.Send)
	chs, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chs) != 0 {
		t.Fatalf("expected 0 channels with no HELLO handler, got %d", len(chs))
	}
}

// --- Happy-path Enumerate ---

func TestEnumerate_HelloOK_OneChannel(t *testing.T) {
	f := fakecrosec.New(t)
	f.Handle(0x0001, fakecrosec.HelloHandler())

	b := crosec.NewBackendForTest(nil, f.Send)
	chs, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chs) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(chs))
	}
	ch := chs[0]
	if ch.ID != "0" {
		t.Errorf("channel ID = %q, want %q", ch.ID, "0")
	}
	wantCaps := hal.CapRead | hal.CapWritePWM | hal.CapRestore
	if ch.Caps != wantCaps {
		t.Errorf("caps = %b, want %b", ch.Caps, wantCaps)
	}
}

// --- Happy-path Read ---

func TestRead_ReturnsRPM(t *testing.T) {
	f := fakecrosec.New(t)
	f.Handle(0x0001, fakecrosec.HelloHandler())
	f.Handle(0x0020, fakecrosec.RPMHandler(2400))

	b := crosec.NewBackendForTest(nil, f.Send)
	chs, _ := b.Enumerate(context.Background())
	if len(chs) == 0 {
		t.Fatal("enumerate returned no channels")
	}
	r, err := b.Read(chs[0])
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if !r.OK {
		t.Fatal("Reading.OK is false")
	}
	if r.RPM != 2400 {
		t.Errorf("RPM = %d, want 2400", r.RPM)
	}
}

func TestRead_ECError_NotOK(t *testing.T) {
	f := fakecrosec.New(t)
	f.Handle(0x0001, fakecrosec.HelloHandler())
	f.Handle(0x0020, fakecrosec.ErrorHandler(errors.New("EC transient error")))

	b := crosec.NewBackendForTest(nil, f.Send)
	chs, _ := b.Enumerate(context.Background())
	if len(chs) == 0 {
		t.Fatal("enumerate returned no channels")
	}
	r, err := b.Read(chs[0])
	if err != nil {
		t.Fatalf("Read returned unexpected non-nil error: %v", err)
	}
	if r.OK {
		t.Fatal("Reading.OK should be false on EC error")
	}
}

// --- Happy-path Write ---

func TestWrite_ScalesPWMToPercent(t *testing.T) {
	f := fakecrosec.New(t)
	f.Handle(0x0001, fakecrosec.HelloHandler())

	var gotPercent uint32
	f.Handle(0x0024, fakecrosec.SetDutyHandler(func(p uint32) { atomic.StoreUint32(&gotPercent, p) }))

	b := crosec.NewBackendForTest(nil, f.Send)
	chs, _ := b.Enumerate(context.Background())
	if len(chs) == 0 {
		t.Fatal("enumerate returned no channels")
	}

	// pwm=128 → percent ≈ 50 (128*100/255 = 50)
	if err := b.Write(chs[0], 128); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if p := atomic.LoadUint32(&gotPercent); p != 50 {
		t.Errorf("percent = %d, want 50", p)
	}

	// pwm=255 → percent = 100
	if err := b.Write(chs[0], 255); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if p := atomic.LoadUint32(&gotPercent); p != 100 {
		t.Errorf("percent = %d for pwm=255, want 100", p)
	}

	// pwm=0 → percent = 0
	if err := b.Write(chs[0], 0); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if p := atomic.LoadUint32(&gotPercent); p != 0 {
		t.Errorf("percent = %d for pwm=0, want 0", p)
	}
}

// --- Restore hands back to auto ---

func TestRestore_SendsAutoFanCtrl(t *testing.T) {
	f := fakecrosec.New(t)
	f.Handle(0x0001, fakecrosec.HelloHandler())

	var restoreCalled int32
	f.Handle(0x0052, fakecrosec.AutoFanCtrlHandler(func() { atomic.StoreInt32(&restoreCalled, 1) }))

	b := crosec.NewBackendForTest(nil, f.Send)
	chs, _ := b.Enumerate(context.Background())
	if len(chs) == 0 {
		t.Fatal("enumerate returned no channels")
	}
	if err := b.Restore(chs[0]); err != nil {
		t.Fatalf("Restore error: %v", err)
	}
	if atomic.LoadInt32(&restoreCalled) != 1 {
		t.Error("EC_CMD_THERMAL_AUTO_FAN_CTRL was not called by Restore")
	}
}

// --- Lockout handling ---

func TestWrite_LockoutTriggersRestore(t *testing.T) {
	f := fakecrosec.New(t)
	f.Handle(0x0001, fakecrosec.HelloHandler())

	writeErr := errors.New("write refused")
	f.Handle(0x0024, fakecrosec.ErrorHandler(writeErr))

	var restoreCalled int32
	f.Handle(0x0052, fakecrosec.AutoFanCtrlHandler(func() { atomic.AddInt32(&restoreCalled, 1) }))

	b := crosec.NewBackendForTest(nil, f.Send)
	chs, _ := b.Enumerate(context.Background())
	if len(chs) == 0 {
		t.Fatal("enumerate returned no channels")
	}

	// Drive failures up to the threshold (maxConsecutiveFailures = 5).
	// The 5th Write should trigger Restore.
	for i := 0; i < 4; i++ {
		if err := b.Write(chs[0], 128); err == nil {
			t.Fatalf("Write %d: expected error, got nil", i+1)
		}
		if atomic.LoadInt32(&restoreCalled) != 0 {
			t.Fatalf("Restore triggered too early at iteration %d", i+1)
		}
	}
	if err := b.Write(chs[0], 128); err == nil {
		t.Fatal("5th Write: expected error, got nil")
	}
	if atomic.LoadInt32(&restoreCalled) != 1 {
		t.Error("Restore should have been called after 5 consecutive failures")
	}
}
