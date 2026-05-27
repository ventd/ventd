package gate

import (
	"testing"
	"time"
)

// allPass returns Deps where every term passes, so the gate opens.
func allPass() Deps {
	return Deps{
		SchemaLoaded:    func() bool { return true },
		PreconditionsOk: func() (bool, string) { return true, "" },
		WizardControl:   func() bool { return true },
		MassStalled:     func(time.Time) (bool, string) { return false, "" },
		SmartDisabled:   func() bool { return false },
		Now:             func() time.Time { return time.Unix(1000, 0) },
	}
}

// TestGate_AndComposition binds RULE-GATE-COMPOSE-01: the gate opens iff
// all five terms pass; flipping any single term closes it with that
// term's reason.
func TestGate_AndComposition(t *testing.T) {
	if s := New(allPass()).Evaluate(); !s.Open || s.Reason != ReasonOK {
		t.Fatalf("all-pass must open the gate; got open=%v reason=%q", s.Open, s.Reason)
	}

	cases := []struct {
		name   string
		mutate func(*Deps)
		want   Reason
	}{
		{"smart-disabled", func(d *Deps) { d.SmartDisabled = func() bool { return true } }, ReasonSmartDisabled},
		{"schema", func(d *Deps) { d.SchemaLoaded = func() bool { return false } }, ReasonSchemaUnloaded},
		{"preconditions", func(d *Deps) { d.PreconditionsOk = func() (bool, string) { return false, "boot warm-up" } }, ReasonPreconditions},
		{"wizard", func(d *Deps) { d.WizardControl = func() bool { return false } }, ReasonWizardOutcome},
		{"mass-stall", func(d *Deps) { d.MassStalled = func(time.Time) (bool, string) { return true, "2 fans stalled" } }, ReasonMassStall},
	}
	for _, tc := range cases {
		d := allPass()
		tc.mutate(&d)
		s := New(d).Evaluate()
		if s.Open {
			t.Errorf("%s: gate must be closed", tc.name)
		}
		if s.Reason != tc.want {
			t.Errorf("%s: reason = %q, want %q", tc.name, s.Reason, tc.want)
		}
	}
}

// TestGate_FailingReasonOrder binds RULE-GATE-REASON-01: when several
// terms fail at once, Reason is the highest-priority (most operator-
// actionable) one: smart_disabled > schema > preconditions > wizard >
// mass_stall.
func TestGate_FailingReasonOrder(t *testing.T) {
	d := Deps{
		SmartDisabled:   func() bool { return true },
		SchemaLoaded:    func() bool { return false },
		PreconditionsOk: func() (bool, string) { return false, "on battery" },
		WizardControl:   func() bool { return false },
		MassStalled:     func(time.Time) (bool, string) { return true, "2 fans stalled" },
		Now:             func() time.Time { return time.Unix(1000, 0) },
	}
	if s := New(d).Evaluate(); s.Reason != ReasonSmartDisabled {
		t.Fatalf("all-fail: reason = %q, want smart_disabled", s.Reason)
	}

	// Clear terms one at a time; the reason advances down the priority list.
	steps := []struct {
		clear  func(*Deps)
		expect Reason
	}{
		{func(d *Deps) { d.SmartDisabled = func() bool { return false } }, ReasonSchemaUnloaded},
		{func(d *Deps) { d.SchemaLoaded = func() bool { return true } }, ReasonPreconditions},
		{func(d *Deps) { d.PreconditionsOk = func() (bool, string) { return true, "" } }, ReasonWizardOutcome},
		{func(d *Deps) { d.WizardControl = func() bool { return true } }, ReasonMassStall},
	}
	for i, step := range steps {
		step.clear(&d)
		if s := New(d).Evaluate(); s.Reason != step.expect {
			t.Errorf("step %d: reason = %q, want %q", i, s.Reason, step.expect)
		}
	}
}

// TestGate_ClosedBeforeFirstEvaluate binds RULE-GATE-FAILSAFE-01: a
// freshly-constructed evaluator reports Open()==false until the first
// Evaluate, even with all-pass deps; a nil evaluator is closed.
func TestGate_ClosedBeforeFirstEvaluate(t *testing.T) {
	e := New(allPass())
	if e.Open() {
		t.Fatalf("gate must be closed before the first Evaluate")
	}
	if s := e.Read(); s == nil || s.Reason != ReasonInitialising {
		t.Fatalf("initial snapshot must be ReasonInitialising; got %+v", s)
	}

	e.Evaluate()
	if !e.Open() {
		t.Fatalf("gate must open after Evaluate with all-pass deps")
	}

	var nilE *Evaluator
	if nilE.Open() {
		t.Fatalf("nil evaluator must report closed")
	}
}
