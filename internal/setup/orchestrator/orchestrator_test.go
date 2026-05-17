package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ventd/ventd/internal/recovery"
)

// fakePhase is a test double that returns a configured outcome (or panics
// when Panic is set). It records each Execute invocation so tests can
// assert run / skip / retry semantics.
type fakePhase struct {
	name       string
	outcome    Outcome
	executions int
	panicMsg   string
}

func (f *fakePhase) Name() string { return f.name }
func (f *fakePhase) Execute(_ context.Context, _ *RunContext) Outcome {
	f.executions++
	if f.panicMsg != "" {
		panic(f.panicMsg)
	}
	return f.outcome
}

func newTestRunContext(t *testing.T) *RunContext {
	t.Helper()
	return &RunContext{
		StateDir: t.TempDir(),
	}
}

func TestOrchestrator_RejectsEmptyContext(t *testing.T) {
	if _, err := New(nil, &fakePhase{name: "a"}); err == nil {
		t.Error("New(nil) should error")
	}
}

func TestOrchestrator_RejectsDuplicatePhaseNames(t *testing.T) {
	rc := newTestRunContext(t)
	_, err := New(rc, &fakePhase{name: "x"}, &fakePhase{name: "x"})
	if err == nil {
		t.Error("New with duplicate names should error")
	}
}

func TestOrchestrator_RejectsEmptyPhaseName(t *testing.T) {
	rc := newTestRunContext(t)
	if _, err := New(rc, &fakePhase{name: ""}); err == nil {
		t.Error("New with empty Name() should error")
	}
}

func TestOrchestrator_RunHappyPathExecutesAllInOrder(t *testing.T) {
	rc := newTestRunContext(t)
	a := &fakePhase{name: "a", outcome: Outcome{Status: StatusSuccess}}
	b := &fakePhase{name: "b", outcome: Outcome{Status: StatusSuccess}}
	c := &fakePhase{name: "c", outcome: Outcome{Status: StatusSuccess}}

	o, err := New(rc, a, b, c)
	if err != nil {
		t.Fatal(err)
	}
	outs, err := o.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(outs) != 3 {
		t.Fatalf("len(outs) = %d, want 3", len(outs))
	}
	for i, name := range []string{"a", "b", "c"} {
		if outs[i].Phase != name {
			t.Errorf("outs[%d].Phase = %q, want %q", i, outs[i].Phase, name)
		}
		if outs[i].Status != StatusSuccess {
			t.Errorf("outs[%d].Status = %q, want Success", i, outs[i].Status)
		}
	}
	if a.executions != 1 || b.executions != 1 || c.executions != 1 {
		t.Errorf("executions: a=%d b=%d c=%d, want 1/1/1",
			a.executions, b.executions, c.executions)
	}
}

func TestOrchestrator_RunShortCircuitsOnFailure(t *testing.T) {
	rc := newTestRunContext(t)
	a := &fakePhase{name: "a", outcome: Outcome{Status: StatusSuccess}}
	b := &fakePhase{name: "b", outcome: Outcome{
		Status: StatusFailed, Class: recovery.ClassMissingHeaders, Detail: "no headers",
	}}
	c := &fakePhase{name: "c", outcome: Outcome{Status: StatusSuccess}}

	o, _ := New(rc, a, b, c)
	outs, err := o.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error on phase failure (expected nil err, status in slice): %v", err)
	}
	if len(outs) != 2 {
		t.Fatalf("len(outs) = %d, want 2 (a + failing b, no c)", len(outs))
	}
	if outs[1].Status != StatusFailed {
		t.Errorf("outs[1].Status = %q, want Failed", outs[1].Status)
	}
	if outs[1].Class != recovery.ClassMissingHeaders {
		t.Errorf("outs[1].Class = %q, want %q", outs[1].Class, recovery.ClassMissingHeaders)
	}
	if c.executions != 0 {
		t.Errorf("c should not have run after b failed; executions = %d", c.executions)
	}
}

func TestOrchestrator_ResumeSkipsSuccessPhases(t *testing.T) {
	rc := newTestRunContext(t)
	a := &fakePhase{name: "a", outcome: Outcome{Status: StatusSuccess}}
	b := &fakePhase{name: "b", outcome: Outcome{Status: StatusSuccess}}

	o, _ := New(rc, a, b)
	if _, err := o.Run(context.Background()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if a.executions != 1 || b.executions != 1 {
		t.Fatalf("first Run executions: a=%d b=%d, want 1/1", a.executions, b.executions)
	}

	// Second Run on the same orchestrator with prior Success
	// outcomes should skip both phases.
	outs, err := o.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if a.executions != 1 || b.executions != 1 {
		t.Errorf("second Run re-executed phases: a=%d b=%d, want 1/1", a.executions, b.executions)
	}
	if len(outs) != 2 || outs[0].Status != StatusSuccess || outs[1].Status != StatusSuccess {
		t.Errorf("resume outcomes wrong: %+v", outs)
	}
}

func TestOrchestrator_ResumeReRunsFailedPhase(t *testing.T) {
	rc := newTestRunContext(t)
	a := &fakePhase{name: "a", outcome: Outcome{Status: StatusSuccess}}
	b := &fakePhase{name: "b", outcome: Outcome{
		Status: StatusFailed, Class: recovery.ClassUnknown, Detail: "first try",
	}}

	o, _ := New(rc, a, b)
	if _, err := o.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Operator fixes the underlying issue: flip b to success.
	b.outcome = Outcome{Status: StatusSuccess}

	outs, err := o.Run(context.Background())
	if err != nil {
		t.Fatalf("retry Run: %v", err)
	}
	if a.executions != 1 {
		t.Errorf("a should not re-run on resume (was Success); executions = %d", a.executions)
	}
	if b.executions != 2 {
		t.Errorf("b should re-run on resume (was Failed); executions = %d", b.executions)
	}
	if outs[1].Status != StatusSuccess {
		t.Errorf("outs[1].Status = %q after retry, want Success", outs[1].Status)
	}
}

func TestOrchestrator_RetryPhaseReExecutesOnlyNamedPhase(t *testing.T) {
	rc := newTestRunContext(t)
	a := &fakePhase{name: "a", outcome: Outcome{Status: StatusSuccess}}
	b := &fakePhase{name: "b", outcome: Outcome{Status: StatusSuccess}}

	o, _ := New(rc, a, b)
	if _, err := o.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	out, err := o.RetryPhase(context.Background(), "b")
	if err != nil {
		t.Fatalf("RetryPhase: %v", err)
	}
	if out.Status != StatusSuccess {
		t.Errorf("retry Status = %q, want Success", out.Status)
	}
	if a.executions != 1 {
		t.Errorf("a should not re-run during RetryPhase(b); executions = %d", a.executions)
	}
	if b.executions != 2 {
		t.Errorf("b should re-run during RetryPhase(b); executions = %d", b.executions)
	}
}

func TestOrchestrator_RetryUnknownPhaseErrors(t *testing.T) {
	rc := newTestRunContext(t)
	o, _ := New(rc, &fakePhase{name: "a", outcome: Outcome{Status: StatusSuccess}})
	if _, err := o.RetryPhase(context.Background(), "nope"); err == nil {
		t.Error("RetryPhase with unknown name should error")
	}
}

func TestOrchestrator_PanicInPhaseBecomesFailedOutcome(t *testing.T) {
	rc := newTestRunContext(t)
	boom := &fakePhase{name: "boom", panicMsg: "kaboom"}

	o, _ := New(rc, boom)
	outs, err := o.Run(context.Background())
	if err != nil {
		t.Fatalf("Run with panicking phase should not return error, got %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("len(outs) = %d, want 1", len(outs))
	}
	if outs[0].Status != StatusFailed {
		t.Errorf("panicked phase Status = %q, want Failed", outs[0].Status)
	}
	if outs[0].Class != recovery.ClassUnknown {
		t.Errorf("panicked phase Class = %q, want ClassUnknown", outs[0].Class)
	}
}

func TestOrchestrator_NonTerminalStatusBecomesFailed(t *testing.T) {
	// A phase implementation bug — returning StatusRunning instead
	// of a terminal Status — must not silently advance the wizard.
	rc := newTestRunContext(t)
	buggy := &fakePhase{name: "buggy", outcome: Outcome{Status: StatusRunning}}
	o, _ := New(rc, buggy)
	outs, _ := o.Run(context.Background())
	if outs[0].Status != StatusFailed {
		t.Errorf("non-terminal Status should coerce to Failed, got %q", outs[0].Status)
	}
}

func TestOrchestrator_OutcomeArtifactPreservedAcrossSaveLoad(t *testing.T) {
	rc := newTestRunContext(t)
	payload, _ := json.Marshal(map[string]string{"board_vendor": "MSI"})
	p := &fakePhase{name: "p", outcome: Outcome{
		Status:   StatusSuccess,
		Artifact: payload,
	}}

	o, _ := New(rc, p)
	if _, err := o.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Build a fresh orchestrator over the same StateDir and confirm
	// the artifact came through unchanged.
	p2 := &fakePhase{name: "p", outcome: Outcome{Status: StatusFailed}} // would fail, but shouldn't be invoked
	o2, _ := New(rc, p2)
	outs, err := o2.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p2.executions != 0 {
		t.Error("resumed phase should not re-execute")
	}
	// Compare artifacts semantically: the checkpoint store reformats
	// with MarshalIndent on Save, so a byte-level compare against the
	// compact form would fail despite the data being identical.
	var got, want map[string]string
	if err := json.Unmarshal(outs[0].Artifact, &got); err != nil {
		t.Fatalf("unmarshal resumed artifact: %v", err)
	}
	if err := json.Unmarshal(payload, &want); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got["board_vendor"] != want["board_vendor"] {
		t.Errorf("artifact mismatch after resume: got %v, want %v", got, want)
	}
}

func TestOrchestrator_ContextCancellationStopsBetweenPhases(t *testing.T) {
	rc := newTestRunContext(t)
	first := &fakePhase{name: "first", outcome: Outcome{Status: StatusSuccess}}
	second := &fakePhase{name: "second", outcome: Outcome{Status: StatusSuccess}}

	o, err := New(rc, first, second)
	if err != nil {
		t.Fatal(err)
	}

	// Cancel the context BEFORE Run; the orchestrator checks ctx.Err
	// after each phase. The first phase still executes (no per-phase
	// pre-check) and then the cancellation bubbles up before "second".
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outs, err := o.Run(ctx)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got err=%v", err)
	}
	if len(outs) != 1 {
		t.Errorf("expected only first phase to run before cancel, got %d outcomes", len(outs))
	}
	if first.executions != 1 {
		t.Errorf("first should have run once before cancel; executions = %d", first.executions)
	}
	if second.executions != 0 {
		t.Errorf("second should not have run after cancel; executions = %d", second.executions)
	}
}
