// Package preflight runs the iterative install-time precondition chain.
//
// The legacy hwmon.PreflightOOT returns the first blocker in a fixed
// order; this package wraps it with an orchestrator that:
//
//   - Runs every Check's Detect() so the operator sees the full picture,
//     not just the first thing that's wrong.
//   - Groups results by Severity (Blocker / Warning / Info) and only
//     refuses if any Blocker is unresolved.
//   - In interactive mode, prints PromptText for each Blocker that has
//     a non-nil AutoFix, asks Y/n, runs the fix on Y, re-detects to
//     verify, and loops until the channel is clean or the operator
//     aborts.
//   - Queues a single reboot at the end of the run when any successful
//     AutoFix sets RequiresReboot=true (e.g. MOK enrollment), so the
//     operator gets one reboot prompt rather than one per check.
//
// The Check definitions live in internal/preflight/checks/. Each check
// is a self-contained Detect/AutoFix pair plus a one-line rule binding;
// adding a new check is one file in checks/ plus a rule entry.
package preflight

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
)

// Severity orders checks by how blocking they are.
type Severity int

const (
	// SeverityInfo is logged but never refuses a run.
	SeverityInfo Severity = iota
	// SeverityWarning is reported in the summary but does not refuse.
	SeverityWarning
	// SeverityBlocker refuses the run unless AutoFix clears it.
	SeverityBlocker
)

func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityBlocker:
		return "blocker"
	}
	return "unknown"
}

// Check is one precondition the orchestrator tests. The contract is:
//
//   - Detect inspects the live system (read-only) and returns
//     (triggered, detail). triggered=false means the check passed.
//   - Explain renders detail as human-readable text shown above the
//     Y/N prompt.
//   - AutoFix is invoked when the operator confirms; nil means
//     "docs only" — no automation possible, the operator must act
//     out-of-band before re-running.
//   - PromptText is the one-line question shown before Y/n, e.g.
//     "Install kernel headers now?".
//   - DocURL is an optional link rendered as "see <url>" after Explain.
type Check struct {
	Name           string
	Severity       Severity
	Detect         func(context.Context) (triggered bool, detail string)
	Explain        func(detail string) string
	AutoFix        func(context.Context) error
	PromptText     string
	DocURL         string
	RequiresReboot bool
}

// Result captures one Check's outcome.
type Result struct {
	Name            string `json:"name"`
	Severity        string `json:"severity"`
	Triggered       bool   `json:"triggered"`
	Detail          string `json:"detail,omitempty"`
	Explanation     string `json:"explanation,omitempty"`
	DocURL          string `json:"doc_url,omitempty"`
	HasAutoFix      bool   `json:"has_auto_fix"`
	RequiresReboot  bool   `json:"requires_reboot,omitempty"`
	FixAttempted    bool   `json:"fix_attempted,omitempty"`
	FixError        string `json:"fix_error,omitempty"`
	StillTriggered  bool   `json:"still_triggered,omitempty"`
	NotApplicable   bool   `json:"not_applicable,omitempty"`
	NotApplicReason string `json:"not_applicable_reason,omitempty"`
}

// Report is the aggregate outcome of one orchestrator run.
type Report struct {
	SchemaVersion int      `json:"schema_version"`
	Results       []Result `json:"results"`
	BlockerCount  int      `json:"blocker_count"`
	WarningCount  int      `json:"warning_count"`
	NeedsReboot   bool     `json:"needs_reboot,omitempty"`
}

// SchemaVersion of the JSON output. Bump on a breaking change so
// install.sh and downstream consumers can detect drift.
const SchemaVersion = 1

// PromptResponse encodes the Y/n outcome.
type PromptResponse int

const (
	PromptYes PromptResponse = iota
	PromptNo
	PromptAbort
)

// Prompter abstracts the interactive surface so tests can drive Y/N
// scripts without a real TTY. The production Prompter reads stdin.
type Prompter interface {
	// AskYN shows question, awaits Y/n, returns the response. An empty
	// line maps to PromptYes (default-yes for autopilot ergonomics).
	// A "q" or EOF maps to PromptAbort.
	AskYN(question string) PromptResponse
	// Print writes operator-facing text. Implementation may colourise.
	Print(line string)
}

// IOPrompter reads from r and writes to w. Use NewStdPrompter to get
// the stdin/stdout-bound variant for the production CLI.
type IOPrompter struct {
	in  *bufio.Reader
	out io.Writer
}

// NewIOPrompter returns a prompter bound to the given streams.
func NewIOPrompter(in io.Reader, out io.Writer) *IOPrompter {
	return &IOPrompter{in: bufio.NewReader(in), out: out}
}

// NewStdPrompter binds to os.Stdin/os.Stdout.
func NewStdPrompter() *IOPrompter {
	return NewIOPrompter(os.Stdin, os.Stdout)
}

func (p *IOPrompter) AskYN(question string) PromptResponse {
	_, _ = fmt.Fprintf(p.out, "%s [Y/n] ", question)
	line, err := p.in.ReadString('\n')
	if err != nil {
		return PromptAbort
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	switch answer {
	case "", "y", "yes":
		return PromptYes
	case "n", "no":
		return PromptNo
	case "q", "quit", "abort":
		return PromptAbort
	}
	// Anything else: treat as no but don't abort. Let the operator
	// re-prompt by retrying the run — surprises map to "skip", not
	// "auto-yes".
	return PromptNo
}

func (p *IOPrompter) Print(line string) { _, _ = fmt.Fprintln(p.out, line) }

// AutoYesPrompter answers Yes to every question. Used by --auto-yes
// for non-interactive smoke tests; never wired into install.sh's
// curl-pipe-bash path (TTY check gates that).
type AutoYesPrompter struct{ Out io.Writer }

func (a *AutoYesPrompter) AskYN(q string) PromptResponse {
	if a.Out != nil {
		_, _ = fmt.Fprintf(a.Out, "%s [auto-yes]\n", q)
	}
	return PromptYes
}

func (a *AutoYesPrompter) Print(line string) {
	if a.Out != nil {
		_, _ = fmt.Fprintln(a.Out, line)
	}
}

// Options control orchestrator behaviour.
type Options struct {
	// Interactive enables the Y/N prompt loop. When false, the run is
	// detect-only: results are populated but no AutoFix is invoked.
	Interactive bool
	// MaxFixAttempts caps how many times the orchestrator will run a
	// single Check's AutoFix before giving up. A value of 0 means 1
	// attempt (the default).
	MaxFixAttempts int
	// Skip names checks that should be excluded from the run.
	Skip map[string]bool
	// Only, when non-empty, restricts the run to the named checks.
	Only map[string]bool
	// Prompter is the interactive surface; defaults to NewStdPrompter().
	Prompter Prompter
	// Logger receives detection / fix events at INFO; nil → discard.
	Logger *slog.Logger
}

// Run executes the orchestrator over checks and returns the aggregated
// report plus a single error that summarises whether any Blocker
// remains unresolved.
//
// Execution model:
//
//  1. Detect every Check in declaration order. Skipped checks emit a
//     NotApplicable result.
//  2. Render the summary so the operator sees the whole list before
//     any fix runs.
//  3. For each Triggered Blocker with a non-nil AutoFix, in interactive
//     mode: prompt, run the fix, re-detect. Re-detect failure or a No
//     answer leaves the Result.StillTriggered=true.
//  4. After the loop, if any successful fix had RequiresReboot, set
//     Report.NeedsReboot. Caller decides whether to surface a reboot
//     prompt (cmd/ventd/preflight.go does for the install path).
func Run(ctx context.Context, checks []Check, opts Options) (Report, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Prompter == nil {
		opts.Prompter = NewStdPrompter()
	}
	if opts.MaxFixAttempts <= 0 {
		opts.MaxFixAttempts = 1
	}

	report := Report{SchemaVersion: SchemaVersion}
	results := make([]Result, 0, len(checks))

	for _, c := range checks {
		if opts.Skip[c.Name] {
			results = append(results, Result{
				Name:            c.Name,
				Severity:        c.Severity.String(),
				NotApplicable:   true,
				NotApplicReason: "skipped by --skip",
				HasAutoFix:      c.AutoFix != nil,
				RequiresReboot:  c.RequiresReboot,
			})
			continue
		}
		if len(opts.Only) > 0 && !opts.Only[c.Name] {
			continue
		}

		r := Result{
			Name:           c.Name,
			Severity:       c.Severity.String(),
			HasAutoFix:     c.AutoFix != nil,
			RequiresReboot: c.RequiresReboot,
			DocURL:         c.DocURL,
		}
		triggered, detail := c.Detect(ctx)
		r.Triggered = triggered
		r.Detail = detail
		if triggered && c.Explain != nil {
			r.Explanation = c.Explain(detail)
		}
		opts.Logger.Info("preflight detect",
			"name", c.Name, "severity", c.Severity.String(),
			"triggered", triggered, "detail", detail)
		results = append(results, r)
	}

	// Summarise once, before any fix runs.
	renderSummary(opts.Prompter, results)

	if opts.Interactive {
		for i := range results {
			r := &results[i]
			c := findCheck(checks, r.Name)
			if c == nil || !r.Triggered || c.AutoFix == nil {
				continue
			}
			if c.Severity != SeverityBlocker {
				// Warnings and Info don't drive the fix loop — the
				// operator can re-run with --only <name> if they
				// want to address one.
				continue
			}
			if !runFixInteractive(ctx, *c, r, opts) {
				// Operator declined or aborted the chain; leave
				// remaining blockers unattempted.
				break
			}
		}
	}

	// Roll up counts.
	for _, r := range results {
		switch {
		case r.NotApplicable:
			continue
		case r.Severity == SeverityBlocker.String() && (r.Triggered || r.StillTriggered) && !fixCleared(r):
			report.BlockerCount++
		case r.Severity == SeverityWarning.String() && r.Triggered:
			report.WarningCount++
		}
		if r.FixAttempted && !r.StillTriggered && r.RequiresReboot {
			report.NeedsReboot = true
		}
	}
	report.Results = results

	if report.BlockerCount > 0 {
		return report, fmt.Errorf("preflight: %d blocker(s) remain", report.BlockerCount)
	}
	return report, nil
}

// fixCleared returns true when an AutoFix attempt successfully cleared
// a previously-triggered check. The post-fix Detect rerun in
// runFixInteractive sets r.StillTriggered=false and r.FixError="" on
// success; this helper centralises the predicate so the rollup loop
// stays readable.
func fixCleared(r Result) bool {
	return r.FixAttempted && !r.StillTriggered && r.FixError == ""
}

// runFixInteractive shows the operator the prompt, runs the fix on Y,
// re-detects, and updates r in place. Returns false when the operator
// aborts the entire chain.
func runFixInteractive(ctx context.Context, c Check, r *Result, opts Options) bool {
	question := c.PromptText
	if question == "" {
		question = fmt.Sprintf("Apply auto-fix for %q?", c.Name)
	}
	resp := opts.Prompter.AskYN(question)
	switch resp {
	case PromptAbort:
		opts.Prompter.Print("Aborted.")
		r.StillTriggered = true
		return false
	case PromptNo:
		opts.Prompter.Print(fmt.Sprintf("  → skipped %s; blocker remains.", c.Name))
		r.StillTriggered = true
		return true
	}

	for attempt := 1; attempt <= opts.MaxFixAttempts; attempt++ {
		r.FixAttempted = true
		opts.Logger.Info("preflight autofix start", "name", c.Name, "attempt", attempt)
		err := c.AutoFix(ctx)
		if err != nil {
			r.FixError = err.Error()
			opts.Logger.Warn("preflight autofix failed",
				"name", c.Name, "attempt", attempt, "err", err)
			opts.Prompter.Print(fmt.Sprintf("  ✗ auto-fix for %s failed: %v", c.Name, err))
			if attempt < opts.MaxFixAttempts {
				continue
			}
			r.StillTriggered = true
			return true
		}
		// RequiresReboot fixes (canonical case: mokutil --import for
		// MOK enrollment) take effect only at next boot — the
		// effect is observable by Detect only AFTER the firmware
		// MOK Manager confirmation. A re-detect here would always
		// report still-triggered and falsely treat the fix as
		// failed, looping until MaxFixAttempts. Trust the AutoFix's
		// nil return and let the end-of-run reboot prompt drive the
		// completion. The rollup loop reads RequiresReboot +
		// !StillTriggered to set Report.NeedsReboot.
		if c.RequiresReboot {
			r.StillTriggered = false
			r.FixError = ""
			opts.Prompter.Print(fmt.Sprintf("  ✓ %s queued — completes after reboot.", c.Name))
			return true
		}
		// Re-detect to verify the fix actually cleared the condition.
		triggered, detail := c.Detect(ctx)
		r.StillTriggered = triggered
		r.Detail = detail
		r.FixError = ""
		if !triggered {
			opts.Prompter.Print(fmt.Sprintf("  ✓ %s cleared.", c.Name))
			return true
		}
		opts.Logger.Warn("preflight autofix did not clear",
			"name", c.Name, "attempt", attempt, "detail", detail)
	}
	r.StillTriggered = true
	opts.Prompter.Print(fmt.Sprintf("  ✗ %s still triggered after auto-fix.", c.Name))
	return true
}

func renderSummary(p Prompter, results []Result) {
	bySev := map[Severity][]*Result{}
	for i := range results {
		r := &results[i]
		if r.NotApplicable {
			continue
		}
		if !r.Triggered {
			continue
		}
		var sev Severity
		switch r.Severity {
		case SeverityBlocker.String():
			sev = SeverityBlocker
		case SeverityWarning.String():
			sev = SeverityWarning
		default:
			sev = SeverityInfo
		}
		bySev[sev] = append(bySev[sev], r)
	}
	if len(bySev) == 0 {
		p.Print("Preflight: all checks passed.")
		return
	}
	for _, sev := range []Severity{SeverityBlocker, SeverityWarning, SeverityInfo} {
		rs := bySev[sev]
		if len(rs) == 0 {
			continue
		}
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].Name < rs[j].Name })
		p.Print("")
		p.Print(strings.ToUpper(sev.String()) + ":")
		for _, r := range rs {
			line := "  • " + r.Name
			if r.Explanation != "" {
				line += " — " + r.Explanation
			} else if r.Detail != "" {
				line += " — " + r.Detail
			}
			p.Print(line)
			if r.DocURL != "" {
				p.Print("    see " + r.DocURL)
			}
		}
	}
}

func findCheck(checks []Check, name string) *Check {
	for i := range checks {
		if checks[i].Name == name {
			return &checks[i]
		}
	}
	return nil
}

// ParseList splits a comma-separated list and returns a set; empty
// strings produce a nil set so callers can compare with len() == 0.
func ParseList(csv string) map[string]bool {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	out := map[string]bool{}
	for _, p := range strings.Split(csv, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out[p] = true
		}
	}
	return out
}
