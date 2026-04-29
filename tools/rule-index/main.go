// rule-index walks .claude/rules/*.md and emits .claude/RULE-INDEX.md.
// Run with --check to exit 1 when the index is stale (used in CI).
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	defaultRulesDir  = ".claude/rules"
	defaultIndexPath = ".claude/RULE-INDEX.md"
)

type ruleEntry struct {
	filename  string
	id        string
	family    string
	summary   string
	boundTest string
}

type freeFormEntry struct {
	filename string
	title    string
}

func main() {
	check := flag.Bool("check", false, "exit 1 if index is stale")
	rulesDir := flag.String("rules", defaultRulesDir, "rules directory")
	indexOut := flag.String("out", defaultIndexPath, "output path")
	flag.Parse()

	rules, freeForm, err := collect(*rulesDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rule-index:", err)
		os.Exit(1)
	}

	var buf bytes.Buffer
	emit(&buf, rules, freeForm)

	if *check {
		existing, readErr := os.ReadFile(*indexOut)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "rule-index: cannot read %s: %v\n", *indexOut, readErr)
			os.Exit(1)
		}
		if !bytes.Equal(existing, buf.Bytes()) {
			fmt.Fprintf(os.Stderr, "rule-index: %s is stale; run: go run ./tools/rule-index\n", *indexOut)
			os.Exit(1)
		}
		return
	}

	if err := os.WriteFile(*indexOut, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "rule-index: cannot write %s: %v\n", *indexOut, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes, %d families)\n", *indexOut, buf.Len(), countFamilies(rules))
}

// collect reads all .md files in dir and classifies them as single-rule or free-form.
func collect(dir string) ([]ruleEntry, []freeFormEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	var rules []ruleEntry
	var freeForm []freeFormEntry

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if e.Name() == "README.md" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		r, ff, parseErr := parseFile(e.Name(), path)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", e.Name(), parseErr)
		}
		if r != nil {
			rules = append(rules, *r)
		} else if ff != nil {
			freeForm = append(freeForm, *ff)
		}
	}

	// deterministic order: family then filename for rules, filename for free-form
	slices.SortFunc(rules, func(a, b ruleEntry) int {
		if a.family != b.family {
			return strings.Compare(a.family, b.family)
		}
		return strings.Compare(a.filename, b.filename)
	})
	slices.SortFunc(freeForm, func(a, b freeFormEntry) int {
		return strings.Compare(a.filename, b.filename)
	})

	return rules, freeForm, nil
}

// parseFile returns either a ruleEntry (first heading is "# RULE-...") or a
// freeFormEntry (any other heading). README.md callers skip before calling.
func parseFile(name, path string) (*ruleEntry, *freeFormEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)

	// locate the first level-1 heading
	firstHeading := ""
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "# ") {
			firstHeading = strings.TrimSpace(line[2:])
			break
		}
	}

	if !strings.HasPrefix(firstHeading, "RULE-") {
		return nil, &freeFormEntry{filename: name, title: firstHeading}, nil
	}

	// single-rule file: "RULE-ID: Summary text"
	id, summary := splitRuleHeading(firstHeading)

	// find first Bound: line
	boundTest := ""
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "Bound: ") {
			rest := line[7:]
			if idx := strings.Index(rest, ":"); idx >= 0 {
				boundTest = rest[idx+1:]
			}
			break
		}
	}

	return &ruleEntry{
		filename:  name,
		id:        id,
		family:    extractFamily(id),
		summary:   summary,
		boundTest: boundTest,
	}, nil, nil
}

// splitRuleHeading parses "RULE-X-Y: Summary text" into (id, summary).
// Summary is truncated to 120 chars with "..." if longer.
func splitRuleHeading(heading string) (id, summary string) {
	idx := strings.Index(heading, ": ")
	if idx < 0 {
		return heading, ""
	}
	id = heading[:idx]
	summary = heading[idx+2:]
	if len(summary) > 120 {
		summary = summary[:117] + "..."
	}
	return id, summary
}

// extractFamily returns the first dash-component after "RULE-".
// "RULE-PROBE-01"         → "PROBE"
// "RULE-CALIB-PR2B-01"   → "CALIB"
// "RULE-EXPERIMENTAL-..." → "EXPERIMENTAL"
func extractFamily(id string) string {
	s := strings.TrimPrefix(id, "RULE-")
	if idx := strings.Index(s, "-"); idx >= 0 {
		return s[:idx]
	}
	return s
}

func countFamilies(rules []ruleEntry) int {
	seen := make(map[string]bool)
	for _, r := range rules {
		seen[r.family] = true
	}
	return len(seen)
}

func emit(buf *bytes.Buffer, rules []ruleEntry, freeForm []freeFormEntry) {
	fmt.Fprint(buf, "# Rule Index\n\n")
	fmt.Fprint(buf, "Canonical map of `.claude/rules/*.md`.\n")
	fmt.Fprint(buf, "Read this file first; open a specific rule file only when the full text is needed.\n")
	fmt.Fprint(buf, "Regenerate with: `go run ./tools/rule-index`\n\n")
	fmt.Fprint(buf, "---\n\n")

	// group single-rule entries by family
	families := make(map[string][]ruleEntry)
	for _, r := range rules {
		families[r.family] = append(families[r.family], r)
	}
	familyNames := make([]string, 0, len(families))
	for k := range families {
		familyNames = append(familyNames, k)
	}
	slices.Sort(familyNames)

	for _, fam := range familyNames {
		members := families[fam]
		fmt.Fprintf(buf, "## RULE-%s\n\n", fam)
		fmt.Fprint(buf, "| File | Bound subtest | Summary |\n")
		fmt.Fprint(buf, "|------|---------------|---------|\n")
		for _, r := range members {
			fmt.Fprintf(buf, "| %s | %s | %s |\n", r.filename, r.boundTest, r.summary)
		}
		fmt.Fprint(buf, "\n")
	}

	// free-form files (multi-rule or policy docs)
	if len(freeForm) > 0 {
		fmt.Fprint(buf, "## Free-form files\n\n")
		fmt.Fprint(buf, "| File | Summary |\n")
		fmt.Fprint(buf, "|------|---------|\n")
		for _, ff := range freeForm {
			fmt.Fprintf(buf, "| %s | %s |\n", ff.filename, ff.title)
		}
		fmt.Fprint(buf, "\n")
	}
}
