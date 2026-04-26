package hwdb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRuleHwdbCapture_01_PendingDirOnly verifies that Capture writes only to
// the supplied pending dir — never to the live catalog path.
// RULE-HWDB-CAPTURE-01.
func TestRuleHwdbCapture_01_PendingDirOnly(t *testing.T) {
	t.Parallel()
	t.Run("TestRuleHwdbCapture_01_PendingDirOnly", func(t *testing.T) {
		t.Parallel()
		run := syntheticCalibrationRun()
		dmi := syntheticDMI()
		cat := syntheticCatalog()
		dir := t.TempDir()

		path, err := Capture(run, dmi, cat, dir)
		if err != nil {
			t.Fatalf("Capture returned unexpected error: %v", err)
		}

		// Written path must be inside the supplied pending dir.
		if !strings.HasPrefix(path, dir) {
			t.Errorf("Capture wrote to %q, want path under %q", path, dir)
		}

		// profiles-v1.yaml (live catalog) must not have been touched.
		profiles, loadErr := LoadEmbedded()
		if loadErr != nil {
			t.Fatalf("LoadEmbedded after Capture: %v", loadErr)
		}
		for _, p := range profiles {
			if p.ID == "community-"+run.DMIFingerprint {
				t.Errorf("Capture wrote ID %q into the live embedded catalog", p.ID)
			}
		}

		// Confirm the file exists and has the expected name.
		want := filepath.Join(dir, run.DMIFingerprint+".yaml")
		if path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("written file not found: %v", statErr)
		}
	})
}

// TestRuleHwdbCapture_02_FailClosedOnAnonymise verifies that Capture returns a
// non-nil error and writes nothing to disk when the anonymiser fails.
// RULE-HWDB-CAPTURE-02.
//
// This test does NOT call t.Parallel() because it modifies atomicAnonymiseFn,
// a package-level atomic, and must complete fully before parallel tests run.
func TestRuleHwdbCapture_02_FailClosedOnAnonymise(t *testing.T) {
	t.Run("TestRuleHwdbCapture_02_FailClosedOnAnonymise", func(t *testing.T) {
		// Inject a failing anonymiser via atomicAnonymiseFn.
		injectedFn := func(*Profile) error {
			return errors.New("injected anonymise failure")
		}
		atomicAnonymiseFn.Store(&injectedFn)
		t.Cleanup(func() {
			restoreFn := func(p *Profile) error { return anonymiseProfile(p) }
			atomicAnonymiseFn.Store(&restoreFn)
		})

		run := syntheticCalibrationRun()
		dmi := syntheticDMI()
		cat := syntheticCatalog()
		dir := t.TempDir()

		_, err := Capture(run, dmi, cat, dir)
		if err == nil {
			t.Fatal("Capture should return error when anonymiser fails")
		}

		// Nothing must have been written.
		entries, rdErr := os.ReadDir(dir)
		if rdErr != nil {
			t.Fatalf("ReadDir: %v", rdErr)
		}
		if len(entries) != 0 {
			t.Errorf("Capture wrote %d files despite anonymise failure; expected none", len(entries))
		}
	})
}

// TestRuleHwdbCapture_03_AllowlistedFieldsOnly verifies that the file written
// by Capture can be loaded via Load() with KnownFields(true), meaning no
// field outside the v1 schema allowlist is present.
// RULE-HWDB-CAPTURE-03.
func TestRuleHwdbCapture_03_AllowlistedFieldsOnly(t *testing.T) {
	t.Parallel()
	t.Run("TestRuleHwdbCapture_03_AllowlistedFieldsOnly", func(t *testing.T) {
		t.Parallel()
		run := syntheticCalibrationRun()
		dmi := syntheticDMI()
		cat := syntheticCatalog()
		dir := t.TempDir()

		path, err := Capture(run, dmi, cat, dir)
		if err != nil {
			t.Fatalf("Capture: %v", err)
		}

		f, openErr := os.Open(path)
		if openErr != nil {
			t.Fatalf("open captured file: %v", openErr)
		}
		defer func() { _ = f.Close() }()

		// Load uses KnownFields(true); any unknown field causes a schema error.
		profiles, loadErr := Load(f)
		if loadErr != nil {
			t.Errorf("Load failed on captured profile — unknown field or schema violation: %v", loadErr)
		}
		if len(profiles) != 1 {
			t.Errorf("expected 1 profile, got %d", len(profiles))
			return
		}

		p := profiles[0]
		if p.ContributedBy != "anonymous" {
			t.Errorf("contributed_by = %q, want %q", p.ContributedBy, "anonymous")
		}
		if p.Verified {
			t.Error("verified should be false for a captured profile")
		}
		if p.SchemaVersion != 1 {
			t.Errorf("schema_version = %d, want 1", p.SchemaVersion)
		}
	})
}

// TestCapture_RejectsNoChannels verifies that Capture returns an error when
// the calibration run has no channels (cannot determine driver module).
func TestCapture_RejectsNoChannels(t *testing.T) {
	t.Parallel()
	run := &CalibrationRun{
		SchemaVersion:  1,
		DMIFingerprint: "a1b2c3d4e5f6a7b8",
		BIOSVersion:    "BIOS v1.0",
		CalibratedAt:   time.Now(),
		Channels:       nil,
	}
	_, err := Capture(run, syntheticDMI(), syntheticCatalog(), t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty channels; got nil")
	}
}

// TestCapture_AtomicWrite verifies the .tmp-then-rename pattern: the final
// path exists and the .tmp file is absent after a successful write.
func TestCapture_AtomicWrite(t *testing.T) {
	t.Parallel()
	run := syntheticCalibrationRun()
	dir := t.TempDir()
	path, err := Capture(run, syntheticDMI(), syntheticCatalog(), dir)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("final path missing: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp file should not exist after successful write; stat err: %v", err)
	}
}

// TestCapture_OverwritesSameFingerprint verifies that a second Capture for
// the same DMI fingerprint overwrites the first file.
func TestCapture_OverwritesSameFingerprint(t *testing.T) {
	t.Parallel()
	run := syntheticCalibrationRun()
	dir := t.TempDir()

	path1, err := Capture(run, syntheticDMI(), syntheticCatalog(), dir)
	if err != nil {
		t.Fatalf("first Capture: %v", err)
	}
	path2, err := Capture(run, syntheticDMI(), syntheticCatalog(), dir)
	if err != nil {
		t.Fatalf("second Capture: %v", err)
	}
	if path1 != path2 {
		t.Errorf("second Capture wrote to a different path: %q vs %q", path1, path2)
	}
}

// --- helpers ---

func syntheticCalibrationRun() *CalibrationRun {
	return &CalibrationRun{
		SchemaVersion:  1,
		DMIFingerprint: "a1b2c3d4e5f6a7b8",
		BIOSVersion:    "BIOS v1.2.3",
		CalibratedAt:   time.Now().UTC().Truncate(time.Second),
		Channels: []ChannelCalibration{
			{HwmonName: "nct6775", ChannelIndex: 1},
			{HwmonName: "nct6775", ChannelIndex: 2},
		},
	}
}

func syntheticDMI() DMI {
	return DMI{
		SysVendor:    "Test Vendor Inc.",
		ProductName:  "Test Product",
		BoardVendor:  "Test Board Vendor",
		BoardName:    "Test Board",
		BoardVersion: "Rev 1.0",
		CPUModelName: "Intel Core i7",
		CPUCoreCount: 8,
	}
}

func syntheticCatalog() *Catalog {
	return &Catalog{
		Drivers: map[string]*DriverProfile{},
		Chips:   map[string]*ChipProfile{},
		Boards:  nil,
	}
}
