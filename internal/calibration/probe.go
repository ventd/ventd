// Package calibration implements the PR 2b channel-validity probe: polarity
// detection, stall_pwm / min_responsive_pwm sweep, phantom detection, and
// BIOS-override detection. Results are written as layer-4 CalibrationResult
// JSON files consumed by the apply path.
package calibration

import (
	"context"
	"time"

	"gopkg.in/yaml.v3"
)

// ChannelProber is the interface used by all probe functions.
// A real implementation wraps a hal.FanBackend + hal.Channel.
// Tests inject a syntheticChannel that simulates fan response.
type ChannelProber interface {
	WritePWM(ctx context.Context, pwm int) error
	ReadRPM(ctx context.Context) (int, error)
	// ReadPWM reads the register value back immediately; used by ProbeBIOSOverride.
	ReadPWM(ctx context.Context) (int, error)
	// Settle waits for the motor to respond to a PWM change.
	// A no-op in tests; time.Sleep(d) in production.
	Settle(ctx context.Context, d time.Duration) error
}

// ParseFixtureYAML unmarshals YAML fixture data into out.
// Exported so the external _test package can call it via calibration.ParseFixtureYAML.
func ParseFixtureYAML(data []byte, out any) error {
	return yaml.Unmarshal(data, out)
}
