package detectors

import (
	"context"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

func TestRULE_DOCTOR_DETECTOR_KernelUpdate_SameKernelNoFact(t *testing.T) {
	det := NewKernelUpdateDetector("6.8.0-49-generic", func() string { return "6.8.0-49-generic" })

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("same kernel emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_KernelUpdate_NewKernelWarning(t *testing.T) {
	det := NewKernelUpdateDetector("6.8.0-49-generic", func() string { return "6.8.0-50-generic" })

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for kernel update, got %d", len(facts))
	}
	f := facts[0]
	if f.Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want Warning", f.Severity)
	}
	if !strings.Contains(f.Title, "6.8.0-49-generic") || !strings.Contains(f.Title, "6.8.0-50-generic") {
		t.Errorf("Title doesn't show the transition: %q", f.Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_KernelUpdate_FirstRunNoOp(t *testing.T) {
	// Empty LastKernel = first daemon run; no comparison possible.
	det := NewKernelUpdateDetector("", func() string { return "6.8.0-49-generic" })

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("first run emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_KernelUpdate_UnreadableProcNoFact(t *testing.T) {
	// /proc/sys/kernel/osrelease unavailable → empty release →
	// graceful degrade.
	det := NewKernelUpdateDetector("6.8.0", func() string { return "" })

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("unreadable /proc emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_KernelUpdate_RespectsContextCancel(t *testing.T) {
	det := NewKernelUpdateDetector("6.8.0", func() string { return "6.9.0" })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestKernelUpdate_EntityHashChangesAcrossTransitions(t *testing.T) {
	// 6.8.0 → 6.9.0 should produce a different EntityHash than
	// 6.8.0 → 6.10.0 so the suppression store can distinguish them.
	a, _ := NewKernelUpdateDetector("6.8.0", func() string { return "6.9.0" }).
		Probe(context.Background(), doctor.Deps{Now: fixedNow})
	b, _ := NewKernelUpdateDetector("6.8.0", func() string { return "6.10.0" }).
		Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 fact each")
	}
	if a[0].EntityHash == b[0].EntityHash {
		t.Errorf("entity hash collided across distinct transitions: %q", a[0].EntityHash)
	}
}
