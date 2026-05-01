//go:build !nonvidia

package nvidia

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestNeedsHelper_RootBypasses pins the recursion guard:
// when euid is 0 (process running as root, including the SUID helper
// itself), needsHelper MUST return false so the call goes direct to
// the NVML library and never re-invokes itself.
func TestNeedsHelper_RootBypasses(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("must run as root to test the root-bypass path")
	}
	// Even with a helper present in PATH, root euid bypasses.
	bin := writeFakeHelper(t)
	t.Setenv(HelperEnvOverride, bin)
	if needsHelper() {
		t.Errorf("needsHelper(): got true under euid=0, want false (recursion guard)")
	}
}

// TestNeedsHelper_NoBinarySkips covers the "helper not installed"
// case: when the binary doesn't exist on disk, needsHelper returns
// false and the dispatch falls through to the direct NVML path. This
// preserves pre-helper behaviour for hosts that haven't been upgraded.
func TestNeedsHelper_NoBinarySkips(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("non-root euid required to exercise the binary-presence check")
	}
	t.Setenv(HelperEnvOverride, "/nonexistent/ventd-nvml-helper")
	if needsHelper() {
		t.Errorf("needsHelper(): got true with absent helper, want false")
	}
}

// TestNeedsHelper_NonRootWithHelperPresent verifies the positive
// path: non-root euid + binary present → use the helper.
func TestNeedsHelper_NonRootWithHelperPresent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("non-root euid required")
	}
	bin := writeFakeHelper(t)
	t.Setenv(HelperEnvOverride, bin)
	if !needsHelper() {
		t.Errorf("needsHelper(): got false with binary present + non-root, want true")
	}
}

// TestRunHelper_PassesArgsThroughEnv verifies that runHelper invokes
// the helper with the exact args it was given. The fake helper echoes
// its argv to a temp file and exits 0; the test asserts the file
// contents match the expected joined args.
func TestRunHelper_PassesArgsThroughEnv(t *testing.T) {
	dir := t.TempDir()
	logfile := filepath.Join(dir, "argv.log")

	// Fake helper script: writes its arguments to logfile, exits 0.
	bin := writeShellHelper(t, dir, fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
exit 0
`, logfile))
	t.Setenv(HelperEnvOverride, bin)

	if err := runHelper("set-fan-speed", "0", "0", "50"); err != nil {
		t.Fatalf("runHelper: unexpected error: %v", err)
	}
	got, err := os.ReadFile(logfile)
	if err != nil {
		t.Fatal(err)
	}
	want := "set-fan-speed\n0\n0\n50\n"
	if string(got) != want {
		t.Errorf("argv mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestRunHelper_ErrorPropagation verifies that a non-zero helper exit
// produces a wrapped error containing the helper's stderr text.
func TestRunHelper_ErrorPropagation(t *testing.T) {
	dir := t.TempDir()
	bin := writeShellHelper(t, dir, `#!/bin/sh
echo "fake error message" >&2
exit 1
`)
	t.Setenv(HelperEnvOverride, bin)

	err := runHelper("set-fan-speed", "0", "0", "50")
	if err == nil {
		t.Fatal("runHelper: expected error from exit-1 helper")
	}
	if msg := err.Error(); !contains(msg, "fake error message") {
		t.Errorf("error %q does not contain helper stderr", msg)
	}
}

// TestExitCode_UnwrapsExitError verifies the helper's exit code is
// extractable through wrapped errors. The helper uses exit code 4
// to signal "driver doesn't support this op"; setFanControlPolicyViaHelper
// translates that to (false, nil) — a contract we must preserve.
func TestExitCode_UnwrapsExitError(t *testing.T) {
	dir := t.TempDir()
	bin := writeShellHelper(t, dir, `#!/bin/sh
echo "unsupported by driver" >&2
exit 4
`)
	t.Setenv(HelperEnvOverride, bin)

	supported, err := setFanControlPolicyViaHelper(0, 0, 0)
	if err != nil {
		t.Fatalf("setFanControlPolicyViaHelper: unexpected error: %v", err)
	}
	if supported {
		t.Errorf("setFanControlPolicyViaHelper: got supported=true on exit-4 helper, want false")
	}
}

// TestErrorsAs_ManualUnwrap exercises the manual-unwrap fallback
// that errorsAs uses to avoid pulling errors.As into this file.
func TestErrorsAs_ManualUnwrap(t *testing.T) {
	// fake an exit-error chain via fmt.Errorf wrap
	cause := &exec.ExitError{ProcessState: &os.ProcessState{}}
	wrapped := fmt.Errorf("nvml helper: outer wrap: %w", cause)

	var got *exec.ExitError
	if !errorsAs(wrapped, &got) {
		t.Fatal("errorsAs: failed to unwrap chained ExitError")
	}
	if got != cause {
		t.Errorf("errorsAs: returned a different ExitError than the cause")
	}

	// Also verify a non-matching error returns false.
	plainErr := errors.New("not an ExitError")
	got = nil
	if errorsAs(plainErr, &got) {
		t.Error("errorsAs: returned true on non-ExitError chain")
	}
}

// ── test helpers ──────────────────────────────────────────────────────

// writeFakeHelper writes a no-op executable to a tempdir so the
// presence-check in needsHelper passes. The body never runs in the
// presence-only tests.
func writeFakeHelper(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("Linux-only test (sh/chmod semantics)")
	}
	return writeShellHelper(t, t.TempDir(), "#!/bin/sh\nexit 0\n")
}

// writeShellHelper writes a shell script with the given body to dir
// and returns its path. The script is marked +x.
func writeShellHelper(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-helper")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake helper: %v", err)
	}
	return path
}

// contains is a tiny substring-search helper (avoids strings import
// for one call site).
func contains(haystack, needle string) bool {
	if len(haystack) < len(needle) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
