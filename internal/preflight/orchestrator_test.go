package preflight

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// scriptedPrompter answers in a fixed sequence and records the
// questions it was asked.
type scriptedPrompter struct {
	answers []PromptResponse
	asked   []string
	out     bytes.Buffer
	idx     int
}

func (s *scriptedPrompter) AskYN(q string) PromptResponse {
	s.asked = append(s.asked, q)
	if s.idx >= len(s.answers) {
		return PromptNo
	}
	a := s.answers[s.idx]
	s.idx++
	return a
}

func (s *scriptedPrompter) Print(line string) { s.out.WriteString(line + "\n") }

// Each subtest below is the bound implementation of one
// RULE-PREFLIGHT-ORCH-* invariant in
// .claude/rules/preflight-orchestrator.md.

func TestOrchestrator(t *testing.T) {
	t.Run("RULE-PREFLIGHT-ORCH-01_blocker_with_no_fix_returns_error", func(t *testing.T) {
		// A triggered Blocker with nil AutoFix can never clear; the
		// rollup MUST count it and Run MUST return a non-nil error.
		// This pins the docs-only branch — the operator has to fix
		// the system out-of-band before the install can proceed.
		checks := []Check{{
			Name:     "docs_only_blocker",
			Severity: SeverityBlocker,
			Detect:   func(context.Context) (bool, string) { return true, "manual action required" },
		}}
		report, err := Run(context.Background(), checks, Options{Prompter: &scriptedPrompter{}})
		if err == nil {
			t.Fatalf("expected error for unresolved blocker, got nil")
		}
		if report.BlockerCount != 1 {
			t.Fatalf("BlockerCount: got %d, want 1", report.BlockerCount)
		}
	})

	t.Run("RULE-PREFLIGHT-ORCH-02_interactive_yes_runs_fix_and_redetects", func(t *testing.T) {
		// Y answer MUST: invoke AutoFix, then re-run Detect. A fix
		// that clears the condition transitions the Result from
		// Triggered=true to StillTriggered=false. Without the
		// re-detect step a buggy AutoFix that "succeeded" but didn't
		// actually fix anything would silently mark the channel
		// clean.
		fixCalls := 0
		detectCalls := 0
		checks := []Check{{
			Name:     "fixable",
			Severity: SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				detectCalls++
				return fixCalls == 0, "x"
			},
			AutoFix:    func(context.Context) error { fixCalls++; return nil },
			PromptText: "Apply?",
		}}
		p := &scriptedPrompter{answers: []PromptResponse{PromptYes}}
		report, err := Run(context.Background(), checks, Options{Interactive: true, Prompter: p})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fixCalls != 1 {
			t.Fatalf("fixCalls: got %d, want 1", fixCalls)
		}
		if detectCalls != 2 {
			t.Fatalf("detectCalls: got %d, want 2 (initial + post-fix verify)", detectCalls)
		}
		if report.BlockerCount != 0 {
			t.Fatalf("BlockerCount: got %d, want 0 after successful fix", report.BlockerCount)
		}
	})

	t.Run("RULE-PREFLIGHT-ORCH-03_no_answer_leaves_blocker_pending", func(t *testing.T) {
		// N answer MUST NOT call AutoFix and MUST leave the blocker
		// counted. The orchestrator continues to the next check
		// rather than aborting — the operator may answer N to one
		// fix but Y to the next.
		fixCalls := 0
		checks := []Check{
			{
				Name:     "declined",
				Severity: SeverityBlocker,
				Detect:   func(context.Context) (bool, string) { return true, "" },
				AutoFix:  func(context.Context) error { fixCalls++; return nil },
			},
			{
				Name:     "second",
				Severity: SeverityWarning,
				Detect:   func(context.Context) (bool, string) { return true, "" },
			},
		}
		p := &scriptedPrompter{answers: []PromptResponse{PromptNo}}
		report, err := Run(context.Background(), checks, Options{Interactive: true, Prompter: p})
		if err == nil {
			t.Fatalf("expected error for declined blocker")
		}
		if fixCalls != 0 {
			t.Fatalf("fixCalls: got %d, want 0 after No", fixCalls)
		}
		if report.BlockerCount != 1 {
			t.Fatalf("BlockerCount: got %d, want 1", report.BlockerCount)
		}
		if report.WarningCount != 1 {
			t.Fatalf("WarningCount: got %d, want 1", report.WarningCount)
		}
	})

	t.Run("RULE-PREFLIGHT-ORCH-04_abort_stops_remaining_checks", func(t *testing.T) {
		// q / EOF answer MUST stop the fix loop entirely — no later
		// blocker is prompted on. This protects the operator who
		// realises mid-run that a different problem needs attention
		// first; they should never be asked about every remaining
		// blocker after they've already aborted.
		secondPrompted := false
		checks := []Check{
			{
				Name:     "first",
				Severity: SeverityBlocker,
				Detect:   func(context.Context) (bool, string) { return true, "" },
				AutoFix:  func(context.Context) error { return nil },
			},
			{
				Name:     "second",
				Severity: SeverityBlocker,
				Detect:   func(context.Context) (bool, string) { return true, "" },
				AutoFix: func(context.Context) error {
					secondPrompted = true
					return nil
				},
			},
		}
		p := &scriptedPrompter{answers: []PromptResponse{PromptAbort}}
		_, err := Run(context.Background(), checks, Options{Interactive: true, Prompter: p})
		if err == nil {
			t.Fatalf("expected error after abort")
		}
		if secondPrompted {
			t.Fatalf("second AutoFix ran after abort")
		}
		if len(p.asked) != 1 {
			t.Fatalf("prompts asked: got %d, want 1 (abort stops chain)", len(p.asked))
		}
	})

	t.Run("RULE-PREFLIGHT-ORCH-05_reboot_required_when_fix_marks_it", func(t *testing.T) {
		// A successful AutoFix on a check with RequiresReboot=true
		// MUST set Report.NeedsReboot. Multiple such fixes still
		// produce a single reboot (one prompt at the end of the
		// chain). MOK enrollment is the canonical case — install.sh
		// reads NeedsReboot and asks once.
		checks := []Check{{
			Name:           "needs_reboot",
			Severity:       SeverityBlocker,
			Detect:         func(context.Context) (bool, string) { return true, "" },
			AutoFix:        func(context.Context) error { return nil },
			RequiresReboot: true,
		}}
		p := &scriptedPrompter{answers: []PromptResponse{PromptYes}}
		report, err := Run(context.Background(), checks, Options{Interactive: true, Prompter: p})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !report.NeedsReboot {
			t.Fatalf("NeedsReboot=false; want true after successful fix")
		}
	})

	t.Run("RULE-PREFLIGHT-ORCH-06_skip_set_excludes_named_checks", func(t *testing.T) {
		// --skip is honoured even for triggered blockers — operator
		// override path. The skipped check produces a NotApplicable
		// result, never a blocker count, never an AutoFix call.
		fixCalls := 0
		checks := []Check{{
			Name:     "skippable",
			Severity: SeverityBlocker,
			Detect:   func(context.Context) (bool, string) { return true, "" },
			AutoFix:  func(context.Context) error { fixCalls++; return nil },
		}}
		report, err := Run(context.Background(), checks, Options{
			Interactive: true,
			Prompter:    &scriptedPrompter{},
			Skip:        map[string]bool{"skippable": true},
		})
		if err != nil {
			t.Fatalf("unexpected error with --skip: %v", err)
		}
		if fixCalls != 0 {
			t.Fatalf("AutoFix ran on skipped check")
		}
		if report.Results[0].NotApplicable != true {
			t.Fatalf("skipped check missing NotApplicable=true")
		}
	})

	t.Run("RULE-PREFLIGHT-ORCH-07_only_set_restricts_run", func(t *testing.T) {
		// --only restricts to the named set so an operator can re-
		// run a single check after fixing it manually. Anything
		// outside the set is dropped from the report entirely.
		called := map[string]int{}
		checks := []Check{
			{Name: "a", Severity: SeverityBlocker, Detect: func(context.Context) (bool, string) {
				called["a"]++
				return false, ""
			}},
			{Name: "b", Severity: SeverityBlocker, Detect: func(context.Context) (bool, string) {
				called["b"]++
				return false, ""
			}},
		}
		report, err := Run(context.Background(), checks, Options{
			Prompter: &scriptedPrompter{},
			Only:     map[string]bool{"b": true},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if called["a"] != 0 {
			t.Fatalf("a Detect ran despite --only=b")
		}
		if called["b"] != 1 {
			t.Fatalf("b Detect did not run")
		}
		if len(report.Results) != 1 || report.Results[0].Name != "b" {
			t.Fatalf("Results: got %+v, want only b", report.Results)
		}
	})

	t.Run("RULE-PREFLIGHT-ORCH-08_max_attempts_caps_retries", func(t *testing.T) {
		// MaxFixAttempts > 1 retries a transient AutoFix failure.
		// The cap MUST be honoured — an infinite-retry orchestrator
		// would hang on a permanently-failing fix.
		attempts := 0
		checks := []Check{{
			Name:     "flakey",
			Severity: SeverityBlocker,
			Detect:   func(context.Context) (bool, string) { return true, "" },
			AutoFix: func(context.Context) error {
				attempts++
				return errors.New("transient")
			},
		}}
		_, err := Run(context.Background(), checks, Options{
			Interactive:    true,
			Prompter:       &scriptedPrompter{answers: []PromptResponse{PromptYes}},
			MaxFixAttempts: 3,
		})
		if err == nil {
			t.Fatalf("expected error after exhausting retries")
		}
		if attempts != 3 {
			t.Fatalf("attempts: got %d, want 3", attempts)
		}
	})

	t.Run("RULE-PREFLIGHT-ORCH-09_warning_does_not_drive_fix_loop", func(t *testing.T) {
		// Severity=Warning produces a triggered Result but does NOT
		// trigger the Y/N prompt. Warnings are reported in the
		// summary; they don't gate the install.
		fixCalls := 0
		checks := []Check{{
			Name:     "warn",
			Severity: SeverityWarning,
			Detect:   func(context.Context) (bool, string) { return true, "" },
			AutoFix:  func(context.Context) error { fixCalls++; return nil },
		}}
		p := &scriptedPrompter{answers: []PromptResponse{PromptYes}}
		report, err := Run(context.Background(), checks, Options{Interactive: true, Prompter: p})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fixCalls != 0 {
			t.Fatalf("Warning AutoFix ran")
		}
		if len(p.asked) != 0 {
			t.Fatalf("Warning prompted: got %d", len(p.asked))
		}
		if report.WarningCount != 1 {
			t.Fatalf("WarningCount: got %d, want 1", report.WarningCount)
		}
	})

	t.Run("RULE-PREFLIGHT-ORCH-11_requires_reboot_skips_redetect", func(t *testing.T) {
		// AutoFix on a RequiresReboot check (canonical: mokutil
		// --import) only QUEUES the change for next-boot firmware
		// confirmation. The post-fix Detect would still report
		// triggered=true (mokutil --list-enrolled won't include the
		// queued import), so a generic re-detect-and-loop would
		// falsely treat the fix as failed and exhaust
		// MaxFixAttempts. The orchestrator MUST trust AutoFix's nil
		// return for these checks: skip the re-detect, mark
		// StillTriggered=false, set Report.NeedsReboot. Caught on
		// Phoenix's HIL desktop where mokutil --import correctly
		// queued enrollment but the test failed before this fix.
		var detectCalls int
		var fixCalls int
		checks := []Check{{
			Name:     "reboot_check",
			Severity: SeverityBlocker,
			Detect: func(context.Context) (bool, string) {
				detectCalls++
				return true, "still queued"
			},
			AutoFix:        func(context.Context) error { fixCalls++; return nil },
			RequiresReboot: true,
		}}
		p := &scriptedPrompter{answers: []PromptResponse{PromptYes}}
		report, err := Run(context.Background(), checks, Options{
			Interactive:    true,
			Prompter:       p,
			MaxFixAttempts: 3, // would normally retry until exhausted
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fixCalls != 1 {
			t.Fatalf("fixCalls: got %d, want 1 (RequiresReboot must not retry)", fixCalls)
		}
		if detectCalls != 1 {
			t.Fatalf("detectCalls: got %d, want 1 (initial only; no post-fix re-detect)", detectCalls)
		}
		if !report.NeedsReboot {
			t.Fatalf("NeedsReboot=false")
		}
		if report.BlockerCount != 0 {
			t.Fatalf("BlockerCount: got %d, want 0 (RequiresReboot fix is treated as cleared)", report.BlockerCount)
		}
	})

	t.Run("RULE-PREFLIGHT-ORCH-10_summary_groups_by_severity", func(t *testing.T) {
		// The pre-fix summary MUST list Blockers above Warnings
		// above Info. Operators read top-down; surfacing the most
		// urgent items first matches the install.sh narrative.
		checks := []Check{
			{Name: "low", Severity: SeverityInfo, Detect: func(context.Context) (bool, string) { return true, "" }},
			{Name: "mid", Severity: SeverityWarning, Detect: func(context.Context) (bool, string) { return true, "" }},
			{Name: "hi", Severity: SeverityBlocker, Detect: func(context.Context) (bool, string) { return true, "" }},
		}
		p := &scriptedPrompter{}
		_, _ = Run(context.Background(), checks, Options{Prompter: p})
		out := p.out.String()
		hi := strings.Index(out, "BLOCKER:")
		mid := strings.Index(out, "WARNING:")
		lo := strings.Index(out, "INFO:")
		if hi < 0 || mid < 0 || lo < 0 {
			t.Fatalf("missing one of BLOCKER/WARNING/INFO in summary:\n%s", out)
		}
		if hi >= mid || mid >= lo {
			t.Fatalf("severity ordering wrong (hi=%d mid=%d lo=%d):\n%s", hi, mid, lo, out)
		}
	})
}

func TestParseList(t *testing.T) {
	if got := ParseList(""); got != nil {
		t.Fatalf("empty: got %v, want nil", got)
	}
	got := ParseList("a, b ,c")
	if !got["a"] || !got["b"] || !got["c"] || len(got) != 3 {
		t.Fatalf("ParseList: %v", got)
	}
}
