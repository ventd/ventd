//go:build !nonvidia

package nvidia

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// HelperPath is the default install location for the SUID-root NVML
// write-helper shipped by the ventd .deb / .rpm postinst. Override via
// the VENTD_NVML_HELPER environment variable for tests / non-standard
// install layouts.
const HelperPath = "/usr/local/sbin/ventd-nvml-helper"

// HelperEnvOverride is the env variable name. Used by tests so they
// can point at a stub helper instead of the production SUID binary.
const HelperEnvOverride = "VENTD_NVML_HELPER"

// helperTimeout caps how long any single helper invocation may run.
// The helper does init/shutdown per call; on a healthy system it
// returns in <100 ms. 5 s is generous for slow startups (cold libnvidia-ml
// load) and tight enough that a wedged helper doesn't stall the
// controller's hot loop indefinitely.
const helperTimeout = 5 * time.Second

// needsHelper returns true when this process lacks root euid AND a
// helper binary is available on disk. The recursion guard sits here:
// when the SUID helper itself runs, its euid is 0 and this returns
// false, so the helper goes direct to the NVML library and never
// re-invokes itself.
//
// When ventd-as-daemon (uid=ventd, euid=ventd) calls a write
// function, this returns true and the dispatch redirects to the
// helper. When the helper executes (uid=ventd, euid=root via SUID
// bit), this returns false and goes direct.
func needsHelper() bool {
	if os.Geteuid() == 0 {
		return false
	}
	path := helperPath()
	if path == "" {
		return false
	}
	if _, err := os.Stat(path); err != nil {
		return false
	}
	return true
}

// helperPath resolves the helper binary path, honouring the env
// override for tests.
func helperPath() string {
	if p := strings.TrimSpace(os.Getenv(HelperEnvOverride)); p != "" {
		return p
	}
	return HelperPath
}

// writeFanSpeedViaHelper executes the SUID helper with the
// `set-fan-speed` subcommand. The helper handles NVML init/shutdown
// per call; we just supply the args and parse the exit code.
func writeFanSpeedViaHelper(index uint, pwm uint8) error {
	pct := int(math.Round(float64(pwm) / 255.0 * 100.0))
	return runHelper("set-fan-speed",
		strconv.FormatUint(uint64(index), 10),
		"0",
		strconv.Itoa(pct),
	)
}

// resetFanSpeedViaHelper executes the helper's `reset-fan` subcommand.
func resetFanSpeedViaHelper(index uint) error {
	return runHelper("reset-fan",
		strconv.FormatUint(uint64(index), 10),
	)
}

// setFanControlPolicyViaHelper executes the helper's `set-fan-policy`
// subcommand. Returns (true, nil) on success; (false, nil) when the
// helper reports unsupported (exit code 4) — matches the direct
// SetFanControlPolicy contract.
func setFanControlPolicyViaHelper(index uint, fanIdx int, policy int) (bool, error) {
	err := runHelper("set-fan-policy",
		strconv.FormatUint(uint64(index), 10),
		strconv.Itoa(fanIdx),
		strconv.Itoa(policy),
	)
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 4 {
		return false, nil // helper reports unsupported on this driver
	}
	return false, err
}

// runHelper executes the helper with the given args under a bounded
// timeout. Returns nil on exit code 0, a wrapped error otherwise.
// The helper's stderr is included in the error message so the daemon
// log captures the failure cause without a separate stream. The
// underlying *exec.ExitError is preserved in the error chain via %w
// so callers can extract the exit code via errors.As — used by
// setFanControlPolicyViaHelper to translate exit code 4 into the
// (false, nil) "unsupported on this driver" return contract.
func runHelper(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), helperTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, helperPath(), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			text = err.Error()
		}
		return fmt.Errorf("nvml helper: %s: %s: %w", strings.Join(args, " "), text, err)
	}
	return nil
}

// exitCode unwraps an *exec.ExitError to its numeric exit code.
// Returns -1 when the error is not an ExitError (e.g. context timeout).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errorsAs(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// errorsAs is a thin wrapper avoiding a direct errors.As import here;
// the nvidia package already imports errors elsewhere but this file
// is otherwise import-bounded.
func errorsAs(err error, target **exec.ExitError) bool {
	for err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			*target = e
			return true
		}
		// unwrap manually to keep imports tight
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
