package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// allowlistRE matches lines of the form:
//
//   - Allowlist: `glob1`, glob2, ...
//     **Allowlist:** glob1, glob2
//     Allowlist: glob1 (new), glob2
var allowlistRE = regexp.MustCompile(`(?m)^\s*[-*]?\s*(?:\*\*)?[Aa]llowlist:(?:\*\*)?\s*(.+)`)

// matchGlob reports whether path matches pattern.
// Supports trailing /** for directory subtrees and stdlib single-level globs.
func matchGlob(pattern, path string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == "**" {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		dir := strings.TrimSuffix(pattern, "/**")
		return path == dir || strings.HasPrefix(path, dir+"/")
	}
	m, _ := filepath.Match(pattern, path)
	return m
}

// parseAllowlist extracts glob patterns from a PR body.
// Returns (globs, true) when an Allowlist: line is found; (nil, false) otherwise.
func parseAllowlist(body string) ([]string, bool) {
	m := allowlistRE.FindStringSubmatch(body)
	if m == nil {
		return nil, false
	}
	raw := strings.ReplaceAll(m[1], "`", "")
	parts := strings.Split(raw, ",")
	var globs []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		// Strip trailing annotations like "(new)" or "(deleted)"
		if i := strings.Index(p, " ("); i >= 0 {
			p = strings.TrimSpace(p[:i])
		}
		if p != "" {
			globs = append(globs, p)
		}
	}
	return globs, true
}

func getPRBody(pr string) (string, error) {
	type prJSON struct {
		Body string `json:"body"`
	}
	out, err := exec.Command("gh", "pr", "view", pr, "--json", "body").Output()
	if err != nil {
		return "", fmt.Errorf("gh pr view: %w", err)
	}
	var v prJSON
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("parse pr json: %w", err)
	}
	return v.Body, nil
}

func getPRFiles(pr string) ([]string, error) {
	out, err := exec.Command("gh", "pr", "diff", "--name-only", pr).Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr diff: %w", err)
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

func run(pr string, w io.Writer) int {
	body, err := getPRBody(pr)
	if err != nil {
		_, _ = fmt.Fprintf(w, "error: %v\n", err)
		return 1
	}

	globs, found := parseAllowlist(body)
	if !found {
		_, _ = fmt.Fprintln(w, "skip: no Allowlist: line found in PR body")
		return 0
	}
	if len(globs) == 0 {
		_, _ = fmt.Fprintln(w, "skip: Allowlist: line is empty")
		return 0
	}

	files, err := getPRFiles(pr)
	if err != nil {
		_, _ = fmt.Fprintf(w, "error: %v\n", err)
		return 1
	}

	var violations []string
	for _, f := range files {
		matched := false
		for _, g := range globs {
			if matchGlob(g, f) {
				matched = true
				break
			}
		}
		if !matched {
			violations = append(violations, f)
		}
	}

	if len(violations) > 0 {
		_, _ = fmt.Fprintf(w, "FAIL: %d file(s) outside allowlist [%s]:\n",
			len(violations), strings.Join(globs, ", "))
		for _, v := range violations {
			_, _ = fmt.Fprintf(w, "  %s\n", v)
		}
		return 1
	}

	_, _ = fmt.Fprintf(w, "ok: %d file(s) all within allowlist [%s]\n",
		len(files), strings.Join(globs, ", "))
	return 0
}

func main() {
	var pr string
	flag.StringVar(&pr, "pr", "", "pull request number")
	flag.Parse()
	if pr == "" && flag.NArg() > 0 {
		pr = flag.Arg(0)
	}
	if pr == "" {
		_, _ = fmt.Fprintln(os.Stderr, "usage: allowlistcheck -pr <number>")
		os.Exit(1)
	}
	os.Exit(run(pr, os.Stderr))
}
