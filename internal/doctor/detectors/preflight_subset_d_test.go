package detectors

import (
	"context"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/recovery"
)

// fixedNow returns a stable timestamp for deterministic Fact.Observed
// assertions in tests.
func fixedNow() time.Time {
	return time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
}

func okProbes() hwmon.Probes {
	return hwmon.Probes{
		KernelRelease:        func() string { return "6.8.0-1-generic" },
		BuildDirExists:       func(string) bool { return true },
		HasBinary:            func(string) bool { return true },
		SecureBootEnabled:    func() (bool, bool) { return false, true },
		MOKKeyAvailable:      func() bool { return true },
		IsContainerised:      func() bool { return false },
		HaveRootOrPasswordlessSudo: func() bool { return true },
		AnotherWizardRunning: func() bool { return false },
		InTreeDriverConflict: func(string) (string, bool) { return "", false },
		LibModulesWritable:   func(string) bool { return true },
		AptLockHeld:          func() bool { return false },
		StaleDKMSState:       func(string) bool { return false },
		DiskFreeBytes:        func(string) (uint64, error) { return 1 << 40, nil },
	}
}

func nctNeed() hwmon.DriverNeed {
	return hwmon.DriverNeed{Key: "nct6687d", ChipName: "NCT6687D", Module: "nct6687", MaxSupportedKernel: "6.10.0"}
}

func TestRULE_DOCTOR_DETECTOR_PreflightSubset_OKEmitsNoFacts(t *testing.T) {
	det := NewPreflightSubsetDetector(nctNeed(), okProbes)

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("OK preflight emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_PreflightSubset_BlockerOnContainer(t *testing.T) {
	probes := okProbes()
	probes.IsContainerised = func() bool { return true }

	det := NewPreflightSubsetDetector(nctNeed(), func() hwmon.Probes { return probes })
	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d (%+v)", len(facts), facts)
	}
	f := facts[0]
	if f.Severity != doctor.SeverityBlocker {
		t.Errorf("Severity = %v, want Blocker", f.Severity)
	}
	if f.Class != recovery.ClassContainerised {
		t.Errorf("Class = %v, want ClassContainerised", f.Class)
	}
	if f.Title == "" {
		t.Errorf("Title is empty")
	}
	if f.EntityHash == "" {
		t.Errorf("EntityHash is empty")
	}
	if !f.Observed.Equal(fixedNow()) {
		t.Errorf("Observed = %v, want %v", f.Observed, fixedNow())
	}
}

func TestRULE_DOCTOR_DETECTOR_PreflightSubset_WarningOnGCCMissing(t *testing.T) {
	probes := okProbes()
	probes.HasBinary = func(name string) bool {
		return name != "gcc"
	}

	det := NewPreflightSubsetDetector(nctNeed(), func() hwmon.Probes { return probes })
	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d (%+v)", len(facts), facts)
	}
	if facts[0].Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want Warning (gcc missing is warning at runtime)", facts[0].Severity)
	}
	if facts[0].Class != recovery.ClassMissingBuildTools {
		t.Errorf("Class = %v, want ClassMissingBuildTools", facts[0].Class)
	}
}

func TestRULE_DOCTOR_DETECTOR_PreflightSubset_RespectsContextCancel(t *testing.T) {
	det := NewPreflightSubsetDetector(nctNeed(), okProbes)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestPreflightSubset_EntityHashStableAcrossCalls(t *testing.T) {
	probes := okProbes()
	probes.IsContainerised = func() bool { return true }

	det := NewPreflightSubsetDetector(nctNeed(), func() hwmon.Probes { return probes })
	a, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	b, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 fact each, got %d / %d", len(a), len(b))
	}
	if a[0].EntityHash != b[0].EntityHash {
		t.Errorf("EntityHash unstable across calls: %q vs %q", a[0].EntityHash, b[0].EntityHash)
	}
}
