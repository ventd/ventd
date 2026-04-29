package probe_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/probe"
)

// TestRULE_PROBE_11_RefuseDoesNotBlockStartup verifies two things:
//
//  1. The contract: LoadWizardOutcome returns OutcomeRefuse without error when
//     the persisted outcome is "refused". This is the condition that used to
//     trigger a fatal exit; it must now be a non-fatal WARN.
//
//  2. The implementation: cmd/ventd/main.go does NOT contain the old fatal-exit
//     string and DOES contain the WARN continuation string. Static analysis of
//     main.go is the only practical way to verify daemon-level behaviour from a
//     library package test.
func TestRULE_PROBE_11_RefuseDoesNotBlockStartup(t *testing.T) {
	// Part 1 — KV contract.
	db := openTestKV(t)

	// A virtualised environment always produces OutcomeRefuse (RULE-PROBE-04).
	r := &probe.ProbeResult{
		RuntimeEnvironment: probe.RuntimeEnvironment{
			Virtualised: true,
			VirtType:    "kvm",
			DetectedVia: []string{"dmi", "systemd-detect-virt", "/sys/hypervisor"},
		},
	}

	if err := probe.PersistOutcome(db, r); err != nil {
		t.Fatalf("PersistOutcome: %v", err)
	}

	outcome, ok, err := probe.LoadWizardOutcome(db)
	if err != nil {
		t.Fatalf("LoadWizardOutcome returned error: %v", err)
	}
	if !ok {
		t.Fatal("LoadWizardOutcome: key absent after PersistOutcome")
	}
	if outcome != probe.OutcomeRefuse {
		t.Fatalf("expected OutcomeRefuse, got %v", outcome)
	}

	// Part 2 — static analysis of cmd/ventd/main.go.
	root := findModuleRoot(t)
	mainPath := filepath.Join(root, "cmd", "ventd", "main.go")

	data, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	src := string(data)

	// The old code fatally returned an error on OutcomeRefuse; that string must
	// be gone.
	const fatalStr = "probe: hardware unsupported"
	if strings.Contains(src, fatalStr) {
		t.Errorf("main.go still contains fatal-exit string %q; hotfix not applied", fatalStr)
	}

	// The new code logs at WARN and continues; that string must be present.
	const warnStr = "probe: hardware refused"
	if !strings.Contains(src, warnStr) {
		t.Errorf("main.go does not contain WARN continuation string %q; hotfix missing", warnStr)
	}
}
