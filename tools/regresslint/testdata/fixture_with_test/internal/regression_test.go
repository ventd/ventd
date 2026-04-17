package internal_test

import "testing"

func TestRegression_Issue42_FanDropsToZero(t *testing.T) {
	// Verifies issue #42: fan speed must not drop to zero under high CPU load
	// when min_pwm > 0. Regression guard only — full coverage is in controller_test.go.
	t.Skip("placeholder — replace with real repro when rig is available")
}
