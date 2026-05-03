package hwmon

// safety_test.go binds every sentinel-related rule in
// .claude/rules/hwmon-safety.md to a named subtest. The goal is that a
// regression in any one sentinel invariant fails in CI at a predictable
// location with a predictable name.
//
// Each subtest is referenced by its rule's Bound: line. If a rule text
// is edited, update the corresponding subtest in the same PR. New rules
// must land with a matching subtest here — missing coverage is a review
// blocker enforced by tools/rulelint.

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/ventd/ventd/internal/hal"
)

// makeFanDir creates a minimal in-tmpdir hwmon chip dir with:
//   - pwm1      (duty-cycle write target)
//   - pwm1_enable
//   - fan1_input (RPM tachometer)
func makeFanDir(t *testing.T) (pwmPath, rpmPath string) {
	t.Helper()
	dir := t.TempDir()
	pwmPath = filepath.Join(dir, "pwm1")
	rpmPath = filepath.Join(dir, "fan1_input")
	enablePath := filepath.Join(dir, "pwm1_enable")
	for _, f := range []struct{ p, c string }{
		{pwmPath, "128\n"},
		{enablePath, "2\n"},
		{rpmPath, "1200\n"},
	} {
		if err := os.WriteFile(f.p, []byte(f.c), 0o600); err != nil {
			t.Fatalf("seed %s: %v", f.p, err)
		}
	}
	return pwmPath, rpmPath
}

func silentBackend() *Backend {
	return NewBackend(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func channelForPWM(pwmPath string) hal.Channel {
	return hal.Channel{
		ID:   pwmPath,
		Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore,
		Opaque: State{
			PWMPath:    pwmPath,
			OrigEnable: -1,
		},
	}
}

// TestSafety_SentinelInvariants is the rule-to-test index for sentinel
// rejection in the hwmon backend. Each subtest binds one sentinel-related
// invariant from .claude/rules/hwmon-safety.md.
func TestSafety_SentinelInvariants(t *testing.T) {

	// ---------- RULE-HWMON-SENTINEL-FAN ----------

	t.Run("sentinel/fan_rejects_65535_rpm", func(t *testing.T) {
		// A fan1_input reading of 65535 is the nct6687 "register
		// unavailable" sentinel. Backend.Read must return OK=false so
		// calibration and the controller tick never see the bogus value.
		pwmPath, rpmPath := makeFanDir(t)
		if err := os.WriteFile(rpmPath, []byte("65535\n"), 0o600); err != nil {
			t.Fatalf("seed rpm: %v", err)
		}
		b := silentBackend()
		ch := channelForPWM(pwmPath)
		r, err := b.Read(ch)
		if err != nil {
			t.Fatalf("Read returned unexpected error: %v", err)
		}
		if r.OK {
			t.Errorf("Read returned OK=true for sentinel 65535 RPM; want OK=false")
		}
	})

	t.Run("sentinel/fan_rejects_implausible_rpm", func(t *testing.T) {
		// Any RPM reading > PlausibleRPMMax (25 000 since 2026-05-03;
		// previously 10 000) is rejected even if not the exact 0xFFFF
		// sentinel. The new cap admits server-class Delta/Sanyo Denki
		// fans up to 22 k while still rejecting the 0x7FFF / 0xFFFF
		// mid-latch glitches some chips emit. Test value 32 000 sits
		// above any legit fan and below the 65 535 raw sentinel.
		pwmPath, rpmPath := makeFanDir(t)
		if err := os.WriteFile(rpmPath, []byte("32000\n"), 0o600); err != nil {
			t.Fatalf("seed rpm: %v", err)
		}
		b := silentBackend()
		ch := channelForPWM(pwmPath)
		r, err := b.Read(ch)
		if err != nil {
			t.Fatalf("Read returned unexpected error: %v", err)
		}
		if r.OK {
			t.Errorf("Read returned OK=true for implausible 32000 RPM; want OK=false")
		}
	})

	t.Run("sentinel/fan_accepts_server_class_rpm", func(t *testing.T) {
		// 18 000 RPM is normal for Sanyo Denki industrial fans on
		// Supermicro / Dell rack hardware. Must pass through after
		// the 2026-05-03 cap raise from 10 000 → 25 000.
		pwmPath, rpmPath := makeFanDir(t)
		if err := os.WriteFile(rpmPath, []byte("18000\n"), 0o600); err != nil {
			t.Fatalf("seed rpm: %v", err)
		}
		b := silentBackend()
		ch := channelForPWM(pwmPath)
		r, err := b.Read(ch)
		if err != nil {
			t.Fatalf("Read returned unexpected error: %v", err)
		}
		if !r.OK {
			t.Errorf("Read returned OK=false for legit 18000 RPM (server fan); want OK=true")
		}
		if r.RPM != 18000 {
			t.Errorf("RPM = %d, want 18000", r.RPM)
		}
	})

	t.Run("sentinel/fan_accepts_normal_rpm", func(t *testing.T) {
		// A legitimate 1200 RPM reading must pass through unchanged.
		pwmPath, rpmPath := makeFanDir(t)
		if err := os.WriteFile(rpmPath, []byte("1200\n"), 0o600); err != nil {
			t.Fatalf("seed rpm: %v", err)
		}
		b := silentBackend()
		ch := channelForPWM(pwmPath)
		r, err := b.Read(ch)
		if err != nil {
			t.Fatalf("Read returned unexpected error: %v", err)
		}
		if !r.OK {
			t.Errorf("Read returned OK=false for valid 1200 RPM; want OK=true")
		}
		if r.RPM != 1200 {
			t.Errorf("RPM = %d, want 1200", r.RPM)
		}
	})

	// ---------- RULE-HWMON-SENTINEL-TEMP (IsSentinelSensorVal, temp* prefix) ----------

	t.Run("sentinel/temp_rejects_255_5_degrees", func(t *testing.T) {
		// 255500 millidegrees = 255.5°C after ReadValue ÷1000 is the
		// nct6687 sentinel value. IsSentinelSensorVal must return true.
		path := filepath.Join(t.TempDir(), "temp1_input")
		scaledVal := float64(255500) / 1000.0 // = 255.5
		if !IsSentinelSensorVal(path, scaledVal) {
			t.Errorf("IsSentinelSensorVal(%q, %.1f) = false; want true (255.5°C sentinel)", path, scaledVal)
		}
	})

	t.Run("sentinel/temp_rejects_above_plausible_cap", func(t *testing.T) {
		// Anything at or above 150°C is implausible for consumer hardware.
		// 150°C itself (the cap value) must be rejected.
		path := filepath.Join(t.TempDir(), "temp2_input")
		if !IsSentinelSensorVal(path, PlausibleTempMaxCelsius) {
			t.Errorf("IsSentinelSensorVal(%q, %.1f) = false; want true (at plausibility cap)", path, PlausibleTempMaxCelsius)
		}
	})

	t.Run("sentinel/temp_rejects_sub_absolute_zero", func(t *testing.T) {
		// Anything at or below −273.15°C is below absolute zero — a sensor
		// latch error or signed/unsigned underflow in a driver. The exact
		// floor value (−273.15°C) must be rejected, as must values below.
		// A -10°C ambient reading is physically possible (cold-room rack)
		// and MUST still pass.
		path := filepath.Join(t.TempDir(), "temp1_input")

		if !IsSentinelSensorVal(path, PlausibleTempMinCelsius) {
			t.Errorf("IsSentinelSensorVal(%q, %.2f) = false; want true (at absolute-zero floor)", path, PlausibleTempMinCelsius)
		}
		if !IsSentinelSensorVal(path, -300.0) {
			t.Errorf("IsSentinelSensorVal(%q, -300.0) = false; want true (below absolute zero)", path)
		}
		if IsSentinelSensorVal(path, -10.0) {
			t.Errorf("IsSentinelSensorVal(%q, -10.0) = true; want false (cold ambient is valid)", path)
		}
	})

	t.Run("sentinel/temp_accepts_normal_reading", func(t *testing.T) {
		// A 45°C reading must not be rejected.
		path := filepath.Join(t.TempDir(), "temp1_input")
		scaledVal := float64(45000) / 1000.0 // = 45.0
		if IsSentinelSensorVal(path, scaledVal) {
			t.Errorf("IsSentinelSensorVal(%q, %.1f) = true; want false (valid 45°C)", path, scaledVal)
		}
	})

	// ---------- RULE-HWMON-SENTINEL-VOLTAGE (IsSentinelSensorVal, in* prefix) ----------

	t.Run("sentinel/voltage_rejects_implausible", func(t *testing.T) {
		// 65535 millivolts = 65.535 V after ReadValue ÷1000. No consumer
		// PSU rail exceeds 20 V; this value must be rejected.
		path := filepath.Join(t.TempDir(), "in1_input")
		scaledVal := float64(65535) / 1000.0 // = 65.535
		if !IsSentinelSensorVal(path, scaledVal) {
			t.Errorf("IsSentinelSensorVal(%q, %.3f) = false; want true (65.5V sentinel)", path, scaledVal)
		}
	})

	t.Run("sentinel/voltage_accepts_normal_reading", func(t *testing.T) {
		// A 12 V reading (ATX 12V rail) must not be rejected.
		path := filepath.Join(t.TempDir(), "in2_input")
		if IsSentinelSensorVal(path, 12.0) {
			t.Errorf("IsSentinelSensorVal(%q, 12.0) = true; want false (valid 12V)", path)
		}
	})
}

// TestIsSentinelRPM_BoundaryValues is a table-driven test for the
// IsSentinelRPM helper used by Backend.Read.
func TestIsSentinelRPM_BoundaryValues(t *testing.T) {
	cases := []struct {
		name string
		rpm  int
		want bool
	}{
		{"exact_sentinel_65535", 65535, true},
		{"above_plausible_cap_25001", 25001, true},
		{"at_plausible_cap_25000", 25000, false},
		{"server_fan_18000_accepted", 18000, false},
		{"consumer_fan_4500_accepted", 4500, false},
		{"normal_1200", 1200, false},
		{"zero", 0, false},
		{"one", 1, false},
		{"below_sentinel_65534", 65534, true}, // > PlausibleRPMMax so still rejected
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsSentinelRPM(tc.rpm)
			if got != tc.want {
				t.Errorf("IsSentinelRPM(%d) = %v, want %v", tc.rpm, got, tc.want)
			}
		})
	}
}
