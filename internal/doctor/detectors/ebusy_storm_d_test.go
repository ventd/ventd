package detectors

import (
	"context"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

// TestEBUSYStormDetector binds RULE-DOCTOR-DETECTOR-EBUSY-STORM: the detector
// emits one Warning fact per channel storming past the threshold, stays silent
// for benign one-off re-acquires (below threshold) and when there are no active
// storms, and a nil status fn is a no-op.
func TestEBUSYStormDetector(t *testing.T) {
	src := func(storms ...EBUSYStorm) EBUSYStormStatusFn {
		return func() []EBUSYStorm { return storms }
	}
	cases := []struct {
		name      string
		fn        EBUSYStormStatusFn
		wantFacts int
	}{
		{"nil-source", nil, 0},
		{"no-storms", src(), 0},
		{"below-threshold", src(EBUSYStorm{ChannelPath: "/sys/.../pwm1", EventCount: ebusyStormWarnEvents - 1, WindowSeconds: 60}), 0},
		{"at-threshold", src(EBUSYStorm{ChannelPath: "/sys/.../pwm1", EventCount: ebusyStormWarnEvents, WindowSeconds: 60}), 1},
		{"two-storms", src(
			EBUSYStorm{ChannelPath: "/sys/.../pwm1", EventCount: 9, WindowSeconds: 60},
			EBUSYStorm{ChannelPath: "/sys/.../pwm3", EventCount: 21, WindowSeconds: 60},
		), 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewEBUSYStormDetector(tc.fn)
			facts, err := d.Probe(context.Background(), doctor.Deps{})
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if len(facts) != tc.wantFacts {
				t.Fatalf("facts = %d, want %d", len(facts), tc.wantFacts)
			}
			for _, f := range facts {
				if f.Severity != doctor.SeverityWarning {
					t.Errorf("severity = %v, want Warning", f.Severity)
				}
				if f.Detector != "ebusy_storm" {
					t.Errorf("detector = %q, want ebusy_storm", f.Detector)
				}
			}
		})
	}
}

// TestEBUSYStormDetector_DetailAndEntityHash pins that each storming channel
// gets a distinct EntityHash (so suppressing one channel's card doesn't suppress
// another's) and an actionable body naming the path + the BIOS remediation.
func TestEBUSYStormDetector_DetailAndEntityHash(t *testing.T) {
	d := NewEBUSYStormDetector(func() []EBUSYStorm {
		return []EBUSYStorm{
			{ChannelPath: "/sys/class/hwmon/hwmon9/pwm1", EventCount: 7, WindowSeconds: 60},
			{ChannelPath: "/sys/class/hwmon/hwmon9/pwm3", EventCount: 12, WindowSeconds: 60},
		}
	})
	facts, err := d.Probe(context.Background(), doctor.Deps{})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("facts = %d, want 2", len(facts))
	}
	if facts[0].EntityHash == facts[1].EntityHash {
		t.Error("per-channel EntityHash must differ so suppression is channel-scoped")
	}
	if !strings.Contains(facts[0].Detail, "pwm1") || !strings.Contains(facts[0].Detail, "BIOS") {
		t.Errorf("detail should name the channel and the BIOS remediation; got: %s", facts[0].Detail)
	}
}
