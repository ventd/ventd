// ventd-nvml-helper proxies a small whitelisted set of NVML write
// operations that require root (`nvmlDeviceSetFanSpeed_v2`,
// `nvmlDeviceSetDefaultFanSpeed_v2`, `nvmlDeviceSetFanControlPolicy`).
//
// Deployment note: the SHIPPED systemd unit runs the daemon as `User=root`
// and the .deb / .rpm postinst installs this binary **NOT SUID** (see
// scripts/postinstall.sh) — so in the default deployment the daemon already
// has root euid, `needsHelper()` returns false, and the daemon calls NVML
// directly; this binary is dormant. The helper exists for the alternative
// posture where the daemon runs as a non-root user: there it is installed
// SUID-root so the daemon can dispatch GPU writes to it. Reads always use
// NVML directly from the daemon (no elevation needed).
//
// SECURITY: a SUID-root install of this binary is runnable by ANY local
// user (SUID binaries authenticate no caller), so it must never be more
// than a thin, range-checked NVML shim. Do not add it to the install path
// without re-reviewing that property — a local unprivileged user able to
// drive a GPU fan to 0% is a hardware-DoS vector. The shipped (root daemon,
// non-SUID helper) posture avoids this entirely.
//
// The helper itself is intentionally tiny (~150 LOC):
//
//   - No persistent state, no IPC, no privileged sockets
//   - No subprocess execution beyond direct NVML library calls
//   - All numeric inputs validated against bounded ranges before NVML touch
//   - Exit codes are stable: 0 success, 1 NVML error, 2 usage error,
//     3 init error, 4 driver-not-supported, 5 invalid input
//
// Issue #770 tracks the architectural decision and security review.
//
// Recursion guard: when invoked SUID-root, this helper calls
// nvidia.WriteFanSpeed which itself checks `os.Geteuid() == 0` and goes
// direct to the library; the helper is never re-invoked from inside
// itself.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/ventd/ventd/internal/nvidia"
)

const usage = `usage: ventd-nvml-helper <subcommand> [args...]

Subcommands:
  set-fan-speed   <gpu_idx> <fan_idx> <pct>      Set fan speed (0-100%)
  reset-fan       <gpu_idx>                       Restore default curve
  set-fan-policy  <gpu_idx> <fan_idx> <0|1>      0=auto continuous, 1=manual

Exit codes:
  0  success
  1  NVML error (set, reset, or policy call failed)
  2  usage error (wrong number of args, unknown subcommand)
  3  NVML init error (driver missing, library unavailable)
  4  driver does not support the requested operation
  5  invalid input (gpu_idx / fan_idx / pct / policy out of range)
`

const (
	exitOK             = 0
	exitNVML           = 1
	exitUsage          = 2
	exitInit           = 3
	exitUnsupported    = 4
	exitInvalidInput   = 5
	maxGPUIdx          = 7
	maxFanIdx          = 15
	policyAutoContinue = 0
	policyManual       = 1
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(exitUsage)
	}

	// Suppress NVML init logs unless something goes wrong; the helper's
	// stderr is parsed by the caller for error propagation.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Bound NVML init at 2 s so a hung dlopen on a partial driver install
	// (mismatched DKMS / stale .so symbols / kernel module wedge) cannot
	// hang the helper subprocess. RULE-GPU-PR2D-09.
	if err := nvidia.InitWithDeadline(context.Background(), logger, 2*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "ventd-nvml-helper: init NVML: %v\n", err)
		os.Exit(exitInit)
	}
	defer nvidia.Shutdown()

	switch os.Args[1] {
	case "set-fan-speed":
		cmdSetFanSpeed(os.Args[2:])
	case "reset-fan":
		cmdResetFan(os.Args[2:])
	case "set-fan-policy":
		cmdSetFanPolicy(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "ventd-nvml-helper: unknown subcommand %q\n%s",
			os.Args[1], usage)
		os.Exit(exitUsage)
	}
}

func cmdSetFanSpeed(args []string) {
	if len(args) != 3 {
		fmt.Fprint(os.Stderr, "set-fan-speed: expected <gpu_idx> <fan_idx> <pct>\n")
		os.Exit(exitUsage)
	}
	gpu, err := parseBoundedUint(args[0], "gpu_idx", maxGPUIdx)
	if err != nil {
		fail(exitInvalidInput, err)
	}
	if _, err := parseBoundedUint(args[1], "fan_idx", maxFanIdx); err != nil {
		fail(exitInvalidInput, err)
	}
	pct, err := parseBoundedUint(args[2], "pct", 100)
	if err != nil {
		fail(exitInvalidInput, err)
	}
	pwm := uint8(uint16(pct) * 255 / 100)
	if err := nvidia.WriteFanSpeed(gpu, pwm); err != nil {
		fail(exitNVML, err)
	}
}

func cmdResetFan(args []string) {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, "reset-fan: expected <gpu_idx>\n")
		os.Exit(exitUsage)
	}
	gpu, err := parseBoundedUint(args[0], "gpu_idx", maxGPUIdx)
	if err != nil {
		fail(exitInvalidInput, err)
	}
	if err := nvidia.ResetFanSpeed(gpu); err != nil {
		fail(exitNVML, err)
	}
}

func cmdSetFanPolicy(args []string) {
	if len(args) != 3 {
		fmt.Fprint(os.Stderr, "set-fan-policy: expected <gpu_idx> <fan_idx> <policy>\n")
		os.Exit(exitUsage)
	}
	gpu, err := parseBoundedUint(args[0], "gpu_idx", maxGPUIdx)
	if err != nil {
		fail(exitInvalidInput, err)
	}
	fanIdx, err := parseBoundedUint(args[1], "fan_idx", maxFanIdx)
	if err != nil {
		fail(exitInvalidInput, err)
	}
	policy, err := parseBoundedUint(args[2], "policy", policyManual)
	if err != nil {
		fail(exitInvalidInput, err)
	}
	supported, err := nvidia.SetFanControlPolicy(gpu, int(fanIdx), int(policy))
	if err != nil {
		fail(exitNVML, err)
	}
	if !supported {
		fmt.Fprintln(os.Stderr,
			"ventd-nvml-helper: NVML reports SetFanControlPolicy unsupported on this driver")
		os.Exit(exitUnsupported)
	}
}

// parseBoundedUint parses a non-negative decimal integer in [0, max].
// Returns a structured error suitable for the helper's stderr stream.
func parseBoundedUint(s, name string, max uint64) (uint, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be a non-negative integer: %w", name, err)
	}
	if v > max {
		return 0, fmt.Errorf("%s %d out of range [0, %d]", name, v, max)
	}
	return uint(v), nil
}

func fail(code int, err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(code)
}
