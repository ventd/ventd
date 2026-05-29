package config

import "testing"

// TestRelaxMarginPWM: nil ⇒ default, explicit 0 honoured (hard floor /
// boost-only), explicit value passed through. RULE-CTRL-SMART-RELAX-FLOOR.
func TestRelaxMarginPWM(t *testing.T) {
	t.Parallel()
	u := func(v uint8) *uint8 { return &v }
	cases := []struct {
		name string
		cfg  SmartConfig
		want uint8
	}{
		{"unset uses default", SmartConfig{}, DefaultMaxRelaxBelowCurve},
		{"explicit zero is honoured", SmartConfig{MaxRelaxBelowCurve: u(0)}, 0},
		{"explicit value passes through", SmartConfig{MaxRelaxBelowCurve: u(40)}, 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.RelaxMarginPWM(); got != tc.want {
				t.Fatalf("RelaxMarginPWM() = %d, want %d", got, tc.want)
			}
		})
	}
}
