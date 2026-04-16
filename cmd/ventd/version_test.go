package main

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

// TestVersionFlag exercises the two output shapes driven by --version and
// --version --json. Uses the in-process printVersion helper so we don't have
// to fork a subprocess — that would require a built binary and complicate
// test hermeticity. The flag parser is exercised separately via the Go
// stdlib; what actually varies between the two shapes is the write path,
// which is what this table covers.
func TestVersionFlag(t *testing.T) {
	origV, origC, origD := version, commit, buildDate
	t.Cleanup(func() {
		version, commit, buildDate = origV, origC, origD
	})
	version = "1.2.3"
	commit = "abc1234"
	buildDate = "2026-04-15T12:00:00Z"

	cases := []struct {
		name   string
		asJSON bool
		check  func(t *testing.T, out string)
	}{
		{
			name:   "plain text shape",
			asJSON: false,
			check: func(t *testing.T, out string) {
				want := "ventd 1.2.3 (abc1234) 2026-04-15T12:00:00Z\n"
				if out != want {
					t.Errorf("plain output\n got: %q\nwant: %q", out, want)
				}
			},
		},
		{
			name:   "json shape with go runtime",
			asJSON: true,
			check: func(t *testing.T, out string) {
				out = strings.TrimSpace(out)
				var got struct {
					Version   string `json:"version"`
					Commit    string `json:"commit"`
					BuildDate string `json:"build_date"`
					Go        string `json:"go"`
				}
				if err := json.Unmarshal([]byte(out), &got); err != nil {
					t.Fatalf("json decode: %v\nraw: %s", err, out)
				}
				if got.Version != "1.2.3" {
					t.Errorf("version=%q want 1.2.3", got.Version)
				}
				if got.Commit != "abc1234" {
					t.Errorf("commit=%q want abc1234", got.Commit)
				}
				if got.BuildDate != "2026-04-15T12:00:00Z" {
					t.Errorf("build_date=%q want 2026-04-15T12:00:00Z", got.BuildDate)
				}
				if got.Go != runtime.Version() {
					t.Errorf("go=%q want %q", got.Go, runtime.Version())
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := printVersion(&buf, tc.asJSON); err != nil {
				t.Fatalf("printVersion: %v", err)
			}
			tc.check(t, buf.String())
		})
	}
}

// TestVersionDefaults guards the "go run ventd --version" path: when the
// ldflags haven't been applied, the printer must still emit a sensible
// string rather than blanks. Keeps CI from accidentally shipping a binary
// whose --version output is " () " when a packager forgets the -X.
func TestVersionDefaults(t *testing.T) {
	origV, origC, origD := version, commit, buildDate
	t.Cleanup(func() {
		version, commit, buildDate = origV, origC, origD
	})
	version = "dev"
	commit = "unknown"
	buildDate = "unknown"

	var buf bytes.Buffer
	if err := printVersion(&buf, false); err != nil {
		t.Fatalf("printVersion: %v", err)
	}
	got := buf.String()
	want := "ventd dev (unknown) unknown\n"
	if got != want {
		t.Errorf("defaults output\n got: %q\nwant: %q", got, want)
	}
}
