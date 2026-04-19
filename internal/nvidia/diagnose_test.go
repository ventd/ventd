//go:build !nonvidia

package nvidia

import (
	"os"
	"strings"
	"testing"
)

// TestRegression_Issue461_DiagnoseNvmlFailure verifies all three branches of
// diagnoseNvmlDevice: device absent, permission denied, and device accessible
// (driver in bad state). Exercises the exact diagnostic strings that operators
// rely on to self-serve the fix without filing a support request.
func TestRegression_Issue461_DiagnoseNvmlFailure(t *testing.T) {
	t.Run("device_absent", func(t *testing.T) {
		path := t.TempDir() + "/nonexistent-nvidiactl"
		got := diagnoseNvmlDevice(path)
		if !strings.Contains(got, "not found") && !strings.Contains(got, "not installed") {
			t.Fatalf("absent device: got %q, want substring 'not found' or 'not installed'", got)
		}
	})

	t.Run("permission_denied", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("running as root; cannot test permission denial (root bypasses DAC)")
		}
		f, err := os.CreateTemp(t.TempDir(), "nvidiactl-*")
		if err != nil {
			t.Fatal(err)
		}
		path := f.Name()
		f.Close()
		// chmod 0000 makes the file unreadable by any non-root process.
		if err := os.Chmod(path, 0000); err != nil {
			t.Fatal(err)
		}
		// Restore write bit so TempDir cleanup can remove the file.
		t.Cleanup(func() { os.Chmod(path, 0600) }) //nolint:errcheck

		got := diagnoseNvmlDevice(path)
		if !strings.Contains(got, "Permission denied") {
			t.Fatalf("perm denied: got %q, want 'Permission denied'", got)
		}
		if !strings.Contains(got, "usermod -aG") {
			t.Fatalf("perm denied: got %q, want 'usermod -aG' fix command", got)
		}
	})

	t.Run("accessible_driver_bad_state", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "nvidiactl-*")
		if err != nil {
			t.Fatal(err)
		}
		path := f.Name()
		f.Close() // file is readable (default 0600)

		got := diagnoseNvmlDevice(path)
		if !strings.Contains(got, "accessible") {
			t.Fatalf("accessible: got %q, want 'accessible'", got)
		}
		if !strings.Contains(got, "nvidia-smi") {
			t.Fatalf("accessible: got %q, want 'nvidia-smi' hint", got)
		}
	})
}
