package main

// Smoke coverage for the ventd --preflight-check subcommand entry point.
//
// Goals (narrow by design):
//
//   * runPreflightCheck always emits parseable JSON to stdout.
//   * The output contains the three keys the validation tooling
//     consumes (reason, reason_string, detail).
//   * preflightReasonString handles every hwmon.Reason constant
//     the subcommand might surface — if a new reason is added to
//     internal/hwmon without a matching switch case here, the
//     subcommand silently returns "UNKNOWN" and the validation
//     matrix loses its human-readable anchor.
//
// This file deliberately does NOT test the host-side probe behaviour.
// hwmon.PreflightOOT reads /proc, /sys, and shells out to modinfo; the
// probe layer has its own unit coverage in internal/hwmon. Here we pin
// only the CLI-surface contract.
//
// Reference for future sessions:
//
//   If you add a new hwmon.Reason constant, extend reasonStringCases
//   below. The guard test flags missing mappings by comparing the
//   expected string to what preflightReasonString returns — a new
//   constant will fall through to "UNKNOWN" and fail the test.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/ventd/ventd/internal/hwmon"
)

// withCapturedStdout replaces os.Stdout with a pipe, runs fn, and returns
// whatever fn wrote. The daemon's other subcommand tests (version_test.go)
// use the same idiom — keep them aligned so one day we can hoist to a
// shared helper.
func withCapturedStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan []byte, 1)
	go func() {
		buf, _ := io.ReadAll(r)
		done <- buf
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

// TestRunPreflightCheck_EmitsParseableJSON runs the subcommand against
// a synthetic DriverNeed on whatever host happens to execute the test.
// The probe may return any reason depending on the sandbox (CI: usually
// KERNEL_HEADERS_MISSING, dev-box: usually OK), but the shape of the
// output is stable — that's what we pin.
func TestRunPreflightCheck_EmitsParseableJSON(t *testing.T) {
	var exitCode int
	out := withCapturedStdout(t, func() {
		exitCode = runPreflightCheck("")
	})
	if exitCode != 0 {
		t.Fatalf("runPreflightCheck exit = %d, want 0 (output=%q)", exitCode, out)
	}

	var parsed map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out), &parsed); err != nil {
		t.Fatalf("preflight output not JSON: %v\n---stdout---\n%s", err, out)
	}

	for _, k := range []string{"reason", "reason_string", "detail"} {
		if _, ok := parsed[k]; !ok {
			t.Fatalf("preflight output missing %q key: got %v", k, parsed)
		}
	}
}

// TestRunPreflightCheck_RespectsMaxKernel — the maxKernel arg is the
// DriverNeed.MaxSupportedKernel ceiling. Pinning that this flag path
// is plumbed end-to-end costs one extra assertion.
func TestRunPreflightCheck_RespectsMaxKernel(t *testing.T) {
	// "0.0" is below any real running kernel; the probe should report
	// KERNEL_TOO_NEW on any machine with a running kernel >= 0.0
	// (i.e. everywhere). This gives us a deterministic reason across
	// every CI environment.
	var exitCode int
	out := withCapturedStdout(t, func() {
		exitCode = runPreflightCheck("0.0")
	})
	if exitCode != 0 {
		t.Fatalf("runPreflightCheck exit = %d, want 0", exitCode)
	}
	var parsed map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out), &parsed); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	if got := parsed["reason_string"]; got != "KERNEL_TOO_NEW" {
		t.Fatalf("reason_string with maxKernel=0.0: got %v, want KERNEL_TOO_NEW (diagnostic: if this is UNKNOWN, a new hwmon.Reason was added without wiring preflightReasonString)", got)
	}
}

// TestPreflightReasonString_HandlesAllKnownReasons is the "new-constant"
// guard described in the file header. It lives here (not in
// internal/hwmon) because preflightReasonString is the CLI-facing
// adapter and is the only caller that needs a complete switch.
func TestPreflightReasonString_HandlesAllKnownReasons(t *testing.T) {
	cases := []struct {
		in   hwmon.Reason
		want string
	}{
		{hwmon.ReasonOK, "OK"},
		{hwmon.ReasonKernelHeadersMissing, "KERNEL_HEADERS_MISSING"},
		{hwmon.ReasonDKMSMissing, "DKMS_MISSING"},
		{hwmon.ReasonSecureBootBlocks, "SECURE_BOOT_BLOCKS"},
		{hwmon.ReasonKernelTooNew, "KERNEL_TOO_NEW"},
	}
	for _, tc := range cases {
		if got := preflightReasonString(tc.in); got != tc.want {
			t.Fatalf("preflightReasonString(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
