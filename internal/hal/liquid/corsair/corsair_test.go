package corsair

import (
	"errors"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hal/liquid"
	"github.com/ventd/ventd/internal/hal/usbbase/hidraw"
	"github.com/ventd/ventd/internal/testfixture/fakehid"
)

// makeBackend constructs a corsairBackend backed by a CorsairPipe for unit tests.
func makeBackend(cfg fakehid.CorsairConfig, opts ProbeOptions, path string) (*corsairBackend, *fakehid.CorsairPipe, *fakehid.CorsairDevice) {
	dev := fakehid.NewCorsairDevice(cfg)
	pipe := fakehid.NewCorsairPipe(dev)
	entry, _ := lookupDevice(cfg.PID)
	pd := &probedDevice{
		hid:     pipe,
		info:    hidraw.DeviceInfo{Path: path, VendorID: cfg.VID, ProductID: cfg.PID},
		hasPump: entry.hasPump,
	}
	pumpMin := opts.PumpMinimum
	if pumpMin == 0 {
		pumpMin = defaultPumpMinimum
	}
	b := &corsairBackend{
		inner:    pd,
		writable: opts.UnsafeCorsairWrites && firmwareAllowList[firmwareVersion{}],
		pumpMin:  pumpMin,
		channels: buildChannels(entry),
	}
	return b, pipe, dev
}

// TestProbe_KnownFirmwareReturnsLive verifies that probeWith with a firmware
// version on the allow-list AND the unsafe flag produces a writable backend.
func TestProbe_KnownFirmwareReturnsLive(t *testing.T) {
	fw := firmwareVersion{major: 9, minor: 9, patch: 9}
	firmwareAllowList[fw] = true
	defer delete(firmwareAllowList, fw)

	openFn := func(path string) (openResult, error) {
		d := fakehid.NewCorsairDevice(fakehid.CorsairConfig{
			VID: 0x1b1c, PID: 0x0c1c,
			FirmwareMajor: 9, FirmwareMinor: 9, FirmwarePatch: 9,
		})
		return openResult{
			hid:  fakehid.NewCorsairPipe(d),
			info: hidraw.DeviceInfo{ProductID: 0x0c1c, VendorID: 0x1b1c, Path: path},
		}, nil
	}

	b, err := probeWith("/dev/hidraw9999", ProbeOptions{UnsafeCorsairWrites: true, PumpMinimum: 50}, openFn)
	if err != nil {
		t.Fatalf("probeWith: %v", err)
	}
	defer func() { _ = b.Close() }()

	cb, ok := b.(*corsairBackend)
	if !ok {
		t.Fatalf("expected *corsairBackend, got %T", b)
	}
	if !cb.writable {
		t.Error("expected writable=true when flag set and firmware allow-listed")
	}
}

// TestProbe_UnknownFirmwareReturnsReadOnly verifies that probeWith with a firmware
// version NOT on the allow-list produces a read-only backend regardless of the flag.
func TestProbe_UnknownFirmwareReturnsReadOnly(t *testing.T) {
	openFn := func(path string) (openResult, error) {
		d := fakehid.NewCorsairDevice(fakehid.CorsairConfig{
			VID: 0x1b1c, PID: 0x0c1c,
			FirmwareMajor: 1, FirmwareMinor: 2, FirmwarePatch: 3,
		})
		return openResult{
			hid:  fakehid.NewCorsairPipe(d),
			info: hidraw.DeviceInfo{ProductID: 0x0c1c, VendorID: 0x1b1c, Path: path},
		}, nil
	}

	b, err := probeWith("/dev/hidraw9999", ProbeOptions{UnsafeCorsairWrites: true, PumpMinimum: 50}, openFn)
	if err != nil {
		t.Fatalf("probeWith: %v", err)
	}
	defer func() { _ = b.Close() }()

	cb := b.(*corsairBackend)
	if cb.writable {
		t.Error("expected writable=false when firmware not on allow-list")
	}
}

// TestReadOnly_SetDutyReturnsErrReadOnly verifies that Write returns
// ErrReadOnlyUnvalidatedFirmware when writable=false.
func TestReadOnly_SetDutyReturnsErrReadOnly(t *testing.T) {
	cfg := fakehid.CorsairConfig{VID: 0x1b1c, PID: 0x0c1c}
	b, _, _ := makeBackend(cfg, ProbeOptions{PumpMinimum: 50}, "/dev/hidraw0")
	b.writable = false

	ch := hal.Channel{ID: "/dev/hidraw0-0"}
	err := b.Write(ch, 80)
	if !errors.Is(err, liquid.ErrReadOnlyUnvalidatedFirmware) {
		t.Errorf("got %v, want ErrReadOnlyUnvalidatedFirmware", err)
	}
}

// TestEncodedFrame_DutyBytesLittleEndian verifies that encodeDutyLE produces
// a little-endian 2-byte encoding of the duty cycle.
func TestEncodedFrame_DutyBytesLittleEndian(t *testing.T) {
	cases := []struct {
		duty   uint8
		wantLo byte
		wantHi byte
	}{
		{0, 0x00, 0x00},
		{1, 0x01, 0x00},
		{80, 0x50, 0x00},
		{255, 0xff, 0x00},
	}
	for _, tc := range cases {
		enc := encodeDutyLE(tc.duty)
		if enc[0] != tc.wantLo || enc[1] != tc.wantHi {
			t.Errorf("encodeDutyLE(%d) = [%02x %02x], want [%02x %02x]",
				tc.duty, enc[0], enc[1], tc.wantLo, tc.wantHi)
		}
	}
}

// TestStaleResponseLoop_TerminatesAfterCap verifies that sendCommand stops after
// maxStaleResponses stale reads and returns an error rather than looping forever.
func TestStaleResponseLoop_TerminatesAfterCap(t *testing.T) {
	// staleOnlyPipe always returns a response with resp[0]=0xFF (not responseID),
	// simulating a device that sends stale frames continuously.
	pipe := &staleOnlyPipe{}

	_, err := sendCommand(pipe, []byte{cmdWake})
	if err == nil {
		t.Fatal("sendCommand: expected error after stale-response cap, got nil")
	}
}

// staleOnlyPipe implements hidIO and always returns a frame where resp[0] != responseID.
type staleOnlyPipe struct{}

func (p *staleOnlyPipe) Write(b []byte) (int, error) { return len(b), nil }
func (p *staleOnlyPipe) Read(b []byte) (int, error) {
	b[0] = 0xFF // != responseID (0x00)
	return frameSize, nil
}
func (p *staleOnlyPipe) SetReadDeadline(t time.Time) error { return nil }
func (p *staleOnlyPipe) Close() error                      { return nil }

// TestConcurrentReads_Serialized verifies that concurrent Read calls on the
// same backend do not race on the per-device mutex. This is a data-race check
// best run with -race; any structural failure (panic, wrong result) is also caught.
func TestConcurrentReads_Serialized(t *testing.T) {
	cfg := fakehid.CorsairConfig{
		VID: 0x1b1c, PID: 0x0c1c, HasPump: true,
		Speeds: []uint16{1200, 900, 0, 0, 0, 0, 0},
	}
	b, _, _ := makeBackend(cfg, ProbeOptions{PumpMinimum: 50}, "/dev/hidraw0")

	ch := hal.Channel{ID: "/dev/hidraw0-0"}
	done := make(chan struct{})
	var panicked bool

	go func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
			close(done)
		}()
		for i := 0; i < 5; i++ {
			_, _ = b.Read(ch)
		}
	}()

	for i := 0; i < 5; i++ {
		_, _ = b.Read(ch)
	}

	<-done
	if panicked {
		t.Error("concurrent Read calls panicked")
	}
}

// TestSetCurve_PreservesOtherChannels verifies that restoring one channel via
// Restore does not affect the fixture state for other channels (i.e., each
// doRestoreChannel call uses the correct channel index).
func TestSetCurve_PreservesOtherChannels(t *testing.T) {
	cfg := fakehid.CorsairConfig{
		VID: 0x1b1c, PID: 0x0c1c, HasPump: true,
	}
	b, _, dev := makeBackend(cfg, ProbeOptions{PumpMinimum: 50}, "/dev/hidraw0")

	// Restore channels 1, 2, and 3 in sequence.
	for idx := 1; idx <= 3; idx++ {
		ch := hal.Channel{ID: "/dev/hidraw0-" + string(rune('0'+idx))}
		if err := b.Restore(ch); err != nil {
			t.Errorf("Restore ch%d: %v", idx, err)
		}
	}

	// Each Restore writes to RestoresReceived via doRestoreChannel → cmdWrite in hw-curve mode.
	if len(dev.RestoresReceived) != 3 {
		t.Errorf("RestoresReceived = %v, want 3 entries", dev.RestoresReceived)
	}
	for i, ch := range dev.RestoresReceived {
		want := i + 1
		if ch != want {
			t.Errorf("RestoresReceived[%d] = %d, want %d", i, ch, want)
		}
	}
}
