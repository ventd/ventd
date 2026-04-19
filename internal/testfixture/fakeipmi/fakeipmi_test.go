package fakeipmi_test

import (
	"errors"
	"os"
	"sync"
	"testing"

	"go.uber.org/goleak"

	"github.com/ventd/ventd/internal/testfixture/fakeipmi"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// ── constructor / DevicePath ─────────────────────────────────────────────────

func TestNew_NilOpts(t *testing.T) {
	f := fakeipmi.New(t, nil)
	if f == nil {
		t.Fatal("New returned nil")
	}
}

func TestDevicePath_NonEmpty(t *testing.T) {
	f := fakeipmi.New(t, nil)
	if f.DevicePath() == "" {
		t.Fatal("DevicePath returned empty string")
	}
}

func TestDevicePath_FileExists(t *testing.T) {
	f := fakeipmi.New(t, nil)
	if _, err := os.Stat(f.DevicePath()); err != nil {
		t.Fatalf("DevicePath file does not exist: %v", err)
	}
}

func TestDevicePath_DistinctPerFixture(t *testing.T) {
	f1 := fakeipmi.New(t, nil)
	f2 := fakeipmi.New(t, nil)
	if f1.DevicePath() == f2.DevicePath() {
		t.Error("distinct fixtures must have distinct device paths")
	}
}

// ── canned responses per vendor ──────────────────────────────────────────────

func TestRespond_Supermicro_GetSensorReading(t *testing.T) {
	f := fakeipmi.New(t, &fakeipmi.Options{Vendor: "supermicro"})
	resp, err := f.Respond(0x04, 0x2D, []byte{0x01})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("empty response")
	}
	if resp[0] != 0x00 {
		t.Errorf("completion code = 0x%02X, want 0x00", resp[0])
	}
}

func TestRespond_Supermicro_SetFanControl(t *testing.T) {
	f := fakeipmi.New(t, &fakeipmi.Options{Vendor: "supermicro"})
	// netfn=0x30 cmd=0x70 is the SM fan write command.
	resp, err := f.Respond(0x30, 0x70, []byte{0x66, 0x01, 0x00, 0x32})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp[0] != 0x00 {
		t.Errorf("completion code = 0x%02X, want 0x00", resp[0])
	}
}

func TestRespond_Dell_GetSensorReading(t *testing.T) {
	f := fakeipmi.New(t, &fakeipmi.Options{Vendor: "dell"})
	resp, err := f.Respond(0x04, 0x2D, []byte{0x01})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp[0] != 0x00 {
		t.Errorf("completion code = 0x%02X, want 0x00", resp[0])
	}
}

func TestRespond_Dell_SetFanControl(t *testing.T) {
	f := fakeipmi.New(t, &fakeipmi.Options{Vendor: "dell"})
	resp, err := f.Respond(0x30, 0x30, []byte{0x01})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp[0] != 0x00 {
		t.Errorf("completion code = 0x%02X, want 0x00", resp[0])
	}
}

func TestRespond_HPE_ReadSucceeds(t *testing.T) {
	f := fakeipmi.New(t, &fakeipmi.Options{Vendor: "hpe"})
	resp, err := f.Respond(0x04, 0x2D, []byte{0x01})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp[0] != 0x00 {
		t.Errorf("completion code = 0x%02X, want 0x00", resp[0])
	}
}

func TestRespond_HPE_WriteReturnsNotImplemented(t *testing.T) {
	f := fakeipmi.New(t, &fakeipmi.Options{Vendor: "hpe"})
	resp, err := f.Respond(0x30, 0x30, []byte{0x01, 0x00, 0x32})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp[0] != 0xC1 {
		t.Errorf("completion code = 0x%02X, want 0xC1 (not implemented)", resp[0])
	}
}

// ── BusyCount backoff ────────────────────────────────────────────────────────

func TestBusyCount_ReturnsNodeBusyNTimes(t *testing.T) {
	const n = 3
	f := fakeipmi.New(t, &fakeipmi.Options{Vendor: "supermicro", BusyCount: n})

	for i := range n {
		resp, err := f.Respond(0x04, 0x2D, []byte{0x01})
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if resp[0] != 0xC3 {
			t.Errorf("call %d: completion code = 0x%02X, want 0xC3 (node busy)", i, resp[0])
		}
	}
}

func TestBusyCount_SucceedsAfterExhaustion(t *testing.T) {
	const n = 2
	f := fakeipmi.New(t, &fakeipmi.Options{Vendor: "supermicro", BusyCount: n})

	for range n {
		f.Respond(0x04, 0x2D, []byte{0x01}) //nolint:errcheck
	}

	resp, err := f.Respond(0x04, 0x2D, []byte{0x01})
	if err != nil {
		t.Fatalf("post-busy call: unexpected error: %v", err)
	}
	if resp[0] != 0x00 {
		t.Errorf("post-busy completion code = 0x%02X, want 0x00", resp[0])
	}
}

func TestBusyCount_ZeroMeansNoBusy(t *testing.T) {
	f := fakeipmi.New(t, &fakeipmi.Options{Vendor: "supermicro", BusyCount: 0})
	resp, err := f.Respond(0x04, 0x2D, []byte{0x01})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp[0] == 0xC3 {
		t.Error("BusyCount=0 should not return 0xC3")
	}
}

// ── FailOn ───────────────────────────────────────────────────────────────────

func TestFailOn_ReturnsErrorAtMatchingSequence(t *testing.T) {
	wantErr := errors.New("ioctl: device error")
	f := fakeipmi.New(t, &fakeipmi.Options{
		Vendor: "supermicro",
		FailOn: map[uint64]error{0: wantErr},
	})
	_, err := f.Respond(0x04, 0x2D, []byte{0x01})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

func TestFailOn_OnlyAffectsMatchingSequence(t *testing.T) {
	wantErr := errors.New("fail on seq 1")
	f := fakeipmi.New(t, &fakeipmi.Options{
		Vendor: "supermicro",
		FailOn: map[uint64]error{1: wantErr},
	})

	// seq=0: no failure
	resp, err := f.Respond(0x04, 0x2D, []byte{0x01})
	if err != nil {
		t.Fatalf("seq=0: unexpected error: %v", err)
	}
	if resp[0] != 0x00 {
		t.Errorf("seq=0: completion code = 0x%02X, want 0x00", resp[0])
	}

	// seq=1: must fail
	_, err = f.Respond(0x04, 0x2D, []byte{0x01})
	if !errors.Is(err, wantErr) {
		t.Errorf("seq=1: err = %v, want %v", err, wantErr)
	}

	// seq=2: no failure again
	resp, err = f.Respond(0x04, 0x2D, []byte{0x01})
	if err != nil {
		t.Fatalf("seq=2: unexpected error: %v", err)
	}
	if resp[0] != 0x00 {
		t.Errorf("seq=2: completion code = 0x%02X, want 0x00", resp[0])
	}
}

// ── SensorResponses override ─────────────────────────────────────────────────

func TestSensorResponses_Override(t *testing.T) {
	custom := []byte{0x00, 0xAB, 0xCD}
	f := fakeipmi.New(t, &fakeipmi.Options{
		Vendor:          "supermicro",
		SensorResponses: map[byte][]byte{0x05: custom},
	})
	resp, err := f.Respond(0x04, 0x2D, []byte{0x05})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp) != len(custom) {
		t.Fatalf("response len = %d, want %d", len(resp), len(custom))
	}
	for i, b := range custom {
		if resp[i] != b {
			t.Errorf("resp[%d] = 0x%02X, want 0x%02X", i, resp[i], b)
		}
	}
}

func TestSensorResponses_FallsBackToDefault(t *testing.T) {
	f := fakeipmi.New(t, &fakeipmi.Options{
		Vendor:          "supermicro",
		SensorResponses: map[byte][]byte{0x05: {0x00, 0xAB}},
	})
	// sensor 0x01 has no override — should use default
	resp, err := f.Respond(0x04, 0x2D, []byte{0x01})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp[0] != 0x00 {
		t.Errorf("completion code = 0x%02X, want 0x00 (default)", resp[0])
	}
}

// ── unknown vendor ───────────────────────────────────────────────────────────

func TestRespond_UnknownVendor_ReturnsError(t *testing.T) {
	f := fakeipmi.New(t, &fakeipmi.Options{Vendor: "zyx-unknown"})
	_, err := f.Respond(0x04, 0x2D, []byte{0x01})
	if err == nil {
		t.Fatal("expected error for unknown vendor, got nil")
	}
}

// ── goroutine safety ─────────────────────────────────────────────────────────

func TestRespond_ConcurrentSafe(t *testing.T) {
	f := fakeipmi.New(t, &fakeipmi.Options{Vendor: "supermicro", BusyCount: 5})
	const workers = 20
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			f.Respond(0x04, 0x2D, []byte{0x01}) //nolint:errcheck
		}()
	}
	wg.Wait()
}
