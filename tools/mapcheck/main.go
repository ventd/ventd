// mapcheck verifies that docs/codebase-map.md still references every
// structural surface of the repository. It guards against the most common
// way a hand-maintained map rots: a new surface is added and the map is not
// updated.
//
// It enforces membership only for LOW-CHURN structural surfaces:
//   - cmd/*            (binaries)
//   - internal/*       (top-level packages, i.e. subsystems)
//   - .github/workflows/*.yml
//
// It deliberately does NOT check catalog file counts, test counts, or rule
// counts. Those churn constantly (the hardware-catalog ingest adds board YAML
// on most PRs) and coupling them to the map would fire this check on unrelated
// work. Fast-moving numbers are kept out of the map for the same reason.
//
// Semantic drift (a package's responsibility changes but its name does not) is
// not detectable here and still relies on review.
//
// Exit 0 when every surface is referenced; exit 1 (listing the gaps) otherwise.
// Run from the repo root: go run ./tools/mapcheck
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const defaultMapPath = "docs/codebase-map.md"

func main() {
	repo := "."
	mapPath := defaultMapPath
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--map", "-map":
			i++
			if i < len(args) {
				mapPath = args[i]
			}
		case "--repo", "-repo":
			i++
			if i < len(args) {
				repo = args[i]
			}
		default:
			fmt.Fprintf(os.Stderr, "mapcheck: unknown argument %q\n", args[i])
			os.Exit(2)
		}
	}

	missing, err := check(repo, filepath.Join(repo, mapPath))
	if err != nil {
		fmt.Fprintln(os.Stderr, "mapcheck:", err)
		os.Exit(1)
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "mapcheck: %s does not reference these surfaces:\n", mapPath)
		for _, m := range missing {
			fmt.Fprintf(os.Stderr, "  - %s\n", m)
		}
		fmt.Fprintf(os.Stderr, "Add each to %s (then re-run), or remove the surface.\n", mapPath)
		os.Exit(1)
	}
	fmt.Println("mapcheck: ok")
}

// check returns the sorted list of expected surface tokens that the map at
// mapPath does not reference. A nil/empty slice means the map is complete.
func check(repoRoot, mapPath string) ([]string, error) {
	want, err := surfaces(repoRoot)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(mapPath)
	if err != nil {
		return nil, fmt.Errorf("read map %s: %w", mapPath, err)
	}
	text := string(body)

	var missing []string
	for _, tok := range want {
		if !references(text, tok) {
			missing = append(missing, tok)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

// surfaces enumerates the structural tokens the map must reference.
func surfaces(repoRoot string) ([]string, error) {
	var tokens []string

	for _, base := range []string{"cmd", "internal"} {
		entries, err := os.ReadDir(filepath.Join(repoRoot, base))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", base, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				tokens = append(tokens, base+"/"+e.Name())
			}
		}
	}

	wfDir := filepath.Join(repoRoot, ".github", "workflows")
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		return nil, fmt.Errorf("read workflows: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yml") {
			tokens = append(tokens, e.Name())
		}
	}

	sort.Strings(tokens)
	return tokens, nil
}

// references reports whether text mentions tok as a whole token. The character
// immediately after a match must not continue an identifier, so "cmd/ventd" is
// not satisfied by "cmd/ventd-ipmi", while "internal/web" is still satisfied by
// "internal/web/authpersist" (the next char is '/').
func references(text, tok string) bool {
	from := 0
	for {
		idx := strings.Index(text[from:], tok)
		if idx < 0 {
			return false
		}
		end := from + idx + len(tok)
		if end >= len(text) || !isIdentChar(text[end]) {
			return true
		}
		from = from + idx + len(tok)
	}
}

func isIdentChar(b byte) bool {
	return b == '-' || b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}
