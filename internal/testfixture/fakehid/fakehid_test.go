package fakehid_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ventd/ventd/internal/hal/usbbase"
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

// TestFakehid_OpsAfterCloseReturnError regresses #305 concern 1: ops on a
// closed DeviceHandle must return a non-nil error containing "closed".
func TestFakehid_OpsAfterCloseReturnError(t *testing.T) {
	h := fakehid.NewDeviceHandle()
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	t.Run("Write", func(t *testing.T) {
		_, err := h.Write([]byte{0x01})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "closed") {
			t.Errorf("error %q does not contain 'closed'", err.Error())
		}
	})

	t.Run("ReadWithTimeout", func(t *testing.T) {
		_, err := h.ReadWithTimeout(make([]byte, 4), time.Second)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "closed") {
			t.Errorf("error %q does not contain 'closed'", err.Error())
		}
	})

	t.Run("GetFeatureReport", func(t *testing.T) {
		buf := []byte{0x01, 0x00, 0x00}
		_, err := h.GetFeatureReport(buf)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "closed") {
			t.Errorf("error %q does not contain 'closed'", err.Error())
		}
	})

	t.Run("SendFeatureReport", func(t *testing.T) {
		_, err := h.SendFeatureReport([]byte{0x01, 0xFF})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "closed") {
			t.Errorf("error %q does not contain 'closed'", err.Error())
		}
	})
}

// ── fakehid.Device tests ──────────────────────────────────────────────────────

func TestDevice_ReadWriteRoundTrip(t *testing.T) {
	d := fakehid.NewDevice(0x1b1c, 0x0c1c, "SN001", -1)
	d.SetOnWrite(func(b []byte) []byte {
		// Echo back with first byte inverted.
		resp := make([]byte, len(b))
		copy(resp, b)
		if len(resp) > 0 {
			resp[0] ^= 0xFF
		}
		return resp
	})

	_, err := d.Write([]byte{0xAA, 0xBB})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, 8)
	n, err := d.Read(buf, time.Second)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != 2 {
		t.Fatalf("Read: got %d bytes, want 2", n)
	}
	if buf[0] != (0xAA^0xFF) || buf[1] != 0xBB {
		t.Errorf("Read: got [%#x %#x], want [%#x %#x]", buf[0], buf[1], 0xAA^0xFF, 0xBB)
	}
}

func TestDevice_OpsAfterClose(t *testing.T) {
	d := fakehid.NewDevice(0x1b1c, 0x0c1c, "SN001", -1)
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !d.IsClosed() {
		t.Error("IsClosed: want true after Close")
	}

	if _, err := d.Write([]byte{0x01}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("Write after Close: got %v, want closed error", err)
	}
	if _, err := d.Read(make([]byte, 4), time.Second); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("Read after Close: got %v, want closed error", err)
	}
}

func TestDevice_Accessors(t *testing.T) {
	d := fakehid.NewDevice(0x1b1c, 0x0c1c, "SERIAL123", 2)
	if d.VendorID() != 0x1b1c {
		t.Errorf("VendorID = %#x, want 0x1b1c", d.VendorID())
	}
	if d.ProductID() != 0x0c1c {
		t.Errorf("ProductID = %#x, want 0x0c1c", d.ProductID())
	}
	if d.SerialNumber() != "SERIAL123" {
		t.Errorf("SerialNumber = %q, want SERIAL123", d.SerialNumber())
	}
	if d.Iface() != 2 {
		t.Errorf("Iface = %d, want 2", d.Iface())
	}
}

// ── Hub tests (goroutine-using; require goleak) ────────────────────────────

func TestHub_AddRemove(t *testing.T) {
	defer goleak.VerifyNone(t)

	hub := fakehid.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := hub.Watch(ctx, []usbbase.Matcher{{VendorID: 0x1b1c}})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	d := fakehid.NewDevice(0x1b1c, 0x0c1c, "SN001", -1)
	hub.Add(d)

	select {
	case ev := <-ch:
		if ev.Kind != usbbase.Add || ev.Device.SerialNumber() != "SN001" {
			t.Errorf("Add event: kind=%v serial=%q", ev.Kind, ev.Device.SerialNumber())
		}
	case <-time.After(time.Second):
		t.Fatal("no Add event within 1s")
	}

	hub.Remove("SN001")

	select {
	case ev := <-ch:
		if ev.Kind != usbbase.Remove {
			t.Errorf("Remove event: kind=%v, want Remove", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("no Remove event within 1s")
	}

	cancel() // close Watch goroutine before goleak check
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("channel not closed after cancel")
	}
}

func TestHub_Watch_Cancel(t *testing.T) {
	defer goleak.VerifyNone(t)

	hub := fakehid.NewHub()
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := hub.Watch(ctx, nil)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel open after cancel; want closed")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed within 1s after cancel")
	}
}

func TestHub_Watch_MultipleSubscribers(t *testing.T) {
	defer goleak.VerifyNone(t)

	hub := fakehid.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := []usbbase.Matcher{{VendorID: 0x1b1c}}
	ch1, _ := hub.Watch(ctx, m)
	ch2, _ := hub.Watch(ctx, m)

	hub.Add(fakehid.NewDevice(0x1b1c, 0x0c1c, "SN001", -1))

	for i, ch := range []<-chan usbbase.Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Kind != usbbase.Add {
				t.Errorf("subscriber %d: got kind %v, want Add", i, ev.Kind)
			}
		case <-time.After(time.Second):
			t.Errorf("subscriber %d: no event within 1s", i)
		}
	}

	cancel()
	// Drain to let goroutines finish.
	for _, ch := range []<-chan usbbase.Event{ch1, ch2} {
		select {
		case <-ch:
		case <-time.After(time.Second):
		}
	}
}
