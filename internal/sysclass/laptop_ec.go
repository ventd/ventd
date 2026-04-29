package sysclass

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ecHandshakeTimeout is the window to observe RPM response after writing
// pwm_enable=1 (§3.1 EC handshake gate).
const ecHandshakeTimeout = 5 * time.Second

// ProbeECHandshake writes pwm_enable=1 to the given path and observes whether
// the associated RPM sensor responds within ecHandshakeTimeout.
//
// pwmEnablePath: e.g. /sys/class/hwmon/hwmon2/pwm1_enable
// rpmPath:       e.g. /sys/class/hwmon/hwmon2/fan1_input
//
// Returns (true, nil) on handshake success, (false, err) on failure or error.
// Called by Envelope C before any probe step on a laptop channel.
func ProbeECHandshake(ctx context.Context, pwmEnablePath, rpmPath string) (bool, error) {
	// Capture initial RPM.
	initRPM, err := readRPM(rpmPath)
	if err != nil {
		return false, fmt.Errorf("ec_handshake: read initial rpm %s: %w", rpmPath, err)
	}

	// Write pwm_enable=1 (manual mode).
	if err := os.WriteFile(pwmEnablePath, []byte("1\n"), 0o644); err != nil {
		return false, fmt.Errorf("ec_handshake: write pwm_enable %s: %w", pwmEnablePath, err)
	}

	deadline := time.Now().Add(ecHandshakeTimeout)
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
		}
		if time.Now().After(deadline) {
			break
		}
		rpm, err := readRPM(rpmPath)
		if err == nil && rpm != initRPM && rpm > 0 {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return false, nil
}

func readRPM(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
}
