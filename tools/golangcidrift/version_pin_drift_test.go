// Package golangcidrift hosts a single test that pins the three places
// the golangci-lint version is declared in this repo (CI workflow,
// install-git-hooks.sh, ci-local.sh hint) to the same value. Bumping
// one without bumping the others is the failure mode PR #1341 closed
// for the missing-binary case; this guards against bumping the version
// out of step.
package golangcidrift

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// pinSpec describes one file we expect to contain a golangci-lint
// version pin, and the regex that extracts it. Capturing group 1 must
// be the version string.
type pinSpec struct {
	relPath string
	pattern *regexp.Regexp
	// human-readable description used in failure messages; mentions
	// what to edit if this pin needs to move.
	context string
}

var pinSpecs = []pinSpec{
	{
		relPath: ".github/workflows/ci.yml",
		pattern: regexp.MustCompile(`go install github\.com/golangci/golangci-lint/v2/cmd/golangci-lint@(v\d+\.\d+\.\d+)`),
		context: "the canonical source; CI installs from here. Bump this first, then mirror to the script pins.",
	},
	{
		relPath: "scripts/install-git-hooks.sh",
		pattern: regexp.MustCompile(`GOLANGCI_LINT_VERSION="(v\d+\.\d+\.\d+)"`),
		context: "install-git-hooks.sh installs golangci-lint at this version on fresh clones. Must match ci.yml.",
	},
	{
		relPath: "scripts/ci-local.sh",
		pattern: regexp.MustCompile(`golangci-lint/v2/cmd/golangci-lint@(v\d+\.\d+\.\d+)`),
		context: "ci-local.sh prints this version in its install-hint when the binary is missing. Must match ci.yml.",
	},
}

func TestGolangciLintVersionPinsAgree(t *testing.T) {
	repoRoot := findRepoRoot(t)

	type found struct {
		spec    pinSpec
		version string
	}
	results := make([]found, 0, len(pinSpecs))
	for _, spec := range pinSpecs {
		path := filepath.Join(repoRoot, spec.relPath)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", spec.relPath, err)
		}
		match := spec.pattern.FindStringSubmatch(string(raw))
		if match == nil {
			t.Fatalf("no golangci-lint version pin matched in %s (regex: %s). %s",
				spec.relPath, spec.pattern, spec.context)
		}
		results = append(results, found{spec: spec, version: match[1]})
	}

	// All extracted versions must be identical.
	canonical := results[0].version
	for _, r := range results[1:] {
		if r.version != canonical {
			var b strings.Builder
			b.WriteString("golangci-lint version pins disagree across the repo:\n")
			for _, x := range results {
				b.WriteString("  " + x.spec.relPath + ": " + x.version + "\n")
			}
			b.WriteString("\nBump policy: edit .github/workflows/ci.yml first (the canonical source), then mirror to scripts/install-git-hooks.sh (GOLANGCI_LINT_VERSION) and scripts/ci-local.sh (install-hint string).")
			t.Fatal(b.String())
		}
	}
}

// findRepoRoot walks up from this test file's directory until it finds
// a go.mod, returning the repo root. Avoids hard-coding "../.." which
// would break if the test were ever moved.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("walked above filesystem root without finding go.mod")
		}
		dir = parent
	}
}
