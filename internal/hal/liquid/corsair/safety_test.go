package corsair

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hal/liquid"
	"github.com/ventd/ventd/internal/hal/usbbase/hidraw"
	"github.com/ventd/ventd/internal/testfixture/fakehid"
)

// TestLiquidSafety_Invariants contains one subtest per RULE-LIQUID-* binding.
// Subtest names must match the Bound: lines in docs/rules/liquid-safety.md
// character-for-character so that rulelint can verify them.
func TestLiquidSafety_Invariants(t *testing.T) {

	// ── PumpMinimumFloor (RULE-LIQUID-01) ─────────────────────────────────────
	// Pump duty must be silently floored to pumpMin even when the caller
	// requests a lower value. A misbehaving controller that writes duty=0 must
	// not be able to stall the pump.
	t.Run("PumpMinimumFloor", func(t *testing.T) {
		dev := fakehid.NewCorsairDevice(fakehid.CorsairConfig{
			VID: 0x1b1c, PID: 0x0c1c, HasPump: true,
		})
		pipe := fakehid.NewCorsairPipe(dev)
		pd := &probedDevice{
			hid:     pipe,
			info:    hidraw.DeviceInfo{Path: "/dev/hidraw0"},
			hasPump: true,
		}
		b := &corsairBackend{
			inner:    pd,
			writable: true,
			pumpMin:  50,
			channels: []channelEntry{
				{index: 0, role: hal.RolePump, isPump: true},
			},
		}

		ch := hal.Channel{ID: "/dev/hidraw0-0"}
		if err := b.Write(ch, 10); err != nil {
			t.Fatalf("Write(10): %v", err)
		}

		if len(dev.DutiesWritten) == 0 {
			t.Fatal("no duties recorded by the fixture")
		}
		got := dev.DutiesWritten[len(dev.DutiesWritten)-1]
		if got.Duty < 50 {
			t.Errorf("pump duty written = %d, want >= pumpMin (50)", got.Duty)
		}
	})

	// ── ReconnectPumpFloor (RULE-LIQUID-02) ───────────────────────────────────
	// After a USB reconnect (reconnecting=true), the first write must issue a
	// pump-floor write before any other command sequence. The floor write must
	// be the very first duty write the device sees.
	t.Run("ReconnectPumpFloor", func(t *testing.T) {
		dev := fakehid.NewCorsairDevice(fakehid.CorsairConfig{
			VID: 0x1b1c, PID: 0x0c1c, HasPump: true,
		})
		pipe := fakehid.NewCorsairPipe(dev)
		pd := &probedDevice{
			hid:     pipe,
			info:    hidraw.DeviceInfo{Path: "/dev/hidraw0"},
			hasPump: true,
		}
		b := &corsairBackend{
			inner:        pd,
			writable:     true,
			pumpMin:      50,
			reconnecting: true,
			channels: []channelEntry{
				{index: 0, role: hal.RolePump, isPump: true},
			},
		}

		ch := hal.Channel{ID: "/dev/hidraw0-0"}
		if err := b.Write(ch, 80); err != nil {
			t.Fatalf("Write(80): %v", err)
		}

		if len(dev.DutiesWritten) < 2 {
			t.Fatalf("expected ≥2 duty writes (floor + requested), got %d", len(dev.DutiesWritten))
		}
		floor := dev.DutiesWritten[0]
		requested := dev.DutiesWritten[1]
		if floor.Duty != 50 {
			t.Errorf("first write (reconnect floor) = %d, want 50", floor.Duty)
		}
		if requested.Duty != 80 {
			t.Errorf("second write (requested duty) = %d, want 80", requested.Duty)
		}
	})

	// ── UnknownFirmwareReadOnly (RULE-LIQUID-03, defence-in-depth) ────────────
	// The v0.4 firmware-allowlist gate was removed in v0.6.1 — Corsair
	// writes now proceed unconditionally on the happy path. The Write
	// path's `if !b.writable { return ErrReadOnlyUnvalidatedFirmware }`
	// remains as defence-in-depth for any future re-introduction of a
	// genuine refusal cause. This test pins that surface: a manually-
	// constructed backend with writable=false (a state production
	// no longer produces) still refuses cleanly without touching HID.
	t.Run("UnknownFirmwareReadOnly", func(t *testing.T) {
		dev := fakehid.NewCorsairDevice(fakehid.CorsairConfig{
			VID: 0x1b1c, PID: 0x0c1c,
		})
		pipe := fakehid.NewCorsairPipe(dev)
		pd := &probedDevice{
			hid:  pipe,
			info: hidraw.DeviceInfo{Path: "/dev/hidraw0"},
		}
		b := &corsairBackend{
			inner:    pd,
			writable: false, // synthetic — production always sets true post-v0.6.1
			pumpMin:  50,
			channels: []channelEntry{{index: 0, role: hal.RoleCase}},
		}

		ch := hal.Channel{ID: "/dev/hidraw0-0"}
		err := b.Write(ch, 80)
		if err == nil {
			t.Fatal("Write on synthetic-read-only device returned nil, want ErrReadOnlyUnvalidatedFirmware")
		}
		if !errors.Is(err, liquid.ErrReadOnlyUnvalidatedFirmware) {
			t.Errorf("got %v, want ErrReadOnlyUnvalidatedFirmware", err)
		}
		if len(dev.DutiesWritten) != 0 {
			t.Errorf("synthetic-read-only Write issued %d duty commands, want 0", len(dev.DutiesWritten))
		}
	})

	// ── RestoreCompletesOnPanic (RULE-LIQUID-04) ───────────────────────────────
	// A panic in one restorer must not abort subsequent restorers. The per-entry
	// recover in restoreAllSafe must fire and allow the remaining channels to be
	// restored.
	t.Run("RestoreCompletesOnPanic", func(t *testing.T) {
		restored := make([]bool, 3)
		restorers := []func() error{
			func() error { restored[0] = true; return nil },
			func() error { panic("synthetic panic in restorer 1") },
			func() error { restored[2] = true; return nil },
		}

		restoreAllSafe(restorers, slog.Default())

		if !restored[0] {
			t.Error("channel 0 was not restored (ran before the panic)")
		}
		if !restored[2] {
			t.Error("channel 2 was not restored after the panic in channel 1")
		}
	})

	// ── SerialisedWrites (RULE-LIQUID-05) ─────────────────────────────────────
	// Concurrent Write calls on the same backend must never issue overlapping
	// HID transfers. The per-device mutex must serialise all HID activity for
	// the full command-plus-response round-trip. CorsairPipe.ConcurrencyViolated
	// is set true if two goroutines call pipe.Write concurrently.
	t.Run("SerialisedWrites", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		dev := fakehid.NewCorsairDevice(fakehid.CorsairConfig{
			VID: 0x1b1c, PID: 0x0c1c, HasPump: true,
			HandleDelay: 10 * time.Millisecond,
		})
		pipe := fakehid.NewCorsairPipe(dev)
		pd := &probedDevice{
			hid:     pipe,
			info:    hidraw.DeviceInfo{Path: "/dev/hidraw0"},
			hasPump: true,
		}
		b := &corsairBackend{
			inner:    pd,
			writable: true,
			pumpMin:  50,
			channels: []channelEntry{
				{index: 0, role: hal.RolePump, isPump: true},
				{index: 1, role: hal.RoleCase},
			},
		}

		chIDs := []string{"/dev/hidraw0-0", "/dev/hidraw0-1"}
		var wg sync.WaitGroup
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				ch := hal.Channel{ID: chIDs[n%2]}
				_ = b.Write(ch, 80)
			}(i)
		}
		wg.Wait()

		if pipe.ConcurrencyViolated {
			t.Error("concurrent HID writes detected: per-device mutex failed to serialise (RULE-LIQUID-05)")
		}
	})

	// ── WriteRequiresFlagAndAllowlist (RULE-LIQUID-06) ────────────────────────
	// v0.4: writes required UnsafeCorsairWrites=true AND an allow-listed
	// firmware. v0.6.1 removed BOTH gates per feedback-dont-default-writes-off
	// — the closed-set primitives (pump-floor, USB-reconnect floor, restore-
	// on-panic, serialised writes) are the safety mechanism. This test now
	// pins the post-v0.6.1 contract: probeWith with any firmware returns a
	// writable backend whose Write does NOT surface ErrReadOnlyUnvalidatedFirmware.
	t.Run("WriteRequiresFlagAndAllowlist", func(t *testing.T) {
		makePipe := func(maj, min, pat uint8) func(string) (openResult, error) {
			return func(path string) (openResult, error) {
				d := fakehid.NewCorsairDevice(fakehid.CorsairConfig{
					VID: 0x1b1c, PID: 0x0c1c,
					FirmwareMajor: maj,
					FirmwareMinor: min,
					FirmwarePatch: pat,
				})
				return openResult{
					hid:  fakehid.NewCorsairPipe(d),
					info: hidraw.DeviceInfo{ProductID: 0x0c1c, VendorID: 0x1b1c, Path: path},
				}, nil
			}
		}
		const testPath = "/dev/hidraw9999"

		// Any firmware: backend is writable on the happy path.
		for _, fw := range [][]uint8{{1, 2, 3}, {9, 9, 9}, {0, 0, 0}} {
			b, err := probeWith(testPath, ProbeOptions{PumpMinimum: 50}, makePipe(fw[0], fw[1], fw[2]))
			if err != nil {
				t.Fatalf("probeWith (fw=%v): %v", fw, err)
			}
			pumpCh := hal.Channel{ID: testPath + "-0"}
			if werr := b.Write(pumpCh, 80); errors.Is(werr, liquid.ErrReadOnlyUnvalidatedFirmware) {
				t.Errorf("fw=%v: Write returned ErrReadOnlyUnvalidatedFirmware, want writable post-v0.6.1", fw)
			}
			_ = b.Close()
		}
	})

	// ── UnbindConflictingDriver (RULE-LIQUID-07) ──────────────────────────────
	// Before opening a hidraw device, ventd must check for a bound kernel driver
	// and attempt to unbind it. If unbind succeeds, the device is accessible.
	// If unbind fails, the error must wrap ErrKernelDriverOwnsDevice and include
	// the driver name and a blacklist remediation hint.
	t.Run("UnbindConflictingDriver", func(t *testing.T) {
		root := t.TempDir()
		sysClassHidraw := filepath.Join(root, "sys", "class", "hidraw")
		sysDrivers := filepath.Join(root, "sys", "bus", "hid", "drivers")
		node := "hidraw0"
		driverName := "corsair-cpro"

		// Build the sysfs tree:
		//   sysClassHidraw/hidraw0/device → hidAddrDir (symlink)
		//   hidAddrDir/driver             → driverDir (symlink)
		hidAddr := "0003:1B1C:0C32.0001"
		hidAddrDir := filepath.Join(root, "devices", hidAddr)
		if err := os.MkdirAll(hidAddrDir, 0o755); err != nil {
			t.Fatal(err)
		}
		driverDir := filepath.Join(sysDrivers, driverName)
		if err := os.MkdirAll(driverDir, 0o755); err != nil {
			t.Fatal(err)
		}
		deviceLink := filepath.Join(sysClassHidraw, node, "device")
		if err := os.MkdirAll(filepath.Dir(deviceLink), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(hidAddrDir, deviceLink); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(driverDir, filepath.Join(hidAddrDir, "driver")); err != nil {
			t.Fatal(err)
		}

		hidrawPath := "/dev/" + node

		// Sub-case A: writable unbind file → unbind succeeds, checkAndUnbindFrom returns nil.
		unbindPath := filepath.Join(driverDir, "unbind")
		if err := os.WriteFile(unbindPath, nil, 0o666); err != nil {
			t.Fatal(err)
		}
		if err := checkAndUnbindFrom(sysClassHidraw, sysDrivers, hidrawPath); err != nil {
			t.Errorf("unbind should succeed with writable unbind file, got: %v", err)
		}
		if err := os.Remove(unbindPath); err != nil {
			t.Fatal(err)
		}

		// Sub-case B: read-only unbind file → unbind fails, error wraps ErrKernelDriverOwnsDevice
		// with actionable message. Skipped when root (root can write anything).
		if os.Getuid() == 0 {
			t.Skip("running as root: read-only files are writable; skipping failure sub-case")
		}
		if err := os.WriteFile(unbindPath, nil, 0o444); err != nil {
			t.Fatal(err)
		}

		err := checkAndUnbindFrom(sysClassHidraw, sysDrivers, hidrawPath)
		if err == nil {
			t.Fatal("expected error when unbind fails, got nil")
		}
		if !errors.Is(err, liquid.ErrKernelDriverOwnsDevice) {
			t.Errorf("error does not wrap ErrKernelDriverOwnsDevice: %v", err)
		}
		msg := err.Error()
		for _, want := range []string{driverName, "blacklist"} {
			if len(msg) < len(want) {
				t.Errorf("error message %q too short to contain %q", msg, want)
				continue
			}
			found := false
			for i := 0; i+len(want) <= len(msg); i++ {
				if msg[i:i+len(want)] == want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("error message %q does not mention %q", msg, want)
			}
		}
	})
}
