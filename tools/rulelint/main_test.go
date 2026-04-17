package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func testRoot(name string) string {
	return filepath.Join("testdata", name)
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
