package acpi

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// fakeACPICall is an in-memory stand-in for /proc/acpi/call. The
// test plans a "response" by setting f.respond before invoking
// Call; the fake captures the written request in f.request for
// assertions.
type fakeACPICall struct {
	request []byte
	respond []byte // returned on next Read
	closed  bool
}

func (f *fakeACPICall) Write(p []byte) (int, error) {
	if f.closed {
		return 0, io.ErrClosedPipe
	}
	f.request = append(f.request, p...)
	return len(p), nil
}

func (f *fakeACPICall) Read(p []byte) (int, error) {
	if f.closed {
		return 0, io.ErrClosedPipe
	}
	n := copy(p, f.respond)
	return n, nil
}

func (f *fakeACPICall) Close() error {
	f.closed = true
	return nil
}

func installFakeACPI(t *testing.T, resp []byte) *fakeACPICall {
	t.Helper()
	fake := &fakeACPICall{respond: resp}
	saved := procACPICallOpener
	procACPICallOpener = func() (acpiCallFile, error) { return fake, nil }
	t.Cleanup(func() { procACPICallOpener = saved })
	return fake
}

// RULE-NBFC-ACPI-01 — Call refuses methods not in the allowlist.
func TestRULE_NBFC_ACPI_01_AllowlistGate(t *testing.T) {
	installFakeACPI(t, []byte("0x42\x00"))
	b := New(map[string]bool{"\\_SB.PCI0.SFNV": true})
	_, err := b.Call("\\_SB.PCI0.EVIL")
	if !errors.Is(err, ErrACPIMethodNotInConfig) {
		t.Errorf("expected ErrACPIMethodNotInConfig, got %v", err)
	}
}

// RULE-NBFC-ACPI-01 — Call admits methods in the allowlist.
func TestRULE_NBFC_ACPI_01_AllowlistAdmits(t *testing.T) {
	installFakeACPI(t, []byte("0x42\x00"))
	b := New(map[string]bool{"\\_SB.PCI0.SFNV": true})
	v, err := b.Call("\\_SB.PCI0.SFNV")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if v != 0x42 {
		t.Errorf("Call = %d, want 0x42", v)
	}
}

// RULE-NBFC-ACPI-02 — response parser handles legacy decimal +
// 0x-prefixed hex.
func TestRULE_NBFC_ACPI_02_ResponseFormats(t *testing.T) {
	cases := []struct {
		name string
		resp []byte
		want uint64
	}{
		{"hex_lower", []byte("0xdeadbeef\x00"), 0xDEADBEEF},
		{"hex_upper", []byte("0xCAFE\x00"), 0xCAFE},
		{"decimal", []byte("12345\x00"), 12345},
		{"hex_with_trailing_whitespace", []byte("0x10\n\x00"), 0x10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			installFakeACPI(t, c.resp)
			b := New(nil) // nil allowlist means open
			got, err := b.Call("\\X")
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			if got != c.want {
				t.Errorf("Call = %d, want %d", got, c.want)
			}
		})
	}
}

// RULE-NBFC-ACPI-02 — unparseable response surfaces a typed error.
func TestRULE_NBFC_ACPI_02_UnparseableResponseTypedError(t *testing.T) {
	installFakeACPI(t, []byte("Error: AE_NOT_FOUND\x00"))
	b := New(nil)
	_, err := b.Call("\\X")
	if !errors.Is(err, ErrACPIResponseUnparseable) {
		t.Errorf("expected ErrACPIResponseUnparseable, got %v", err)
	}
}

// RULE-NBFC-ACPI-03 — Available() distinguishes "module not loaded"
// (ENOENT) from "no-op invocation failed".
func TestRULE_NBFC_ACPI_03_AvailableDistinguishesCauses(t *testing.T) {
	saved := procACPICallOpener
	procACPICallOpener = func() (acpiCallFile, error) { return nil, ErrACPICallNotLoaded }
	t.Cleanup(func() { procACPICallOpener = saved })

	err := Available()
	if !errors.Is(err, ErrACPICallNotLoaded) {
		t.Errorf("expected ErrACPICallNotLoaded, got %v", err)
	}
}

// RULE-NBFC-ACPI-03 — Available returns nil when module is loaded.
func TestRULE_NBFC_ACPI_03_AvailableSucceedsWhenLoaded(t *testing.T) {
	installFakeACPI(t, []byte("0x0\x00"))
	if err := Available(); err != nil {
		t.Errorf("Available: %v", err)
	}
}

// Call request format pins the wire-level shape acpi_call expects:
// "<method> [arg1] [arg2]..." with decimal arguments.
func TestACPICall_RequestFormat(t *testing.T) {
	fake := installFakeACPI(t, []byte("0x0\x00"))
	b := New(nil)
	if _, err := b.Call("\\_SB.PCI0.SFNV", 42, 100); err != nil {
		t.Fatalf("Call: %v", err)
	}
	want := []byte("\\_SB.PCI0.SFNV 42 100")
	if !bytes.Equal(fake.request, want) {
		t.Errorf("request = %q, want %q", fake.request, want)
	}
}

// Empty method path is refused without touching /proc/acpi/call.
func TestACPICall_EmptyMethodRefused(t *testing.T) {
	fake := installFakeACPI(t, []byte("0x0\x00"))
	b := New(nil)
	_, err := b.Call("")
	if err == nil {
		t.Fatal("expected error on empty method")
	}
	if fake.request != nil {
		t.Errorf("empty method should not touch the bridge; request = %q", fake.request)
	}
}
