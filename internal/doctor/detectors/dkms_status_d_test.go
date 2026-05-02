package detectors

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

func stubDKMSExec(out string, err error) DKMSExec {
	return func(ctx context.Context, args ...string) (string, error) {
		return out, err
	}
}

func TestRULE_DOCTOR_DETECTOR_DKMSStatus_HappyAllInstalled(t *testing.T) {
	out := strings.Join([]string{
		"nct6687/0.5, 6.8.0-49-generic, x86_64: installed",
		"corsair-cpro/1.0, 6.8.0-49-generic, x86_64: installed",
	}, "\n")
	det := NewDKMSStatusDetector(stubDKMSExec(out, nil))

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("all-installed emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_DKMSStatus_FailureSurfacesAsBlocker(t *testing.T) {
	out := strings.Join([]string{
		"nct6687/0.5, 6.8.0-49-generic, x86_64: failed",
		"corsair-cpro/1.0, 6.8.0-49-generic, x86_64: installed",
	}, "\n")
	det := NewDKMSStatusDetector(stubDKMSExec(out, nil))

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for failed module, got %d (%+v)", len(facts), facts)
	}
	f := facts[0]
	if f.Severity != doctor.SeverityBlocker {
		t.Errorf("Severity = %v, want Blocker", f.Severity)
	}
	if f.Class != recovery.ClassDKMSBuildFailed {
		t.Errorf("Class = %v, want ClassDKMSBuildFailed", f.Class)
	}
	if !strings.Contains(f.Title, "nct6687") {
		t.Errorf("Title doesn't name the failing module: %q", f.Title)
	}
	if !strings.Contains(f.Title, "6.8.0-49-generic") {
		t.Errorf("Title doesn't name the kernel: %q", f.Title)
	}
	if len(f.Journal) != 1 {
		t.Errorf("Journal len = %d, want 1", len(f.Journal))
	}
}

func TestRULE_DOCTOR_DETECTOR_DKMSStatus_BrokenAlsoFails(t *testing.T) {
	// Some DKMS forks use "broken" instead of "failed".
	out := "nct6687/0.5, 6.10.0-arch1-1, x86_64: broken"
	det := NewDKMSStatusDetector(stubDKMSExec(out, nil))

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Errorf("expected 1 fact for broken module, got %d", len(facts))
	}
}

func TestRULE_DOCTOR_DETECTOR_DKMSStatus_DKMSAbsentEmitsNothing(t *testing.T) {
	det := NewDKMSStatusDetector(stubDKMSExec("", errors.New("dkms not on PATH")))

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Errorf("DKMS-absent should not propagate as error: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("DKMS-absent emitted facts (preflight detector territory): %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_DKMSStatus_RespectsContextCancel(t *testing.T) {
	det := NewDKMSStatusDetector(stubDKMSExec("nct6687/0.5: failed", nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestParseDKMSStatusLine(t *testing.T) {
	cases := []struct {
		line, mod, kver, status string
	}{
		{"nct6687/0.5, 6.8.0-49-generic, x86_64: installed", "nct6687", "6.8.0-49-generic", "installed"},
		{"nct6687/0.5: added", "nct6687", "", "added"},
		{"nct6687/0.5, 6.8.0-49-generic, x86_64: failed", "nct6687", "6.8.0-49-generic", "failed"},
		{"corsair-cpro/1.0, 6.10.0-arch1-1, x86_64: built", "corsair-cpro", "6.10.0-arch1-1", "built"},
		{"", "", "", ""},
		{"  ", "", "", ""},
		{"some header without colons", "", "", ""},
	}
	for _, c := range cases {
		mod, kver, status := parseDKMSStatusLine(c.line)
		if mod != c.mod || kver != c.kver || status != c.status {
			t.Errorf("parseDKMSStatusLine(%q) = (%q, %q, %q), want (%q, %q, %q)",
				c.line, mod, kver, status, c.mod, c.kver, c.status)
		}
	}
}

func TestIsDKMSFailureStatus(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{"installed", false},
		{"built", false},
		{"added", false},
		{"failed", true},
		{"FAILED", true},
		{"broken", true},
		{"failed (config: bad gcc version)", true}, // some DKMS variants prefix
		{"", false},
	}
	for _, c := range cases {
		if got := isDKMSFailureStatus(c.status); got != c.want {
			t.Errorf("isDKMSFailureStatus(%q) = %v, want %v", c.status, got, c.want)
		}
	}
}
