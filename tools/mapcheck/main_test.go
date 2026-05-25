package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeTree materialises a minimal repo: the given cmd/internal package dirs,
// workflow files, and a codebase-map.md with the supplied body.
func writeTree(t *testing.T, cmds, internals, workflows []string, mapBody string) string {
	t.Helper()
	root := t.TempDir()
	// The base dirs always exist in the real repo; create them unconditionally
	// so an empty surface list does not look like a missing directory.
	mustMkdir(t, filepath.Join(root, "cmd"))
	mustMkdir(t, filepath.Join(root, "internal"))
	wfDir := filepath.Join(root, ".github", "workflows")
	mustMkdir(t, wfDir)
	for _, c := range cmds {
		mustMkdir(t, filepath.Join(root, "cmd", c))
	}
	for _, p := range internals {
		mustMkdir(t, filepath.Join(root, "internal", p))
	}
	for _, w := range workflows {
		mustWrite(t, filepath.Join(wfDir, w), "name: x\n")
	}
	mustMkdir(t, filepath.Join(root, "docs"))
	mustWrite(t, filepath.Join(root, "docs", "codebase-map.md"), mapBody)
	return root
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func runCheck(t *testing.T, root string) []string {
	t.Helper()
	missing, err := check(root, filepath.Join(root, "docs", "codebase-map.md"))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return missing
}

func TestCheck_Complete(t *testing.T) {
	root := writeTree(t,
		[]string{"ventd", "ventd-recover"},
		[]string{"web", "hwmon"},
		[]string{"ci.yml", "meta-lint.yml"},
		"refs cmd/ventd cmd/ventd-recover internal/web internal/hwmon ci.yml meta-lint.yml\n")
	if got := runCheck(t, root); len(got) != 0 {
		t.Fatalf("expected complete, missing: %v", got)
	}
}

func TestCheck_MissingEachSurfaceType(t *testing.T) {
	root := writeTree(t,
		[]string{"ventd", "ventd-recover"},
		[]string{"web", "hwmon"},
		[]string{"ci.yml", "meta-lint.yml"},
		// omits cmd/ventd-recover, internal/hwmon, meta-lint.yml
		"refs cmd/ventd internal/web ci.yml\n")
	want := []string{"cmd/ventd-recover", "internal/hwmon", "meta-lint.yml"}
	got := runCheck(t, root)
	if !slices.Equal(got, want) {
		t.Fatalf("missing mismatch:\n got %v\nwant %v", got, want)
	}
}

// A prefixed sibling must not satisfy a shorter token: documenting
// cmd/ventd-ipmi alone does not cover cmd/ventd.
func TestCheck_PrefixDoesNotSatisfy(t *testing.T) {
	root := writeTree(t,
		[]string{"ventd", "ventd-ipmi"},
		nil, nil,
		"only mentions cmd/ventd-ipmi here\n")
	got := runCheck(t, root)
	if !slices.Contains(got, "cmd/ventd") {
		t.Fatalf("expected cmd/ventd flagged missing, got %v", got)
	}
	if slices.Contains(got, "cmd/ventd-ipmi") {
		t.Fatalf("cmd/ventd-ipmi should be satisfied, got %v", got)
	}
}

// A sub-package mention (next char '/') satisfies the parent package token.
func TestReferences_SlashBoundarySatisfiesParent(t *testing.T) {
	if !references("see internal/web/authpersist for auth", "internal/web") {
		t.Fatal("internal/web should be satisfied by internal/web/authpersist")
	}
	if references("see cmd/ventd-recover only", "cmd/ventd") {
		t.Fatal("cmd/ventd must not be satisfied by cmd/ventd-recover")
	}
	if !references("token at end internal/web", "internal/web") {
		t.Fatal("token at end-of-string should match")
	}
}
