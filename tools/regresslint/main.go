package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type label struct {
	Name string `json:"name"`
}

type issue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	State       string    `json:"state"`
	Labels      []label   `json:"labels"`
	HTMLURL     string    `json:"html_url"`
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

func (iss issue) hasBug() bool {
	for _, l := range iss.Labels {
		if l.Name == "bug" {
			return true
		}
	}
	return false
}

func (iss issue) hasExempt() bool {
	for _, l := range iss.Labels {
		if l.Name == "no-regression-test" {
			return true
		}
	}
	return false
}

func (iss issue) isPR() bool { return iss.PullRequest != nil }

func loadIssues(path string) ([]issue, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var issues []issue
	if err := json.Unmarshal(data, &issues); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return issues, nil
}

const (
	ghOwner = "ventd"
	ghRepo  = "ventd"
)

func fetchIssues(token string) ([]issue, error) {
	client := &http.Client{}
	var all []issue
	for page := 1; ; page++ {
		url := fmt.Sprintf(
			"https://api.github.com/repos/%s/%s/issues?state=closed&labels=bug&per_page=100&page=%d",
			ghOwner, ghRepo, page,
		)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
		}

		var batch []issue
		err = json.NewDecoder(resp.Body).Decode(&batch)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decoding GitHub response (page %d): %w", page, err)
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// hasRegressionTest reports whether the repo rooted at root contains a
// TestRegression_Issue<N>_* top-level function, a t.Run("Issue<N>_...")
// literal, or a // regresses #N / // covers #N magic comment anywhere
// under internal/ or cmd/.
func hasRegressionTest(root string, issueNum int) (bool, error) {
	funcPat := fmt.Sprintf("func TestRegression_Issue%d_", issueNum)
	tRunPat := fmt.Sprintf(`t.Run("Issue%d_`, issueNum)
	regressPat := fmt.Sprintf("// regresses #%d", issueNum)
	coversPat := fmt.Sprintf("// covers #%d", issueNum)

	for _, dir := range []string{"internal", "cmd"} {
		dirPath := filepath.Join(root, dir)
		if _, err := os.Stat(dirPath); os.IsNotExist(err) {
			continue
		}
		found, err := walkForPatterns(dirPath, funcPat, tRunPat, regressPat, coversPat)
		if err != nil {
			return false, fmt.Errorf("walking %s: %w", dirPath, err)
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

func walkForPatterns(dir string, patterns ...string) (bool, error) {
	var found bool
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			for _, p := range patterns {
				if strings.Contains(line, p) {
					found = true
					return nil
				}
			}
		}
		return sc.Err()
	})
	return found, err
}

func run(root, issuesFile, token string, strict bool, w io.Writer) int {
	var (
		issues []issue
		err    error
	)

	if issuesFile != "" {
		issues, err = loadIssues(issuesFile)
		if err != nil {
			_, _ = fmt.Fprintf(w, "ERROR: %v\n", err)
			return 1
		}
	} else {
		if token == "" {
			_, _ = fmt.Fprintln(w, "ERROR: GITHUB_TOKEN env var required when -issues is not set")
			return 1
		}
		issues, err = fetchIssues(token)
		if err != nil {
			_, _ = fmt.Fprintf(w, "ERROR: fetching GitHub issues: %v\n", err)
			return 1
		}
	}

	var violations []string
	checked, exempt := 0, 0

	for _, iss := range issues {
		if iss.isPR() || iss.State != "closed" || !iss.hasBug() {
			continue
		}
		if iss.hasExempt() {
			exempt++
			continue
		}
		checked++
		found, err := hasRegressionTest(root, iss.Number)
		if err != nil {
			_, _ = fmt.Fprintf(w, "ERROR: searching tests for issue #%d: %v\n", iss.Number, err)
			return 1
		}
		if !found {
			violations = append(violations, fmt.Sprintf(
				"  #%d %s\n    link: %s\n    action: add TestRegression_Issue%d_* or label 'no-regression-test'",
				iss.Number, iss.Title, iss.HTMLURL, iss.Number,
			))
		}
	}

	if len(violations) > 0 {
		if strict {
			_, _ = fmt.Fprintf(w, "FAIL: %d closed bug(s) missing a regression test:\n\n", len(violations))
			for _, v := range violations {
				_, _ = fmt.Fprintln(w, v)
				_, _ = fmt.Fprintln(w)
			}
			return 1
		}
		_, _ = fmt.Fprintf(w, "WARN: %d closed bug(s) missing regression test (not fatal; run with -strict to fail)\n", len(violations))
		return 0
	}

	_, _ = fmt.Fprintf(w, "ok: %d closed bug(s) checked, %d exempt\n", checked, exempt)
	return 0
}

func main() {
	var (
		root       string
		issuesFile string
		strict     bool
	)
	flag.StringVar(&root, "root", ".", "repo root to search for regression tests")
	flag.StringVar(&issuesFile, "issues", "", "JSON file of issues (omit to query GitHub API via GITHUB_TOKEN)")
	// TODO: flip -strict=true once the backlog of unlabeled closed bugs is triaged (TX-REGRESSION-AUDIT).
	flag.BoolVar(&strict, "strict", false, "exit 1 on violations instead of warning")
	flag.Parse()

	os.Exit(run(root, issuesFile, os.Getenv("GITHUB_TOKEN"), strict, os.Stderr))
}
