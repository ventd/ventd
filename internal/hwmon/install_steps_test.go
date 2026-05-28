package hwmon

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hal/msiec"
)

// fakeHALBackend is a minimal hal.FanBackend used by the stepVerify
// HAL-fallback tests. Only Enumerate is meaningful — the install
// pipeline calls only that path. Read/Write/Restore/Close return
// zero values so the interface is satisfied without producing
// surprising state during a test that goes off-script.
type fakeHALBackend struct {
	name     string
	channels []hal.Channel
	enumErr  error
}

func (b *fakeHALBackend) Name() string                          { return b.name }
func (b *fakeHALBackend) Close() error                          { return nil }
func (b *fakeHALBackend) Read(hal.Channel) (hal.Reading, error) { return hal.Reading{}, nil }
func (b *fakeHALBackend) Write(hal.Channel, uint8) error        { return nil }
func (b *fakeHALBackend) Restore(hal.Channel) error             { return nil }
func (b *fakeHALBackend) Enumerate(_ context.Context) ([]hal.Channel, error) {
	return b.channels, b.enumErr
}

// withEmptyHwmonPoll swaps stepVerifyHwmonPoll for a stub returning no
// pwm paths and restores the original on cleanup. Required because test
// hosts may have real hwmon devices (the homelab dev box does; CI
// runners typically don't), and the HAL-fallback contract is only
// observable when the hwmon poll returns empty.
func withEmptyHwmonPoll(t *testing.T) {
	t.Helper()
	orig := stepVerifyHwmonPoll
	stepVerifyHwmonPoll = func() []string { return nil }
	t.Cleanup(func() { stepVerifyHwmonPoll = orig })
}

func TestStepVerify_FallsBackToHALBackend(t *testing.T) {
	// Drive stepVerify's hwmon poll to empty so the HAL-fallback branch
	// is the load-bearing decision. Without this swap, dev hosts with
	// real motherboards would short-circuit on hwmon hits and the test
	// would silently pass for the wrong reason.

	t.Run("HAL backend with one CapWritePWM channel accepts install", func(t *testing.T) {
		withEmptyHwmonPoll(t)
		hal.Reset()
		t.Cleanup(hal.Reset)
		hal.Register("fakemsiec", &fakeHALBackend{
			name: "fakemsiec",
			channels: []hal.Channel{
				{
					ID:     "/sys/devices/platform/fakemsiec",
					Role:   hal.RoleCPU,
					Caps:   hal.CapRead | hal.CapWritePWM | hal.CapRestore,
					Opaque: struct{}{},
				},
			},
		})
		var loggedLines []string
		c := PipelineConfig{
			Driver: DriverNeed{
				Key:        "fakemsiec_driver",
				Module:     "fakemsiec",
				HALBackend: "fakemsiec",
			},
			Logger: slog.New(slog.DiscardHandler),
			Log:    func(s string) { loggedLines = append(loggedLines, s) },
		}
		if err := stepVerify(c); err != nil {
			t.Fatalf("stepVerify: %v", err)
		}
		// Confirm the success log mentions the backend so operators see
		// the non-hwmon route in the journal, not just generic success.
		joined := strings.Join(loggedLines, "\n")
		if !strings.Contains(joined, "fakemsiec backend") {
			t.Errorf("expected log line to mention 'fakemsiec backend', got:\n%s", joined)
		}
	})

	t.Run("HAL backend with zero CapWritePWM channels still rejects", func(t *testing.T) {
		withEmptyHwmonPoll(t)
		hal.Reset()
		t.Cleanup(hal.Reset)
		// Channel exists but is read-only — not a controllable surface.
		hal.Register("fakemsiec_readonly", &fakeHALBackend{
			name: "fakemsiec_readonly",
			channels: []hal.Channel{
				{ID: "x", Caps: hal.CapRead, Opaque: struct{}{}},
			},
		})
		c := PipelineConfig{
			Driver: DriverNeed{
				Key:        "fakemsiec_readonly_driver",
				Module:     "fakemsiec_readonly",
				HALBackend: "fakemsiec_readonly",
			},
			Logger: slog.New(slog.DiscardHandler),
			Log:    func(string) {},
		}
		err := stepVerify(c)
		if !errors.Is(err, ErrNoPWMChannelsAppeared) {
			t.Errorf("want ErrNoPWMChannelsAppeared, got %v", err)
		}
	})

	t.Run("HAL backend not registered still rejects (no panic)", func(t *testing.T) {
		withEmptyHwmonPoll(t)
		hal.Reset()
		t.Cleanup(hal.Reset)
		c := PipelineConfig{
			Driver: DriverNeed{
				Key:        "missing_backend_driver",
				Module:     "missing",
				HALBackend: "this-backend-does-not-exist",
			},
			Logger: slog.New(slog.DiscardHandler),
			Log:    func(string) {},
		}
		err := stepVerify(c)
		if !errors.Is(err, ErrNoPWMChannelsAppeared) {
			t.Errorf("want ErrNoPWMChannelsAppeared, got %v", err)
		}
	})

	t.Run("HAL backend Enumerate error treated as no channels", func(t *testing.T) {
		withEmptyHwmonPoll(t)
		hal.Reset()
		t.Cleanup(hal.Reset)
		hal.Register("fakemsiec_errorbackend", &fakeHALBackend{
			name:    "fakemsiec_errorbackend",
			enumErr: errors.New("simulated enumerate failure"),
		})
		c := PipelineConfig{
			Driver: DriverNeed{
				Key:        "errorbackend_driver",
				Module:     "errorbackend",
				HALBackend: "fakemsiec_errorbackend",
			},
			Logger: slog.New(slog.DiscardHandler),
			Log:    func(string) {},
		}
		err := stepVerify(c)
		if !errors.Is(err, ErrNoPWMChannelsAppeared) {
			t.Errorf("want ErrNoPWMChannelsAppeared, got %v", err)
		}
	})

	t.Run("driver with empty HALBackend keeps existing hwmon-only behaviour", func(t *testing.T) {
		withEmptyHwmonPoll(t)
		hal.Reset()
		t.Cleanup(hal.Reset)
		// Register a backend that WOULD say yes — but Driver.HALBackend
		// is empty so it must NOT be consulted. This pins the
		// regression-safety contract: hwmon-shaped drivers (it87,
		// nct6687d) are unchanged.
		hal.Register("would_accept_but_not_consulted", &fakeHALBackend{
			name: "would_accept_but_not_consulted",
			channels: []hal.Channel{
				{ID: "y", Caps: hal.CapWritePWM, Opaque: struct{}{}},
			},
		})
		c := PipelineConfig{
			Driver: DriverNeed{
				Key:        "it8688e_like",
				Module:     "it87",
				HALBackend: "", // hwmon-shaped driver
			},
			Logger: slog.New(slog.DiscardHandler),
			Log:    func(string) {},
		}
		err := stepVerify(c)
		if !errors.Is(err, ErrNoPWMChannelsAppeared) {
			t.Errorf("want ErrNoPWMChannelsAppeared (hwmon-only check), got %v", err)
		}
	})
}

// TestStepVerify_FirmwarePinSuggestionForMsiec pins #1168: when stepVerify
// can't see a controllable channel and the driver is msi-ec, it must
// consult the firmware-diagnose layer and surface an
// ErrFirmwareNotCatalogued that still unwraps to ErrNoPWMChannelsAppeared
// (so setup's retry loop keeps working) but carries the detected
// firmware + suggestions for the wizard's recovery card.
func TestStepVerify_FirmwarePinSuggestionForMsiec(t *testing.T) {
	withEmptyHwmonPoll(t)
	hal.Reset()
	t.Cleanup(hal.Reset)
	// Backend present but reports no controllable channels — matches
	// the "msi-ec.ko loaded but platform device never appeared" mode.
	hal.Register("msiec", &fakeHALBackend{name: "msiec", channels: nil})

	// Inject a dmesg-shaped firmware-not-supported line.
	prevDiag := stepVerifyDiagnoseFirmware
	prevSugg := stepVerifySuggestFirmwarePins
	t.Cleanup(func() {
		stepVerifyDiagnoseFirmware = prevDiag
		stepVerifySuggestFirmwarePins = prevSugg
	})
	stepVerifyDiagnoseFirmware = func(context.Context) (string, error) {
		return "16R8IMS1.999", nil
	}
	stepVerifySuggestFirmwarePins = func(detected string, maxN int) []msiec.FirmwareSuggestion {
		if detected != "16R8IMS1.999" || maxN != 3 {
			t.Fatalf("stepVerify called SuggestFirmwarePins(%q, %d); want (16R8IMS1.999, 3)", detected, maxN)
		}
		return []msiec.FirmwareSuggestion{
			{Firmware: "16R8IMS1.117", Group: "CONF_G2_6"},
		}
	}

	c := PipelineConfig{
		Driver: PipelineConfig{}.Driver,
		Logger: slog.New(slog.DiscardHandler),
		Log:    func(string) {},
	}
	c.Driver.Module = "msi-ec"
	c.Driver.HALBackend = "msiec"
	err := stepVerify(c)
	// Setup retry loop relies on this unwrap chain.
	if !errors.Is(err, ErrNoPWMChannelsAppeared) {
		t.Fatalf("err = %v; want errors.Is(ErrNoPWMChannelsAppeared)", err)
	}
	var enriched *ErrFirmwareNotCatalogued
	if !errors.As(err, &enriched) {
		t.Fatalf("err = %v; want errors.As(*ErrFirmwareNotCatalogued)", err)
	}
	if enriched.DetectedFirmware != "16R8IMS1.999" {
		t.Errorf("DetectedFirmware = %q; want 16R8IMS1.999", enriched.DetectedFirmware)
	}
	if len(enriched.Suggestions) != 1 || enriched.Suggestions[0].Firmware != "16R8IMS1.117" {
		t.Errorf("Suggestions = %+v; want exactly [16R8IMS1.117/CONF_G2_6]", enriched.Suggestions)
	}
	if !strings.Contains(enriched.Error(), "16R8IMS1.117") {
		t.Errorf("Error() should mention the suggested pin; got: %s", enriched.Error())
	}
}

// TestStepVerify_NonMsiecDriverSkipsFirmwareDiagnose pins the contract that
// hwmon-shaped drivers (it87, nct6687d, …) are unaffected by the
// firmware-diagnose enrichment. Their failure mode is "wrong chip variant"
// rather than "unsupported firmware string."
func TestStepVerify_NonMsiecDriverSkipsFirmwareDiagnose(t *testing.T) {
	withEmptyHwmonPoll(t)
	hal.Reset()
	t.Cleanup(hal.Reset)

	called := false
	prev := stepVerifyDiagnoseFirmware
	t.Cleanup(func() { stepVerifyDiagnoseFirmware = prev })
	stepVerifyDiagnoseFirmware = func(context.Context) (string, error) {
		called = true
		return "anything", nil
	}

	c := PipelineConfig{
		Driver: PipelineConfig{}.Driver,
		Logger: slog.New(slog.DiscardHandler),
		Log:    func(string) {},
	}
	c.Driver.Module = "it87"
	c.Driver.HALBackend = ""
	err := stepVerify(c)
	if !errors.Is(err, ErrNoPWMChannelsAppeared) {
		t.Fatalf("err = %v; want ErrNoPWMChannelsAppeared", err)
	}
	if called {
		t.Errorf("firmware-diagnose should not be consulted for non-msi-ec drivers; it87 install triggered it")
	}
	var enriched *ErrFirmwareNotCatalogued
	if errors.As(err, &enriched) {
		t.Errorf("non-msi-ec error should not be ErrFirmwareNotCatalogued; got %+v", enriched)
	}
}

func TestHalBackendChannelCount(t *testing.T) {
	t.Run("unregistered backend returns 0", func(t *testing.T) {
		hal.Reset()
		t.Cleanup(hal.Reset)
		if n := halBackendChannelCount("absent"); n != 0 {
			t.Errorf("got %d, want 0", n)
		}
	})

	t.Run("counts only CapWritePWM channels", func(t *testing.T) {
		hal.Reset()
		t.Cleanup(hal.Reset)
		hal.Register("mixed", &fakeHALBackend{
			name: "mixed",
			channels: []hal.Channel{
				{ID: "ro", Caps: hal.CapRead, Opaque: struct{}{}},
				{ID: "w1", Caps: hal.CapWritePWM, Opaque: struct{}{}},
				{ID: "w2", Caps: hal.CapWritePWM | hal.CapRestore, Opaque: struct{}{}},
				{ID: "rpm", Caps: hal.CapWriteRPMTarget, Opaque: struct{}{}},
			},
		})
		if n := halBackendChannelCount("mixed"); n != 2 {
			t.Errorf("got %d, want 2 (only CapWritePWM channels counted)", n)
		}
	})
}

// TestUnloadSupersededCompetitors covers the #1397 fix: after a verified OOT
// install, a superseded in-kernel driver that stayed resident (loaded
// alongside the OOT driver rather than failing the modprobe with EPERM) is
// torn down and blacklisted, so the in-session module set matches the
// post-reboot single-driver state.
func TestUnloadSupersededCompetitors(t *testing.T) {
	origLoaded, origRemove, origBlacklist := moduleIsLoaded, modprobeRemove, persistBlacklist
	t.Cleanup(func() {
		moduleIsLoaded, modprobeRemove, persistBlacklist = origLoaded, origRemove, origBlacklist
	})

	nct6687Cfg := func(logs *[]string) PipelineConfig {
		return PipelineConfig{
			Driver: DriverNeed{Module: "nct6687", ChipName: "NCT6687D"},
			Log:    func(s string) { *logs = append(*logs, s) },
		}
	}

	t.Run("resident_competitor_unloaded_and_blacklisted", func(t *testing.T) {
		var removed, blacklisted []string
		moduleIsLoaded = func(m string) bool { return m == "nct6683" }
		modprobeRemove = func(m string) ([]byte, error) { removed = append(removed, m); return nil, nil }
		persistBlacklist = func(_, m string) error { blacklisted = append(blacklisted, m); return nil }

		var logs []string
		unloadSupersededCompetitors(nct6687Cfg(&logs))

		if len(removed) != 1 || removed[0] != "nct6683" {
			t.Fatalf("want nct6683 unloaded, got %v", removed)
		}
		if len(blacklisted) != 1 || blacklisted[0] != "nct6683" {
			t.Fatalf("want nct6683 blacklisted, got %v", blacklisted)
		}
	})

	t.Run("competitor_not_loaded_is_left_alone", func(t *testing.T) {
		var removed, blacklisted []string
		moduleIsLoaded = func(string) bool { return false }
		modprobeRemove = func(m string) ([]byte, error) { removed = append(removed, m); return nil, nil }
		persistBlacklist = func(_, m string) error { blacklisted = append(blacklisted, m); return nil }

		var logs []string
		unloadSupersededCompetitors(nct6687Cfg(&logs))

		if len(removed) != 0 || len(blacklisted) != 0 {
			t.Fatalf("want no action when competitor absent; removed=%v blacklisted=%v", removed, blacklisted)
		}
	})

	t.Run("busy_competitor_still_blacklisted_for_next_boot", func(t *testing.T) {
		var blacklisted []string
		moduleIsLoaded = func(string) bool { return true }
		modprobeRemove = func(string) ([]byte, error) {
			return []byte("FATAL: Module nct6683 is in use"), errors.New("exit status 1")
		}
		persistBlacklist = func(_, m string) error { blacklisted = append(blacklisted, m); return nil }

		var logs []string
		unloadSupersededCompetitors(nct6687Cfg(&logs)) // must not panic and must still blacklist

		if len(blacklisted) != 1 || blacklisted[0] != "nct6683" {
			t.Fatalf("blacklist must be written even when unload fails, got %v", blacklisted)
		}
	})

	t.Run("driver_with_no_competitors_is_a_noop", func(t *testing.T) {
		moduleIsLoaded = func(string) bool {
			t.Fatal("moduleIsLoaded should not run for a driver with no competitors")
			return false
		}
		modprobeRemove = func(string) ([]byte, error) { t.Fatal("modprobeRemove should not run"); return nil, nil }
		persistBlacklist = func(string, string) error { t.Fatal("persistBlacklist should not run"); return nil }

		var logs []string
		cfg := PipelineConfig{Driver: DriverNeed{Module: "it87"}, Log: func(s string) { logs = append(logs, s) }}
		unloadSupersededCompetitors(cfg)
	})
}
