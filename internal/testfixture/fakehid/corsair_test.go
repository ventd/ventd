package fakehid_test

import (
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ventd/ventd/internal/testfixture/fakehid"
)

// buildFrame constructs a minimal 1024-byte frame matching the corsair framing
// convention (responseID=0x00, writeCmd=0x08, then cmd bytes).
func buildFrame(cmd []byte) []byte {
	frame := make([]byte, 1024)
	frame[0] = 0x00
	frame[1] = 0x08
	copy(frame[2:], cmd)
	return frame
}

func TestCorsairFixture_Wake(t *testing.T) {
	defer goleak.VerifyNone(t)

	dev := fakehid.NewCorsairDevice(fakehid.CorsairConfig{
		VID: 0x1b1c, PID: 0x0c1c, HasPump: true,
	})
	pipe := fakehid.NewCorsairPipe(dev)

	frame := buildFrame([]byte{0x01, 0x03, 0x00, 0x02}) // cmd_wake
	n, err := pipe.Write(frame)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 1024 {
		t.Errorf("Write returned %d, want 1024", n)
	}

	resp := make([]byte, 1024)
	if err := pipe.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	rn, err := pipe.Read(resp)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if rn == 0 {
		t.Fatal("empty response")
	}
	if resp[0] != 0x00 {
		t.Errorf("resp[0] = 0x%02x, want 0x00", resp[0])
	}
}

func TestCorsairFixture_GetFirmware(t *testing.T) {
	defer goleak.VerifyNone(t)

	dev := fakehid.NewCorsairDevice(fakehid.CorsairConfig{
		VID: 0x1b1c, PID: 0x0c1c,
		FirmwareMajor: 2, FirmwareMinor: 10, FirmwarePatch: 5,
	})
	pipe := fakehid.NewCorsairPipe(dev)

	frame := buildFrame([]byte{0x02, 0x13}) // cmd_get_firmware
	if _, err := pipe.Write(frame); err != nil {
		t.Fatalf("Write: %v", err)
	}

	resp := make([]byte, 1024)
	if _, err := pipe.Read(resp); err != nil {
		t.Fatalf("Read: %v", err)
	}

	if resp[2] != 2 || resp[3] != 10 || resp[4] != 5 {
		t.Errorf("firmware = %d.%d.%d, want 2.10.5", resp[2], resp[3], resp[4])
	}
}

func TestCorsairFixture_WriteDuty(t *testing.T) {
	defer goleak.VerifyNone(t)

	dev := fakehid.NewCorsairDevice(fakehid.CorsairConfig{
		VID: 0x1b1c, PID: 0x0c1c, HasPump: true,
	})
	pipe := fakehid.NewCorsairPipe(dev)

	// Open endpoint in fixed-percent mode.
	openFrame := buildFrame([]byte{0x0d, 0x1a}) // cmd_open_endpoint + MODE_HW_FIXED_PERCENT
	if _, err := pipe.Write(openFrame); err != nil {
		t.Fatalf("open endpoint write: %v", err)
	}
	if _, err := pipe.Read(make([]byte, 1024)); err != nil {
		t.Fatalf("open endpoint read: %v", err)
	}

	// Write duty to channel 0 (pump), duty=80, LE uint16 → [80, 0].
	writeFrame := buildFrame([]byte{0x06, 0x00, 80, 0}) // cmd_write ch0 duty=80
	if _, err := pipe.Write(writeFrame); err != nil {
		t.Fatalf("write duty: %v", err)
	}
	if _, err := pipe.Read(make([]byte, 1024)); err != nil {
		t.Fatalf("read duty ack: %v", err)
	}

	if len(dev.DutiesWritten) == 0 {
		t.Fatal("no duties written")
	}
	got := dev.DutiesWritten[0]
	if got.Channel != 0 || got.Duty != 80 {
		t.Errorf("duty = ch%d/%d, want ch0/80", got.Channel, got.Duty)
	}
}

func TestCorsairFixture_ReadSpeeds(t *testing.T) {
	defer goleak.VerifyNone(t)

	dev := fakehid.NewCorsairDevice(fakehid.CorsairConfig{
		VID:    0x1b1c,
		PID:    0x0c1c,
		Speeds: []uint16{1200, 900, 0, 0, 0, 0, 0},
	})
	pipe := fakehid.NewCorsairPipe(dev)

	// Open endpoint in get-speeds mode.
	openFrame := buildFrame([]byte{0x0d, 0x17}) // MODE_GET_SPEEDS
	if _, err := pipe.Write(openFrame); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := pipe.Read(make([]byte, 1024)); err != nil {
		t.Fatalf("open read: %v", err)
	}

	// Send cmd_read.
	readFrame := buildFrame([]byte{0x08})
	if _, err := pipe.Write(readFrame); err != nil {
		t.Fatalf("read cmd: %v", err)
	}
	resp := make([]byte, 1024)
	if _, err := pipe.Read(resp); err != nil {
		t.Fatalf("read resp: %v", err)
	}

	count := int(resp[1])
	if count != 7 {
		t.Fatalf("speed count = %d, want 7", count)
	}
	ch0 := uint16(resp[2]) | uint16(resp[3])<<8
	ch1 := uint16(resp[4]) | uint16(resp[5])<<8
	if ch0 != 1200 {
		t.Errorf("ch0 RPM = %d, want 1200", ch0)
	}
	if ch1 != 900 {
		t.Errorf("ch1 RPM = %d, want 900", ch1)
	}
}
