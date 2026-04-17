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
	defer f.Close()

	// ruleHeadingRE matches ## RULE-<ID>: <invariant>
	ruleHeadingRE := regexp.MustCompile(`^## RULE-(\S+):\s+(.+)$`)

	var rules []ruleEntry
	var errs []string
	curIdx := -1

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()

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
	defer f.Close()

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
	defer f.Close()

	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		for _, m := range tRunRE.FindAllStringSubmatch(sc.Text(), -1) {
			names = append(names, m[1])
		}
	}
	return names, sc.Err()
}

func run(root string, w io.Writer) int {
	rulesDir := filepath.Join(root, ".claude", "rules")
	rules, parseErrs := parseRulesDir(rulesDir)

	var forwardErrs []string
	claimedByFile := make(map[string]map[string]bool)

	for _, r := range rules {
		for _, b := range r.bounds {
			absPath := filepath.Join(root, b.targetFile)
			if _, err := os.Stat(absPath); os.IsNotExist(err) {
				forwardErrs = append(forwardErrs, fmt.Sprintf(
					"%s: RULE-%s: bound file not found: %s",
					r.file, r.id, b.targetFile,
				))
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
				forwardErrs = append(forwardErrs, fmt.Sprintf(
					"%s: RULE-%s: subtest %q not found in %s",
					r.file, r.id, b.subtestName, b.targetFile,
				))
				continue
			}
			if claimedByFile[b.targetFile] == nil {
				claimedByFile[b.targetFile] = make(map[string]bool)
			}
			claimedByFile[b.targetFile][b.subtestName] = true
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
				fmt.Fprintf(w, "WARN: %s: subtest %q unclaimed by any rule\n",
					targetFile, name)
			}
		}
	}

	if len(parseErrs) > 0 || len(forwardErrs) > 0 {
		for _, e := range parseErrs {
			fmt.Fprintln(w, "ERROR:", e)
		}
		for _, e := range forwardErrs {
			fmt.Fprintln(w, "ERROR:", e)
		}
		return 1
	}

	totalBounds := 0
	for _, r := range rules {
		totalBounds += len(r.bounds)
	}
	fmt.Fprintf(w, "ok: %d rule(s), %d bound(s) verified\n", len(rules), totalBounds)
	return 0
}

func main() {
	var root string
	flag.StringVar(&root, "root", ".", "repo root (default: current directory)")
	flag.Parse()
	os.Exit(run(root, os.Stderr))
}
