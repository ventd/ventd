package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestRegression_Issue484_InstallerRegistersRecoverUnit verifies that
// scripts/install.sh, when run in VENTD_TEST_MODE=1, installs:
//   - ventd-recover.service to VENTD_SYSTEMD_DIR
//   - ventd-recover binary to VENTD_SBIN_DIR
//
// This is the regression guard for fix-484: on a fresh install the
// recover binary and service were not being wired up, leaving the
// OnFailure= hook pointing at a binary that did not exist.
func TestRegression_Issue484_InstallerRegistersRecoverUnit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("installer is linux-only")
	}
	// Find the repo root by walking up from this test file's directory.
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	installScript := filepath.Join(repoRoot, "scripts", "install.sh")
	if _, err := os.Stat(installScript); err != nil {
		t.Skipf("install.sh not found at %s (running outside source tree?): %v", installScript, err)
	}

	// Scratch sysroot — every installed file lands here, never on the host.
	scratch := t.TempDir()
	systemdDir := filepath.Join(scratch, "etc", "systemd", "system")
	sbinDir := filepath.Join(scratch, "usr", "local", "sbin")
	prefix := filepath.Join(scratch, "usr", "local", "bin")
	etcDir := filepath.Join(scratch, "etc", "ventd")
	stateDir := filepath.Join(scratch, "var", "lib", "ventd")
	stateParent := filepath.Join(scratch, "var", "lib")
	for _, d := range []string{systemdDir, sbinDir, prefix, etcDir, stateParent} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Stub main binary (installer just copies it).
	stubVentd := filepath.Join(scratch, "ventd-stub")
	if err := os.WriteFile(stubVentd, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("create stub ventd: %v", err)
	}

	// Stub recover binary — injected via VENTD_RECOVER_BIN so the installer
	// doesn't need to find it in the source tree.
	stubRecover := filepath.Join(scratch, "ventd-recover-stub")
	if err := os.WriteFile(stubRecover, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("create stub recover: %v", err)
	}

	cmd := exec.Command("bash", installScript, stubVentd)
	cmd.Env = append(os.Environ(),
		"VENTD_TEST_MODE=1",
		"VENTD_INIT_SYSTEM=systemd",
		"VENTD_PREFIX="+prefix,
		"VENTD_SYSTEMD_DIR="+systemdDir,
		"VENTD_SBIN_DIR="+sbinDir,
		"VENTD_ETC_DIR="+etcDir,
		"VENTD_STATE_DIR="+stateDir,
		"VENTD_RECOVER_BIN="+stubRecover,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, out)
	}

	// 1. Unit file installed.
	unitDst := filepath.Join(systemdDir, "ventd-recover.service")
	if _, err := os.Stat(unitDst); err != nil {
		t.Errorf("ventd-recover.service not installed at %s: %v\ninstaller output:\n%s",
			unitDst, err, out)
	}

	// 2. Binary installed.
	binDst := filepath.Join(sbinDir, "ventd-recover")
	if _, err := os.Stat(binDst); err != nil {
		t.Errorf("ventd-recover binary not installed at %s: %v\ninstaller output:\n%s",
			binDst, err, out)
	}

	// 3. Main unit also landed (sanity check that the installer ran fully).
	mainUnit := filepath.Join(systemdDir, "ventd.service")
	if _, err := os.Stat(mainUnit); err != nil {
		t.Errorf("ventd.service not installed at %s: %v", mainUnit, err)
	}
}
