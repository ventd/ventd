package detectors

import (
	"context"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

// stubPlatformProfile returns a canned (current, choices, ok) triple.
func stubPlatformProfile(current string, choices []string, ok bool) PlatformProfileReadFn {
	return func() (string, []string, bool) {
		return current, choices, ok
	}
}

func TestRULE_DOCTOR_DETECTOR_ECLockedLaptop_HPPavilionPatternEmitsInfo(t *testing.T) {
	// HP Pavilion x360 14-cd0xxx HIL fingerprint: monitor-only,
	// platform_profile present with the typical 3-choice enum.
	det := NewECLockedLaptopDetector(0, stubPlatformProfile(
		"balanced",
		[]string{"low-power", "balanced", "performance"},
		true,
	))

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 informational fact, got %d", len(facts))
	}
	f := facts[0]
	if f.Severity != doctor.SeverityOK {
		t.Errorf("Severity = %v, want OK (informational; mirrors experimental_flags pattern)", f.Severity)
	}
	if !strings.Contains(f.Title, "balanced") {
		t.Errorf("Title doesn't name the active profile: %q", f.Title)
	}
	for _, want := range []string{"low-power", "balanced", "performance", "platform_profile", "#872"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("Detail missing %q: %q", want, f.Detail)
		}
	}
}

func TestRULE_DOCTOR_DETECTOR_ECLockedLaptop_DesktopWithChannelsNoFact(t *testing.T) {
	// Desktop with smart_mode applicable — controllable channels > 0.
	// Even if platform_profile exists (some workstations expose it),
	// this card MUST stay quiet so smart_mode owns the surface.
	det := NewECLockedLaptopDetector(4, stubPlatformProfile(
		"balanced",
		[]string{"low-power", "balanced", "performance"},
		true,
	))

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("desktop with channels emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_ECLockedLaptop_NoPlatformProfileNoFact(t *testing.T) {
	// Server / embedded host with no platform_profile interface and no
	// controllable channels. Monitor-only for a different reason —
	// other detectors handle it.
	det := NewECLockedLaptopDetector(0, stubPlatformProfile("", nil, false))

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("no platform_profile emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_ECLockedLaptop_SingleChoiceEnumNoFact(t *testing.T) {
	// Degenerate platform_profile with one choice — the operator can't
	// pick anything useful. Don't surface a card that promises control
	// the hardware can't deliver.
	det := NewECLockedLaptopDetector(0, stubPlatformProfile(
		"balanced",
		[]string{"balanced"},
		true,
	))

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("single-choice enum emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_ECLockedLaptop_RespectsContextCancel(t *testing.T) {
	det := NewECLockedLaptopDetector(0, stubPlatformProfile(
		"balanced",
		[]string{"low-power", "balanced", "performance"},
		true,
	))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestECLockedLaptop_EntityHashStableAcrossProbes(t *testing.T) {
	// Two Probe calls on the same host must produce the same EntityHash
	// so suppression-store dismissals stick across daemon restarts.
	det := NewECLockedLaptopDetector(0, stubPlatformProfile(
		"performance",
		[]string{"low-power", "balanced", "performance"},
		true,
	))

	a, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	b, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 fact each")
	}
	if a[0].EntityHash != b[0].EntityHash {
		t.Errorf("entity hash drifted across probes: %q vs %q", a[0].EntityHash, b[0].EntityHash)
	}
}

func TestECLockedLaptop_EntityHashDistinguishesChoicesShape(t *testing.T) {
	// HP-style 3-choice enum vs Dell-style 4-choice "cool"-included enum
	// must produce different hashes — operators on each platform can
	// suppress independently.
	hp, _ := NewECLockedLaptopDetector(0, stubPlatformProfile(
		"balanced",
		[]string{"low-power", "balanced", "performance"},
		true,
	)).Probe(context.Background(), doctor.Deps{Now: fixedNow})

	dell, _ := NewECLockedLaptopDetector(0, stubPlatformProfile(
		"balanced",
		[]string{"low-power", "cool", "balanced", "performance"},
		true,
	)).Probe(context.Background(), doctor.Deps{Now: fixedNow})

	if len(hp) != 1 || len(dell) != 1 {
		t.Fatalf("expected 1 fact each")
	}
	if hp[0].EntityHash == dell[0].EntityHash {
		t.Errorf("entity hash collided across distinct choice shapes: %q", hp[0].EntityHash)
	}
}
