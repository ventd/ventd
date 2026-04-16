package main

import (
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwmon"
)

// TestRebindTrigger_MatchSignalsRestart covers the happy path of Option A
// (#95): a rebind event whose key matches a configured HwmonDevice sends
// exactly one value on restartCh.
func TestRebindTrigger_MatchSignalsRestart(t *testing.T) {
	const chip = "/sys/devices/platform/nct6687.2608"
	var live atomic.Pointer[config.Config]
	live.Store(&config.Config{
		Fans: []config.Fan{
			{Name: "cpu", Type: "hwmon", HwmonDevice: chip, PWMPath: "/sys/class/hwmon/hwmon5/pwm1"},
		},
	})
	restartCh := make(chan struct{}, 1)
	trig := newRebindTrigger(&live, restartCh, slog.New(slog.NewTextHandler(io.Discard, nil)))

	trig(chip, hwmon.DeviceFingerprint{ChipName: "nct6687d"})

	select {
	case <-restartCh:
	case <-time.After(time.Second):
		t.Fatalf("restart was not signalled within 1s")
	}
}

// TestRebindTrigger_NoMatchDoesNotSignal covers the common case where a
// topology change concerns a device nothing in the config cares about
// (unrelated USB hwmon, drm-device add events, etc.): the trigger must not
// cause a spurious restart.
func TestRebindTrigger_NoMatchDoesNotSignal(t *testing.T) {
	var live atomic.Pointer[config.Config]
	live.Store(&config.Config{
		Fans: []config.Fan{
			{Name: "cpu", Type: "hwmon", HwmonDevice: "/sys/devices/platform/nct6687.2608", PWMPath: "/sys/class/hwmon/hwmon5/pwm1"},
		},
	})
	restartCh := make(chan struct{}, 1)
	trig := newRebindTrigger(&live, restartCh, slog.New(slog.NewTextHandler(io.Discard, nil)))

	trig("/sys/devices/platform/asus-wmi-sensors", hwmon.DeviceFingerprint{ChipName: "asus_wmi"})

	select {
	case <-restartCh:
		t.Fatalf("unexpected restart signal for non-matching device")
	case <-time.After(50 * time.Millisecond):
	}
}

// TestRebindTrigger_SensorMatch confirms the same match rule applies when
// the configured HwmonDevice belongs to a Sensor rather than a Fan — e.g.
// a thermistor-only chip providing input to a curve without owning any
// pwm channel.
func TestRebindTrigger_SensorMatch(t *testing.T) {
	const chip = "/sys/devices/platform/k10temp"
	var live atomic.Pointer[config.Config]
	live.Store(&config.Config{
		Sensors: []config.Sensor{
			{Name: "tctl", Type: "hwmon", HwmonDevice: chip, Path: "/sys/class/hwmon/hwmon1/temp1_input"},
		},
	})
	restartCh := make(chan struct{}, 1)
	trig := newRebindTrigger(&live, restartCh, slog.New(slog.NewTextHandler(io.Discard, nil)))

	trig(chip, hwmon.DeviceFingerprint{ChipName: "k10temp"})

	select {
	case <-restartCh:
	case <-time.After(time.Second):
		t.Fatalf("restart was not signalled for sensor match")
	}
}

// TestRebindTrigger_DropsWhenRestartPending verifies the non-blocking send:
// if restartCh already holds a pending signal, a second invocation is
// dropped rather than blocking the watcher goroutine.
func TestRebindTrigger_DropsWhenRestartPending(t *testing.T) {
	const chip = "/sys/devices/platform/nct6687.2608"
	var live atomic.Pointer[config.Config]
	live.Store(&config.Config{
		Fans: []config.Fan{
			{Name: "cpu", Type: "hwmon", HwmonDevice: chip, PWMPath: "/sys/class/hwmon/hwmon5/pwm1"},
		},
	})
	restartCh := make(chan struct{}, 1)
	trig := newRebindTrigger(&live, restartCh, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// First call fills the channel.
	trig(chip, hwmon.DeviceFingerprint{ChipName: "nct6687d"})
	// Second call must not block.
	done := make(chan struct{})
	go func() {
		trig(chip, hwmon.DeviceFingerprint{ChipName: "nct6687d"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("second trigger blocked on full restartCh — non-blocking send regressed")
	}

	// The channel should still hold exactly one value.
	select {
	case <-restartCh:
	default:
		t.Fatalf("expected one pending restart on restartCh")
	}
	select {
	case <-restartCh:
		t.Fatalf("expected exactly one pending restart; found a second")
	default:
	}
}

// TestRebindTrigger_IgnoresEmptyHwmonDevice protects against a degenerate
// config where an entry has HwmonDevice set to the empty string: the
// trigger must not match every key="" promotion.
func TestRebindTrigger_IgnoresEmptyHwmonDevice(t *testing.T) {
	var live atomic.Pointer[config.Config]
	live.Store(&config.Config{
		Fans: []config.Fan{
			{Name: "f", Type: "hwmon", HwmonDevice: "", PWMPath: "/sys/class/hwmon/hwmon5/pwm1"},
		},
	})
	restartCh := make(chan struct{}, 1)
	trig := newRebindTrigger(&live, restartCh, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Even an empty-key match must not signal.
	trig("", hwmon.DeviceFingerprint{})
	select {
	case <-restartCh:
		t.Fatalf("trigger signalled on empty HwmonDevice — match rule regressed")
	case <-time.After(25 * time.Millisecond):
	}
}
