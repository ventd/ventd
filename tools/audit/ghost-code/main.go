// ghost-code — surfaces exported methods on exported types in the
// ventd module that have zero production callers.
//
// Motivation: issue #1033 ("smart-mode Layer-B + Layer-C have no
// production data feed") and #1035 (11 additional smart-mode wiring
// gaps) and #1037 (polarity wiring dead in production) are all the
// same class of bug: a method with rule-bound test coverage but no
// production call site. The senior review at v0.5.26 missed all 13
// of them because the rule catalogue + lifecycle wiring tests looked
// like proof of correctness. They aren't.
//
// This tool runs the mechanical sweep described in
// (audit note in git history) as a CI gate. On
// every release branch we expect zero new ghost methods; an
// allowlist `tools/audit/ghost-code/allowlist.txt` captures the
// false positives + known-deferred items so the gate fires only on
// regressions.
//
// Usage:
//
//	go run ./tools/audit/ghost-code            # surface all candidates
//	go run ./tools/audit/ghost-code -strict    # exit 1 on any new ghost
//	go run ./tools/audit/ghost-code -json      # machine-readable output
//
// The tool is deliberately scope-limited: it uses go/ast to enumerate
// method declarations and the standard "go list -deps" tooling to
// resolve packages — no SSA-based call graph (that would catch
// interface-dispatch cases too, but is significantly slower and a
// larger maintenance surface). The mechanical regex grep below has
// the same recall as the audit doc's reference snippet; we accept
// the same false-positive set (interface dispatch, function values,
// reflection-driven methods).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	// scanDirs lists the directories the tool walks. Test files
	// (*_test.go) are excluded; tools/ is excluded because tooling
	// itself is the wrong audit target (audit-the-auditor reviews
	// belong elsewhere).
	scanDirs = "internal,cmd"
)

// methodDecl is one exported method on an exported receiver type.
type methodDecl struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Receiver string `json:"receiver"`
	Method   string `json:"method"`
}

// ghost is a method with zero non-test call sites.
type ghost struct {
	methodDecl
	TestCallers int `json:"test_callers"`
}

func main() {
	strict := flag.Bool("strict", false, "exit 1 when any ghost outside the allowlist is found")
	jsonOut := flag.Bool("json", false, "emit JSON instead of human-readable text")
	flag.Parse()

	roots := strings.Split(scanDirs, ",")

	// Phase 1: enumerate every exported method on an exported type.
	methods, err := enumerateMethods(roots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enumerate: %v\n", err)
		os.Exit(2)
	}

	// Phase 2: build a production-only call-site index. The regex
	// `\.MethodName\(` is the same shape as the audit doc's manual
	// sweep — captures `obj.M(args)`, `pkg.Type{}.M(args)`, chained
	// calls. Misses dynamic dispatch via function values (rare in
	// this codebase).
	prodCalls, testCalls, err := buildCallIndex(roots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "index: %v\n", err)
		os.Exit(2)
	}

	// Phase 3: filter for zero-prod methods, after subtracting
	// reflection-driven names + obvious test fixtures.
	ghosts := []ghost{}
	for _, m := range methods {
		if isFalsePositiveName(m.Method) {
			continue
		}
		if isTestFixtureType(m.Receiver) {
			continue
		}
		prod := prodCalls[m.Method]
		if prod > 0 {
			continue
		}
		ghosts = append(ghosts, ghost{methodDecl: m, TestCallers: testCalls[m.Method]})
	}

	// Phase 4: filter against the allowlist. The allowlist file is
	// a newline-separated list of "Receiver.Method" entries — known
	// false positives or known-deferred items the audit team has
	// triaged and accepted.
	allow, err := loadAllowlist()
	if err != nil {
		fmt.Fprintf(os.Stderr, "allowlist: %v\n", err)
		os.Exit(2)
	}
	flagged := []ghost{}
	for _, g := range ghosts {
		key := g.Receiver + "." + g.Method
		if allow[key] {
			continue
		}
		flagged = append(flagged, g)
	}

	// Phase 5: output.
	sort.Slice(flagged, func(i, j int) bool {
		if flagged[i].Receiver != flagged[j].Receiver {
			return flagged[i].Receiver < flagged[j].Receiver
		}
		return flagged[i].Method < flagged[j].Method
	})

	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"total_methods":   len(methods),
			"total_ghosts":    len(ghosts),
			"after_allowlist": len(flagged),
			"flagged":         flagged,
		})
	} else {
		fmt.Printf("ghost-code: scanned %d exported method declarations across %s/\n",
			len(methods), scanDirs)
		fmt.Printf("ghost-code: %d zero-prod-caller methods before allowlist; %d after\n",
			len(ghosts), len(flagged))
		if len(flagged) > 0 {
			fmt.Println("ghost-code: methods with no production caller (review for ghost-code class bugs):")
			for _, g := range flagged {
				fmt.Printf("  %s:%d\t%s.%s\t(tests=%d)\n",
					g.File, g.Line, g.Receiver, g.Method, g.TestCallers)
			}
		}
	}

	if *strict && len(flagged) > 0 {
		fmt.Fprintf(os.Stderr, "ghost-code: %d new candidates outside the allowlist; failing in -strict mode\n",
			len(flagged))
		os.Exit(1)
	}
}

// enumerateMethods walks the given roots and returns every exported
// method declaration on an exported receiver type. Test files are
// excluded by filename (suffix `_test.go`).
func enumerateMethods(roots []string) ([]methodDecl, error) {
	var out []methodDecl
	fset := token.NewFileSet()
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				// Parse errors aren't audit-relevant; skip the file.
				return nil
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
					continue
				}
				if !ast.IsExported(fn.Name.Name) {
					continue
				}
				recvType := receiverTypeName(fn.Recv.List[0].Type)
				if recvType == "" || !ast.IsExported(recvType) {
					continue
				}
				pos := fset.Position(fn.Pos())
				out = append(out, methodDecl{
					File:     pos.Filename,
					Line:     pos.Line,
					Receiver: recvType,
					Method:   fn.Name.Name,
				})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// receiverTypeName extracts the type name from a function receiver
// expression. Handles `T` and `*T`; returns "" for more exotic
// expressions (which the regex grep wouldn't pick up either).
func receiverTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// methodCallRegex matches `\.Method(` for any Method that begins
// with an uppercase letter. The capture group extracts the method
// name; the trailing `(` distinguishes calls from field reads.
var methodCallRegex = regexp.MustCompile(`\.([A-Z][A-Za-z0-9_]*)\(`)

// buildCallIndex walks the same roots and counts occurrences of
// `.MethodName(` across every non-test and test file separately.
// Returns (productionCounts, testCounts) keyed by method name.
//
// The counts are per-occurrence, not per-file — a method called
// three times in one file counts as three. This matches the audit
// doc's mechanical grep and is the right signal for "is this method
// reachable at all" rather than "from how many files".
func buildCallIndex(roots []string) (prod, test map[string]int, err error) {
	prod = map[string]int{}
	test = map[string]int{}
	for _, root := range roots {
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			isTest := strings.HasSuffix(path, "_test.go")
			matches := methodCallRegex.FindAllSubmatch(data, -1)
			for _, m := range matches {
				name := string(m[1])
				if isTest {
					test[name]++
				} else {
					prod[name]++
				}
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return prod, test, nil
}

// isFalsePositiveName matches reflection-driven method names that
// have implicit callers in Go's standard library. encoding/json
// invokes MarshalJSON/UnmarshalJSON via reflection; fmt invokes
// String/Format; errors.Unwrap walks via the Unwrap interface.
func isFalsePositiveName(name string) bool {
	switch name {
	case "MarshalJSON", "UnmarshalJSON",
		"MarshalYAML", "UnmarshalYAML",
		"MarshalText", "UnmarshalText",
		"MarshalBinary", "UnmarshalBinary",
		"String", "Format", "Error", "Unwrap",
		"Read", "Write", "Close", "Open":
		// Generic interface-method names — too common to mechanically
		// distinguish ghost from interface implementation. Manual
		// triage applies; allowlist captures the verified-fine ones.
		return true
	}
	return false
}

// isTestFixtureType matches receiver-type names that signal "this
// is a test scaffolding type" — `Fake*`, `Stub*`, `Mock*`. The
// methods on these types ARE used (in tests); the regex pattern
// just can't see test callers when filtering for production.
func isTestFixtureType(name string) bool {
	switch {
	case strings.HasPrefix(name, "Fake"):
		return true
	case strings.HasPrefix(name, "Stub"):
		return true
	case strings.HasPrefix(name, "Mock"):
		return true
	}
	return false
}

// loadAllowlist reads tools/audit/ghost-code/allowlist.txt. The
// file format is one entry per line, "Receiver.Method", with `#`
// comments and blank lines ignored. Missing file is not an error
// — the allowlist is optional.
func loadAllowlist() (map[string]bool, error) {
	out := map[string]bool{}
	data, err := os.ReadFile("tools/audit/ghost-code/allowlist.txt")
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		// Strip trailing inline comments — the allowlist file
		// invites them for verification rationale, and the parser
		// shouldn't fold the comment into the key.
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out[line] = true
	}
	return out, nil
}
