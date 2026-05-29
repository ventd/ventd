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

func TestRULE_DOCTOR_DETECTOR_ECLockedLaptop_HybridHostEmitsControllerActiveInfo(t *testing.T) {
	// Hybrid host (#1415): controllable PWM channel(s) > 0 AND
	// platform_profile present. ventd's platform_profile controller
	// starts unconditionally whenever the interface exists, so the
	// operator MUST get visibility into the active selector loop even
	// though smart_mode also owns the writable channels. The Dell
	// Latitude 7280 fingerprint: 1 dell_smm pwm + 4-choice enum.
	det := NewECLockedLaptopDetector(1, stubPlatformProfile(
		"balanced",
		[]string{"cool", "quiet", "balanced", "performance"},
		true,
	))

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("hybrid host expected 1 informational fact, got %d", len(facts))
	}
	f := facts[0]
	if f.Severity != doctor.SeverityOK {
		t.Errorf("Severity = %v, want OK (informational)", f.Severity)
	}
	if !strings.Contains(f.Title, "platform_profile controller active") {
		t.Errorf("hybrid Title doesn't announce the active controller: %q", f.Title)
	}
	if !strings.Contains(f.Title, "balanced") {
		t.Errorf("hybrid Title doesn't name the active profile: %q", f.Title)
	}
	for _, want := range []string{"cool", "performance", "platform_profile", "smart-mode", "back-off"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("hybrid Detail missing %q: %q", want, f.Detail)
		}
	}
}

func TestRULE_DOCTOR_DETECTOR_ECLockedLaptop_HybridAndMonitorOnlyHashDistinct(t *testing.T) {
	// The hybrid "controller active" card and the monitor-only
	// "EC-owned" card are independently suppressible — an operator on a
	// hybrid box dismissing one must not silence the other class.
	choices := []string{"low-power", "balanced", "performance"}
	hybrid, _ := NewECLockedLaptopDetector(1, stubPlatformProfile("balanced", choices, true)).
		Probe(context.Background(), doctor.Deps{Now: fixedNow})
	mono, _ := NewECLockedLaptopDetector(0, stubPlatformProfile("balanced", choices, true)).
		Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(hybrid) != 1 || len(mono) != 1 {
		t.Fatalf("expected 1 fact each (hybrid=%d mono=%d)", len(hybrid), len(mono))
	}
	if hybrid[0].EntityHash == mono[0].EntityHash {
		t.Errorf("hybrid and monitor-only cards collided on EntityHash: %q", hybrid[0].EntityHash)
	}
}

func TestRULE_DOCTOR_DETECTOR_ECLockedLaptop_DesktopNoPlatformProfileNoFact(t *testing.T) {
	// Desktop with controllable channels but NO platform_profile
	// interface — smart_mode owns the surface and there is no selector
	// loop to surface. Stays quiet.
	det := NewECLockedLaptopDetector(4, stubPlatformProfile("", nil, false))

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("desktop without platform_profile emitted facts: %+v", facts)
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
