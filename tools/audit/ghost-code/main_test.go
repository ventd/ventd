package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReceiverTypeName covers the AST node shapes the receiver-type
// extractor supports. The audit grep was wrong for years on
// generic-receiver methods; pinning the supported shapes here means
// a regression in receiverTypeName surfaces as a test failure
// rather than as a silently-skipped method in the audit output.
func TestReceiverTypeName(t *testing.T) {
	// We exercise receiverTypeName indirectly through enumerateMethods
	// — constructing ast.Expr fixtures by hand is enough boilerplate
	// to obscure the assertion. The integration test below covers
	// pointer and value receivers via real-source enumeration.
	tmp := t.TempDir()
	src := `package x
type Foo struct{}
func (f *Foo) PtrMethod() {}
func (f Foo) ValueMethod() {}
type unexported struct{}
func (u *unexported) ShouldBeSkipped() {}  // receiver not exported
func (f *Foo) lowercase() {}               // method not exported
`
	if err := os.WriteFile(filepath.Join(tmp, "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := enumerateMethods([]string{tmp})
	if err != nil {
		t.Fatalf("enumerateMethods: %v", err)
	}
	wantMethods := map[string]bool{"PtrMethod": true, "ValueMethod": true}
	gotMethods := map[string]bool{}
	for _, m := range got {
		gotMethods[m.Method] = true
	}
	for w := range wantMethods {
		if !gotMethods[w] {
			t.Errorf("missing exported method %q in enumeration", w)
		}
	}
	for g := range gotMethods {
		if !wantMethods[g] {
			t.Errorf("unexpected method %q in enumeration (should have been filtered)", g)
		}
	}
}

// TestEnumerateMethodsSkipsTestFiles verifies that *_test.go files
// are NOT walked. The grep that surfaced #1033 used this same
// filename heuristic; if enumerateMethods drifts and starts
// counting test-file declarations as production methods, every
// method declared on a test fixture flips from "ghost candidate" to
// "decoy entry" — silently masking real ghosts.
func TestEnumerateMethodsSkipsTestFiles(t *testing.T) {
	tmp := t.TempDir()
	prod := `package x
type Foo struct{}
func (f *Foo) RealMethod() {}
`
	tst := `package x
type Bar struct{}
func (b *Bar) FixtureMethod() {}
`
	if err := os.WriteFile(filepath.Join(tmp, "x.go"), []byte(prod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "x_test.go"), []byte(tst), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := enumerateMethods([]string{tmp})
	if err != nil {
		t.Fatalf("enumerateMethods: %v", err)
	}
	for _, m := range got {
		if m.Method == "FixtureMethod" {
			t.Fatalf("test-file method %q leaked into production enumeration", m.Method)
		}
	}
	found := false
	for _, m := range got {
		if m.Method == "RealMethod" {
			found = true
		}
	}
	if !found {
		t.Errorf("production method RealMethod not enumerated")
	}
}

// TestBuildCallIndexCountsProductionAndTestSeparately covers the
// separation between production and test call sites. The whole
// point of the audit is "test callers don't count as production
// wiring"; if buildCallIndex regresses on the filename split,
// every test-only method silently passes the zero-prod gate.
func TestBuildCallIndexCountsProductionAndTestSeparately(t *testing.T) {
	tmp := t.TempDir()
	prod := `package x
type C struct{}
func consumer() { var c C; c.Method() }
`
	tst := `package x
import "testing"
func TestMethod(t *testing.T) { var c C; c.Method(); c.Method() }
`
	if err := os.WriteFile(filepath.Join(tmp, "x.go"), []byte(prod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "x_test.go"), []byte(tst), 0o644); err != nil {
		t.Fatal(err)
	}
	prodCounts, testCounts, err := buildCallIndex([]string{tmp})
	if err != nil {
		t.Fatalf("buildCallIndex: %v", err)
	}
	if got := prodCounts["Method"]; got != 1 {
		t.Errorf("prodCounts[Method] = %d, want 1", got)
	}
	if got := testCounts["Method"]; got != 2 {
		t.Errorf("testCounts[Method] = %d, want 2", got)
	}
}

// TestIsFalsePositiveName pins the reflection-driven method names
// the audit filters out implicitly. Adding a method called
// MarshalJSON to a Stringer is fine; adding a brand-new method
// called Marshal would be flagged. A regression that drops
// MarshalJSON from the FP list would noisily flag every Stringer.
func TestIsFalsePositiveName(t *testing.T) {
	expectedFP := []string{
		"MarshalJSON", "UnmarshalJSON",
		"MarshalYAML", "UnmarshalYAML",
		"MarshalText", "UnmarshalText",
		"MarshalBinary", "UnmarshalBinary",
		"String", "Format", "Error", "Unwrap",
		"Read", "Write", "Close", "Open",
	}
	for _, n := range expectedFP {
		if !isFalsePositiveName(n) {
			t.Errorf("isFalsePositiveName(%q) = false, want true", n)
		}
	}
	notFP := []string{"SetDrift", "Observe", "ProbeIPMIPolarity", "WriteFanCurveGated"}
	for _, n := range notFP {
		if isFalsePositiveName(n) {
			t.Errorf("isFalsePositiveName(%q) = true, want false — flagging a real method as FP silently masks ghost code", n)
		}
	}
}

// TestIsTestFixtureType pins the test-fixture prefix matching.
// `Fake*`, `Stub*`, `Mock*` are the conventional Go names for
// scaffolding types. A regression here either flags every fake (noise
// floor rises, real ghosts get drowned) or silently passes them
// through (test fixtures appear in the ghost report).
func TestIsTestFixtureType(t *testing.T) {
	fixtures := []string{"FakeHwmon", "StubReader", "MockBackend", "FakeNVML"}
	for _, n := range fixtures {
		if !isTestFixtureType(n) {
			t.Errorf("isTestFixtureType(%q) = false, want true", n)
		}
	}
	notFixtures := []string{"Estimator", "Aggregator", "Backend", "Library"}
	for _, n := range notFixtures {
		if isTestFixtureType(n) {
			t.Errorf("isTestFixtureType(%q) = true, want false — flagging a real production type masks ghost code on it", n)
		}
	}
}

// TestLoadAllowlistHandlesInlineComments verifies the allowlist
// parser strips trailing `#` comments. The initial version of the
// parser folded the inline comment into the map key, so
// `Clock.Advance  # faketime` became a key matching nothing — the
// filter silently became a no-op. The bug shipped in the first
// iteration of this tool; a real test catches it on the second.
func TestLoadAllowlistHandlesInlineComments(t *testing.T) {
	dir := t.TempDir()
	// Put the allowlist where loadAllowlist looks for it.
	if err := os.MkdirAll(filepath.Join(dir, "tools/audit/ghost-code"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `# Top-level comment
Clock.Advance  # inline rationale should be stripped
DTFake.SetCompatible            # padded inline comment
NoComment.Method
# blank line follows

`
	if err := os.WriteFile(filepath.Join(dir, "tools/audit/ghost-code/allowlist.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// loadAllowlist reads from cwd-relative path; chdir into the
	// tempdir so the call exercises the real path.
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	allow, err := loadAllowlist()
	if err != nil {
		t.Fatalf("loadAllowlist: %v", err)
	}
	expectedKeys := []string{"Clock.Advance", "DTFake.SetCompatible", "NoComment.Method"}
	for _, k := range expectedKeys {
		if !allow[k] {
			t.Errorf("allowlist missing entry %q after parse — keys are: %v", k, keys(allow))
		}
	}
	// Specifically guard against the inline-comment fold regression.
	for k := range allow {
		if strings.Contains(k, "#") {
			t.Errorf("allowlist key %q contains '#' — inline-comment strip regressed", k)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestLoadAllowlistMissingFileIsNotAnError pins the optional-file
// contract. A fresh checkout with no allowlist should run the tool
// successfully (allowlist absent → no entries → every candidate is
// flagged).
func TestLoadAllowlistMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	allow, err := loadAllowlist()
	if err != nil {
		t.Fatalf("loadAllowlist: %v", err)
	}
	if len(allow) != 0 {
		t.Errorf("expected empty allowlist when file absent; got %d entries", len(allow))
	}
}
