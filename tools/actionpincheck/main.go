package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// allowlistedOwners may use v-tag pins without a full SHA (RULE-CI-03).
var allowlistedOwners = map[string]bool{
	"actions": true,
	"github":  true,
	"docker":  true,
}

var (
	usesRE    = regexp.MustCompile(`^\s+(?:-\s+)?uses:\s+(\S+)(?:\s+(.*))?$`)
	sha40RE   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	vtagRE    = regexp.MustCompile(`^v\d`)
	commentRE = regexp.MustCompile(`^#\s+v\d+\.\d+\.\d+(-\S+)?(\s.*)?$`)
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: actionpincheck <workflow-file>...")
		os.Exit(1)
	}
	var total int
	for _, arg := range os.Args[1:] {
		matches, err := filepath.Glob(arg)
		if err != nil || len(matches) == 0 {
			matches = []string{arg}
		}
		for _, path := range matches {
			f, err := os.Open(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				total++
				continue
			}
			total += check(path, f, os.Stderr)
			_ = f.Close()
		}
	}
	if total > 0 {
		os.Exit(1)
	}
}

// check scans a workflow YAML reader for action-pinning violations.
// Returns the number of violations found; writes diagnostics to w.
func check(name string, r io.Reader, w io.Writer) int {
	var violations int
	var blockActive bool
	var blockIndent int
	lineNum := 0

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		lineNum++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " \t"))

		// Exit block scalar when indentation returns to or below the opener.
		if blockActive {
			if indent > blockIndent {
				continue
			}
			blockActive = false
		}

		// Detect block scalar openers: key ending with |, >, |- etc.
		// Strip inline comment first so "run: | # note" is detected correctly.
		bare := trimmed
		if i := strings.Index(bare, " #"); i >= 0 {
			bare = strings.TrimRight(bare[:i], " \t")
		}
		if strings.HasSuffix(bare, "|") || strings.HasSuffix(bare, "|-") ||
			strings.HasSuffix(bare, "|+") || strings.HasSuffix(bare, ">") ||
			strings.HasSuffix(bare, ">-") || strings.HasSuffix(bare, ">+") {
			blockActive = true
			blockIndent = indent
		}

		m := usesRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		usesVal := m[1]
		comment := strings.TrimSpace(m[2])

		// Skip GitHub Actions expression templates (${{ matrix.* }}, etc.).
		if strings.Contains(usesVal, "${{") {
			continue
		}

		// Local actions (./path) have no @ref and need no pin.
		atIdx := strings.LastIndex(usesVal, "@")
		if atIdx < 0 {
			continue
		}

		repoPath := usesVal[:atIdx]
		ref := usesVal[atIdx+1:]
		owner := strings.SplitN(repoPath, "/", 2)[0]

		isSHA := sha40RE.MatchString(ref)
		isVTag := vtagRE.MatchString(ref)

		if !isSHA && !isVTag {
			_, _ = fmt.Fprintf(w, "%s:%d: RULE-CI-01: %q pins to %q — use a 40-char SHA or v-tag\n",
				name, lineNum, usesVal, ref)
			violations++
			continue
		}

		if isSHA {
			if !commentRE.MatchString(comment) {
				_, _ = fmt.Fprintf(w, "%s:%d: RULE-CI-02: %q missing trailing version comment (want: # v<major>.<minor>.<patch>)\n",
					name, lineNum, usesVal)
				violations++
			}
			continue
		}

		// v-tag without SHA — check allowlist (RULE-CI-03).
		if !allowlistedOwners[owner] {
			_, _ = fmt.Fprintf(w, "%s:%d: RULE-CI-03: %q pins by v-tag; %q not in allowlist; use a 40-char SHA\n",
				name, lineNum, usesVal, owner)
			violations++
		}
	}
	return violations
}
