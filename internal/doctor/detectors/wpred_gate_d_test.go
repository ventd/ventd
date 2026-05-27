package detectors

import (
	"context"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

// TestWPredGateDetector binds RULE-DOCTOR-DETECTOR-WPREDGATE: the
// w_pred_gate detector emits one OK fact when the gate is open or closed
// for a benign reason, a Warning on mass-stall, and nothing in
// monitor-only mode (no gate). A nil status fn is a no-op.
func TestWPredGateDetector(t *testing.T) {
	mk := func(open bool, reason, detail string, has bool) WPredGateStatusFn {
		return func() (bool, string, string, bool) { return open, reason, detail, has }
	}
	cases := []struct {
		name      string
		fn        WPredGateStatusFn
		wantFacts int
		wantSev   doctor.Severity
	}{
		{"monitor-only", mk(false, "", "", false), 0, doctor.SeverityOK},
		{"open", mk(true, "", "", true), 1, doctor.SeverityOK},
		{"smart-disabled", mk(false, "smart_disabled", "", true), 1, doctor.SeverityOK},
		{"boot-warmup", mk(false, "hard_precondition", "boot warm-up", true), 1, doctor.SeverityOK},
		{"wizard", mk(false, "wizard_not_control", "", true), 1, doctor.SeverityOK},
		{"mass-stall", mk(false, "mass_stall", "2 channels stalled", true), 1, doctor.SeverityWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewWPredGateDetector(tc.fn)
			facts, err := d.Probe(context.Background(), doctor.Deps{})
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if len(facts) != tc.wantFacts {
				t.Fatalf("facts = %d, want %d", len(facts), tc.wantFacts)
			}
			if tc.wantFacts > 0 && facts[0].Severity != tc.wantSev {
				t.Errorf("severity = %v, want %v", facts[0].Severity, tc.wantSev)
			}
		})
	}

	// nil status fn is a no-op.
	if facts, err := (&WPredGateDetector{}).Probe(context.Background(), doctor.Deps{}); err != nil || facts != nil {
		t.Fatalf("nil status fn must be a no-op; got facts=%v err=%v", facts, err)
	}
}
