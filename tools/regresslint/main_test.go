package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRegressLint(t *testing.T) {
	tests := []struct {
		name       string
		root       string
		issuesFile string
		strict     bool
		wantCode   int
		wantSubstr string
	}{
		{
			name:       "happy path — all closed bugs have regression tests",
			root:       "testdata/fixture_with_test",
			issuesFile: "testdata/fixture_with_test/issues.json",
			strict:     false,
			wantCode:   0,
			wantSubstr: "ok:",
		},
		{
			name:       "missing regression test",
			root:       "testdata/fixture_missing",
			issuesFile: "testdata/fixture_missing/issues.json",
			strict:     true,
			wantCode:   1,
			wantSubstr: "FAIL:",
		},
		{
			name:       "exempt — no-regression-test label skips check",
			root:       ".",
			issuesFile: "testdata/happy.json",
			strict:     false,
			wantCode:   0,
			wantSubstr: "exempt",
		},
		{
			name:       "malformed JSON input",
			root:       ".",
			issuesFile: "testdata/malformed.json",
			strict:     false,
			wantCode:   1,
			wantSubstr: "ERROR:",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			got := run(tc.root, tc.issuesFile, "", tc.strict, &buf)
			if got != tc.wantCode {
				t.Errorf("exit code: got %d, want %d\noutput:\n%s", got, tc.wantCode, buf.String())
			}
			if !strings.Contains(buf.String(), tc.wantSubstr) {
				t.Errorf("output %q does not contain %q", buf.String(), tc.wantSubstr)
			}
		})
	}
}

func TestMissingReport_ContainsActionHint(t *testing.T) {
	var buf bytes.Buffer
	code := run("testdata/fixture_missing", "testdata/fixture_missing/issues.json", "", true, &buf)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	out := buf.String()
	if !strings.Contains(out, "#99") {
		t.Errorf("report missing issue number: %q", out)
	}
	if !strings.Contains(out, "no-regression-test") {
		t.Errorf("report missing action hint: %q", out)
	}
	if !strings.Contains(out, "https://github.com/ventd/ventd/issues/99") {
		t.Errorf("report missing issue link: %q", out)
	}
}

func TestHasRegressionTest(t *testing.T) {
	tests := []struct {
		name      string
		root      string
		num       int
		wantFound bool
	}{
		{
			name:      "func declaration present",
			root:      "testdata/fixture_with_test",
			num:       42,
			wantFound: true,
		},
		{
			name:      "no matching test",
			root:      "testdata/fixture_missing",
			num:       99,
			wantFound: false,
		},
		{
			name:      "missing dir — no error, not found",
			root:      "testdata/fixture_missing",
			num:       42,
			wantFound: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := hasRegressionTest(tc.root, tc.num)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantFound {
				t.Errorf("hasRegressionTest(%q, %d) = %v, want %v", tc.root, tc.num, got, tc.wantFound)
			}
		})
	}
}
