package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var tRunRE = regexp.MustCompile(`t\.Run\("([^"]+)"`)

type ruleEntry struct {
	file   string
	id     string
	desc   string
	bounds []boundEntry
}

type boundEntry struct {
	targetFile  string
	subtestName string
	allowOrphan bool // set by <!-- rulelint:allow-orphan --> on the line after Bound:
}

// parseRulesDir walks dir/*.md and collects all rule entries.
// Files with no ## RULE- headings are silently skipped.
func parseRulesDir(dir string) ([]ruleEntry, []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []string{fmt.Sprintf("cannot read rules dir %s: %v", dir, err)}
	}

	var rules []ruleEntry
	var errs []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		r, e2 := parseRuleFile(filepath.Join(dir, e.Name()))
		errs = append(errs, e2...)
		rules = append(rules, r...)
	}
	return rules, errs
}

func parseRuleFile(path string) ([]ruleEntry, []string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, []string{fmt.Sprintf("cannot open %s: %v", path, err)}
	}
	defer func() { _ = f.Close() }()

	// ruleHeadingRE matches ## RULE-<ID>: <invariant>
	ruleHeadingRE := regexp.MustCompile(`^## RULE-(\S+):\s+(.+)$`)

	var rules []ruleEntry
	var errs []string
	curIdx := -1

	// lastBound tracks the indices of the most recently parsed valid Bound: entry
	// so the allow-orphan marker on the very next line can be attached to it.
	lastBoundRule := -1
	lastBoundEntry := -1

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()

		// Allow-orphan marker: must appear on the line DIRECTLY after a Bound: line.
		if lastBoundRule >= 0 && line == "<!-- rulelint:allow-orphan -->" {
			rules[lastBoundRule].bounds[lastBoundEntry].allowOrphan = true
			lastBoundRule = -1
			continue
		}
		// Any other line resets the "last was Bound:" state.
		lastBoundRule = -1

		if strings.HasPrefix(line, "## ") {
			if m := ruleHeadingRE.FindStringSubmatch(line); m != nil {
				rules = append(rules, ruleEntry{file: path, id: m[1], desc: m[2]})
				curIdx = len(rules) - 1
			} else {
				curIdx = -1
			}
			continue
		}

		if curIdx < 0 {
			continue
		}

		if rest, ok := strings.CutPrefix(line, "Bound:"); ok {
			raw := strings.TrimSpace(rest)
			if raw == "" {
				errs = append(errs, fmt.Sprintf(
					"%s: RULE-%s: malformed Bound line (empty value): %q",
					path, rules[curIdx].id, line,
				))
				continue
			}
			idx := strings.IndexByte(raw, ':')
			if idx < 0 {
				errs = append(errs, fmt.Sprintf(
					"%s: RULE-%s: malformed Bound line (no colon separator): %q",
					path, rules[curIdx].id, line,
				))
				continue
			}
			targetFile := raw[:idx]
			subtestName := raw[idx+1:]
			if targetFile == "" || subtestName == "" {
				errs = append(errs, fmt.Sprintf(
					"%s: RULE-%s: malformed Bound line (empty file or subtest): %q",
					path, rules[curIdx].id, line,
				))
				continue
			}
			rules[curIdx].bounds = append(rules[curIdx].bounds, boundEntry{
				targetFile:  targetFile,
				subtestName: subtestName,
			})
			// Arm the marker detector for the next line.
			lastBoundRule = curIdx
			lastBoundEntry = len(rules[curIdx].bounds) - 1
		}
	}
	if err := sc.Err(); err != nil {
		errs = append(errs, fmt.Sprintf("scanning %s: %v", path, err))
	}
	return rules, errs
}

// containsSubtest reports whether path contains a top-level func named name
// or a t.Run("<name>", ...) literal.
func containsSubtest(path, name string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	tRunNeedle := fmt.Sprintf(`t.Run("%s"`, name)
	funcNeedle := fmt.Sprintf("func %s(", name)

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, tRunNeedle) {
			return true, nil
		}
		if strings.HasPrefix(strings.TrimSpace(line), funcNeedle) {
			return true, nil
		}
	}
	return false, sc.Err()
}

// enumerateSubtests returns all t.Run literal names found in path.
func enumerateSubtests(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		for _, m := range tRunRE.FindAllStringSubmatch(sc.Text(), -1) {
			names = append(names, m[1])
		}
	}
	return names, sc.Err()
}

// runOptions configures non-default rulelint behaviour for run().
type runOptions struct {
	suggest        bool // --suggest: print Levenshtein suggestion on missing-subtest errors
	uniqueBindings bool // --check-binding-uniqueness: assert no two rules bind to the same file:subtest
}

func run(root string, w io.Writer) int {
	return runWithOptions(root, w, runOptions{})
}

func runWithOptions(root string, w io.Writer, opts runOptions) int {
	rulesDir := filepath.Join(root, ".claude", "rules")
	rules, parseErrs := parseRulesDir(rulesDir)

	var forwardErrs []string
	claimedByFile := make(map[string]map[string]bool)

	// bindingOwner maps "<file>:<subtest>" → first rule ID that bound it.
	// Populated only when opts.uniqueBindings is true.
	bindingOwner := make(map[string]string)

	for _, r := range rules {
		for _, b := range r.bounds {
			absPath := filepath.Join(root, b.targetFile)
			if _, err := os.Stat(absPath); os.IsNotExist(err) {
				if !b.allowOrphan {
					forwardErrs = append(forwardErrs, fmt.Sprintf(
						"%s: RULE-%s: bound file not found: %s",
						r.file, r.id, b.targetFile,
					))
				}
				continue
			}
			found, err := containsSubtest(absPath, b.subtestName)
			if err != nil {
				forwardErrs = append(forwardErrs, fmt.Sprintf(
					"%s: RULE-%s: error reading %s: %v",
					r.file, r.id, b.targetFile, err,
				))
				continue
			}
			if !found {
				if !b.allowOrphan {
					msg := fmt.Sprintf(
						"%s: RULE-%s: subtest %q not found in %s",
						r.file, r.id, b.subtestName, b.targetFile,
					)
					if opts.suggest {
						if hint := suggestSubtest(absPath, b.subtestName); hint != "" {
							msg += fmt.Sprintf(" (did you mean %q?)", hint)
						}
					}
					forwardErrs = append(forwardErrs, msg)
				}
				continue
			}
			// File exists and subtest is present. If the allow-orphan marker is
			// still present, the impl PR forgot to remove it.
			if b.allowOrphan {
				forwardErrs = append(forwardErrs, fmt.Sprintf(
					"%s: RULE-%s: allow-orphan marker present but binding is already resolved: %s:%s",
					r.file, r.id, b.targetFile, b.subtestName,
				))
				continue
			}
			if claimedByFile[b.targetFile] == nil {
				claimedByFile[b.targetFile] = make(map[string]bool)
			}
			claimedByFile[b.targetFile][b.subtestName] = true

			if opts.uniqueBindings {
				key := b.targetFile + ":" + b.subtestName
				if prior, ok := bindingOwner[key]; ok {
					forwardErrs = append(forwardErrs, fmt.Sprintf(
						"RULE-%s: duplicate binding to %s — already claimed by RULE-%s",
						r.id, key, prior,
					))
				} else {
					bindingOwner[key] = r.id
				}
			}
		}
	}

	// Reverse check: warn on unclaimed subtests (non-failing).
	for targetFile, claimed := range claimedByFile {
		absPath := filepath.Join(root, targetFile)
		subtests, err := enumerateSubtests(absPath)
		if err != nil {
			continue
		}
		for _, name := range subtests {
			if !claimed[name] {
				_, _ = fmt.Fprintf(w, "WARN: %s: subtest %q unclaimed by any rule\n",
					targetFile, name)
			}
		}
	}

	if len(parseErrs) > 0 || len(forwardErrs) > 0 {
		for _, e := range parseErrs {
			_, _ = fmt.Fprintln(w, "ERROR:", e)
		}
		for _, e := range forwardErrs {
			_, _ = fmt.Fprintln(w, "ERROR:", e)
		}
		return 1
	}

	totalBounds := 0
	for _, r := range rules {
		totalBounds += len(r.bounds)
	}
	_, _ = fmt.Fprintf(w, "ok: %d rule(s), %d bound(s) verified\n", len(rules), totalBounds)
	return 0
}

// suggestSubtest enumerates t.Run literals and top-level Test* funcs in path
// and returns the closest match to want by Damerau-Levenshtein distance.
// Returns "" when no candidate is close enough to bother suggesting.
func suggestSubtest(path, want string) string {
	candidates, err := enumerateSubtestsAndFuncs(path)
	if err != nil || len(candidates) == 0 {
		return ""
	}
	best := ""
	bestDist := -1
	for _, c := range candidates {
		d := damerauLevenshtein(want, c)
		if bestDist < 0 || d < bestDist {
			bestDist = d
			best = c
		}
	}
	maxDist := 3
	if len(want) <= 8 {
		maxDist = 2
	}
	if bestDist <= 0 || bestDist > maxDist {
		return ""
	}
	return best
}

// enumerateSubtestsAndFuncs returns t.Run literal names plus top-level
// "func TestXxx" identifiers in path.
func enumerateSubtestsAndFuncs(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	funcRE := regexp.MustCompile(`^func\s+(Test\w+)\s*\(`)

	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		for _, m := range tRunRE.FindAllStringSubmatch(line, -1) {
			names = append(names, m[1])
		}
		if m := funcRE.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	return names, sc.Err()
}

// damerauLevenshtein returns the Optimal String Alignment distance between
// a and b. Adjacent transpositions count as one edit (the common
// "swapped two characters" typo class).
func damerauLevenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	d := make([][]int, la+1)
	for i := range d {
		d[i] = make([]int, lb+1)
		d[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		d[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min3(
				d[i-1][j]+1,
				d[i][j-1]+1,
				d[i-1][j-1]+cost,
			)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				if v := d[i-2][j-2] + 1; v < d[i][j] {
					d[i][j] = v
				}
			}
		}
	}
	return d[la][lb]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

func main() {
	var root string
	var suggest bool
	var uniqueBindings bool
	flag.StringVar(&root, "root", ".", "repo root (default: current directory)")
	flag.BoolVar(&suggest, "suggest", false, "on missing-subtest errors, print the closest matching subtest name as a suggestion")
	flag.BoolVar(&uniqueBindings, "check-binding-uniqueness", false, "fail when two rules bind to the same file:subtest")
	flag.Parse()
	os.Exit(runWithOptions(root, os.Stderr, runOptions{
		suggest:        suggest,
		uniqueBindings: uniqueBindings,
	}))
}
