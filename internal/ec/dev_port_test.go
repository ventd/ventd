package ec

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// fakeDevPort simulates the /dev/port two-port EC interface. Reads
// at offset 0x66 return a synthetic status byte; reads at 0x62
// return queued data; writes track the protocol state machine. The
// fake is just sophisticated enough to validate the read/write
// handshake without booting a real EC.
type fakeDevPort struct {
	mu     sync.Mutex
	regs   [256]byte
	closed bool

	// state machine
	pendingCmd  uint8 // 0x80 = READ, 0x81 = WRITE, 0 = none
	pendingReg  uint8
	readyToRead bool
	readVal     uint8

	// observability for tests
	cmdWrites  []uint8
	dataWrites []struct{ port, val uint8 }
}

func (f *fakeDevPort) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, io.ErrClosedPipe
	}
	if len(p) != 1 {
		return 0, io.ErrShortBuffer
	}
	switch uint8(off) {
	case portCmdStatus:
		// Synthetic status: never busy. OBF set iff readyToRead.
		var s uint8
		if f.readyToRead {
			s |= statusOBF
		}
		p[0] = s
		return 1, nil
	case portData:
		if !f.readyToRead {
			// Reading data without data ready returns garbage; in
			// real hardware undefined. Here return 0 so tests can
			// distinguish.
			p[0] = 0
			return 1, nil
		}
		p[0] = f.readVal
		f.readyToRead = false
		return 1, nil
	default:
		p[0] = 0
		return 1, nil
	}
}

func (f *fakeDevPort) WriteAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, io.ErrClosedPipe
	}
	if len(p) != 1 {
		return 0, io.ErrShortBuffer
	}
	val := p[0]
	port := uint8(off)
	switch port {
	case portCmdStatus:
		f.cmdWrites = append(f.cmdWrites, val)
		f.pendingCmd = val
		f.pendingReg = 0
	case portData:
		f.dataWrites = append(f.dataWrites, struct{ port, val uint8 }{port, val})
		switch f.pendingCmd {
		case cmdReadEC:
			if f.pendingReg == 0 {
				// First data byte after READ_EC is the register address.
				f.pendingReg = val
				f.readVal = f.regs[val]
				f.readyToRead = true
				f.pendingCmd = 0
			}
		case cmdWriteEC:
			if f.pendingReg == 0 {
				// First data byte after WRITE_EC is the register addr.
				f.pendingReg = val
			} else {
				// Second data byte is the value to store.
				f.regs[f.pendingReg] = val
				f.pendingCmd = 0
				f.pendingReg = 0
			}
		}
	}
	return 1, nil
}

func (f *fakeDevPort) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func installFakeDevPort(t *testing.T) *fakeDevPort {
	t.Helper()
	fake := &fakeDevPort{}
	saved := openDevPortFn
	openDevPortFn = func() (devPortFile, error) { return fake, nil }
	t.Cleanup(func() { openDevPortFn = saved })
	return fake
}

// TestDevPort_ReadWriteRoundTrip pins the ACPI READ_EC / WRITE_EC
// handshake end-to-end against the fake.
func TestDevPort_ReadWriteRoundTrip(t *testing.T) {
	installFakeDevPort(t)
	tr, err := openDevPort()
	if err != nil {
		t.Fatalf("openDevPort: %v", err)
	}
	defer tr.Close()

	if err := tr.Write(0x10, 0xCA); err != nil {
		t.Fatalf("Write: %v", err)
	}
	v, err := tr.Read(0x10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v != 0xCA {
		t.Errorf("Read = %#x, want 0xCA", v)
	}
}

// TestDevPort_CommandSequenceIsCorrect pins that READ_EC and WRITE_EC
// commands fire on the right port in the right order (RULE-NBFC-EC-03).
func TestDevPort_CommandSequenceIsCorrect(t *testing.T) {
	fake := installFakeDevPort(t)
	tr, err := openDevPort()
	if err != nil {
		t.Fatalf("openDevPort: %v", err)
	}
	defer tr.Close()

	if err := tr.Write(0x05, 0xFF); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := tr.Read(0x05); err != nil {
		t.Fatalf("Read: %v", err)
	}

	if len(fake.cmdWrites) != 2 {
		t.Fatalf("expected 2 commands, got %d (%v)", len(fake.cmdWrites), fake.cmdWrites)
	}
	if fake.cmdWrites[0] != cmdWriteEC {
		t.Errorf("first cmd = %#x, want WRITE_EC %#x", fake.cmdWrites[0], cmdWriteEC)
	}
	if fake.cmdWrites[1] != cmdReadEC {
		t.Errorf("second cmd = %#x, want READ_EC %#x", fake.cmdWrites[1], cmdReadEC)
	}
}

// TestDevPort_LittleEndianWord16 pins 16-bit little-endian access
// across two consecutive registers.
func TestDevPort_LittleEndianWord16(t *testing.T) {
	fake := installFakeDevPort(t)
	tr, err := openDevPort()
	if err != nil {
		t.Fatalf("openDevPort: %v", err)
	}
	defer tr.Close()

	if err := tr.Write16(0x20, 0xCAFE); err != nil {
		t.Fatalf("Write16: %v", err)
	}
	if fake.regs[0x20] != 0xFE {
		t.Errorf("regs[0x20] = %#x, want 0xFE", fake.regs[0x20])
	}
	if fake.regs[0x21] != 0xCA {
		t.Errorf("regs[0x21] = %#x, want 0xCA", fake.regs[0x21])
	}
	v, err := tr.Read16(0x20)
	if err != nil {
		t.Fatalf("Read16: %v", err)
	}
	if v != 0xCAFE {
		t.Errorf("Read16 = %#x, want 0xCAFE", v)
	}
}

// TestDevPort_HandshakeTimeoutSurfacesErrECBusy — when the synthetic
// status never advances (we force it stuck busy), the transport
// surfaces ErrECBusy via the deadline.
func TestDevPort_HandshakeTimeoutSurfacesErrECBusy(t *testing.T) {
	// Install a fake whose status byte stays IBF-set forever.
	stuck := &stuckDevPort{}
	saved := openDevPortFn
	openDevPortFn = func() (devPortFile, error) { return stuck, nil }
	defer func() { openDevPortFn = saved }()

	// Shrink the timeout for the test.
	savedTimeout := pollTimeout
	pollTimeout = 10 * time.Millisecond
	defer func() { pollTimeout = savedTimeout }()

	tr, err := openDevPort()
	if err != nil {
		t.Fatalf("openDevPort: %v", err)
	}
	defer tr.Close()

	_, err = tr.Read(0x10)
	if !errors.Is(err, ErrECBusy) {
		t.Errorf("expected ErrECBusy, got %v", err)
	}
}

// stuckDevPort always reports IBF=1 (input buffer full) so any
// handshake hits the timeout.
type stuckDevPort struct{ closed bool }

func (s *stuckDevPort) ReadAt(p []byte, off int64) (int, error) {
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	if uint8(off) == portCmdStatus {
		p[0] = statusIBF
		return 1, nil
	}
	p[0] = 0
	return 1, nil
}
func (s *stuckDevPort) WriteAt(p []byte, off int64) (int, error) { return len(p), nil }
func (s *stuckDevPort) Close() error                             { s.closed = true; return nil }

// TestDevPort_Name pins identifier.
func TestDevPort_Name(t *testing.T) {
	installFakeDevPort(t)
	tr, err := openDevPort()
	if err != nil {
		t.Fatalf("openDevPort: %v", err)
	}
	defer tr.Close()
	if got := tr.Name(); got != "dev_port" {
		t.Errorf("Name = %q, want dev_port", got)
	}
}
