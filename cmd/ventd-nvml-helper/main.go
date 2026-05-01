// ventd-nvml-helper is a SUID-root helper for NVML write operations.
//
// ventd runs as the unprivileged `ventd` user (correct security posture
// per RULE-INSTALL-01) but NVIDIA's NVML library requires root for fan
// control writes (`nvmlDeviceSetFanSpeed_v2`, `nvmlDeviceSetDefaultFanSpeed_v2`,
// `nvmlDeviceSetFanControlPolicy`). This helper is installed SUID-root by
// the ventd .deb / .rpm postinst and proxies a small whitelisted set of
// NVML write subcommands. The daemon dispatches to this helper for write
// operations; reads continue to use NVML directly from the daemon (no
// elevation needed).
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
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"

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
	if err := nvidia.Init(logger); err != nil {
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
