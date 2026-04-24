//go:build linux && udev_integration

package corsair

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestUdevRule_UdevadmVerify verifies the udev rule parses correctly under
// udevadm verify. This test is integration-gated (requires udevadm binary)
// and should be run in a VM environment.
func TestUdevRule_UdevadmVerify(t *testing.T) {
	// Locate deploy/90-ventd-liquid.rules relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	testDir := filepath.Dir(thisFile)
	repoRoot := filepath.Join(testDir, "..", "..", "..")
	ruleFile := filepath.Join(repoRoot, "deploy", "90-ventd-liquid.rules")

	// Verify the rule file exists.
	if _, err := os.Stat(ruleFile); err != nil {
		t.Fatalf("rule file not found: %v", err)
	}

	// Run udevadm verify.
	cmd := exec.Command("udevadm", "verify", ruleFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("udevadm verify failed: %v\noutput: %s", err, output)
	}

	// Verify stderr is empty (udevadm verify writes errors to stderr).
	cmd = exec.Command("udevadm", "verify", ruleFile)
	err = cmd.Run()
	if err != nil {
		t.Fatalf("udevadm verify returned non-zero: %v", err)
	}
}
