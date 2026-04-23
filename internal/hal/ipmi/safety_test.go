package ipmi_test

// safety_test.go binds every rule in .claude/rules/ipmi-safety.md to a
// named subtest. Do not delete a subtest without either (a) deleting the
// matching rule or (b) replacing it with a stronger test that still covers
// the same invariant.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"go.uber.org/goleak"

	"github.com/ventd/ventd/internal/hal/ipmi"
	"github.com/ventd/ventd/internal/testfixture/fakeipmi"
)

// TestIPMISafety_Invariants is the rule-to-test index for the IPMI backend
// safety-critical command path. Each subtest binds one invariant from
// .claude/rules/ipmi-safety.md.
func TestIPMISafety_Invariants(t *testing.T) {
	// sendRecvAdapter bridges fakeipmi.Fake.Respond to the ipmi.WithSendRecv
	// hook. req = [netfn, cmd, data...]; resp is a 128-byte buffer the adapter
	// fills with [cc, payload...] from the fixture response.
	sendRecvAdapter := func(f *fakeipmi.Fake) func(req, resp []byte) error {
		return func(req, resp []byte) error {
			if len(req) < 2 {
				return errors.New("fakeipmi: short IPMI request")
			}
			result, err := f.Respond(req[0], req[1], req[2:])
			if err != nil {
				return err
			}
			copy(resp, result)
			return nil
		}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	// RULE-IPMI-1
	t.Run("supermicro_x11_happy_path", func(t *testing.T) {
		f := fakeipmi.New(t, &fakeipmi.Options{
			Vendor:  "supermicro",
			SDRFans: []fakeipmi.SDRFanRecord{{SensorNumber: 0x01, Name: "FAN1"}},
		})
		b := ipmi.NewBackendForTest(logger, "supermicro", ipmi.WithSendRecv(sendRecvAdapter(f)))

		channels, err := b.Enumerate(ctx)
		if err != nil {
			t.Fatalf("Enumerate: %v", err)
		}
		if len(channels) == 0 {
			t.Fatal("expected >=1 channel from Supermicro X11 SDR enumeration")
		}
		ch := channels[0]

		reading, err := b.Read(ch)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if !reading.OK {
			t.Fatal("expected OK reading from Supermicro fan sensor")
		}

		if err := b.Write(ch, 128); err != nil {
			t.Fatalf("Write: %v", err)
		}

		if err := b.Restore(ch); err != nil {
			t.Fatalf("Restore: %v", err)
		}
	})

	// RULE-IPMI-2
	t.Run("dell_r750_happy_path", func(t *testing.T) {
		f := fakeipmi.New(t, &fakeipmi.Options{
			Vendor:  "dell",
			SDRFans: []fakeipmi.SDRFanRecord{{SensorNumber: 0x21, Name: "FAN_CPU1"}},
		})
		b := ipmi.NewBackendForTest(logger, "dell", ipmi.WithSendRecv(sendRecvAdapter(f)))

		channels, err := b.Enumerate(ctx)
		if err != nil {
			t.Fatalf("Enumerate: %v", err)
		}
		if len(channels) == 0 {
			t.Fatal("expected >=1 channel from Dell R750 SDR enumeration")
		}
		ch := channels[0]

		reading, err := b.Read(ch)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if !reading.OK {
			t.Fatal("expected OK reading from Dell fan sensor")
		}

		if err := b.Write(ch, 64); err != nil {
			t.Fatalf("Write: %v", err)
		}

		if err := b.Restore(ch); err != nil {
			t.Fatalf("Restore: %v", err)
		}
	})

	// RULE-IPMI-3
	t.Run("hpe_ilo_license_required", func(t *testing.T) {
		b := ipmi.NewBackendForTest(logger, "hpe")
		ch := ipmi.MakeTestChannel(0x01, "FAN1")

		err := b.Write(ch, 128)
		if err == nil {
			t.Fatal("expected error from HPE Write, got nil")
		}
		if !strings.Contains(err.Error(), "iLO Advanced") {
			t.Errorf("HPE Write error %q does not mention 'iLO Advanced'", err.Error())
		}

		// Restore is a no-op for HPE — must not panic or attempt BMC writes.
		if err := b.Restore(ch); err != nil {
			t.Errorf("Restore on HPE should be a no-op, got %v", err)
		}
	})

	// RULE-IPMI-4
	t.Run("unknown_vendor_refuses_manual_mode", func(t *testing.T) {
		b := ipmi.NewBackendForTest(logger, "unknown")
		ch := ipmi.MakeTestChannel(0x01, "FAN1")

		err := b.Write(ch, 128)
		if err == nil {
			t.Fatal("expected error from unknown-vendor Write, got nil")
		}
		if !strings.Contains(err.Error(), "unsupported vendor") {
			t.Errorf("unknown-vendor Write error %q does not contain 'unsupported vendor'", err.Error())
		}
	})

	// RULE-IPMI-5
	t.Run("bmc_busy_retry_succeeds", func(t *testing.T) {
		f := fakeipmi.New(t, &fakeipmi.Options{Vendor: "supermicro", BusyCount: 1})
		b := ipmi.NewBackendForTest(logger, "supermicro", ipmi.WithSendRecv(sendRecvAdapter(f)))
		ch := ipmi.MakeTestChannel(0x01, "FAN1")

		// First Write: BMC returns CC=0xC3 (node busy) — must surface as error.
		err := b.Write(ch, 128)
		if err == nil {
			t.Fatal("expected error while BMC busy (CC=0xC3), got nil")
		}

		// Second Write: BusyCount exhausted — must succeed.
		if err := b.Write(ch, 128); err != nil {
			t.Fatalf("Write after busy cleared: %v", err)
		}
	})

	// RULE-IPMI-6
	t.Run("ioctl_timeout_no_goroutine_leak", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		timeoutErr := errors.New("ipmi: BMC response timeout")
		b := ipmi.NewBackendForTest(logger, "supermicro",
			ipmi.WithSendRecv(func(req, resp []byte) error {
				return timeoutErr
			}),
		)
		ch := ipmi.MakeTestChannel(0x01, "FAN1")

		err := b.Write(ch, 128)
		if err == nil {
			t.Fatal("expected error from timed-out Write, got nil")
		}
		if !errors.Is(err, timeoutErr) {
			t.Errorf("Write error %v does not wrap timeout sentinel; want errors.Is match", err)
		}
	})

	// RULE-IPMI-7
	t.Run("restore_on_exit_all_channels", func(t *testing.T) {
		f := fakeipmi.New(t, &fakeipmi.Options{Vendor: "supermicro"})

		type cmd struct{ netfn, cmdByte byte }
		var (
			mu   sync.Mutex
			cmds []cmd
		)

		adapter := func(req, resp []byte) error {
			if len(req) >= 2 {
				mu.Lock()
				cmds = append(cmds, cmd{req[0], req[1]})
				mu.Unlock()
			}
			result, err := f.Respond(req[0], req[1], req[2:])
			if err != nil {
				return err
			}
			copy(resp, result)
			return nil
		}

		b := ipmi.NewBackendForTest(logger, "supermicro", ipmi.WithSendRecv(adapter))
		ch1 := ipmi.MakeTestChannel(0x01, "FAN1")
		ch2 := ipmi.MakeTestChannel(0x02, "FAN2")

		// Establish fan control for both channels.
		if err := b.Write(ch1, 128); err != nil {
			t.Fatalf("Write ch1: %v", err)
		}
		if err := b.Write(ch2, 128); err != nil {
			t.Fatalf("Write ch2: %v", err)
		}

		// Reset the log; capture only Restore commands from here.
		mu.Lock()
		cmds = nil
		mu.Unlock()

		// Watchdog exit path: Restore every channel.
		if err := b.Restore(ch1); err != nil {
			t.Errorf("Restore ch1: %v", err)
		}
		if err := b.Restore(ch2); err != nil {
			t.Errorf("Restore ch2: %v", err)
		}

		mu.Lock()
		restoreCmds := append([]cmd(nil), cmds...)
		mu.Unlock()

		// Every channel must have triggered a Supermicro SET_FAN_MODE command.
		const wantRestores = 2
		if len(restoreCmds) != wantRestores {
			t.Fatalf("Restore sent %d commands, want %d (one per channel)", len(restoreCmds), wantRestores)
		}
		for i, c := range restoreCmds {
			if c.netfn != 0x30 || c.cmdByte != 0x45 {
				t.Errorf("restore[%d]: got (netfn=0x%02x cmd=0x%02x), want (0x30, 0x45 SET_FAN_MODE)", i, c.netfn, c.cmdByte)
			}
		}
	})
}
