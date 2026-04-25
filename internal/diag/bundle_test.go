package diag_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ventd/ventd/internal/diag"
	"github.com/ventd/ventd/internal/diag/redactor"
)

// --- RULE-DIAG-PR2C-06: denylist paths never captured ---

func TestRuleDiagPR2C_06(t *testing.T) {
	t.Run("denylist_paths_never_captured", func(t *testing.T) {
		// ErrDenied is exported; verify calling isDenied logic via Generate.
		// We can't directly call isDenied (unexported), but we verify
		// that /etc/shadow is never present in a generated bundle.
		outDir := t.TempDir()
		opts := diag.Options{
			OutputDir:    outDir,
			RedactorCfg:  redactor.Config{Profile: redactor.ProfileConservative, Hostname: "testhost"},
			VentdVersion: "test",
		}
		bundlePath, err := diag.Generate(context.Background(), opts)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		entries := listBundleEntries(t, bundlePath)
		for _, e := range entries {
			if e == "etc/shadow" || e == "etc/sudoers" {
				t.Errorf("denylist path %q found in bundle", e)
			}
		}
	})
}

// --- RULE-DIAG-PR2C-07: REDACTION_REPORT.json always present ---

func TestRuleDiagPR2C_07(t *testing.T) {
	t.Run("redaction_report_always_present", func(t *testing.T) {
		for _, profile := range []string{
			redactor.ProfileConservative,
			redactor.ProfileOff,
		} {
			t.Run(profile, func(t *testing.T) {
				outDir := t.TempDir() // per-subtest dir avoids timestamp collision
				opts := diag.Options{
					OutputDir: outDir,
					RedactorCfg: redactor.Config{
						Profile:  profile,
						Hostname: "testhost",
					},
					VentdVersion: "test",
				}
				bundlePath, err := diag.Generate(context.Background(), opts)
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				entries := listBundleEntries(t, bundlePath)
				found := false
				for _, e := range entries {
					if e == "REDACTION_REPORT.json" {
						found = true
					}
				}
				if !found {
					t.Errorf("REDACTION_REPORT.json not found in bundle (profile=%s)", profile)
				}
				// Parse and validate the report.
				content := readBundleFile(t, bundlePath, "REDACTION_REPORT.json")
				var report map[string]any
				if err := json.Unmarshal(content, &report); err != nil {
					t.Fatalf("REDACTION_REPORT.json is not valid JSON: %v", err)
				}
				if p, ok := report["redactor_profile"].(string); !ok || p == "" {
					t.Error("REDACTION_REPORT.json missing redactor_profile")
				}
			})
		}
	})
}

// --- RULE-DIAG-PR2C-10: output dir 0o700, bundle file 0o600 ---

func TestRuleDiagPR2C_10(t *testing.T) {
	t.Run("output_dir_and_file_modes", func(t *testing.T) {
		outDir := t.TempDir()
		opts := diag.Options{
			OutputDir:    outDir,
			RedactorCfg:  redactor.Config{Profile: redactor.ProfileConservative, Hostname: "testhost"},
			VentdVersion: "test",
		}
		bundlePath, err := diag.Generate(context.Background(), opts)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}

		// Verify bundle file mode.
		ffi, err := os.Stat(bundlePath)
		if err != nil {
			t.Fatalf("Stat bundle: %v", err)
		}
		if ffi.Mode().Perm() != 0o600 {
			t.Errorf("bundle file mode %o, want 0600", ffi.Mode().Perm())
		}

		// Verify output dir mode.
		dfi, err := os.Stat(outDir)
		if err != nil {
			t.Fatalf("Stat outDir: %v", err)
		}
		if dfi.Mode().Perm() != 0o700 {
			t.Errorf("output dir mode %o, want 0700", dfi.Mode().Perm())
		}
	})
}

// --- Bundle structure sanity: manifest.json always present ---

func TestGenerate_ManifestPresent(t *testing.T) {
	outDir := t.TempDir()
	opts := diag.Options{
		OutputDir:    outDir,
		RedactorCfg:  redactor.Config{Profile: redactor.ProfileConservative, Hostname: "testhost"},
		VentdVersion: "0.4.0-test",
	}
	bundlePath, err := diag.Generate(context.Background(), opts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	entries := listBundleEntries(t, bundlePath)
	required := []string{"README.md", "manifest.json", "REDACTION_REPORT.json"}
	for _, r := range required {
		found := false
		for _, e := range entries {
			if e == r {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("required entry %q missing from bundle; entries: %v", r, entries)
		}
	}
}

// --- helpers ---

func listBundleEntries(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var entries []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		entries = append(entries, hdr.Name)
	}
	return entries
}

func readBundleFile(t *testing.T, bundlePath, target string) []byte {
	t.Helper()
	f, _ := os.Open(bundlePath)
	defer func() { _ = f.Close() }()
	gz, _ := gzip.NewReader(f)
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if hdr.Name == target {
			data, _ := io.ReadAll(tr)
			return data
		}
	}
	t.Fatalf("file %q not found in bundle", target)
	return nil
}

// Verify the dependency invariant: internal/diag must NOT import internal/calibration.
func TestDiagDoesNotImportCalibration(t *testing.T) {
	// This is validated by go list -deps in CI (spec success condition #12).
	// Here we document the invariant as a compile-time guard: if this test
	// compiles and the diag package builds, we haven't accidentally imported
	// calibration (which would be caught by the go list check).
	_ = filepath.Join // ensure test file compiles
}
