package fallback

import (
	"testing"

	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/probe"
)

// RULE-FALLBACK-TIER-01: A channel with a non-empty TachPath admits
// to Tier 0 (R8 ceiling 1.0). Tach-presence is the strongest evidence
// that the controller can close the loop on real RPM feedback.
func TestSelectTier_TachPresent_ReturnsTier0(t *testing.T) {
	t.Parallel()
	ch := &probe.ControllableChannel{
		PWMPath:  "/sys/class/hwmon/hwmon3/pwm1",
		TachPath: "/sys/class/hwmon/hwmon3/fan1_input",
		Driver:   "nct6798",
	}
	if got := SelectTier(ch); got != layer_a.TierRPMTach {
		t.Fatalf("SelectTier with tach: got tier %d, want %d (TierRPMTach)",
			got, layer_a.TierRPMTach)
	}
	if ceil := layer_a.R8Ceiling(layer_a.TierRPMTach); ceil != 1.0 {
		t.Fatalf("Tier 0 ceiling = %v, want 1.0 (RULE-CONFA-TIER-01 pin)", ceil)
	}
}

// RULE-FALLBACK-TIER-02: A tach-less channel whose driver is in the
// thermal-invert family admits to Tier 4 (R8 ceiling 0.45). Verifies
// every entry in thermalInvertDrivers maps consistently.
func TestSelectTier_TachlessLaptopEC_ReturnsTier4(t *testing.T) {
	t.Parallel()
	cases := []string{
		"legion-laptop", "msi-ec", "thinkpad_acpi", "dell-smm-hwmon",
		"hp-wmi-sensors", "asus-wmi-sensors", "surface_fan",
		"applesmc", "macsmc-hwmon", "qnap8528",
	}
	for _, drv := range cases {
		drv := drv
		t.Run(drv, func(t *testing.T) {
			t.Parallel()
			ch := &probe.ControllableChannel{
				PWMPath:  "/sys/class/hwmon/hwmon0/pwm1",
				TachPath: "", // tach-less
				Driver:   drv,
			}
			got := SelectTier(ch)
			if got != layer_a.TierThermalInvert {
				t.Fatalf("driver %q: got tier %d, want %d (TierThermalInvert)",
					drv, got, layer_a.TierThermalInvert)
			}
		})
	}
	if ceil := layer_a.R8Ceiling(layer_a.TierThermalInvert); ceil != 0.45 {
		t.Fatalf("Tier 4 ceiling = %v, want 0.45", ceil)
	}
}

// RULE-FALLBACK-TIER-03: A tach-less channel whose driver is NOT in
// the recognised laptop / NAS EC set admits to Tier 7 (open-loop,
// R8 ceiling 0.0). This is the safe fallback — predictive control
// is refused entirely (conf_A=0 ⇒ w_pred=0).
func TestSelectTier_TachlessUnknownDriver_ReturnsTier7(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		driver string
	}{
		{"unknown-driver", "some-future-ec"},
		{"empty-driver", ""},
		{"nct-without-tach (anomaly)", "nct6798"}, // Nuvoton normally has tach; without one, refuse
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ch := &probe.ControllableChannel{
				PWMPath:  "/sys/class/hwmon/hwmon0/pwm1",
				TachPath: "",
				Driver:   tc.driver,
			}
			got := SelectTier(ch)
			if got != layer_a.TierOpenLoopPinned {
				t.Fatalf("driver %q: got tier %d, want %d (TierOpenLoopPinned)",
					tc.driver, got, layer_a.TierOpenLoopPinned)
			}
		})
	}
	if ceil := layer_a.R8Ceiling(layer_a.TierOpenLoopPinned); ceil != 0.0 {
		t.Fatalf("Tier 7 ceiling = %v, want 0.0", ceil)
	}
}

// Defensive: nil channel pointer must not panic and must return
// Tier 7. Catches a wiring bug where the daemon's Admit loop walks
// a nil entry.
func TestSelectTier_NilChannel_ReturnsTier7(t *testing.T) {
	t.Parallel()
	if got := SelectTier(nil); got != layer_a.TierOpenLoopPinned {
		t.Fatalf("nil channel: got tier %d, want %d", got, layer_a.TierOpenLoopPinned)
	}
}
