package nvml

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/nvidia"
)

// TestNVML_NoCGO verifies RULE-GPU-PR2D-02: no import "C" in non-test source files.
func TestNVML_NoCGO(t *testing.T) {
	dir := filepath.Join(".") // this package directory
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	cgoImport := []byte(`import "C"`)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip test files — they may legitimately mention the string for test purposes.
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(data), string(cgoImport)) {
			t.Errorf("file %s contains CGO import — forbidden in nvml package (RULE-GPU-PR2D-02)", name)
		}
	}
}

// TestNVML_GracefulMissingLib verifies RULE-GPU-PR2D-03: absent library returns
// ErrLibraryUnavailable, no panic, daemon continues.
func TestNVML_GracefulMissingLib(t *testing.T) {
	// Probe whether libnvidia-ml.so.1 is loadable before testing the graceful path.
	// If NVML initialises successfully this test is not applicable in this env.
	err := Open(nil)
	if err == nil {
		// NVML initialised successfully — graceful-missing path can't be tested here.
		// Clean up and skip.
		Close()
		t.Skip("libnvidia-ml.so.1 present and initialised; graceful-missing not testable in this environment")
	}
	// Library absent or init failed — verify the error wraps ErrLibraryUnavailable.
	if !errors.Is(err, nvidia.ErrLibraryUnavailable) && !errors.Is(err, nvidia.ErrInitFailed) {
		t.Errorf("Open with absent lib: want ErrLibraryUnavailable or ErrInitFailed, got: %v", err)
	}
}

// TestNVML_SymbolRegexp guards the RTLD pattern: ensure loader.go references
// nvidia.Init, not a raw purego.Dlopen, to keep the single-loader invariant.
func TestNVML_SymbolRegexp(t *testing.T) {
	data, err := os.ReadFile("loader.go")
	if err != nil {
		t.Fatalf("read loader.go: %v", err)
	}
	if regexp.MustCompile(`purego\.Dlopen`).Match(data) {
		t.Error("loader.go calls purego.Dlopen directly — must delegate to nvidia.Init to avoid double-open")
	}
}
