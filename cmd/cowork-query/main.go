package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	eventsPath  = ".cowork/events.jsonl"
	lessonsPath = ".cowork/LESSONS.md"
)

type Event struct {
	Ts      string `json:"ts"`
	Kind    string `json:"kind"`
	Task    string `json:"task"`
	PR      int    `json:"pr"`
	SHA     string `json:"sha"`
	By      string `json:"by"`
	Text    string `json:"text"`
	Model   string `json:"model"`
	Alias   string `json:"alias"`
	Reason  string `json:"reason"`
	Session string `json:"session"`
}

// parseTS parses ISO-8601 timestamps; also extracts date from session labels like "2026-04-18T-session-resume".
func parseTS(s string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	if len(s) >= 10 {
		if t, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func loadEvents() ([]Event, error) {
	f, err := os.Open(eventsPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", eventsPath, err)
	}
	defer f.Close()
	var events []Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			events = append(events, e)
		}
	}
	return events, sc.Err()
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// merged lists PRs merged within the time window.
func cmdMerged(args []string) error {
	fs := flag.NewFlagSet("merged", flag.ExitOnError)
	since := fs.String("since", "24h", "time window (e.g. 24h, 7d)")
	asJSON := fs.Bool("json", false, "JSON output")
	_ = fs.Parse(args)

	dur, err := parseDuration(*since)
	if err != nil {
		return fmt.Errorf("--since: %w", err)
	}
	cutoff := time.Now().Add(-dur)

	events, err := loadEvents()
	if err != nil {
		return err
	}

	type row struct {
		TS   time.Time
		Task string `json:"task"`
		PR   int    `json:"pr"`
		SHA  string `json:"sha"`
		By   string `json:"by"`
		Text string `json:"text"`
	}
	var rows []row
	for _, e := range events {
		if e.Kind != "merge" {
			continue
		}
		t, ok := parseTS(e.Ts)
		if !ok || t.Before(cutoff) {
			continue
		}
		rows = append(rows, row{t, e.Task, e.PR, e.SHA, e.By, e.Text})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TS.Before(rows[j].TS) })

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tTASK\tPR\tSHA\tBY\tDESCRIPTION")
	for _, r := range rows {
		pr := ""
		if r.PR != 0 {
			pr = fmt.Sprintf("#%d", r.PR)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%.8s\t%s\t%s\n",
			r.TS.Format("2006-01-02 15:04"), r.Task, pr, r.SHA, r.By, trunc(r.Text, 60))
	}
	return w.Flush()
}

// tpm shows the event timeline for a PR as a proxy for workflow complexity.
// Raw tool-call counts require spawn-mcp session logs.
func cmdTPM(args []string) error {
	fs := flag.NewFlagSet("tpm", flag.ExitOnError)
	pr := fs.Int("pr", 0, "PR number")
	asJSON := fs.Bool("json", false, "JSON output")
	_ = fs.Parse(args)
	if *pr == 0 {
		return fmt.Errorf("--pr is required")
	}

	events, err := loadEvents()
	if err != nil {
		return err
	}

	type entry struct {
		Kind   string `json:"kind"`
		Task   string `json:"task"`
		TS     string `json:"ts"`
		Detail string `json:"detail,omitempty"`
	}
	var matched []entry
	for _, e := range events {
		if e.PR != *pr {
			continue
		}
		detail := e.Reason
		if detail == "" {
			detail = e.Text
		}
		matched = append(matched, entry{e.Kind, e.Task, e.Ts, detail})
	}

	if *asJSON {
		out := map[string]interface{}{
			"pr":          *pr,
			"event_count": len(matched),
			"events":      matched,
			"note":        "event_count proxies lifecycle complexity; raw tool-call counts require session logs",
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "PR\t#%d\n", *pr)
	fmt.Fprintf(w, "events in log\t%d\n", len(matched))
	if len(matched) == 0 {
		_ = w.Flush()
		fmt.Println("(no events found; raw tool-call data requires session logs)")
		return nil
	}
	fmt.Fprintln(w, "KIND\tTASK\tTS\tDETAIL")
	for _, m := range matched {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.Kind, m.Task, m.TS, trunc(m.Detail, 60))
	}
	fmt.Fprintln(w, "\nnote\traw tool-call counts require session logs; event_count is lifecycle proxy")
	return w.Flush()
}

var wallClockRe = regexp.MustCompile(`~?(\d+)\s*min`)

// slowCC finds CC sessions that ran longer than threshold.
// Sources: (1) dispatch→accept/merge duration; (2) note events with explicit wall-clock mentions.
func cmdSlowCC(args []string) error {
	fs := flag.NewFlagSet("slow-cc", flag.ExitOnError)
	threshold := fs.String("threshold", "30m", "duration threshold (e.g. 30m, 1h)")
	asJSON := fs.Bool("json", false, "JSON output")
	_ = fs.Parse(args)

	thresh, err := parseDuration(*threshold)
	if err != nil {
		return fmt.Errorf("--threshold: %w", err)
	}

	events, err := loadEvents()
	if err != nil {
		return err
	}

	type dispatchRec struct {
		task    string
		alias   string
		session string
		model   string
		ts      time.Time
		tsOK    bool
	}
	dispatches := map[string]dispatchRec{}
	for _, e := range events {
		if e.Kind != "dispatch" {
			continue
		}
		t, ok := parseTS(e.Ts)
		dispatches[e.Task] = dispatchRec{e.Task, e.Alias, e.Session, e.Model, t, ok}
	}

	type result struct {
		Task     string `json:"task"`
		Alias    string `json:"alias"`
		Session  string `json:"session"`
		Model    string `json:"model"`
		Duration string `json:"duration"`
		Source   string `json:"source"`
	}
	seen := map[string]bool{}
	var results []result

	// Source 1: dispatch→accept/merge where both timestamps are parseable.
	for _, e := range events {
		if e.Kind != "merge" && e.Kind != "accept" {
			continue
		}
		d, ok := dispatches[e.Task]
		if !ok || !d.tsOK {
			continue
		}
		t, ok := parseTS(e.Ts)
		if !ok {
			continue
		}
		dur := t.Sub(d.ts)
		if dur < thresh {
			continue
		}
		key := d.task + ":" + d.session
		if seen[key] {
			continue
		}
		seen[key] = true
		results = append(results, result{
			Task:     d.task,
			Alias:    d.alias,
			Session:  d.session,
			Model:    d.model,
			Duration: dur.Round(time.Minute).String(),
			Source:   "computed",
		})
	}

	// Source 2: note events with explicit wall-clock mentions.
	for _, e := range events {
		if e.Kind != "note" {
			continue
		}
		m := wallClockRe.FindStringSubmatch(e.Text)
		if m == nil {
			continue
		}
		mins, _ := strconv.Atoi(m[1])
		dur := time.Duration(mins) * time.Minute
		if dur < thresh {
			continue
		}
		task := e.Task
		if task == "" {
			continue
		}
		d := dispatches[task]
		key := task + ":" + d.session
		if seen[key] {
			continue
		}
		seen[key] = true
		results = append(results, result{
			Task:     task,
			Alias:    d.alias,
			Session:  d.session,
			Model:    d.model,
			Duration: dur.String() + " (noted)",
			Source:   "note",
		})
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}
	if len(results) == 0 {
		fmt.Printf("no CC sessions exceeding %s found\n", *threshold)
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TASK\tALIAS\tSESSION\tDURATION\tSOURCE")
	for _, r := range results {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Task, r.Alias, r.Session, r.Duration, r.Source)
	}
	return w.Flush()
}

// staleRole queries GitHub for open issues with a label idle longer than age.
func cmdStaleRole(args []string) error {
	fs := flag.NewFlagSet("stale-role", flag.ExitOnError)
	label := fs.String("label", "", "GitHub label to filter")
	age := fs.String("age", "72h", "minimum idle age (e.g. 72h, 7d)")
	asJSON := fs.Bool("json", false, "JSON output")
	_ = fs.Parse(args)
	if *label == "" {
		return fmt.Errorf("--label is required")
	}
	maxAge, err := parseDuration(*age)
	if err != nil {
		return fmt.Errorf("--age: %w", err)
	}

	out, err := exec.Command("gh", "issue", "list",
		"--label", *label,
		"--state", "open",
		"--json", "number,title,createdAt,updatedAt,labels",
		"--limit", "200",
	).Output()
	if err != nil {
		return fmt.Errorf("gh issue list: %w (is gh authenticated?)", err)
	}

	var issues []struct {
		Number    int                     `json:"number"`
		Title     string                  `json:"title"`
		UpdatedAt string                  `json:"updatedAt"`
		Labels    []struct{ Name string } `json:"labels"`
	}
	if err := json.Unmarshal(out, &issues); err != nil {
		return fmt.Errorf("parse gh output: %w", err)
	}

	cutoff := time.Now().Add(-maxAge)
	type row struct {
		Number    int      `json:"number"`
		Title     string   `json:"title"`
		UpdatedAt string   `json:"updated_at"`
		IdleFor   string   `json:"idle_for"`
		Labels    []string `json:"labels"`
	}
	var rows []row
	for _, iss := range issues {
		t, err := time.Parse(time.RFC3339, iss.UpdatedAt)
		if err != nil || !t.Before(cutoff) {
			continue
		}
		var lbls []string
		for _, l := range iss.Labels {
			lbls = append(lbls, l.Name)
		}
		rows = append(rows, row{iss.Number, iss.Title, iss.UpdatedAt, time.Since(t).Round(time.Hour).String(), lbls})
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	if len(rows) == 0 {
		fmt.Printf("no open issues with label %q idle > %s\n", *label, *age)
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "#\tIDLE\tTITLE")
	for _, r := range rows {
		fmt.Fprintf(w, "#%d\t%s\t%s\n", r.Number, r.IdleFor, trunc(r.Title, 70))
	}
	return w.Flush()
}

// throughput computes PR/hr over a time window from merge events.
func cmdThroughput(args []string) error {
	fs := flag.NewFlagSet("throughput", flag.ExitOnError)
	since := fs.String("since", "7d", "time window (e.g. 7d, 24h)")
	asJSON := fs.Bool("json", false, "JSON output")
	_ = fs.Parse(args)

	dur, err := parseDuration(*since)
	if err != nil {
		return fmt.Errorf("--since: %w", err)
	}
	cutoff := time.Now().Add(-dur)

	events, err := loadEvents()
	if err != nil {
		return err
	}

	var mergeCount int
	var earliest, latest time.Time
	for _, e := range events {
		if e.Kind != "merge" {
			continue
		}
		t, ok := parseTS(e.Ts)
		if !ok || t.Before(cutoff) {
			continue
		}
		mergeCount++
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
		if t.After(latest) {
			latest = t
		}
	}

	var prPerHour float64
	if mergeCount >= 2 {
		window := latest.Sub(earliest).Hours()
		if window > 0 {
			prPerHour = float64(mergeCount) / window
		}
	}

	if *asJSON {
		out := map[string]interface{}{
			"window":      *since,
			"merge_count": mergeCount,
			"pr_per_hour": prPerHour,
			"earliest":    earliest.Format(time.RFC3339),
			"latest":      latest.Format(time.RFC3339),
			"note":        "rate spans first-to-last merge in window, not full session wall-clock",
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "window\t%s\n", *since)
	fmt.Fprintf(w, "merges\t%d\n", mergeCount)
	if mergeCount >= 2 {
		fmt.Fprintf(w, "PR/hr\t%.2f\n", prPerHour)
		fmt.Fprintf(w, "first merge\t%s\n", earliest.Format("2006-01-02 15:04 UTC"))
		fmt.Fprintf(w, "last merge\t%s\n", latest.Format("2006-01-02 15:04 UTC"))
		fmt.Fprintf(w, "note\trate spans first-to-last merge, not full session wall-clock\n")
	} else {
		fmt.Fprintf(w, "PR/hr\tn/a (need ≥2 timestamped merges)\n")
	}
	return w.Flush()
}

// lessons prints the last N entries from LESSONS.md.
func cmdLessons(args []string) error {
	fs := flag.NewFlagSet("lessons", flag.ExitOnError)
	n := fs.Int("n", 10, "number of entries to show")
	asJSON := fs.Bool("json", false, "JSON output")
	_ = fs.Parse(args)

	data, err := os.ReadFile(lessonsPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", lessonsPath, err)
	}

	// Split on "\n## " to get individual sections; drop file header before first ##.
	sections := strings.Split(string(data), "\n## ")
	if len(sections) > 0 && !strings.HasPrefix(strings.TrimSpace(sections[0]), "## ") {
		sections = sections[1:]
	}
	if *n > len(sections) {
		*n = len(sections)
	}
	last := sections[len(sections)-*n:]

	if *asJSON {
		type lesson struct {
			Index int    `json:"index"`
			Text  string `json:"text"`
		}
		total := len(sections)
		out := make([]lesson, len(last))
		for i, s := range last {
			out[i] = lesson{total - *n + i + 1, strings.TrimSpace(s)}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	for i, s := range last {
		if i > 0 {
			fmt.Println("\n---")
		}
		fmt.Println("##", strings.TrimSpace(s))
	}
	return nil
}

func usage() {
	fmt.Fprint(os.Stderr, `cowork-query — query .cowork/events.jsonl

Usage:
  cowork-query <command> [flags]

Commands:
  merged      --since <dur>                 list merged PRs in window
  tpm         --pr <n>                      event timeline for a PR
  slow-cc     --threshold <dur>             CC sessions longer than threshold
  stale-role  --label <label> --age <dur>   open GitHub issues idle > age
  throughput  --since <dur>                 PR/hr over window
  lessons     [--n <n>]                     last N LESSONS.md entries

All commands accept --json for machine-readable output.
Durations: Go syntax (30m, 1h) or days suffix (7d).
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	cmds := map[string]func([]string) error{
		"merged":     cmdMerged,
		"tpm":        cmdTPM,
		"slow-cc":    cmdSlowCC,
		"stale-role": cmdStaleRole,
		"throughput": cmdThroughput,
		"lessons":    cmdLessons,
	}
	fn, ok := cmds[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
	if err := fn(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
