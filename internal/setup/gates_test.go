package setup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/ventd/ventd/internal/recovery"
)

// TestRULE_WIZARD_GATE_PreRefusalSkipsBody verifies that when Pre
// returns a non-ClassUnknown FailureClass the gate's Body is NEVER
// invoked and OnFailCleanup runs.
// Bound to RULE-WIZARD-GATE-01.
func TestRULE_WIZARD_GATE_PreRefusalSkipsBody(t *testing.T) {
	bodyCalled := false
	cleanupCalled := false
	gate := PhaseGate{
		Name: "test_phase",
		Pre: func(ctx context.Context) (recovery.FailureClass, string, error) {
			return recovery.ClassContainerised, "running in container", nil
		},
		Body: func(ctx context.Context) error {
			bodyCalled = true
			return nil
		},
		OnFailCleanup: func(ctx context.Context) {
			cleanupCalled = true
		},
	}
	err := RunGate(context.Background(), gate, discardLogger())
	if err == nil {
		t.Fatalf("expected GateError")
	}
	var ge *GateError
	if !errors.As(err, &ge) {
		t.Fatalf("err is %T, want *GateError", err)
	}
	if ge.Class != recovery.ClassContainerised {
		t.Errorf("class = %q, want %q", ge.Class, recovery.ClassContainerised)
	}
	if bodyCalled {
		t.Errorf("Body must not run after Pre refusal")
	}
	if !cleanupCalled {
		t.Errorf("OnFailCleanup must run after Pre refusal")
	}
}

// TestRULE_WIZARD_GATE_BodyErrorTriggersCleanup verifies that a Body
// error invokes OnFailCleanup and propagates as a *GateError. If Body
// already returned a *GateError, its class is preserved.
// Bound to RULE-WIZARD-GATE-02.
func TestRULE_WIZARD_GATE_BodyErrorTriggersCleanup(t *testing.T) {
	cleanupCalled := false
	gate := PhaseGate{
		Name: "test_phase",
		Body: func(ctx context.Context) error {
			return &GateError{
				Phase: "inner_phase",
				Class: recovery.ClassDKMSStateCollision,
				Cause: errors.New("dkms residue"),
			}
		},
		OnFailCleanup: func(ctx context.Context) {
			cleanupCalled = true
		},
	}
	err := RunGate(context.Background(), gate, discardLogger())
	var ge *GateError
	if !errors.As(err, &ge) {
		t.Fatalf("err is %T, want *GateError", err)
	}
	if ge.Class != recovery.ClassDKMSStateCollision {
		t.Errorf("class = %q, want preserved %q (a sub-gate's class must propagate)",
			ge.Class, recovery.ClassDKMSStateCollision)
	}
	if !cleanupCalled {
		t.Errorf("OnFailCleanup must run on Body error")
	}
}

// TestRULE_WIZARD_GATE_PostRefusalTriggersCleanup verifies that a
// Post refusal (Body succeeded but postcondition failed) runs cleanup
// and surfaces the FailureClass.
// Bound to RULE-WIZARD-GATE-03.
func TestRULE_WIZARD_GATE_PostRefusalTriggersCleanup(t *testing.T) {
	cleanupCalled := false
	gate := PhaseGate{
		Name: "test_phase",
		Body: func(ctx context.Context) error { return nil },
		Post: func(ctx context.Context) (recovery.FailureClass, string, error) {
			return recovery.ClassMissingModule, "module loaded but pwm sysfs absent", nil
		},
		OnFailCleanup: func(ctx context.Context) {
			cleanupCalled = true
		},
	}
	err := RunGate(context.Background(), gate, discardLogger())
	var ge *GateError
	if !errors.As(err, &ge) {
		t.Fatalf("err is %T, want *GateError", err)
	}
	if ge.Class != recovery.ClassMissingModule {
		t.Errorf("class = %q, want %q", ge.Class, recovery.ClassMissingModule)
	}
	if !cleanupCalled {
		t.Errorf("OnFailCleanup must run on Post refusal")
	}
}

// TestRULE_WIZARD_GATE_PanicRecoversAndCleansUp verifies that a panic
// inside Body is recovered, OnFailCleanup runs, and the gate returns
// a *GateError instead of propagating the panic to Manager.run.
// Bound to RULE-WIZARD-GATE-04.
func TestRULE_WIZARD_GATE_PanicRecoversAndCleansUp(t *testing.T) {
	cleanupCalled := false
	gate := PhaseGate{
		Name: "test_phase",
		Body: func(ctx context.Context) error {
			panic("simulated body panic")
		},
		OnFailCleanup: func(ctx context.Context) {
			cleanupCalled = true
		},
	}
	err := RunGate(context.Background(), gate, discardLogger())
	if err == nil {
		t.Fatalf("expected error from panicking gate")
	}
	var ge *GateError
	if !errors.As(err, &ge) {
		t.Fatalf("err is %T, want *GateError", err)
	}
	if !cleanupCalled {
		t.Errorf("OnFailCleanup must run after Body panic")
	}
}

// TestRULE_WIZARD_GATE_HappyPathRunsBody verifies that when Pre,
// Body, and Post all succeed the gate returns nil and OnFailCleanup
// is NOT invoked.
// Bound to RULE-WIZARD-GATE-05.
func TestRULE_WIZARD_GATE_HappyPathRunsBody(t *testing.T) {
	cleanupCalled := false
	bodyCalled := false
	gate := PhaseGate{
		Name: "test_phase",
		Pre: func(ctx context.Context) (recovery.FailureClass, string, error) {
			return recovery.ClassUnknown, "", nil
		},
		Body: func(ctx context.Context) error {
			bodyCalled = true
			return nil
		},
		Post: func(ctx context.Context) (recovery.FailureClass, string, error) {
			return recovery.ClassUnknown, "", nil
		},
		OnFailCleanup: func(ctx context.Context) {
			cleanupCalled = true
		},
	}
	if err := RunGate(context.Background(), gate, discardLogger()); err != nil {
		t.Fatalf("happy-path gate returned error: %v", err)
	}
	if !bodyCalled {
		t.Errorf("Body must run on happy path")
	}
	if cleanupCalled {
		t.Errorf("OnFailCleanup must NOT run on happy path")
	}
}

// TestRULE_WIZARD_GATE_NilBodyRefused verifies that a malformed gate
// with no Body returns a GateError without invoking cleanup
// (the malformed config is a programming error, not a runtime
// state to clean up).
// Bound to RULE-WIZARD-GATE-06.
func TestRULE_WIZARD_GATE_NilBodyRefused(t *testing.T) {
	cleanupCalled := false
	gate := PhaseGate{
		Name: "test_phase",
		// Body deliberately nil
		OnFailCleanup: func(ctx context.Context) {
			cleanupCalled = true
		},
	}
	err := RunGate(context.Background(), gate, discardLogger())
	if err == nil {
		t.Fatalf("expected error for nil Body")
	}
	var ge *GateError
	if !errors.As(err, &ge) {
		t.Fatalf("err is %T, want *GateError", err)
	}
	if cleanupCalled {
		t.Errorf("OnFailCleanup must NOT run for nil-Body programming error")
	}
}

// discardLogger keeps test output clean — slog with io.Discard.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
