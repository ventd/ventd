package setup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ventd/ventd/internal/recovery"
)

// PhaseGate wraps one phase of the wizard's setup goroutine with explicit
// pre/post/cleanup hooks. The motivation (per v0.5.9 PR-D plan):
//
//   - Pre runs BEFORE the body, so failures classified as a refused-entry
//     surface as a recovery card instead of a mid-flight crash. The
//     installing_driver Pre is where the comprehensive PreflightOOT chain
//     runs — every blocker becomes an actionable card up-front.
//   - Body is the existing per-phase work (driver install, fan scan,
//     calibration, etc.). Returning a non-nil error invokes
//     OnFailCleanup.
//   - Post is a postcondition check after Body succeeds. Catches the
//     case where the body returned nil but the resulting state isn't
//     usable for the next phase (module loaded but no PWM channels
//     visible; calibration "succeeded" but no fans actually written).
//   - OnFailCleanup runs on phase failure (any of: Pre refused, Body
//     errored, Post failed, ctx cancelled, panic recovered). Each phase
//     declares what state it owns; cleanup undoes that state so the
//     next install attempt starts from a clean slate.
//
// The wizard's existing `setPhase` machinery in setup.go is the gate
// driver — RunGate calls it before Body to update the wizard status,
// and on failure stamps Manager.errMsg + FailureClass + Remediation
// so the calibration error banner picks up actionable cards.
//
// A nil Pre/Post/OnFailCleanup is a no-op. Body is required.
type PhaseGate struct {
	// Name is one of the recovery.Phase* constants. Used as the
	// wizard's user-facing phase string and to disambiguate
	// classifier rules that depend on the phase context.
	Name string

	// Description is the phase_msg shown in the wizard banner while
	// this gate's body is running. Optional — the gate driver
	// composes a default if empty.
	Description string

	// Pre returns a recovery.FailureClass when entry should be
	// refused and the wizard should surface a remediation card.
	// (recovery.ClassUnknown, "", nil) means proceed.
	Pre func(ctx context.Context) (recovery.FailureClass, string, error)

	// Body executes the phase's actual work. A non-nil error
	// triggers OnFailCleanup, then propagates to Manager.run.
	Body func(ctx context.Context) error

	// Post checks the postcondition. If the resulting state is not
	// usable for the next phase, Post returns a refusal class +
	// detail; OnFailCleanup runs and the gate fails.
	// (recovery.ClassUnknown, "", nil) means proceed.
	Post func(ctx context.Context) (recovery.FailureClass, string, error)

	// OnFailCleanup is invoked on every failure path: Pre refusal,
	// Body error, Post refusal, ctx.Done, and panic-recover. Best-
	// effort — errors here are logged at WARN but never returned
	// to the caller.
	OnFailCleanup func(ctx context.Context)
}

// GateError captures a failed PhaseGate's outcome with enough
// information for the calibration error banner to render the right
// recovery card. Errors propagated from Body that are not GateError
// values are wrapped with ClassUnknown.
type GateError struct {
	Phase  string
	Class  recovery.FailureClass
	Detail string
	Cause  error
}

func (e *GateError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("setup: phase %q refused (%s): %s",
			e.Phase, e.Class, e.Detail)
	}
	if e.Cause != nil {
		return fmt.Sprintf("setup: phase %q failed (%s): %v",
			e.Phase, e.Class, e.Cause)
	}
	return fmt.Sprintf("setup: phase %q failed (%s)", e.Phase, e.Class)
}

func (e *GateError) Unwrap() error { return e.Cause }

// RunGate executes a single PhaseGate. Returns nil on success, a
// *GateError on any refusal/failure (with cleanup already run), or a
// context error when ctx is cancelled.
//
// Panic recovery is intentional — a panic in Body must still trigger
// OnFailCleanup so the phase doesn't leave orphan state. The recovered
// panic is wrapped in a GateError with ClassUnknown so the wizard
// can render the generic "Send diagnostic bundle" card. Logging the
// panic (with stack) happens in the wizard's own deferred recover —
// this gate just does cleanup and surfaces the failure.
func RunGate(ctx context.Context, gate PhaseGate, logger *slog.Logger) (gateErr error) {
	if logger == nil {
		logger = slog.Default()
	}
	if gate.Body == nil {
		return &GateError{
			Phase: gate.Name,
			Class: recovery.ClassUnknown,
			Cause: errors.New("setup: gate.Body is nil"),
		}
	}

	defer func() {
		if r := recover(); r != nil {
			runCleanup(ctx, gate, logger, "panic")
			gateErr = &GateError{
				Phase: gate.Name,
				Class: recovery.ClassUnknown,
				Cause: fmt.Errorf("panic in phase %s body: %v", gate.Name, r),
			}
		}
	}()

	if err := ctx.Err(); err != nil {
		return err
	}

	if gate.Pre != nil {
		class, detail, err := gate.Pre(ctx)
		if err != nil {
			runCleanup(ctx, gate, logger, "pre-error")
			return &GateError{
				Phase:  gate.Name,
				Class:  classOrUnknown(class),
				Detail: detail,
				Cause:  err,
			}
		}
		if class != recovery.ClassUnknown {
			runCleanup(ctx, gate, logger, "pre-refused")
			return &GateError{
				Phase:  gate.Name,
				Class:  class,
				Detail: detail,
			}
		}
	}

	if err := gate.Body(ctx); err != nil {
		runCleanup(ctx, gate, logger, "body-error")
		// If Body already returned a *GateError (a sub-gate
		// surfacing through this one), preserve its class so the
		// banner doesn't widen to ClassUnknown.
		var ge *GateError
		if errors.As(err, &ge) {
			return ge
		}
		return &GateError{
			Phase: gate.Name,
			Class: recovery.ClassUnknown,
			Cause: err,
		}
	}

	if gate.Post != nil {
		class, detail, err := gate.Post(ctx)
		if err != nil {
			runCleanup(ctx, gate, logger, "post-error")
			return &GateError{
				Phase:  gate.Name,
				Class:  classOrUnknown(class),
				Detail: detail,
				Cause:  err,
			}
		}
		if class != recovery.ClassUnknown {
			runCleanup(ctx, gate, logger, "post-refused")
			return &GateError{
				Phase:  gate.Name,
				Class:  class,
				Detail: detail,
			}
		}
	}

	return nil
}

// runCleanup invokes OnFailCleanup with a fresh context so cleanup
// gets a chance to complete even when the parent ctx has been
// cancelled. Cleanup that takes longer than 30s is considered
// runaway — the wizard's outer deferred close handles that case.
func runCleanup(ctx context.Context, gate PhaseGate, logger *slog.Logger, reason string) {
	if gate.OnFailCleanup == nil {
		return
	}
	logger.Info("phase cleanup",
		"phase", gate.Name, "reason", reason)
	defer func() {
		if r := recover(); r != nil {
			logger.Warn("phase cleanup panicked",
				"phase", gate.Name, "panic", r)
		}
	}()
	// Pass through the parent ctx; cleanup that respects cancellation
	// can shed work mid-flight, but most cleanup is bounded I/O that
	// runs to completion regardless.
	gate.OnFailCleanup(ctx)
}

func classOrUnknown(c recovery.FailureClass) recovery.FailureClass {
	if c == recovery.ClassUnknown {
		return recovery.ClassUnknown
	}
	return c
}
