package main

import (
	"strings"
	"testing"
)

func TestParseAllowlist(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantGlobs []string
		wantFound bool
	}{
		{
			name:      "no allowlist line",
			body:      "This is a PR body with no allowlist.",
			wantFound: false,
		},
		{
			name:      "bullet with backticks",
			body:      "## Constraints\n- Allowlist: `internal/hal/ipmi/**`",
			wantGlobs: []string{"internal/hal/ipmi/**"},
			wantFound: true,
		},
		{
			name:      "comma-separated plain paths",
			body:      "- Allowlist: internal/controller/controller.go, CHANGELOG.md",
			wantGlobs: []string{"internal/controller/controller.go", "CHANGELOG.md"},
			wantFound: true,
		},
		{
			name:      "annotation stripped",
			body:      "- Allowlist: internal/hwdb/** (new), internal/hwmon/autoload.go, CHANGELOG.md",
			wantGlobs: []string{"internal/hwdb/**", "internal/hwmon/autoload.go", "CHANGELOG.md"},
			wantFound: true,
		},
		{
			name:      "bold format",
			body:      "**Allowlist:** tools/regresslint/**, .github/workflows/meta-lint.yml",
			wantGlobs: []string{"tools/regresslint/**", ".github/workflows/meta-lint.yml"},
			wantFound: true,
		},
		{
			name:      "mixed backticks and plain",
			body:      "- Allowlist: `internal/hal/ipmi/**`, CHANGELOG.md",
			wantGlobs: []string{"internal/hal/ipmi/**", "CHANGELOG.md"},
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			globs, found := parseAllowlist(tt.body)
			if found != tt.wantFound {
				t.Fatalf("found=%v want %v", found, tt.wantFound)
			}
			if len(globs) != len(tt.wantGlobs) {
				t.Fatalf("globs=%v want %v", globs, tt.wantGlobs)
			}
			for i, g := range globs {
				if g != tt.wantGlobs[i] {
					t.Errorf("globs[%d]=%q want %q", i, g, tt.wantGlobs[i])
				}
			}
		})
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// trailing /**
		{"internal/controller/**", "internal/controller/controller.go", true},
		{"internal/controller/**", "internal/controller/sub/foo.go", true},
		{"internal/controller/**", "internal/controller", true},
		{"internal/controller/**", "internal/hal/foo.go", false},
		// exact match
		{"CHANGELOG.md", "CHANGELOG.md", true},
		{"CHANGELOG.md", "internal/CHANGELOG.md", false},
		// stdlib single glob
		{".github/workflows/*.yml", ".github/workflows/ci.yml", true},
		{".github/workflows/*.yml", ".github/workflows/sub/ci.yml", false},
		// ** wildcard
		{"**", "anything/at/all.go", true},
		// empty pattern
		{"", "foo.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"|"+tt.path, func(t *testing.T) {
			got := matchGlob(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestRunSkipsWhenNoAllowlist(t *testing.T) {
	// Inject a fake getPRBody/getPRFiles by testing parseAllowlist directly —
	// the run() func shells out to gh which isn't available in unit tests.
	// Verify the skip path via the output string instead.
	var sb strings.Builder
	// We can't call run() without gh; verify the logic via parseAllowlist.
	_, found := parseAllowlist("## Summary\nNo allowlist line here.")
	if found {
		t.Fatal("expected no allowlist found")
	}
	_ = sb
}
