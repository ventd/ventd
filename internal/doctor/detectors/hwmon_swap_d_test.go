package detectors

import (
	"context"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

// hwmonStub uses the existing stubHwmonFS from kmod_loaded_d_test.go
// for live filesystem mocking — same package, shared test helper.

func TestRULE_DOCTOR_DETECTOR_HwmonSwap_NoChangeNoFacts(t *testing.T) {
	fs := &stubHwmonFS{names: map[string]string{
		"hwmon0": "nct6687",
		"hwmon1": "coretemp",
	}}
	det := NewHwmonSwapDetector(map[string]string{
		"nct6687":  "hwmon0",
		"coretemp": "hwmon1",
	}, fs)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("no-change emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_HwmonSwap_IndexFlipBlocker(t *testing.T) {
	// Baseline: nct6687 was hwmon0, coretemp was hwmon1.
	// Live: indices flipped after a module reload.
	fs := &stubHwmonFS{names: map[string]string{
		"hwmon0": "coretemp", // was hwmon1
		"hwmon1": "nct6687",  // was hwmon0
	}}
	det := NewHwmonSwapDetector(map[string]string{
		"nct6687":  "hwmon0",
		"coretemp": "hwmon1",
	}, fs)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 2 {
		t.Fatalf("expected 2 facts (one per moved chip), got %d", len(facts))
	}
	for _, f := range facts {
		if f.Severity != doctor.SeverityBlocker {
			t.Errorf("Severity = %v, want Blocker", f.Severity)
		}
		if !strings.Contains(f.Title, "moved") {
			t.Errorf("Title doesn't say moved: %q", f.Title)
		}
	}
}

func TestRULE_DOCTOR_DETECTOR_HwmonSwap_DisappearedChipBlocker(t *testing.T) {
	fs := &stubHwmonFS{names: map[string]string{
		"hwmon0": "coretemp", // nct6687 went away
	}}
	det := NewHwmonSwapDetector(map[string]string{
		"nct6687":  "hwmon0",
		"coretemp": "hwmon1",
	}, fs)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 2 {
		// nct6687 disappeared + coretemp moved (hwmon1 → hwmon0)
		t.Fatalf("expected 2 facts (disappear + move), got %d (%+v)", len(facts), facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_HwmonSwap_EmptyBaselineNoOp(t *testing.T) {
	fs := &stubHwmonFS{names: map[string]string{"hwmon0": "nct6687"}}
	det := NewHwmonSwapDetector(nil, fs)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("empty baseline emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_HwmonSwap_BaselineCopiedNotShared(t *testing.T) {
	// Mutating the input map after construction must not affect the
	// detector's view — defensive copy invariant.
	src := map[string]string{"nct6687": "hwmon0"}
	det := NewHwmonSwapDetector(src, &stubHwmonFS{names: map[string]string{"hwmon0": "nct6687"}})

	src["nct6687"] = "hwmonZZZ" // would-be-poison
	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("post-construction map mutation leaked into detector: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_HwmonSwap_RespectsContextCancel(t *testing.T) {
	det := NewHwmonSwapDetector(map[string]string{"nct6687": "hwmon0"}, &stubHwmonFS{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestTrimNL_TrailingWhitespace(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"nct6687\n", "nct6687"},
		{"nct6687 \n", "nct6687"},
		{"nct6687\t\n", "nct6687"},
		{"\n", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := trimNL(c.in); got != c.want {
			t.Errorf("trimNL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
