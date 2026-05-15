package nbfc

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ventd/ventd/internal/acpi"
	"github.com/ventd/ventd/internal/ec"
	"github.com/ventd/ventd/internal/hal"
	nbfcdb "github.com/ventd/ventd/internal/hwdb/nbfc"
)

// fakeECTransport is a tiny Transport that backs reads and writes
// with an in-memory 256-byte map. Implements ec.Transport via the
// production interface. Tests can read t.bytes[reg] after a Write
// to confirm the backend wrote the expected value.
type fakeECTransport struct {
	bytes  [256]byte
	closed bool
}

func (f *fakeECTransport) Name() string { return "fake_ec" }
func (f *fakeECTransport) Close() error { f.closed = true; return nil }
func (f *fakeECTransport) Read(reg uint8) (uint8, error) {
	if f.closed {
		return 0, io.ErrClosedPipe
	}
	return f.bytes[reg], nil
}
func (f *fakeECTransport) Write(reg uint8, val uint8) error {
	if f.closed {
		return io.ErrClosedPipe
	}
	f.bytes[reg] = val
	return nil
}
func (f *fakeECTransport) Read16(reg uint8) (uint16, error) {
	if f.closed {
		return 0, io.ErrClosedPipe
	}
	return uint16(f.bytes[reg]) | uint16(f.bytes[reg+1])<<8, nil
}
func (f *fakeECTransport) Write16(reg uint8, val uint16) error {
	if f.closed {
		return io.ErrClosedPipe
	}
	f.bytes[reg] = byte(val)
	f.bytes[reg+1] = byte(val >> 8)
	return nil
}

// testConfig builds a minimal one-fan config for round-trip tests.
func testConfig() *nbfcdb.Config {
	return &nbfcdb.Config{
		NotebookModel: "Test Box",
		FanConfigurations: []nbfcdb.FanConfiguration{
			{
				FanDisplayName:     "CPU Fan",
				ReadRegister:       0x10,
				WriteRegister:      0x11,
				MinSpeedValue:      20,
				MaxSpeedValue:      100,
				ResetRequired:      true,
				FanSpeedResetValue: 50,
			},
		},
	}
}

// RULE-NBFC-HAL-01 — backend satisfies hal.FanBackend; Enumerate
// returns one channel per FanConfiguration.
func TestRULE_NBFC_HAL_01_EnumerateOneChannelPerFan(t *testing.T) {
	b, err := New(ProbeOpts{
		Config:       testConfig(),
		Transport:    &fakeECTransport{},
		WriteEnabled: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close()
	chs, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(chs) != 1 {
		t.Fatalf("Enumerate len = %d, want 1", len(chs))
	}
	if chs[0].Role != hal.RoleCPU {
		t.Errorf("Role = %v, want RoleCPU (inferred from name)", chs[0].Role)
	}
	if chs[0].Caps&(hal.CapRead|hal.CapWritePWM|hal.CapRestore) == 0 {
		t.Errorf("Caps = %v, want CapRead+CapWritePWM+CapRestore", chs[0].Caps)
	}
}

// RULE-NBFC-HAL-02 — Write clamps + scales + writes to WriteRegister.
func TestRULE_NBFC_HAL_02_WriteScalesAndWrites(t *testing.T) {
	fake := &fakeECTransport{}
	b, err := New(ProbeOpts{
		Config:       testConfig(),
		Transport:    fake,
		WriteEnabled: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close()
	chs, _ := b.Enumerate(context.Background())

	// pwm=255 → MaxSpeedValue=100; pwm=0 → MinSpeedValue=20; midpoint scales linearly.
	cases := []struct {
		pwm uint8
		reg uint8
	}{
		{0, 20},
		{255, 100},
		{128, 60}, // ~50% maps to (20 + 80*0.5) = 60
	}
	for _, c := range cases {
		if err := b.Write(chs[0], c.pwm); err != nil {
			t.Fatalf("Write(%d): %v", c.pwm, err)
		}
		if fake.bytes[0x11] != c.reg {
			t.Errorf("Write(pwm=%d) -> reg byte = %#x, want %#x", c.pwm, fake.bytes[0x11], c.reg)
		}
	}
}

// RULE-NBFC-HAL-02 — Read reverses the scaling.
func TestRULE_NBFC_HAL_02_ReadReversesScaling(t *testing.T) {
	fake := &fakeECTransport{}
	b, err := New(ProbeOpts{
		Config:       testConfig(),
		Transport:    fake,
		WriteEnabled: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close()
	chs, _ := b.Enumerate(context.Background())

	fake.bytes[0x10] = 100 // max
	r, err := b.Read(chs[0])
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !r.OK {
		t.Fatal("Reading.OK is false")
	}
	if r.PWM != 255 {
		t.Errorf("Read PWM = %d, want 255 (max register → max pwm)", r.PWM)
	}

	fake.bytes[0x10] = 20 // min
	r, _ = b.Read(chs[0])
	if r.PWM != 0 {
		t.Errorf("Read PWM = %d, want 0 (min register → min pwm)", r.PWM)
	}
}

// RULE-NBFC-HAL-03 — Restore writes FanSpeedResetValue per fan with
// ResetRequired=true.
func TestRULE_NBFC_HAL_03_RestoreWritesResetValue(t *testing.T) {
	fake := &fakeECTransport{}
	b, err := New(ProbeOpts{
		Config:       testConfig(),
		Transport:    fake,
		WriteEnabled: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close()
	chs, _ := b.Enumerate(context.Background())

	if err := b.Restore(chs[0]); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if fake.bytes[0x11] != 50 {
		t.Errorf("Restore wrote %#x to WriteRegister, want 50 (FanSpeedResetValue)", fake.bytes[0x11])
	}
}

// RULE-NBFC-HAL-04 — Lua-driven configs are refused at construction.
func TestRULE_NBFC_HAL_04_LuaConfigRefusedAtNew(t *testing.T) {
	cfg := testConfig()
	cfg.LuaLibraries = []string{"math"}
	_, err := New(ProbeOpts{
		Config:       cfg,
		Transport:    &fakeECTransport{},
		WriteEnabled: true,
	})
	if !errors.Is(err, ErrNBFCConfigNeedsLuaRuntime) {
		t.Errorf("expected ErrNBFCConfigNeedsLuaRuntime, got %v", err)
	}
}

// RULE-NBFC-HAL-04 — ACPI-using configs are refused at New when the
// ACPI bridge is nil (no acpi_call DKMS module loaded).
func TestRULE_NBFC_HAL_04_ACPIConfigRefusedWhenBridgeNil(t *testing.T) {
	cfg := testConfig()
	cfg.FanConfigurations[0].ReadAcpiMethod = "\\_SB.PCI0.SFNV"
	_, err := New(ProbeOpts{
		Config:       cfg,
		Transport:    &fakeECTransport{},
		WriteEnabled: true,
	})
	if !errors.Is(err, ErrNBFCConfigNeedsAcpiBridge) {
		t.Errorf("expected ErrNBFCConfigNeedsAcpiBridge, got %v", err)
	}
}

// RULE-NBFC-HAL-04 — ACPI-using configs construct cleanly when the
// bridge is wired. Read dispatches through the bridge instead of
// the EC transport.
func TestRULE_NBFC_HAL_04_ACPIConfigAdmitsWhenBridgeWired(t *testing.T) {
	cfg := testConfig()
	cfg.FanConfigurations[0].ReadAcpiMethod = "\\_SB.PCI0.SFNV"
	// Drop the register fields so the config is pure-ACPI.
	cfg.FanConfigurations[0].ReadRegister = 0
	cfg.FanConfigurations[0].WriteRegister = 0
	bridge := acpi.New(cfg.AcpiMethodsUsed())
	b, err := New(ProbeOpts{
		Config:       cfg,
		Transport:    nil,
		ACPI:         bridge,
		WriteEnabled: true,
	})
	if err != nil {
		t.Fatalf("New with bridge wired: %v", err)
	}
	defer b.Close()
}

// RULE-NBFC-HAL-05 — ReadWriteWords=true → Read16/Write16.
func TestRULE_NBFC_HAL_05_Words16Routes(t *testing.T) {
	cfg := testConfig()
	cfg.ReadWriteWords = true
	cfg.FanConfigurations[0].MinSpeedValue = 0
	cfg.FanConfigurations[0].MaxSpeedValue = 0xFFFF
	fake := &fakeECTransport{}
	b, err := New(ProbeOpts{
		Config:       cfg,
		Transport:    fake,
		WriteEnabled: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close()
	chs, _ := b.Enumerate(context.Background())

	if err := b.Write(chs[0], 255); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// uint16 max → little-endian 0xFF, 0xFF
	if fake.bytes[0x11] != 0xFF || fake.bytes[0x12] != 0xFF {
		t.Errorf("16-bit Write didn't write two bytes: %#x %#x", fake.bytes[0x11], fake.bytes[0x12])
	}
}

// RULE-NBFC-HAL-WRITE-GATE — Write returns ErrNBFCWriteGated when
// WriteEnabled is false.
func TestRULE_NBFC_HAL_WriteGated(t *testing.T) {
	fake := &fakeECTransport{}
	b, err := New(ProbeOpts{
		Config:       testConfig(),
		Transport:    fake,
		WriteEnabled: false,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close()
	chs, _ := b.Enumerate(context.Background())

	err = b.Write(chs[0], 128)
	if !errors.Is(err, ErrNBFCWriteGated) {
		t.Errorf("expected ErrNBFCWriteGated, got %v", err)
	}
	// Verify the underlying transport saw nothing.
	if fake.bytes[0x11] != 0 {
		t.Errorf("transport touched when gated: byte = %#x", fake.bytes[0x11])
	}
	// Read should still work.
	if _, err := b.Read(chs[0]); err != nil {
		t.Errorf("Read should work when writes are gated: %v", err)
	}
	// Restore should be a clean no-op (nothing to restore).
	if err := b.Restore(chs[0]); err != nil {
		t.Errorf("Restore should no-op when gated: %v", err)
	}
}

// RegistersUsed feeds the EC allowlist — sanity check.
func TestConfig_RegistersUsedCoversReadWriteRegisters(t *testing.T) {
	cfg := testConfig()
	regs := cfg.RegistersUsed()
	if !regs[0x10] || !regs[0x11] {
		t.Errorf("RegistersUsed should include 0x10 + 0x11, got %v", regs)
	}
	if regs[0x42] {
		t.Errorf("RegistersUsed should not include unrelated registers")
	}
}

// Backend.Close is idempotent.
func TestBackend_CloseIdempotent(t *testing.T) {
	b, err := New(ProbeOpts{
		Config:       testConfig(),
		Transport:    &fakeECTransport{},
		WriteEnabled: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("second Close: %v, want nil", err)
	}
}

// ec.Transport interface satisfaction check (compile-time).
var _ ec.Transport = (*fakeECTransport)(nil)

// RULE-NBFC-ACPI-04 — Backend.New accepts an ACPI-using config when
// the ACPI bridge is wired AND the EC transport is absent (pure-ACPI
// case, e.g. HP 250 G8 Notebook PC). The earlier
// TestRULE_NBFC_HAL_04_ACPIConfigAdmitsWhenBridgeWired covers the
// "bridge wired" admit; this test specifically pins the dispatch-
// surface contract introduced in PR B3 by confirming that
// Config.AcpiMethodsUsed() resolves to the expected non-empty set,
// the allowlist round-trips into the bridge, and a subsequent
// Enumerate succeeds without touching the EC.
func TestRULE_NBFC_ACPI_04_BackendDispatchesACPI(t *testing.T) {
	cfg := testConfig()
	cfg.FanConfigurations[0].ReadAcpiMethod = "\\_SB.PCI0.SFNV"
	cfg.FanConfigurations[0].WriteAcpiMethod = "\\_SB.PCI0.SFNW"
	cfg.FanConfigurations[0].ReadRegister = 0
	cfg.FanConfigurations[0].WriteRegister = 0

	methods := cfg.AcpiMethodsUsed()
	if !methods["\\_SB.PCI0.SFNV"] || !methods["\\_SB.PCI0.SFNW"] {
		t.Fatalf("AcpiMethodsUsed missing expected methods: %v", methods)
	}
	bridge := acpi.New(methods)
	b, err := New(ProbeOpts{
		Config:       cfg,
		Transport:    nil, // pure-ACPI; no EC required
		ACPI:         bridge,
		WriteEnabled: true,
	})
	if err != nil {
		t.Fatalf("New (pure-ACPI with bridge): %v", err)
	}
	defer b.Close()
	chs, err := b.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(chs) != 1 {
		t.Fatalf("Enumerate len = %d, want 1", len(chs))
	}
	// The bridge holds a closed-set allowlist drawn from the config;
	// invoking a method outside the set must refuse.
	_, err = bridge.Call("\\_SB.EVIL")
	if !errors.Is(err, acpi.ErrACPIMethodNotInConfig) {
		t.Errorf("bridge allowlist should reject unknown method; got %v", err)
	}
}
