# `tools/audit/ghost-code`

Mechanical sweep for ghost-code in the ventd module — methods declared with rule-bound tests but no production caller.

## Motivation

Issue #1033 (smart-mode Layer-B/C had no production data feed), #1035 (11 additional smart-mode wiring gaps), and #1037 (polarity wiring dead in production) are all the same class of bug: a method with test coverage and a rule binding but no production call site. The senior review at v0.5.26 missed all 13 because the rule catalogue + lifecycle wiring tests *looked* like proof of correctness. They aren't proof; they're documentation of intent.

This tool runs the mechanical sweep described in `docs/audits/2026-05-11/pass-1-callsite-sweep.md` as a runnable command. On every PR + release branch it surfaces any method whose only callers live in `_test.go` files. The allowlist captures the verified-fine ones (test fixtures, interface-dispatch satisfiers); anything not in the allowlist is a regression candidate.

## Usage

```bash
# Surface all candidates (human-readable)
go run ./tools/audit/ghost-code

# Machine-readable
go run ./tools/audit/ghost-code -json

# Fail CI on any new ghost (use after the allowlist is curated)
go run ./tools/audit/ghost-code -strict
```

## Output

```
ghost-code: scanned 449 exported method declarations across internal,cmd/
ghost-code: 70 zero-prod-caller methods before allowlist; 56 after
ghost-code: methods with no production caller (review for ghost-code class bugs):
  internal/confidence/aggregator/aggregator.go:158	Aggregator.SetDrift	(tests=1)
  ...
```

Each row is `<file:line>\t<Receiver.Method>\t(tests=<N>)`:

- `tests=0` — method has zero test callers either. Either a brand-new method, or a method nobody uses anywhere. Highest signal.
- `tests>0` — method is exercised in tests, but no production call site reaches it. Same class as #1033: the test verifies the function works when called; nothing in production calls it.

## Allowlist

`allowlist.txt` is one `Receiver.Method` per line, `#` comments and blank lines OK. The allowlist is for entries the audit team has verified are NOT ghost-code class bugs:

1. **Test fixture** — type is test scaffolding (the regex grep can't see test callers because it filters by filename).
2. **Interface dispatch** — method satisfies an interface that the production code calls via the interface type. The regex grep doesn't traverse interface dispatch.

**Don't allowlist a method just because it's "probably called somewhere."** If you're unsure, leave it flagged. The whole point of the tool is to surface ambiguity for human review. The allowlist is narrow on purpose; broad entries silently bring back the failure mode this tool exists to catch.

When adding an entry, include a comment with the verification rationale: `Clock.Advance  # faketime fake clock`. When removing an entry, the method should now be reached from production — run the tool to confirm the post-allowlist count decreased by 1.

## Limitations

The tool uses `go/ast` to enumerate method declarations and a regex pattern (`\.Method\(`) to count call sites. It does NOT use SSA-based call-graph analysis — that would catch interface-dispatch automatically but is significantly slower and a larger maintenance surface.

False-positive classes it can't distinguish without the allowlist:

- **Interface dispatch**: `iface.Method()` looks identical to `concreteType.Method()` in the regex.
- **Function values**: `f := obj.Method; f(args)` — the bare reference isn't followed by `(`, so the call is uncountable.
- **Reflection-driven methods**: encoding/json's MarshalJSON, fmt's String, errors.Unwrap. Hard-coded list in `isFalsePositiveName`.

A future audit pass can build a real call graph if these blind spots start producing too many escapes. For now the mechanical sweep + curated allowlist catches the regression class that motivated the tool.

## Adding to CI

Add to `.github/workflows/`:

```yaml
- name: ghost-code audit
  run: go run ./tools/audit/ghost-code -strict
```

The `-strict` flag exits non-zero when any non-allowlisted candidate is found. Push-time enforcement is the design intent — making a method reachable should be part of the PR that adds the method.

## When to re-run manually

- After any PR that adds new exported methods on a `Runtime` / `Manager` / `Server` / `Backend` type.
- Before tagging any release. The pre-release checklist gains a one-line `go run ./tools/audit/ghost-code` step.
- When investigating "feature X doesn't seem to work in production" reports — the first question becomes "is the method that should fire for feature X actually called from anywhere?"
