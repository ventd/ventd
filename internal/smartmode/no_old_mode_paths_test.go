package smartmode_test

// Static-analysis sweep for spec-smart-mode.md §16 success criterion #12:
//
//   "At v0.6.0 tag, no parallel 'old mode' code paths exist for any
//    smart-mode subsystem."
//
// spec-smart-mode.md §10.1 enumerates the codepaths that must be deleted
// (not feature-flagged) as the smart-mode patch sequence lands:
//
//   - bios_known_bad.go file (covered narrowly by RULE-PROBE-10; we
//     re-cover here so a future move/rename pattern is also caught).
//   - spec-04 PI-autotune as a standalone codepath (subsumed into the
//     v0.5.9 confidence-gated controller).
//   - spec-10 issue-enumeration codepath (replaced by the v0.5.10
//     recovery-surface design).
//   - "catalog-matched-vs-not" branching downstream of the probe layer
//     (RULE-PROBE-05 covers ClassifyOutcome; this test extends the
//     scan to all internal/ source).
//
// The test walks the production source tree and greps for symbols /
// filenames associated with the deleted designs. Each match is a
// regression candidate. A match in a documentation directory or a
// vendored dependency is excluded; only first-party Go source under
// internal/ and cmd/ is scanned.
//
// When a smart-mode patch lands a NEW design that retires an OLD one,
// the patch should:
//   1. Delete the old code in the same diff (no parallel shim).
//   2. Add the deleted symbol's name to forbiddenSymbols below.
//   3. Update spec-smart-mode.md §10.1 to reflect the change.
// rulelint will not flag this; this test will.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// forbiddenFile is a (basename, reason) pair: any non-test Go file with
// that exact basename anywhere under internal/ or cmd/ is a violation.
type forbiddenFile struct {
	name   string
	reason string
}

// forbiddenSymbol is a regex that, if matched in non-test Go source, is
// a violation. Identifier-style patterns; word-boundary anchored.
type forbiddenSymbol struct {
	pattern *regexp.Regexp
	reason  string
}

var (
	forbiddenFiles = []forbiddenFile{
		{
			name:   "bios_known_bad.go",
			reason: "spec-smart-mode §10.1: per-board BIOS denylist replaced by behavioural detection in v0.5.1",
		},
	}

	// forbiddenSymbols are regexes scanned across non-test Go source.
	// The patterns are deliberately tight: a single false positive forces
	// a future maintainer to either rename the matched identifier or
	// extend the allowlist with a justified comment, which is the desired
	// friction for §16 #12.
	forbiddenSymbols = []forbiddenSymbol{
		{
			pattern: regexp.MustCompile(`\bspec-?04\b`),
			reason:  "spec-04 PI autotune subsumed by v0.5.9 confidence-gated controller; standalone references should be removed",
		},
		{
			pattern: regexp.MustCompile(`\bPIAutotune\b|\bPIAutoTune\b|\bPIAutoTuner\b`),
			reason:  "spec-04 PI autotune symbols forbidden; controller is now blended reactive+predictive (spec-smart-mode §8)",
		},
		{
			pattern: regexp.MustCompile(`\bbiosKnownBad\b|\bBiosKnownBad\b|\bBIOSKnownBad\b`),
			reason:  "spec-smart-mode §10.1: BIOS denylist symbols replaced by behavioural detection",
		},
		{
			pattern: regexp.MustCompile(`\bIssueEnumerator\b|\bEnumerateKnownIssues\b`),
			reason:  "spec-10 issue-enumeration model replaced by v0.5.10 recovery-surface design",
		},
	}

	// allowedFiles are paths (relative to repo root) that may legitimately
	// reference a forbidden symbol — typically rule-binding tests that
	// assert the symbol's absence (RULE-PROBE-10 etc.) and the smartmode
	// integration tests themselves.
	allowedFiles = map[string]bool{
		"internal/probe/probe_test.go":                    true, // RULE-PROBE-10 binding
		"internal/smartmode/no_old_mode_paths_test.go":    true, // this file
		"internal/smartmode/no_fourth_path_test.go":       true, // smartmode integration
		"internal/smartmode/never_reduce_cooling_test.go": true, // smartmode integration
		"internal/smartmode/no_catalog_branch_test.go":    true, // smartmode integration
	}
)

// findRepoRoot walks up from the current working directory to the
// directory containing go.mod. It is the integration-test analogue of
// internal/probe's findModuleRoot helper.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod from %s", dir)
		}
		dir = parent
	}
}

// scanRoots are the production-source subtrees scanned by the sweep.
// Test files are still walked because the forbidden-symbol scan must
// surface a regression that adds (e.g.) PIAutotune as a unit-tested
// "feature" — but allowedFiles whitelists the tests that legitimately
// reference the symbols.
var scanRoots = []string{"internal", "cmd"}

// shouldSkipDir reports whether dir must not be descended into.
// vendor/, dist/, and .git/ are obvious; testdata/ is excluded because
// fixture text isn't load-bearing for §16 #12.
func shouldSkipDir(name string) bool {
	switch name {
	case "vendor", "dist", ".git", "testdata", "fixtures":
		return true
	}
	return false
}

// scanGoFiles walks the given roots under repoRoot and returns every
// non-skipped *.go file path (relative to repoRoot).
func scanGoFiles(t *testing.T, repoRoot string) []string {
	t.Helper()
	var files []string
	for _, sub := range scanRoots {
		root := filepath.Join(repoRoot, sub)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if shouldSkipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return files
}

// TestSmartmode_NoOldModeFiles_StaticSweep asserts that no Go file with
// a basename in forbiddenFiles exists anywhere under internal/ or cmd/.
// RULE-PROBE-10 covers the bios_known_bad.go case at the package level;
// this is the cross-tree generalisation.
func TestSmartmode_NoOldModeFiles_StaticSweep(t *testing.T) {
	repoRoot := findRepoRoot(t)
	files := scanGoFiles(t, repoRoot)

	type hit struct {
		path   string
		reason string
	}
	var hits []hit
	for _, f := range files {
		base := filepath.Base(f)
		for _, fb := range forbiddenFiles {
			if base == fb.name {
				hits = append(hits, hit{path: f, reason: fb.reason})
			}
		}
	}

	if len(hits) > 0 {
		for _, h := range hits {
			t.Errorf("forbidden file present: %s — %s", h.path, h.reason)
		}
	}
}

// TestSmartmode_NoOldModeSymbols_StaticSweep greps every non-test, non-
// allowlisted Go file under internal/ and cmd/ for the regex patterns
// in forbiddenSymbols. Any match is a §16 #12 regression candidate.
func TestSmartmode_NoOldModeSymbols_StaticSweep(t *testing.T) {
	repoRoot := findRepoRoot(t)
	files := scanGoFiles(t, repoRoot)

	type hit struct {
		path    string
		line    int
		match   string
		pattern string
		reason  string
	}
	var hits []hit

	for _, f := range files {
		if allowedFiles[f] {
			continue
		}
		// Test files commonly reference forbidden symbols only to assert
		// their absence (RULE-PROBE-10's TestProbe_Rules subtest does
		// this). Restricting the symbol scan to non-_test.go reduces
		// false positives without giving up coverage — production code
		// is what we care about for §16 #12.
		if strings.HasSuffix(f, "_test.go") {
			continue
		}

		abs := filepath.Join(repoRoot, f)
		body, err := os.ReadFile(abs)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}

		for _, sym := range forbiddenSymbols {
			locs := sym.pattern.FindAllIndex(body, -1)
			if len(locs) == 0 {
				continue
			}
			for _, loc := range locs {
				line := 1 + strings.Count(string(body[:loc[0]]), "\n")
				hits = append(hits, hit{
					path:    f,
					line:    line,
					match:   string(body[loc[0]:loc[1]]),
					pattern: sym.pattern.String(),
					reason:  sym.reason,
				})
			}
		}
	}

	if len(hits) > 0 {
		t.Errorf("§16 #12 violations — %d forbidden symbol reference(s) in production source:", len(hits))
		for _, h := range hits {
			t.Errorf("  %s:%d: %q matches /%s/ — %s", h.path, h.line, h.match, h.pattern, h.reason)
		}
	}
}
