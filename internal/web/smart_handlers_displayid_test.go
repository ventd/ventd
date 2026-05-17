package web

import (
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// TestDisplayChannelID_NvidiaBareIndexBecomesComposed pins issue #998:
// an NVIDIA fan whose PWMPath is the bare GPU index surfaces as the
// composed "gpu<idx>:fan0" form in /api/v1/{confidence,smart}/* JSON
// responses, parallel to the sensor-side #927 fix that exposes
// "gpu0:temp" et al.
func TestDisplayChannelID_NvidiaBareIndexBecomesComposed(t *testing.T) {
	live := &config.Config{
		Fans: []config.Fan{
			{Name: "gpu0 fan", Type: "nvidia", PWMPath: "0"},
			{Name: "gpu1 fan", Type: "nvidia", PWMPath: "1"},
		},
	}
	cases := []struct {
		in, want string
	}{
		{"0", "gpu0:fan0"},
		{"1", "gpu1:fan0"},
	}
	for _, c := range cases {
		if got := displayChannelID(c.in, live); got != c.want {
			t.Errorf("displayChannelID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDisplayChannelID_HwmonSysfsPathPassesThrough pins the other half
// of the contract: hwmon channels surface their sysfs path unchanged.
func TestDisplayChannelID_HwmonSysfsPathPassesThrough(t *testing.T) {
	live := &config.Config{
		Fans: []config.Fan{
			{Name: "cpu fan", Type: "hwmon", PWMPath: "/sys/class/hwmon/hwmon3/pwm1"},
		},
	}
	in := "/sys/class/hwmon/hwmon3/pwm1"
	if got := displayChannelID(in, live); got != in {
		t.Errorf("displayChannelID(hwmon) rewrote %q to %q", in, got)
	}
}

// TestDisplayChannelID_BareIntegerWithoutMatchingNvidiaFanPassesThrough
// — defensive: if a bare integer arrives but the config has no nvidia
// fan with that PWMPath (e.g. stale aggregator entry, or a hwmon fan
// whose synthetic PWMPath happens to be a single digit), the helper
// MUST NOT rewrite. Otherwise the API surface would lie about fan
// type.
func TestDisplayChannelID_BareIntegerWithoutMatchingNvidiaFanPassesThrough(t *testing.T) {
	live := &config.Config{
		Fans: []config.Fan{
			{Name: "hwmon fan", Type: "hwmon", PWMPath: "0"}, // pathological collision
		},
	}
	if got := displayChannelID("0", live); got != "0" {
		t.Errorf("displayChannelID rewrote a non-nvidia bare-integer ID: got %q want %q", got, "0")
	}
}

// TestDisplayChannelID_NilConfigPassesThrough covers the test/early-
// startup path where live config hasn't been loaded yet.
func TestDisplayChannelID_NilConfigPassesThrough(t *testing.T) {
	if got := displayChannelID("0", nil); got != "0" {
		t.Errorf("displayChannelID(nil cfg) rewrote ID: got %q want %q", got, "0")
	}
}

// TestDisplayChannelID_NonNumericPassesThrough covers the future-proofing:
// if a downstream caller starts using a composed ID (e.g. msi-ec channels
// expose "msi-ec/fan1" today), the helper must not mangle it.
func TestDisplayChannelID_NonNumericPassesThrough(t *testing.T) {
	live := &config.Config{}
	cases := []string{
		"msi-ec/fan1",
		"gpu0:fan0",
		"some-future-id",
	}
	for _, in := range cases {
		if got := displayChannelID(in, live); got != in {
			t.Errorf("displayChannelID(%q) = %q, want unchanged", in, got)
		}
	}
}
