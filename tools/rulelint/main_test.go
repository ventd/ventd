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
