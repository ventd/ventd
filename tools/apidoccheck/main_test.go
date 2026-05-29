package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func route(name string, auth bool, methods ...string) Route {
	return Route{Name: name, Path: "/api/" + name, Auth: auth, Methods: methods}
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name     string
		routes   []Route
		docs     map[string]*DocEP
		baseline map[string]bool
		wantSub  []string // substrings that must each appear in exactly the violations
		wantNone bool     // expect zero violations
	}{
		{
			name:     "fully documented and consistent",
			routes:   []Route{route("status", true, "GET")},
			docs:     map[string]*DocEP{"/api/status": {Path: "/api/status", Methods: []string{"GET"}, Auth: "session"}},
			wantNone: true,
		},
		{
			name:    "undocumented route, no baseline",
			routes:  []Route{route("smart/status", true, "GET")},
			docs:    map[string]*DocEP{},
			wantSub: []string{"UNDOCUMENTED: /api/smart/status"},
		},
		{
			name:     "undocumented route covered by baseline",
			routes:   []Route{route("smart/status", true, "GET")},
			docs:     map[string]*DocEP{},
			baseline: map[string]bool{"smart/status": true},
			wantNone: true,
		},
		{
			name:     "stale baseline: now documented",
			routes:   []Route{route("status", true, "GET")},
			docs:     map[string]*DocEP{"/api/status": {Path: "/api/status", Methods: []string{"GET"}, Auth: "session"}},
			baseline: map[string]bool{"status": true},
			wantSub:  []string{"STALE BASELINE"},
		},
		{
			name:     "dead baseline: not a real route",
			routes:   []Route{route("status", true, "GET")},
			docs:     map[string]*DocEP{"/api/status": {Path: "/api/status", Methods: []string{"GET"}, Auth: "session"}},
			baseline: map[string]bool{"gone": true},
			wantSub:  []string{"DEAD BASELINE"},
		},
		{
			name:    "method drift: doc says a method the route rejects",
			routes:  []Route{route("login-ish", false, "POST")},
			docs:    map[string]*DocEP{"/api/login-ish": {Path: "/api/login-ish", Methods: []string{"GET", "POST"}, Auth: "none"}},
			wantSub: []string{"METHOD DRIFT", "documents GET"},
		},
		{
			name:    "auth drift: doc none but route gated",
			routes:  []Route{route("config", true, "GET")},
			docs:    map[string]*DocEP{"/api/config": {Path: "/api/config", Methods: []string{"GET"}, Auth: "none"}},
			wantSub: []string{"AUTH DRIFT", "session-gated"},
		},
		{
			name:    "auth drift: doc session but route open",
			routes:  []Route{route("ping", false, "GET")},
			docs:    map[string]*DocEP{"/api/ping": {Path: "/api/ping", Methods: []string{"GET"}, Auth: "session"}},
			wantSub: []string{"AUTH DRIFT", "unauthenticated"},
		},
		{
			name:    "phantom doc: documented but unregistered",
			routes:  []Route{route("status", true, "GET")},
			docs:    map[string]*DocEP{"/api/ghost": {Path: "/api/ghost", Methods: []string{"GET"}, Auth: "session"}},
			wantSub: []string{"PHANTOM DOC: /api/ghost", "UNDOCUMENTED: /api/status"},
		},
		{
			name:     "empty method allow-list means any — no method drift",
			routes:   []Route{route("events", true)}, // no methods == any
			docs:     map[string]*DocEP{"/api/events": {Path: "/api/events", Methods: []string{"GET"}, Auth: "session"}},
			wantNone: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.baseline == nil {
				tc.baseline = map[string]bool{}
			}
			got := check(tc.routes, tc.docs, tc.baseline)
			if tc.wantNone {
				if len(got) != 0 {
					t.Fatalf("expected no violations, got: %v", got)
				}
				return
			}
			blob := strings.Join(got, "\n")
			for _, sub := range tc.wantSub {
				if !strings.Contains(blob, sub) {
					t.Errorf("expected a violation containing %q; got:\n%s", sub, blob)
				}
			}
		})
	}
}

func TestParseRoutes(t *testing.T) {
	routes, err := parseRoutes(filepath.Join("testdata", "server.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Route{}
	for _, r := range routes {
		byName[r.Name] = r
	}
	if len(routes) != 4 {
		t.Fatalf("want 4 routes, got %d: %v", len(routes), byName)
	}
	if r := byName["status"]; !r.Auth || r.Handler != "handleStatus" {
		t.Errorf("status: want auth+handleStatus, got %+v", r)
	}
	if r := byName["ping"]; r.Auth {
		t.Errorf("ping: want auth=false, got %+v", r)
	}
	if r := byName["config"]; len(r.Methods) != 3 || r.Methods[0] != "GET" || r.Methods[1] != "PUT" || r.Methods[2] != "PATCH" {
		t.Errorf("config: want GET,PUT,PATCH, got %v", r.Methods)
	}
	if r := byName["events"]; len(r.Methods) != 0 {
		t.Errorf("events: want no methods (any), got %v", r.Methods)
	}
}

func TestParseDocs(t *testing.T) {
	docs, err := parseDocs(filepath.Join("testdata", "api.md"))
	if err != nil {
		t.Fatal(err)
	}
	if d := docs["/api/status"]; d == nil || d.Auth != "session" || len(d.Methods) != 1 || d.Methods[0] != "GET" {
		t.Errorf("status doc: %+v", d)
	}
	if d := docs["/api/config"]; d == nil || len(d.Methods) != 2 {
		t.Errorf("config doc: want GET+PUT (two sections merged), got %+v", d)
	}
	if _, ok := docs["/api/v1/status"]; ok {
		t.Error("/api/v1/* mirror sections must not be parsed as endpoints")
	}
}

// The real repo must stay green: every registered route is either documented
// or in the baseline, with no method/auth/phantom drift.
func TestRepoInSync(t *testing.T) {
	repo := "../.."
	if _, err := os.Stat(filepath.Join(repo, serverFile)); err != nil {
		t.Skipf("not in a repo checkout: %v", err)
	}
	routes, err := parseRoutes(filepath.Join(repo, serverFile))
	if err != nil {
		t.Fatal(err)
	}
	docs, err := parseDocs(filepath.Join(repo, apiDocFile))
	if err != nil {
		t.Fatal(err)
	}
	for i := range routes {
		if d, ok := docs[routes[i].Path]; ok {
			routes[i].Doc = d
		}
	}
	baseline, err := loadBaseline(filepath.Join(repo, baselineFile))
	if err != nil {
		t.Fatal(err)
	}
	if v := check(routes, docs, baseline); len(v) != 0 {
		t.Fatalf("apidoccheck found %d violation(s):\n%s", len(v), strings.Join(v, "\n"))
	}
}
