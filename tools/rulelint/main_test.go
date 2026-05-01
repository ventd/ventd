package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testRoot(name string) string {
	return filepath.Join("testdata", name)
}

// stageFixture builds a minimal repo root in t.TempDir().
// rulesContent is written to .claude/rules/example.md.
// If testContent is non-empty it is written to pkg/somefile_test.go.
func stageFixture(t *testing.T, rulesContent, testContent string) string {
	t.Helper()
	root := t.TempDir()
	rulesDir := filepath.Join(root, ".claude", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "example.md"), []byte(rulesContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if testContent != "" {
		pkgDir := filepath.Join(root, "pkg")
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "somefile_test.go"), []byte(testContent), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRulelint_ForwardHappy(t *testing.T) {
	var buf strings.Builder
	code := run(testRoot("happy"), &buf)
	out := buf.String()
	if code != 0 {
		t.Fatalf("exit %d; output:\n%s", code, out)
	}
	if !strings.Contains(out, "ok:") {
		t.Errorf("expected ok summary; got: %q", out)
	}
}

func TestRulelint_MissingFile(t *testing.T) {
	var buf strings.Builder
	code := run(testRoot("missing_file"), &buf)
	out := buf.String()
	if code != 1 {
		t.Fatalf("exit %d, want 1; output:\n%s", code, out)
	}
	if !strings.Contains(out, "bound file not found") {
		t.Errorf("expected 'bound file not found' in output; got: %q", out)
	}
}

func TestRulelint_MissingSubtest(t *testing.T) {
	var buf strings.Builder
	code := run(testRoot("missing_subtest"), &buf)
	out := buf.String()
	if code != 1 {
		t.Fatalf("exit %d, want 1; output:\n%s", code, out)
	}
	if !strings.Contains(out, "not found in") {
		t.Errorf("expected 'not found in' in output; got: %q", out)
	}
}

func TestRulelint_MalformedBound(t *testing.T) {
	var buf strings.Builder
	code := run(testRoot("malformed_bound"), &buf)
	out := buf.String()
	if code != 1 {
		t.Fatalf("exit %d, want 1; output:\n%s", code, out)
	}
	if !strings.Contains(out, "malformed Bound") {
		t.Errorf("expected 'malformed Bound' in output; got: %q", out)
	}
}

func TestRulelint_ReverseWarn(t *testing.T) {
	var buf strings.Builder
	code := run(testRoot("reverse_warn"), &buf)
	out := buf.String()
	if code != 0 {
		t.Fatalf("exit %d, want 0 (reverse warn must not fail); output:\n%s", code, out)
	}
	if !strings.Contains(out, "WARN:") {
		t.Errorf("expected WARN in output; got: %q", out)
	}
	if !strings.Contains(out, "stop_disabled") {
		t.Errorf("expected unclaimed subtest 'stop_disabled' in WARN; got: %q", out)
	}
}

// --- allow-orphan tests -------------------------------------------------------

func TestRulelint_AllowOrphan_MissingFile(t *testing.T) {
	root := stageFixture(t, `
## RULE-CLAMP-01: PWM writes are clamped to [min_pwm, max_pwm]

Bound: pkg/nonexistent_test.go:clamp_below_min
<!-- rulelint:allow-orphan -->
`, "")
	var buf strings.Builder
	code := run(root, &buf)
	out := buf.String()
	if code != 0 {
		t.Fatalf("exit %d, want 0 (marked orphan must not error); output:\n%s", code, out)
	}
	if strings.Contains(out, "ERROR") {
		t.Errorf("unexpected ERROR in output: %q", out)
	}
}

func TestRulelint_AllowOrphan_MissingSubtest(t *testing.T) {
	root := stageFixture(t, `
## RULE-CLAMP-01: PWM writes are clamped to [min_pwm, max_pwm]

Bound: pkg/somefile_test.go:nonexistent_subtest
<!-- rulelint:allow-orphan -->
`, `package pkg

import "testing"

func TestSafety(t *testing.T) {
	t.Run("other_subtest", func(t *testing.T) {})
}
`)
	var buf strings.Builder
	code := run(root, &buf)
	out := buf.String()
	if code != 0 {
		t.Fatalf("exit %d, want 0 (marked orphan must not error); output:\n%s", code, out)
	}
	if strings.Contains(out, "ERROR") {
		t.Errorf("unexpected ERROR in output: %q", out)
	}
}

func TestRulelint_AllowOrphan_MalformedBound(t *testing.T) {
	// A malformed Bound: line followed by the marker still produces a parse error.
	// The marker is not attached (no valid boundEntry was created) so it is ignored.
	root := stageFixture(t, `
## RULE-CLAMP-01: PWM writes are clamped to [min_pwm, max_pwm]

Bound: no-colon-here
<!-- rulelint:allow-orphan -->
`, "")
	var buf strings.Builder
	code := run(root, &buf)
	out := buf.String()
	if code != 1 {
		t.Fatalf("exit %d, want 1 (malformed Bound must still error); output:\n%s", code, out)
	}
	if !strings.Contains(out, "malformed Bound") {
		t.Errorf("expected 'malformed Bound' in output; got: %q", out)
	}
}

func TestRulelint_AllowOrphan_WrongPosition(t *testing.T) {
	// Marker separated from the Bound: line by a blank line is not recognised.
	// The missing file therefore still produces an error.
	root := stageFixture(t, `
## RULE-CLAMP-01: PWM writes are clamped to [min_pwm, max_pwm]

Bound: pkg/nonexistent_test.go:clamp_below_min

<!-- rulelint:allow-orphan -->
`, "")
	var buf strings.Builder
	code := run(root, &buf)
	out := buf.String()
	if code != 1 {
		t.Fatalf("exit %d, want 1 (misplaced marker must be ignored); output:\n%s", code, out)
	}
	if !strings.Contains(out, "bound file not found") {
		t.Errorf("expected 'bound file not found' in output; got: %q", out)
	}
}

func TestRulelint_AllowOrphan_Mixed(t *testing.T) {
	// 3 marked rules + 3 unmarked rules, all bound to a missing file → exactly 3 errors.
	root := stageFixture(t, `
## RULE-A-01: first marked rule

Bound: pkg/nonexistent_test.go:subtest_a1
<!-- rulelint:allow-orphan -->

## RULE-B-01: first unmarked rule

Bound: pkg/nonexistent_test.go:subtest_b1

## RULE-A-02: second marked rule

Bound: pkg/nonexistent_test.go:subtest_a2
<!-- rulelint:allow-orphan -->

## RULE-B-02: second unmarked rule

Bound: pkg/nonexistent_test.go:subtest_b2

## RULE-A-03: third marked rule

Bound: pkg/nonexistent_test.go:subtest_a3
<!-- rulelint:allow-orphan -->

## RULE-B-03: third unmarked rule

Bound: pkg/nonexistent_test.go:subtest_b3
`, "")
	var buf strings.Builder
	code := run(root, &buf)
	out := buf.String()
	if code != 1 {
		t.Fatalf("exit %d, want 1; output:\n%s", code, out)
	}
	errorCount := strings.Count(out, "ERROR:")
	if errorCount != 3 {
		t.Errorf("want exactly 3 ERROR lines, got %d; output:\n%s", errorCount, out)
	}
}

func TestRulelint_AllowOrphan_Resolved(t *testing.T) {
	// Marker present but the binding target exists and the subtest is found →
	// rulelint errors so the impl PR is reminded to remove the marker.
	root := stageFixture(t, `
## RULE-CLAMP-01: PWM writes are clamped to [min_pwm, max_pwm]

Bound: pkg/somefile_test.go:clamp_below_min
<!-- rulelint:allow-orphan -->
`, `package pkg

import "testing"

func TestSafety(t *testing.T) {
	t.Run("clamp_below_min", func(t *testing.T) {})
}
`)
	var buf strings.Builder
	code := run(root, &buf)
	out := buf.String()
	if code != 1 {
		t.Fatalf("exit %d, want 1 (stale marker must error); output:\n%s", code, out)
	}
	if !strings.Contains(out, "allow-orphan marker present but binding is already resolved") {
		t.Errorf("expected stale-marker message in output; got: %q", out)
	}
}

// --- --suggest tests ---------------------------------------------------------

func TestRulelint_Suggest_NearMissTransposition(t *testing.T) {
	// Rule binds to a typo'd subtest name (one transposition away from the
	// real subtest). With --suggest, rulelint must emit a "did you mean" hint
	// pointing at the real name.
	root := stageFixture(t, `
## RULE-CLAMP-01: PWM clamp invariants
Bound: pkg/somefile_test.go:TestEventFlags_Bits0Through12Lokced
`, `package pkg

import "testing"

func TestEventFlags_Bits0Through12Locked(t *testing.T) {}
`)
	var buf strings.Builder
	code := runWithOptions(root, &buf, runOptions{suggest: true})
	out := buf.String()
	if code != 1 {
		t.Fatalf("exit %d, want 1; output:\n%s", code, out)
	}
	if !strings.Contains(out, `did you mean "TestEventFlags_Bits0Through12Locked"`) {
		t.Errorf("expected 'did you mean' suggestion for near-miss; got: %q", out)
	}
}

func TestRulelint_Suggest_FarMissNoSuggestion(t *testing.T) {
	// Rule binds to a name that is not close to any real subtest. With
	// --suggest, rulelint should NOT add a suggestion (would be misleading).
	root := stageFixture(t, `
## RULE-CLAMP-01: PWM clamp invariants
Bound: pkg/somefile_test.go:totally_different_concept_xyz
`, `package pkg

import "testing"

func TestSafety(t *testing.T) {
	t.Run("clamp_below_min", func(t *testing.T) {})
}
`)
	var buf strings.Builder
	code := runWithOptions(root, &buf, runOptions{suggest: true})
	out := buf.String()
	if code != 1 {
		t.Fatalf("exit %d, want 1; output:\n%s", code, out)
	}
	if strings.Contains(out, "did you mean") {
		t.Errorf("did not expect a suggestion for far miss; got: %q", out)
	}
}

func TestRulelint_Suggest_OffByDefault(t *testing.T) {
	// Without --suggest, even a near-miss must NOT carry a hint.
	root := stageFixture(t, `
## RULE-CLAMP-01: PWM clamp invariants
Bound: pkg/somefile_test.go:TestFooBat
`, `package pkg

import "testing"

func TestFooBar(t *testing.T) {}
`)
	var buf strings.Builder
	code := runWithOptions(root, &buf, runOptions{suggest: false})
	out := buf.String()
	if code != 1 {
		t.Fatalf("exit %d, want 1; output:\n%s", code, out)
	}
	if strings.Contains(out, "did you mean") {
		t.Errorf("suggestion leaked when --suggest is off; got: %q", out)
	}
}

// --- --check-binding-uniqueness tests ----------------------------------------

func TestRulelint_BindingUniqueness_DuplicateFails(t *testing.T) {
	// Two rules bind to the same file:subtest. With --check-binding-uniqueness,
	// rulelint must fail and name both rule IDs.
	root := stageFixture(t, `
## RULE-A-01: first rule
Bound: pkg/somefile_test.go:shared_subtest

## RULE-A-02: second rule
Bound: pkg/somefile_test.go:shared_subtest
`, `package pkg

import "testing"

func TestSafety(t *testing.T) {
	t.Run("shared_subtest", func(t *testing.T) {})
}
`)
	var buf strings.Builder
	code := runWithOptions(root, &buf, runOptions{uniqueBindings: true})
	out := buf.String()
	if code != 1 {
		t.Fatalf("exit %d, want 1; output:\n%s", code, out)
	}
	if !strings.Contains(out, "duplicate binding") {
		t.Errorf("expected 'duplicate binding' in output; got: %q", out)
	}
	if !strings.Contains(out, "RULE-A-01") || !strings.Contains(out, "RULE-A-02") {
		t.Errorf("expected both rule IDs in duplicate diagnostic; got: %q", out)
	}
}

func TestRulelint_BindingUniqueness_OffByDefault(t *testing.T) {
	// Same fixture as above, but without --check-binding-uniqueness, no error.
	root := stageFixture(t, `
## RULE-A-01: first rule
Bound: pkg/somefile_test.go:shared_subtest

## RULE-A-02: second rule
Bound: pkg/somefile_test.go:shared_subtest
`, `package pkg

import "testing"

func TestSafety(t *testing.T) {
	t.Run("shared_subtest", func(t *testing.T) {})
}
`)
	var buf strings.Builder
	code := runWithOptions(root, &buf, runOptions{uniqueBindings: false})
	out := buf.String()
	if code != 0 {
		t.Fatalf("exit %d, want 0 (uniqueness check off); output:\n%s", code, out)
	}
}

func TestRulelint_BindingUniqueness_DistinctOK(t *testing.T) {
	// Two rules bound to DIFFERENT subtests in the same file pass
	// --check-binding-uniqueness.
	root := stageFixture(t, `
## RULE-A-01: first rule
Bound: pkg/somefile_test.go:subtest_a

## RULE-A-02: second rule
Bound: pkg/somefile_test.go:subtest_b
`, `package pkg

import "testing"

func TestSafety(t *testing.T) {
	t.Run("subtest_a", func(t *testing.T) {})
	t.Run("subtest_b", func(t *testing.T) {})
}
`)
	var buf strings.Builder
	code := runWithOptions(root, &buf, runOptions{uniqueBindings: true})
	out := buf.String()
	if code != 0 {
		t.Fatalf("exit %d, want 0 (distinct subtests OK); output:\n%s", code, out)
	}
}
