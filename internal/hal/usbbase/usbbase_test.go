package usbbase_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/hal/usbbase"
	"github.com/ventd/ventd/internal/testfixture/fakehid"
)

// newBusWithDevice registers one device on a fresh Layer and returns
// the Bus and DeviceHandle for use in sub-tests.
func newBusWithDevice(path string) (*usbbase.Bus, *fakehid.DeviceHandle) {
	layer := fakehid.New()
	h := fakehid.NewDeviceHandle()
	dev := usbbase.Device{
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
	want := []usbbase.Device{
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
	// buf[0] must be set to the report ID by GetFeature before forwarding.
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
