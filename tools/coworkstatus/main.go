// Command coworkstatus renders a one-screen Cowork dashboard: every open PR
// with CI rollup alongside Cowork's most recent event for that task.
//
// PR state comes from `gh pr list --json ...`; Cowork's decision log comes
// from .cowork/events.jsonl. Stdlib only.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

type statusCheck struct {
	State      string `json:"state"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Name       string `json:"name"`
	Context    string `json:"context"`
}

type pullRequest struct {
	Number            int           `json:"number"`
	Title             string        `json:"title"`
	HeadRefOid        string        `json:"headRefOid"`
	HeadRefName       string        `json:"headRefName"`
	IsDraft           bool          `json:"isDraft"`
	Mergeable         string        `json:"mergeable"`
	StatusCheckRollup []statusCheck `json:"statusCheckRollup"`
}

type event struct {
	TS     string `json:"ts"`
	Kind   string `json:"kind"`
	Task   string `json:"task,omitempty"`
	PR     int    `json:"pr,omitempty"`
	SHA    string `json:"sha,omitempty"`
	Reason string `json:"reason,omitempty"`
	Text   string `json:"text,omitempty"`
	Alias  string `json:"alias,omitempty"`
	Model  string `json:"model,omitempty"`
	By     string `json:"by,omitempty"`
}

// taskIDRE extracts a task ID like P1-HAL-01 or T0-META-03 from the PR title.
var taskIDRE = regexp.MustCompile(`\b([A-Z]+\d*-[A-Z]+-\d+[a-z]?)\b`)

func extractTaskID(title string) string {
	if m := taskIDRE.FindStringSubmatch(title); m != nil {
		return m[1]
	}
	return ""
}

func fetchPRs() ([]pullRequest, error) {
	cmd := exec.Command("gh", "pr", "list",
		"--json", "number,title,headRefOid,headRefName,statusCheckRollup,isDraft,mergeable",
		"--limit", "50",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	var prs []pullRequest
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}
	return prs, nil
}

func readEvents(path string) ([]event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var events []event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("parse event: %w", err)
		}
		events = append(events, e)
	}
	return events, sc.Err()
}

// latestByTask returns the most recent event per task ID.
func latestByTask(events []event) map[string]event {
	out := map[string]event{}
	for _, e := range events {
		if e.Task == "" {
			continue
		}
		if cur, ok := out[e.Task]; !ok || e.TS > cur.TS {
			out[e.Task] = e
		}
	}
	return out
}

func ciSummary(checks []statusCheck) string {
	var total, pass, fail, pending int
	for _, c := range checks {
		total++
		// GH surfaces either status/conclusion (check runs) or state (status contexts).
		state := strings.ToUpper(c.Conclusion)
		if state == "" {
			state = strings.ToUpper(c.State)
		}
		switch state {
		case "SUCCESS":
			pass++
		case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED":
			fail++
		default:
			pending++
		}
	}
	if total == 0 {
		return "no checks"
	}
	if fail > 0 {
		return fmt.Sprintf("%d/%d red (%d pending)", fail, total, pending)
	}
	if pending > 0 {
		return fmt.Sprintf("%d/%d green, %d pending", pass, total, pending)
	}
	return fmt.Sprintf("%d/%d green", pass, total)
}

func render(w io.Writer, prs []pullRequest, latest map[string]event) {
	sort.Slice(prs, func(i, j int) bool { return prs[i].Number < prs[j].Number })

	_, _ = fmt.Fprintf(w, "# Cowork status — %d open PR(s)\n\n", len(prs))

	if len(prs) == 0 {
		_, _ = fmt.Fprintln(w, "_No open PRs._")
		return
	}

	for _, pr := range prs {
		tag := ""
		if pr.IsDraft {
			tag = " [draft]"
		}
		_, _ = fmt.Fprintf(w, "## #%d%s — %s\n", pr.Number, tag, pr.Title)
		_, _ = fmt.Fprintf(w, "- branch: `%s`\n", pr.HeadRefName)
		_, _ = fmt.Fprintf(w, "- head:   `%s`\n", shortSHA(pr.HeadRefOid))
		_, _ = fmt.Fprintf(w, "- ci:     %s\n", ciSummary(pr.StatusCheckRollup))
		if pr.Mergeable != "" {
			_, _ = fmt.Fprintf(w, "- merge:  %s\n", strings.ToLower(pr.Mergeable))
		}
		taskID := extractTaskID(pr.Title)
		if taskID != "" {
			_, _ = fmt.Fprintf(w, "- task:   %s\n", taskID)
			if e, ok := latest[taskID]; ok {
				_, _ = fmt.Fprintf(w, "- event:  %s `%s` %s\n", e.TS, e.Kind, eventDetail(e))
			} else {
				_, _ = fmt.Fprintln(w, "- event:  (no cowork events)")
			}
		}
		_, _ = fmt.Fprintln(w)
	}
}

func shortSHA(s string) string {
	if len(s) >= 7 {
		return s[:7]
	}
	return s
}

func eventDetail(e event) string {
	parts := []string{}
	if e.PR != 0 {
		parts = append(parts, fmt.Sprintf("pr=#%d", e.PR))
	}
	if e.SHA != "" {
		parts = append(parts, fmt.Sprintf("sha=%s", shortSHA(e.SHA)))
	}
	if e.Reason != "" {
		parts = append(parts, fmt.Sprintf("reason=%q", e.Reason))
	}
	if e.Text != "" {
		parts = append(parts, fmt.Sprintf("note=%q", e.Text))
	}
	return strings.Join(parts, " ")
}

func run(eventsPath string, w io.Writer) error {
	prs, err := fetchPRs()
	if err != nil {
		return err
	}
	events, err := readEvents(eventsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("events: %w", err)
	}
	render(w, prs, latestByTask(events))
	return nil
}

func main() {
	eventsPath := flag.String("events", ".cowork/events.jsonl", "path to events.jsonl")
	flag.Parse()

	if err := run(*eventsPath, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "coworkstatus:", err)
		os.Exit(1)
	}
}
