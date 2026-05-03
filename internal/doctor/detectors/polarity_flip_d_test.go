package detectors

import (
	"context"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/doctor"
)

// stubSignguard is a deterministic SignguardSnapshotter for tests.
// Channels listed in confirmed[] are reported as confirmed; everything
// else is unconfirmed.
type stubSignguard struct {
	confirmed map[string]struct{}
}

func (s *stubSignguard) Confirmed(channelID string) bool {
	_, ok := s.confirmed[channelID]
	return ok
}

func sgWithConfirmed(channels ...string) *stubSignguard {
	m := make(map[string]struct{}, len(channels))
	for _, c := range channels {
		m[c] = struct{}{}
	}
	return &stubSignguard{confirmed: m}
}

func TestRULE_DOCTOR_DETECTOR_PolarityFlip_AllConfirmedNoFacts(t *testing.T) {
	det := NewPolarityFlipDetector([]string{"hwmon0:pwm1", "hwmon0:pwm2"}, sgWithConfirmed("hwmon0:pwm1", "hwmon0:pwm2"))

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("all-confirmed emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_PolarityFlip_OneUnconfirmedYieldsWarning(t *testing.T) {
	det := NewPolarityFlipDetector(
		[]string{"hwmon0:pwm1", "hwmon0:pwm2", "hwmon0:pwm3"},
		sgWithConfirmed("hwmon0:pwm1", "hwmon0:pwm3"), // pwm2 unconfirmed
	)

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for unconfirmed channel, got %d (%+v)", len(facts), facts)
	}
	f := facts[0]
	if f.Severity != doctor.SeverityWarning {
		t.Errorf("Severity = %v, want Warning", f.Severity)
	}
	if f.Detector != "polarity_flip" {
		t.Errorf("Detector = %q, want %q", f.Detector, "polarity_flip")
	}
	if f.EntityHash == "" {
		t.Errorf("EntityHash is empty")
	}
	if !f.Observed.Equal(fixedNow()) {
		t.Errorf("Observed = %v, want %v", f.Observed, fixedNow())
	}
}

func TestRULE_DOCTOR_DETECTOR_PolarityFlip_NilSignguardEmitsNothing(t *testing.T) {
	det := NewPolarityFlipDetector([]string{"hwmon0:pwm1"}, nil)

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("nil signguard emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_PolarityFlip_RespectsContextCancel(t *testing.T) {
	det := NewPolarityFlipDetector([]string{"hwmon0:pwm1"}, sgWithConfirmed())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestPolarityFlip_EntityHashUniqueAcrossChannels(t *testing.T) {
	det := NewPolarityFlipDetector(
		[]string{"hwmon0:pwm1", "hwmon0:pwm2"},
		sgWithConfirmed(), // both unconfirmed
	)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 2 {
		t.Fatalf("expected 2 facts, got %d", len(facts))
	}
	if facts[0].EntityHash == facts[1].EntityHash {
		t.Errorf("entity hashes collided across channels: %q", facts[0].EntityHash)
	}
}

// staleSignguard simulates a signguard that's slow on first call but
// stable on the second — confirms the detector doesn't cache state
// from a prior tick (probes are pure read).
type staleSignguard struct {
	calls int
}

func (s *staleSignguard) Confirmed(string) bool {
	s.calls++
	return s.calls > 1 // unconfirmed first call, confirmed second
}

func TestPolarityFlip_NoStateAcrossProbeCalls(t *testing.T) {
	sg := &staleSignguard{}
	det := NewPolarityFlipDetector([]string{"x"}, sg)

	first, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(first) != 1 {
		t.Fatalf("first call: expected 1 fact, got %d", len(first))
	}
	second, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(second) != 0 {
		t.Errorf("second call: expected 0 facts (stub flipped to confirmed), got %d", len(second))
	}
}

// timeNowFromDeps is exercised indirectly via fixedNow above; this
// micro-test pins the nil-Now fallback to time.Now for safety.
func TestTimeNowFromDeps_NilFallback(t *testing.T) {
	got := timeNowFromDeps(doctor.Deps{}) // Now nil
	if time.Since(got) > time.Second {
		t.Errorf("nil-Now fallback returned stale time: %v", got)
	}
}
