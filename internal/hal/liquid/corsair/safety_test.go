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
// Subtest names must match the Bound: lines in .claude/rules/liquid-safety.md
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

	// ── UnknownFirmwareReadOnly (RULE-LIQUID-03) ───────────────────────────────
	// A device whose firmware is not on the allow-list must return
	// ErrReadOnlyUnvalidatedFirmware from Write and must not issue any HID
	// command.
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
			writable: false,
			pumpMin:  50,
			channels: []channelEntry{{index: 0, role: hal.RoleCase}},
		}

		ch := hal.Channel{ID: "/dev/hidraw0-0"}
		err := b.Write(ch, 80)
		if err == nil {
			t.Fatal("Write on read-only device returned nil, want ErrReadOnlyUnvalidatedFirmware")
		}
		if !errors.Is(err, liquid.ErrReadOnlyUnvalidatedFirmware) {
			t.Errorf("got %v, want ErrReadOnlyUnvalidatedFirmware", err)
		}
		if len(dev.DutiesWritten) != 0 {
			t.Errorf("read-only Write issued %d duty commands, want 0", len(dev.DutiesWritten))
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
	// Write access requires BOTH UnsafeCorsairWrites=true AND firmware on the
	// allow-list. Either condition false → ErrReadOnlyUnvalidatedFirmware.
	// Both true → writable (Write does not return that error).
	t.Run("WriteRequiresFlagAndAllowlist", func(t *testing.T) {
		// Insert a test firmware version into the allow-list for this subtest.
		fw := firmwareVersion{major: 9, minor: 9, patch: 9}
		firmwareAllowList[fw] = true
		defer delete(firmwareAllowList, fw)

		// makePipe returns an openFn that injects a CorsairPipe with the given firmware.
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

		// Paths whose sysfs node won't exist → CheckAndUnbind returns nil.
		const testPath = "/dev/hidraw9999"

		// Case 1: flag=false, firmware on list → read-only.
		b1, err := probeWith(testPath, ProbeOptions{UnsafeCorsairWrites: false, PumpMinimum: 50}, makePipe(9, 9, 9))
		if err != nil {
			t.Fatalf("probeWith (flag=false): %v", err)
		}
		ch := hal.Channel{ID: testPath + "-1"}
		if werr := b1.Write(ch, 80); !errors.Is(werr, liquid.ErrReadOnlyUnvalidatedFirmware) {
			t.Errorf("flag=false: got %v, want ErrReadOnlyUnvalidatedFirmware", werr)
		}
		_ = b1.Close()

		// Case 2: flag=true, firmware NOT on list → read-only.
		b2, err := probeWith(testPath, ProbeOptions{UnsafeCorsairWrites: true, PumpMinimum: 50}, makePipe(1, 2, 3))
		if err != nil {
			t.Fatalf("probeWith (fw not listed): %v", err)
		}
		if werr := b2.Write(ch, 80); !errors.Is(werr, liquid.ErrReadOnlyUnvalidatedFirmware) {
			t.Errorf("fw not listed: got %v, want ErrReadOnlyUnvalidatedFirmware", werr)
		}
		_ = b2.Close()

		// Case 3: flag=true AND firmware on list → writable.
		b3, err := probeWith(testPath, ProbeOptions{UnsafeCorsairWrites: true, PumpMinimum: 50}, makePipe(9, 9, 9))
		if err != nil {
			t.Fatalf("probeWith (both true): %v", err)
		}
		pumpCh := hal.Channel{ID: testPath + "-0"}
		if werr := b3.Write(pumpCh, 80); errors.Is(werr, liquid.ErrReadOnlyUnvalidatedFirmware) {
			t.Error("flag+fw both true: Write returned ErrReadOnlyUnvalidatedFirmware, want writable")
		}
		_ = b3.Close()
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
