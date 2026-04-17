package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractTaskID(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{"refactor(hal): FanBackend interface (P1-HAL-01)", "P1-HAL-01"},
		{"[BLOCKED] ci: rule-to-subtest binding lint (T0-META-01)", "T0-META-01"},
		{"feat: fingerprint-keyed hwdb (P1-FP-01)", "P1-FP-01"},
		{"chore: random bookkeeping", ""},
	}
	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			if got := extractTaskID(c.title); got != c.want {
				t.Fatalf("extractTaskID(%q) = %q, want %q", c.title, got, c.want)
			}
		})
	}
}

func TestReadEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	body := `{"ts":"2026-04-17T23:10:00Z","kind":"merge","task":"P0-02","pr":238,"sha":"4aa6a37","by":"cowork"}
{"ts":"2026-04-17T23:45:00Z","kind":"merge","task":"P0-03","pr":239,"sha":"c08d9b3","by":"cowork"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	events, err := readEvents(path)
	if err != nil {
		t.Fatalf("readEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len=%d, want 2", len(events))
	}
	if events[0].Task != "P0-02" || events[1].Task != "P0-03" {
		t.Fatalf("bad tasks: %+v", events)
	}
}

func TestLatestByTask(t *testing.T) {
	events := []event{
		{TS: "2026-04-17T10:00:00Z", Kind: "dispatch", Task: "P1-HAL-01"},
		{TS: "2026-04-17T20:00:00Z", Kind: "accept", Task: "P1-HAL-01", PR: 247},
		{TS: "2026-04-17T11:00:00Z", Kind: "note", Task: "P1-HAL-01", Text: "older"},
		{TS: "2026-04-17T09:00:00Z", Kind: "dispatch", Task: "P1-FP-01"},
	}
	latest := latestByTask(events)
	if latest["P1-HAL-01"].Kind != "accept" {
		t.Fatalf("latest[P1-HAL-01] kind=%q, want accept", latest["P1-HAL-01"].Kind)
	}
	if latest["P1-FP-01"].Kind != "dispatch" {
		t.Fatalf("latest[P1-FP-01] kind=%q, want dispatch", latest["P1-FP-01"].Kind)
	}
}

func TestCISummary(t *testing.T) {
	cases := []struct {
		name   string
		checks []statusCheck
		want   string
	}{
		{"empty", nil, "no checks"},
		{"all green", []statusCheck{
			{Conclusion: "SUCCESS"}, {Conclusion: "SUCCESS"},
		}, "2/2 green"},
		{"some red", []statusCheck{
			{Conclusion: "SUCCESS"}, {Conclusion: "FAILURE"},
		}, "1/2 red (0 pending)"},
		{"in progress", []statusCheck{
			{Conclusion: "SUCCESS"}, {Status: "IN_PROGRESS"},
		}, "1/2 green, 1 pending"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ciSummary(c.checks); got != c.want {
				t.Fatalf("ciSummary = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRender(t *testing.T) {
	prs := []pullRequest{
		{
			Number:      247,
			Title:       "refactor(hal): FanBackend interface (P1-HAL-01)",
			HeadRefOid:  "c6726f93e3bd4a3de5bd29ee3f0fa5ed9950a33e",
			HeadRefName: "claude/fan-backend-interface-FYoaH",
			IsDraft:     true,
			Mergeable:   "MERGEABLE",
			StatusCheckRollup: []statusCheck{
				{Conclusion: "SUCCESS"}, {Conclusion: "SUCCESS"},
			},
		},
	}
	latest := map[string]event{
		"P1-HAL-01": {TS: "2026-04-17T20:00:00Z", Kind: "accept", Task: "P1-HAL-01", PR: 247},
	}
	var buf bytes.Buffer
	render(&buf, prs, latest)
	out := buf.String()
	for _, want := range []string{"#247", "[draft]", "P1-HAL-01", "c6726f9", "accept", "pr=#247"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}
