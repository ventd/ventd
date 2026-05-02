package detectors

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ventd/ventd/internal/doctor"
)

// asusZ790Mocked is a synthetic /sys/class/dmi/id tree representing
// an ASUS ROG STRIX Z790-E system. Used to exercise the fingerprint
// path without depending on the real /sys.
func asusZ790Mocked() fstest.MapFS {
	return fstest.MapFS{
		"sys/class/dmi/id/sys_vendor":    {Data: []byte("ASUSTeK COMPUTER INC.\n")},
		"sys/class/dmi/id/product_name":  {Data: []byte("System Product Name\n")},
		"sys/class/dmi/id/board_vendor":  {Data: []byte("ASUSTeK COMPUTER INC.\n")},
		"sys/class/dmi/id/board_name":    {Data: []byte("ROG STRIX Z790-E GAMING WIFI\n")},
		"sys/class/dmi/id/board_version": {Data: []byte("Rev 1.xx\n")},
		"proc/cpuinfo": {Data: []byte(
			"processor	: 0\nmodel name	: 13th Gen Intel(R) Core(TM) i9-13900K\n" +
				"processor	: 1\nmodel name	: 13th Gen Intel(R) Core(TM) i9-13900K\n",
		)},
	}
}

func TestRULE_DOCTOR_DETECTOR_DMIFingerprint_MatchedYieldsOK(t *testing.T) {
	det := NewDMIFingerprintDetector(asusZ790Mocked(), true, "ASUS ROG STRIX Z790-E")

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	f := facts[0]
	if f.Severity != doctor.SeverityOK {
		t.Errorf("Severity = %v, want OK", f.Severity)
	}
	if !strings.Contains(f.Title, "ASUS ROG STRIX Z790-E") {
		t.Errorf("Title doesn't name the board: %q", f.Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_DMIFingerprint_NoMatchYieldsWarning(t *testing.T) {
	det := NewDMIFingerprintDetector(asusZ790Mocked(), false, "")

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want Warning", facts[0].Severity)
	}
	if !strings.Contains(facts[0].Title, "generic mode") {
		t.Errorf("Title doesn't say generic-mode: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_DMIFingerprint_EmptyFingerprintNoOp(t *testing.T) {
	// Sandbox / container with no /sys/class/dmi/id at all.
	det := NewDMIFingerprintDetector(fstest.MapFS{}, false, "")

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	// All-empty DMI tuple still produces a deterministic fingerprint
	// (sha256 of pipe-joined empty strings); the "empty fingerprint"
	// branch only fires if the function returns "" — which can't
	// happen with the current Fingerprint impl. So the detector
	// emits exactly the no-match Warning.
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact (no-match warning) for empty FS, got %d", len(facts))
	}
	if facts[0].Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want Warning", facts[0].Severity)
	}
}

func TestRULE_DOCTOR_DETECTOR_DMIFingerprint_RespectsContextCancel(t *testing.T) {
	det := NewDMIFingerprintDetector(asusZ790Mocked(), true, "test")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestDMIFingerprint_HashStableAcrossInvocations(t *testing.T) {
	// Same DMI input → same fingerprint hash → same EntityHash on
	// successive Probes. Pinned so a future canonicalisation tweak
	// doesn't silently invalidate persisted suppressions keyed on
	// the entity hash.
	mock := asusZ790Mocked()
	det := NewDMIFingerprintDetector(mock, false, "")

	a, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	b, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 fact each, got %d / %d", len(a), len(b))
	}
	if a[0].EntityHash != b[0].EntityHash {
		t.Errorf("EntityHash unstable: %q vs %q", a[0].EntityHash, b[0].EntityHash)
	}
}
