package fakehid_test

import (
	"testing"
	"time"

	"github.com/ventd/ventd/internal/testfixture/fakehid"
)

func TestNew(t *testing.T) {
	_ = fakehid.New()
}

func TestNewDeviceHandle(t *testing.T) {
	_ = fakehid.NewDeviceHandle()
}

func TestLayer_EnumerateEmpty(t *testing.T) {
	l := fakehid.New()
	devs, err := l.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(devs) != 0 {
		t.Errorf("Enumerate: got %d devices, want 0", len(devs))
	}
}

func TestLayer_OpenPath_Missing(t *testing.T) {
	l := fakehid.New()
	_, err := l.OpenPath("/dev/hidraw99")
	if err == nil {
		t.Fatal("OpenPath: expected error for unknown path, got nil")
	}
}

func TestDeviceHandle_CloseTwice(t *testing.T) {
	h := fakehid.NewDeviceHandle()
	if err := h.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if !h.IsClosed() {
		t.Error("IsClosed: want true")
	}
}

func TestDeviceHandle_ReadQueue(t *testing.T) {
	h := fakehid.NewDeviceHandle()
	h.QueueRead([]byte{0x01, 0x02})
	h.QueueRead([]byte{0x03, 0x04})

	buf := make([]byte, 4)
	n, err := h.ReadWithTimeout(buf, time.Second)
	if err != nil || n != 2 || buf[0] != 0x01 || buf[1] != 0x02 {
		t.Fatalf("first read: n=%d err=%v buf=%v", n, err, buf[:n])
	}
	n, err = h.ReadWithTimeout(buf, time.Second)
	if err != nil || n != 2 || buf[0] != 0x03 || buf[1] != 0x04 {
		t.Fatalf("second read: n=%d err=%v buf=%v", n, err, buf[:n])
	}
	_, err = h.ReadWithTimeout(buf, time.Second)
	if err == nil {
		t.Error("third read: expected queue-empty error, got nil")
	}
}

func TestDeviceHandle_WriteCapture(t *testing.T) {
	h := fakehid.NewDeviceHandle()
	payload := []byte{0xAA, 0xBB}
	n, err := h.Write(payload)
	if err != nil || n != len(payload) {
		t.Fatalf("Write: n=%d err=%v", n, err)
	}
	written := h.Written()
	if len(written) != 1 || written[0][0] != 0xAA || written[0][1] != 0xBB {
		t.Errorf("Written: got %v", written)
	}
}

func TestDeviceHandle_FeatureReport(t *testing.T) {
	h := fakehid.NewDeviceHandle()
	h.SetFeatureReport(0x05, []byte{0x05, 0xDE, 0xAD})

	buf := make([]byte, 4)
	buf[0] = 0x05
	n, err := h.GetFeatureReport(buf)
	if err != nil || n != 3 || buf[0] != 0x05 || buf[1] != 0xDE || buf[2] != 0xAD {
		t.Fatalf("GetFeatureReport: n=%d err=%v buf=%v", n, err, buf[:n])
	}

	// Unknown report ID returns error.
	buf[0] = 0x99
	_, err = h.GetFeatureReport(buf)
	if err == nil {
		t.Error("GetFeatureReport: expected error for unknown id, got nil")
	}
}

func TestDeviceHandle_SendFeatureReport(t *testing.T) {
	h := fakehid.NewDeviceHandle()
	payload := []byte{0x01, 0xFF}
	n, err := h.SendFeatureReport(payload)
	if err != nil || n != len(payload) {
		t.Fatalf("SendFeatureReport: n=%d err=%v", n, err)
	}
	written := h.Written()
	if len(written) != 1 || written[0][0] != 0x01 || written[0][1] != 0xFF {
		t.Errorf("Written after SendFeatureReport: got %v", written)
	}
}
