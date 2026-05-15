package ec

import (
	"errors"
	"io"
	"testing"
)

// fakeECFile is a 256-byte in-memory backing store for an ec_sys-
// shaped Transport. Implements ReaderAt + WriterAt + Closer.
type fakeECFile struct {
	buf    [256]byte
	closed bool
}

func (f *fakeECFile) ReadAt(p []byte, off int64) (int, error) {
	if f.closed {
		return 0, io.ErrClosedPipe
	}
	if off < 0 || off+int64(len(p)) > int64(len(f.buf)) {
		return 0, io.ErrUnexpectedEOF
	}
	return copy(p, f.buf[off:off+int64(len(p))]), nil
}

func (f *fakeECFile) WriteAt(p []byte, off int64) (int, error) {
	if f.closed {
		return 0, io.ErrClosedPipe
	}
	if off < 0 || off+int64(len(p)) > int64(len(f.buf)) {
		return 0, io.ErrUnexpectedEOF
	}
	return copy(f.buf[off:off+int64(len(p))], p), nil
}

func (f *fakeECFile) Close() error {
	f.closed = true
	return nil
}

// installFakeECSys wires the package-level seam to a fresh fake +
// the test cleans up automatically. Returns the fake so the caller
// can read / mutate the backing bytes directly.
func installFakeECSys(t *testing.T) *fakeECFile {
	t.Helper()
	fake := &fakeECFile{}
	saved := openECSysFn
	openECSysFn = func() (ecSysFile, error) { return fake, nil }
	t.Cleanup(func() { openECSysFn = saved })
	return fake
}

// TestECSys_ReadWriteRoundTrip pins the basic happy path: a byte
// written via Write is observable via Read at the same offset.
func TestECSys_ReadWriteRoundTrip(t *testing.T) {
	installFakeECSys(t)
	tr, err := openECSys()
	if err != nil {
		t.Fatalf("openECSys: %v", err)
	}
	defer tr.Close()

	if err := tr.Write(0x42, 0xAB); err != nil {
		t.Fatalf("Write: %v", err)
	}
	v, err := tr.Read(0x42)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v != 0xAB {
		t.Errorf("Read = %#x, want 0xAB", v)
	}
}

// TestECSys_LittleEndianWords pins the 16-bit access semantics:
// low byte at reg, high byte at reg+1. Matches nbfc-linux's
// ec_read_word / ec_write_word.
func TestECSys_LittleEndianWords(t *testing.T) {
	fake := installFakeECSys(t)
	tr, err := openECSys()
	if err != nil {
		t.Fatalf("openECSys: %v", err)
	}
	defer tr.Close()

	if err := tr.Write16(0x10, 0xBEEF); err != nil {
		t.Fatalf("Write16: %v", err)
	}
	if fake.buf[0x10] != 0xEF {
		t.Errorf("buf[0x10] = %#x, want 0xEF (low byte)", fake.buf[0x10])
	}
	if fake.buf[0x11] != 0xBE {
		t.Errorf("buf[0x11] = %#x, want 0xBE (high byte)", fake.buf[0x11])
	}
	v, err := tr.Read16(0x10)
	if err != nil {
		t.Fatalf("Read16: %v", err)
	}
	if v != 0xBEEF {
		t.Errorf("Read16 = %#x, want 0xBEEF", v)
	}
}

// TestECSys_Name pins the transport identifier for logs + diag bundles.
func TestECSys_Name(t *testing.T) {
	installFakeECSys(t)
	tr, err := openECSys()
	if err != nil {
		t.Fatalf("openECSys: %v", err)
	}
	defer tr.Close()
	if got := tr.Name(); got != "ec_sys" {
		t.Errorf("Name = %q, want ec_sys", got)
	}
}

// TestECSys_CloseIdempotent pins that double-close is safe.
func TestECSys_CloseIdempotent(t *testing.T) {
	installFakeECSys(t)
	tr, err := openECSys()
	if err != nil {
		t.Fatalf("openECSys: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("second Close should be no-op, got %v", err)
	}
}

// TestECSys_WriteSupportDisabledPropagates pins that
// ErrECSysWriteSupportDisabled travels back from the open path so
// the preflight check can surface the modprobe remediation.
func TestECSys_WriteSupportDisabledPropagates(t *testing.T) {
	saved := openECSysFn
	openECSysFn = func() (ecSysFile, error) { return nil, ErrECSysWriteSupportDisabled }
	t.Cleanup(func() { openECSysFn = saved })

	_, err := openECSys()
	if !errors.Is(err, ErrECSysWriteSupportDisabled) {
		t.Fatalf("expected ErrECSysWriteSupportDisabled; got %v", err)
	}
}
