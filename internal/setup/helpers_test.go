package setup

import (
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// TestNeeded covers the first-boot detection predicate. It returns true
// whenever the live config has no Controls defined — the same signal the
// web handler uses to decide whether to redirect to the wizard.
func TestNeeded(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{"empty_controls_is_needed", &config.Config{}, true},
		{"nil_controls_is_needed", &config.Config{Controls: nil}, true},
		{"zero_length_is_needed", &config.Config{Controls: []config.Control{}}, true},
		{"one_control_is_not_needed", &config.Config{
			Controls: []config.Control{{Fan: "cpu_fan", Curve: "cpu_curve"}},
		}, false},
		{"many_controls_is_not_needed", &config.Config{
			Controls: []config.Control{
				{Fan: "f1", Curve: "c1"},
				{Fan: "f2", Curve: "c2"},
			},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Needed(tc.cfg); got != tc.want {
				t.Errorf("Needed() = %v, want %v", got, tc.want)
			}
		})
	}
}
