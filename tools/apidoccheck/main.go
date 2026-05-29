// apidoccheck verifies that docs/api.md stays in sync with the API surface
// that internal/web actually registers. It guards against the most common way
// a hand-maintained API reference rots: a route is added, renamed, re-gated,
// or has its method allow-list changed, and the docs are not updated — so a
// script written against the docs breaks (the exact failure that motivated
// this check: POST /login was documented as JSON when the handler is
// form-encoded).
//
// It reads the declarative `[]apiRoute{…}` table passed to
// `(*Server).registerAPIRoutes` in internal/web/server.go via the Go AST — no
// runtime, no build of the web package — and the `### `<METHOD> /api/<path>“
// section headers (plus their `**Auth**:` annotation) in docs/api.md, then
// enforces:
//
//  1. Coverage      — every registered /api route has a doc section, unless
//     it is listed in the burn-down baseline (undocumented.txt).
//  2. No phantoms   — every documented /api endpoint is a real route.
//  3. Method drift  — every documented method is in the route's method
//     allow-list (when the route declares one).
//  4. Auth drift    — the doc's `**Auth**: none|session` matches the route's
//     `auth` bool.
//
// The baseline exists so the check can land green on a repo that already has
// undocumented routes: it locks in current coverage and fails on any NEW
// undocumented route, while the listed debt is burned down over time. A
// baseline entry that has since been documented is itself an error (so the
// list can only shrink).
//
// Exit 0 when in sync; exit 1 (listing every violation) otherwise.
// Run from the repo root:  go run ./tools/apidoccheck
// Other modes:  -json (machine-readable surface dump), -route <name> (one route).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	serverFile   = "internal/web/server.go"
	apiDocFile   = "docs/api.md"
	baselineFile = "tools/apidoccheck/undocumented.txt"
)

// Route is one entry of the registerAPIRoutes table.
type Route struct {
	Name    string   `json:"name"`          // trailing path after /api/, no leading slash
	Path    string   `json:"path"`          // /api/<name>
	Methods []string `json:"methods"`       // HTTP verbs; empty == any
	Auth    bool     `json:"auth"`          // session-gated
	Handler string   `json:"handler"`       // handler func name, for context
	Doc     *DocEP   `json:"doc,omitempty"` // matched doc section, nil if undocumented
}

// DocEP is one documented endpoint section.
type DocEP struct {
	Path    string   `json:"path"`
	Methods []string `json:"methods"`
	Auth    string   `json:"auth"` // "none" | "session" | "" (unstated)
}

func main() {
	repo := flag.String("repo", ".", "repository root")
	asJSON := flag.Bool("json", false, "emit the route<->doc surface as JSON and exit 0")
	route := flag.String("route", "", "print one route's details (by name, e.g. profile/active) and exit")
	flag.Parse()

	routes, err := parseRoutes(filepath.Join(*repo, serverFile))
	if err != nil {
		fmt.Fprintln(os.Stderr, "apidoccheck: parsing routes:", err)
		os.Exit(2)
	}
	docs, err := parseDocs(filepath.Join(*repo, apiDocFile))
	if err != nil {
		fmt.Fprintln(os.Stderr, "apidoccheck: parsing docs:", err)
		os.Exit(2)
	}
	for i := range routes {
		if d, ok := docs[routes[i].Path]; ok {
			routes[i].Doc = d
		}
	}

	if *route != "" {
		name := strings.TrimPrefix(strings.TrimPrefix(*route, "/api/"), "/")
		for _, r := range routes {
			if r.Name == name {
				b, _ := json.MarshalIndent(r, "", "  ")
				fmt.Println(string(b))
				return
			}
		}
		fmt.Fprintf(os.Stderr, "no such route: %s\n", name)
		os.Exit(2)
	}
	if *asJSON {
		b, _ := json.MarshalIndent(routes, "", "  ")
		fmt.Println(string(b))
		return
	}

	baseline, err := loadBaseline(filepath.Join(*repo, baselineFile))
	if err != nil {
		fmt.Fprintln(os.Stderr, "apidoccheck: loading baseline:", err)
		os.Exit(2)
	}

	violations := check(routes, docs, baseline)
	if len(violations) == 0 {
		fmt.Printf("apidoccheck: ok — %d routes, %d documented, %d allowlisted (baseline)\n",
			len(routes), countDocumented(routes), len(baseline))
		return
	}
	fmt.Fprintf(os.Stderr, "apidoccheck: %d violation(s):\n\n", len(violations))
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, "  - "+v)
	}
	fmt.Fprintln(os.Stderr, "\nFix docs/api.md, or — for a deliberately undocumented route —")
	fmt.Fprintln(os.Stderr, "add its name to "+baselineFile+" with a reason.")
	os.Exit(1)
}

func countDocumented(routes []Route) int {
	n := 0
	for _, r := range routes {
		if r.Doc != nil {
			n++
		}
	}
	return n
}

// check returns a sorted list of human-readable violation strings.
func check(routes []Route, docs map[string]*DocEP, baseline map[string]bool) []string {
	var v []string
	routeByPath := map[string]bool{}

	for _, r := range routes {
		routeByPath[r.Path] = true
		doc := docs[r.Path]
		switch {
		case doc == nil && !baseline[r.Name]:
			v = append(v, fmt.Sprintf("UNDOCUMENTED: %s (%s) has no section in %s",
				r.Path, methodList(r.Methods), apiDocFile))
		case doc != nil && baseline[r.Name]:
			v = append(v, fmt.Sprintf("STALE BASELINE: %s is now documented — remove %q from %s",
				r.Path, r.Name, baselineFile))
		}
		if doc == nil {
			continue
		}
		// Method drift: documented method must be in the route's allow-list.
		if len(r.Methods) > 0 {
			allowed := map[string]bool{}
			for _, m := range r.Methods {
				allowed[m] = true
			}
			for _, dm := range doc.Methods {
				if !allowed[dm] {
					v = append(v, fmt.Sprintf("METHOD DRIFT: %s documents %s but the route only allows %s",
						r.Path, dm, methodList(r.Methods)))
				}
			}
		}
		// Auth drift.
		switch doc.Auth {
		case "none":
			if r.Auth {
				v = append(v, fmt.Sprintf("AUTH DRIFT: %s documents `Auth: none` but the route is session-gated", r.Path))
			}
		case "session":
			if !r.Auth {
				v = append(v, fmt.Sprintf("AUTH DRIFT: %s documents `Auth: session` but the route is unauthenticated", r.Path))
			}
		}
	}

	// Phantom docs: a documented /api endpoint with no matching route.
	for path := range docs {
		if !routeByPath[path] {
			v = append(v, fmt.Sprintf("PHANTOM DOC: %s is documented in %s but no such route is registered",
				path, apiDocFile))
		}
	}

	// Baseline entries that don't correspond to any route at all are dead.
	names := map[string]bool{}
	for _, r := range routes {
		names[r.Name] = true
	}
	for name := range baseline {
		if !names[name] {
			v = append(v, fmt.Sprintf("DEAD BASELINE: %q in %s is not a registered route — remove it",
				name, baselineFile))
		}
	}

	sort.Strings(v)
	return v
}

func methodList(m []string) string {
	if len(m) == 0 {
		return "any method"
	}
	return strings.Join(m, ",")
}

// ---- route table (Go AST) --------------------------------------------------

var httpMethod = map[string]string{
	"MethodGet": "GET", "MethodPost": "POST", "MethodPut": "PUT",
	"MethodPatch": "PATCH", "MethodDelete": "DELETE", "MethodHead": "HEAD",
	"MethodOptions": "OPTIONS",
}

func parseRoutes(path string) ([]Route, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var routes []Route
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "registerAPIRoutes" || len(call.Args) != 1 {
			return true
		}
		lit, ok := call.Args[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, el := range lit.Elts {
			rl, ok := el.(*ast.CompositeLit)
			if !ok {
				continue
			}
			routes = append(routes, routeFromLit(rl))
		}
		return true
	})
	if len(routes) == 0 {
		return nil, fmt.Errorf("no registerAPIRoutes([]apiRoute{…}) table found in %s", path)
	}
	for i := range routes {
		routes[i].Path = "/api/" + routes[i].Name
	}
	return routes, nil
}

func routeFromLit(rl *ast.CompositeLit) Route {
	var r Route
	for _, e := range rl.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "name":
			if bl, ok := kv.Value.(*ast.BasicLit); ok {
				r.Name = strings.Trim(bl.Value, `"`)
			}
		case "auth":
			if id, ok := kv.Value.(*ast.Ident); ok {
				r.Auth = id.Name == "true"
			}
		case "handler":
			if sel, ok := kv.Value.(*ast.SelectorExpr); ok {
				r.Handler = sel.Sel.Name
			}
		case "methods":
			if cl, ok := kv.Value.(*ast.CompositeLit); ok {
				for _, me := range cl.Elts {
					if sel, ok := me.(*ast.SelectorExpr); ok {
						if v, ok := httpMethod[sel.Sel.Name]; ok {
							r.Methods = append(r.Methods, v)
						}
					}
				}
			}
		}
	}
	return r
}

// ---- doc parser ------------------------------------------------------------

var (
	epHeader = regexp.MustCompile("^### `([A-Z]+) (/api/[a-zA-Z0-9/_-]+)`")
	authLine = regexp.MustCompile(`^\*\*Auth\*\*:\s*(none|session)`)
)

func parseDocs(path string) (map[string]*DocEP, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()

	eps := map[string]*DocEP{}
	var cur *DocEP
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := epHeader.FindStringSubmatch(line); m != nil {
			method, p := m[1], m[2]
			// /api/v1/* are mirror aliases, not separately documented sections.
			if strings.HasPrefix(p, "/api/v1/") {
				cur = nil
				continue
			}
			ep := eps[p]
			if ep == nil {
				ep = &DocEP{Path: p}
				eps[p] = ep
			}
			ep.Methods = append(ep.Methods, method)
			cur = ep
			continue
		}
		if cur != nil {
			if m := authLine.FindStringSubmatch(line); m != nil {
				cur.Auth = m[1]
			}
		}
	}
	return eps, sc.Err()
}

// ---- baseline --------------------------------------------------------------

func loadBaseline(path string) (map[string]bool, error) {
	out := map[string]bool{}
	fh, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out, sc.Err()
}
