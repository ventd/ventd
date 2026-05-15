package ec

import (
	"errors"
	"strings"
	"testing"
)

// TestAvailable_PrefersECSys pins the precedence: ec_sys is tried
// first and used when it opens cleanly. RULE-NBFC-EC-01.
func TestAvailable_PrefersECSys(t *testing.T) {
	installFakeECSys(t)
	installFakeDevPort(t)
	tr, err := Available()
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	defer tr.Close()
	if tr.Name() != "ec_sys" {
		t.Errorf("Available picked %q, want ec_sys", tr.Name())
	}
}

// TestAvailable_FallsBackToDevPort pins that /dev/port is used when
// ec_sys fails to open (kernel module not loaded, etc.).
func TestAvailable_FallsBackToDevPort(t *testing.T) {
	saved := openECSysFn
	openECSysFn = func() (ecSysFile, error) { return nil, errors.New("ec_sys not loaded") }
	t.Cleanup(func() { openECSysFn = saved })

	installFakeDevPort(t)
	tr, err := Available()
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	defer tr.Close()
	if tr.Name() != "dev_port" {
		t.Errorf("Available picked %q, want dev_port", tr.Name())
	}
}

// TestAvailable_BothFailReturnsCombinedError pins that both errors
// are surfaced when neither transport opens. RULE-NBFC-EC-01's
// chain-of-causes contract.
func TestAvailable_BothFailReturnsCombinedError(t *testing.T) {
	savedSys, savedPort := openECSysFn, openDevPortFn
	openECSysFn = func() (ecSysFile, error) { return nil, errors.New("ec_sys broken") }
	openDevPortFn = func() (devPortFile, error) { return nil, errors.New("port broken") }
	t.Cleanup(func() {
		openECSysFn = savedSys
		openDevPortFn = savedPort
	})

	_, err := Available()
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !errors.Is(err, ErrECNotAvailable) {
		t.Errorf("expected ErrECNotAvailable in chain; got %v", err)
	}
	for _, want := range []string{"ec_sys broken", "port broken"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q; got %v", want, err)
		}
	}
}

// TestWithAllowlist_RejectsUnknown pins the closed-set discipline:
// any register not in the allowlist returns ErrECRegisterNotInConfig
// without performing I/O. RULE-NBFC-EC-02.
func TestWithAllowlist_RejectsUnknown(t *testing.T) {
	fake := installFakeECSys(t)
	tr, err := openECSys()
	if err != nil {
		t.Fatalf("openECSys: %v", err)
	}
	defer tr.Close()
	wrapped := WithAllowlist(tr, map[uint8]bool{0x10: true})

	if _, err := wrapped.Read(0x10); err != nil {
		t.Errorf("allowed read should succeed: %v", err)
	}
	if _, err := wrapped.Read(0x42); !errors.Is(err, ErrECRegisterNotInConfig) {
		t.Errorf("unallowed read should return ErrECRegisterNotInConfig; got %v", err)
	}
	if err := wrapped.Write(0x42, 0xFF); !errors.Is(err, ErrECRegisterNotInConfig) {
		t.Errorf("unallowed write should return ErrECRegisterNotInConfig; got %v", err)
	}
	// Make sure no bytes leaked into the backing store.
	if fake.buf[0x42] != 0 {
		t.Errorf("disallowed write modified backing store: buf[0x42] = %#x", fake.buf[0x42])
	}
}

// TestWithAllowlist_Read16RequiresBothBytes pins that a 16-bit op
// rejects if EITHER byte address is missing from the allowlist.
func TestWithAllowlist_Read16RequiresBothBytes(t *testing.T) {
	installFakeECSys(t)
	tr, _ := openECSys()
	defer tr.Close()

	// Only the low byte allowed.
	wrapped := WithAllowlist(tr, map[uint8]bool{0x10: true})
	if _, err := wrapped.Read16(0x10); !errors.Is(err, ErrECRegisterNotInConfig) {
		t.Errorf("Read16 with missing high byte should refuse; got %v", err)
	}
	if err := wrapped.Write16(0x10, 0xCAFE); !errors.Is(err, ErrECRegisterNotInConfig) {
		t.Errorf("Write16 with missing high byte should refuse; got %v", err)
	}
}
