package usbbase_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/hal/usbbase"
	"github.com/ventd/ventd/internal/testfixture/fakehid"
)

// ── Bus / Handle tests (low-level layer) ─────────────────────────────────────

// newBusWithDevice registers one device on a fresh Layer and returns
// the Bus and DeviceHandle for use in sub-tests.
func newBusWithDevice(path string) (*usbbase.Bus, *fakehid.DeviceHandle) {
	layer := fakehid.New()
	h := fakehid.NewDeviceHandle()
	dev := usbbase.DeviceInfo{
		VendorID:     0x1234,
		ProductID:    0x5678,
		Path:         path,
		Manufacturer: "ACME",
		Product:      "Fan Controller",
		Serial:       "SN001",
	}
	layer.AddDevice(dev, h)
	return usbbase.NewWithLayer(layer), h
}

func TestEnumerate_ReturnsList(t *testing.T) {
	layer := fakehid.New()
	want := []usbbase.DeviceInfo{
		{VendorID: 0x0001, ProductID: 0x0002, Path: "/dev/hidraw0", Manufacturer: "Corp", Product: "Widget", Serial: "ABC"},
		{VendorID: 0x0003, ProductID: 0x0004, Path: "/dev/hidraw1"},
	}
	for _, d := range want {
		layer.AddDevice(d, fakehid.NewDeviceHandle())
	}

	bus := usbbase.NewWithLayer(layer)
	got, err := bus.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Enumerate: got %d devices, want %d", len(got), len(want))
	}
	for i, g := range got {
		if g != want[i] {
			t.Errorf("Enumerate[%d]: got %+v, want %+v", i, g, want[i])
		}
	}
}

func TestEnumerate_Empty(t *testing.T) {
	bus := usbbase.NewWithLayer(fakehid.New())
	got, err := bus.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Enumerate: got %d devices, want 0", len(got))
	}
}

func TestOpen_UnknownPath(t *testing.T) {
	bus := usbbase.NewWithLayer(fakehid.New())
	_, err := bus.Open("/dev/hidraw99")
	if err == nil {
		t.Fatal("Open: expected error for unknown path, got nil")
	}
}

func TestOpenClose_Lifecycle(t *testing.T) {
	bus, raw := newBusWithDevice("/dev/hidraw0")
	handle, err := bus.Open("/dev/hidraw0")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if !raw.IsClosed() {
		t.Error("underlying handle not closed after Handle.Close()")
	}
}

func TestClose_Idempotent(t *testing.T) {
	bus, _ := newBusWithDevice("/dev/hidraw0")
	handle, _ := bus.Open("/dev/hidraw0")

	if err := handle.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestRead_Passthrough(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{"single byte", []byte{0xFF}},
		{"multi byte", []byte{0x01, 0x02, 0x03}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bus, raw := newBusWithDevice("/dev/hidrawR")
			raw.QueueRead(tc.payload)

			handle, _ := bus.Open("/dev/hidrawR")
			defer func() { _ = handle.Close() }()

			buf := make([]byte, 16)
			n, err := handle.Read(buf, time.Second)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if n != len(tc.payload) {
				t.Fatalf("Read: got %d bytes, want %d", n, len(tc.payload))
			}
			if !bytes.Equal(buf[:n], tc.payload) {
				t.Errorf("Read: got %v, want %v", buf[:n], tc.payload)
			}
		})
	}
}

func TestWrite_Passthrough(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{"single byte", []byte{0xAA}},
		{"multi byte", []byte{0x01, 0x02, 0x03, 0x04}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bus, raw := newBusWithDevice("/dev/hidrawW")
			handle, _ := bus.Open("/dev/hidrawW")
			defer func() { _ = handle.Close() }()

			if err := handle.Write(tc.payload); err != nil {
				t.Fatalf("Write: %v", err)
			}
			written := raw.Written()
			if len(written) != 1 {
				t.Fatalf("Written: got %d entries, want 1", len(written))
			}
			if !bytes.Equal(written[0], tc.payload) {
				t.Errorf("Written[0]: got %v, want %v", written[0], tc.payload)
			}
		})
	}
}

func TestGetFeature_Passthrough(t *testing.T) {
	bus, raw := newBusWithDevice("/dev/hidrawF")
	raw.SetFeatureReport(0x07, []byte{0x07, 0xBE, 0xEF})

	handle, _ := bus.Open("/dev/hidrawF")
	defer func() { _ = handle.Close() }()

	buf := make([]byte, 4)
	n, err := handle.GetFeature(0x07, buf)
	if err != nil {
		t.Fatalf("GetFeature: %v", err)
	}
	if n != 3 {
		t.Fatalf("GetFeature: got %d bytes, want 3", n)
	}
	if buf[0] != 0x07 {
		t.Errorf("GetFeature: buf[0] = %#x, want 0x07", buf[0])
	}
	if buf[1] != 0xBE || buf[2] != 0xEF {
		t.Errorf("GetFeature: got %v, want [07 BE EF]", buf[:n])
	}
}

func TestGetFeature_EmptyBuf(t *testing.T) {
	bus, _ := newBusWithDevice("/dev/hidrawFE")
	handle, _ := bus.Open("/dev/hidrawFE")
	defer func() { _ = handle.Close() }()

	_, err := handle.GetFeature(0x01, nil)
	if err == nil {
		t.Error("GetFeature with nil buf: expected error, got nil")
	}
}

func TestSendFeature_Passthrough(t *testing.T) {
	bus, raw := newBusWithDevice("/dev/hidrawSF")
	handle, _ := bus.Open("/dev/hidrawSF")
	defer func() { _ = handle.Close() }()

	payload := []byte{0x03, 0xCA, 0xFE}
	if err := handle.SendFeature(payload); err != nil {
		t.Fatalf("SendFeature: %v", err)
	}
	written := raw.Written()
	if len(written) != 1 || !bytes.Equal(written[0], payload) {
		t.Errorf("SendFeature: Written = %v, want %v", written, [][]byte{payload})
	}
}

// TestHandle_ConcurrentWriteAndClose regresses #305 concern 2.
func TestHandle_ConcurrentWriteAndClose(t *testing.T) {
	const numGoroutines = 10

	bus, raw := newBusWithDevice("/dev/hidrawCC")
	handle, err := bus.Open("/dev/hidrawCC")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var (
		wg           sync.WaitGroup
		closedErrors atomic.Int64
	)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := handle.Write([]byte{0x01}); err != nil {
				if strings.Contains(err.Error(), "closed") {
					closedErrors.Add(1)
				}
			}
		}()
	}
	_ = handle.Close()
	wg.Wait()

	rawWrites := int64(len(raw.Written()))
	total := rawWrites + closedErrors.Load()
	if total != numGoroutines {
		t.Errorf("rawWrites(%d) + closedErrors(%d) = %d, want %d",
			rawWrites, closedErrors.Load(), total, numGoroutines)
	}
}

// ── Matcher tests ─────────────────────────────────────────────────────────────

func TestMatcher_Matches(t *testing.T) {
	tests := []struct {
		name    string
		matcher usbbase.Matcher
		vid     uint16
		pid     uint16
		iface   int
		want    bool
	}{
		{
			name:    "exact VID+PID match",
			matcher: usbbase.Matcher{VendorID: 0x1b1c, ProductIDs: []uint16{0x0c1c}},
			vid:     0x1b1c, pid: 0x0c1c, iface: -1,
			want: true,
		},
		{
			name:    "VID match second of multiple PIDs",
			matcher: usbbase.Matcher{VendorID: 0x1b1c, ProductIDs: []uint16{0x0c1c, 0x0c20}},
			vid:     0x1b1c, pid: 0x0c20, iface: -1,
			want: true,
		},
		{
			name:    "VID match wrong PID",
			matcher: usbbase.Matcher{VendorID: 0x1b1c, ProductIDs: []uint16{0x0c1c}},
			vid:     0x1b1c, pid: 0x0c20, iface: -1,
			want: false,
		},
		{
			name:    "wrong VID",
			matcher: usbbase.Matcher{VendorID: 0x1b1c, ProductIDs: []uint16{0x0c1c}},
			vid:     0x1234, pid: 0x0c1c, iface: -1,
			want: false,
		},
		{
			name:    "VID+interface match",
			matcher: usbbase.Matcher{VendorID: 0x1b1c, Interface: 0},
			vid:     0x1b1c, pid: 0x0c1c, iface: 0,
			want: true,
		},
		{
			name:    "VID+interface mismatch",
			matcher: usbbase.Matcher{VendorID: 0x1b1c, Interface: 1},
			vid:     0x1b1c, pid: 0x0c1c, iface: 0,
			want: false,
		},
		{
			name:    "interface -1 on matcher means any",
			matcher: usbbase.Matcher{VendorID: 0x1b1c, Interface: -1},
			vid:     0x1b1c, pid: 0x0c1c, iface: 5,
			want: true,
		},
		{
			name:    "interface -1 on device matches specific matcher interface",
			matcher: usbbase.Matcher{VendorID: 0x1b1c, Interface: 2},
			vid:     0x1b1c, pid: 0x0c1c, iface: -1,
			want: true,
		},
		{
			name:    "empty ProductIDs matches any PID",
			matcher: usbbase.Matcher{VendorID: 0x1b1c},
			vid:     0x1b1c, pid: 0xFFFF, iface: -1,
			want: true,
		},
		{
			name:    "zero VendorID matches any VID",
			matcher: usbbase.Matcher{ProductIDs: []uint16{0x0c1c}},
			vid:     0xDEAD, pid: 0x0c1c, iface: -1,
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.matcher.Matches(tc.vid, tc.pid, tc.iface)
			if got != tc.want {
				t.Errorf("Matches(%#x, %#x, %d) = %v, want %v",
					tc.vid, tc.pid, tc.iface, got, tc.want)
			}
		})
	}
}

// ── Hub-based Enumerate / Watch tests ────────────────────────────────────────

func TestHub_Enumerate_ByVIDPID(t *testing.T) {
	hub := fakehid.NewHub()
	hub.Add(fakehid.NewDevice(0x1b1c, 0x0c1c, "SN001", -1))
	hub.Add(fakehid.NewDevice(0x1234, 0xABCD, "SN002", -1))

	matchers := []usbbase.Matcher{{VendorID: 0x1b1c, ProductIDs: []uint16{0x0c1c}}}
	devs, err := hub.Enumerate(matchers)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("Enumerate: got %d devices, want 1", len(devs))
	}
	if devs[0].VendorID() != 0x1b1c || devs[0].ProductID() != 0x0c1c {
		t.Errorf("Enumerate: VID=%#x PID=%#x, want 0x1b1c/0x0c1c",
			devs[0].VendorID(), devs[0].ProductID())
	}
}

func TestHub_Enumerate_ByVIDInterface(t *testing.T) {
	hub := fakehid.NewHub()
	hub.Add(fakehid.NewDevice(0x1b1c, 0x0c1c, "SN001", 0))
	hub.Add(fakehid.NewDevice(0x1b1c, 0x0c1c, "SN002", 1))

	matchers := []usbbase.Matcher{{VendorID: 0x1b1c, Interface: 1}}
	devs, err := hub.Enumerate(matchers)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("Enumerate: got %d devices, want 1", len(devs))
	}
	if devs[0].SerialNumber() != "SN002" {
		t.Errorf("Enumerate: got serial %q, want SN002", devs[0].SerialNumber())
	}
}

func TestHub_Enumerate_EmptyMatchers(t *testing.T) {
	hub := fakehid.NewHub()
	hub.Add(fakehid.NewDevice(0x1b1c, 0x0c1c, "SN001", -1))

	devs, err := hub.Enumerate(nil)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(devs) != 0 {
		t.Errorf("Enumerate with no matchers: got %d devices, want 0", len(devs))
	}
}

func TestHub_Watch_AddRemoveEvents(t *testing.T) {
	hub := fakehid.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	matchers := []usbbase.Matcher{{VendorID: 0x1b1c}}
	ch, err := hub.Watch(ctx, matchers)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	d := fakehid.NewDevice(0x1b1c, 0x0c1c, "SN001", -1)
	hub.Add(d)

	select {
	case ev := <-ch:
		if ev.Kind != usbbase.Add {
			t.Errorf("first event: got kind %v, want Add", ev.Kind)
		}
		if ev.Device.VendorID() != 0x1b1c {
			t.Errorf("first event: VID = %#x, want 0x1b1c", ev.Device.VendorID())
		}
	case <-time.After(time.Second):
		t.Fatal("Watch: no Add event received within 1s")
	}

	hub.Remove("SN001")

	select {
	case ev := <-ch:
		if ev.Kind != usbbase.Remove {
			t.Errorf("second event: got kind %v, want Remove", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("Watch: no Remove event received within 1s")
	}
}

func TestHub_Watch_MatcherFiltersEvents(t *testing.T) {
	hub := fakehid.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Only watch for 0x1b1c devices.
	matchers := []usbbase.Matcher{{VendorID: 0x1b1c}}
	ch, err := hub.Watch(ctx, matchers)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Add a non-matching device first — should NOT appear on ch.
	hub.Add(fakehid.NewDevice(0x1234, 0x0001, "SN_OTHER", -1))

	// Add a matching device.
	hub.Add(fakehid.NewDevice(0x1b1c, 0x0c1c, "SN_MATCH", -1))

	select {
	case ev := <-ch:
		if ev.Device.VendorID() != 0x1b1c {
			t.Errorf("received event for wrong VID %#x, want 0x1b1c", ev.Device.VendorID())
		}
	case <-time.After(time.Second):
		t.Fatal("Watch: no matching event received within 1s")
	}

	// Ensure no spurious second event.
	select {
	case ev := <-ch:
		t.Errorf("unexpected second event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHub_Watch_CancellationClosesChannel(t *testing.T) {
	hub := fakehid.NewHub()
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := hub.Watch(ctx, []usbbase.Matcher{{VendorID: 0x1b1c}})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel still open after ctx cancel; want closed")
		}
	case <-time.After(time.Second):
		t.Fatal("Watch channel not closed within 1s after ctx cancel")
	}
}

// TestPackageLevel_ErrUnsupported verifies that the package-level Enumerate
// and Watch return ErrUnsupported on unsupported build configurations
// (non-Linux or no CGO/hidraw tag, which is the case in CI).
func TestPackageLevel_ErrUnsupported(t *testing.T) {
	_, err := usbbase.Enumerate(nil)
	if err == nil {
		t.Skip("real HID layer available; skipping stub test")
	}
	if !errors.Is(err, usbbase.ErrUnsupported) {
		t.Errorf("Enumerate: got %v, want ErrUnsupported", err)
	}

	_, err = usbbase.Watch(context.Background(), nil)
	if !errors.Is(err, usbbase.ErrUnsupported) {
		t.Errorf("Watch: got %v, want ErrUnsupported", err)
	}
}
