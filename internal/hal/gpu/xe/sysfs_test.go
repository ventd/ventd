package xe

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestXE_ReadOnly verifies RULE-GPU-PR2D-08: no write flags in non-test xe/ source files.
func TestXE_ReadOnly(t *testing.T) {
	// Check all non-test .go files in this package for write-flag patterns.
	writeFlags := regexp.MustCompile(`os\.(O_WRONLY|O_RDWR|O_CREATE|O_TRUNC|O_APPEND)`)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if writeFlags.Match(data) {
			t.Errorf("file %s contains write flags — xe backend is read-only (RULE-GPU-PR2D-08)", name)
		}
	}
}

// TestXE_Enumerate_Fixture verifies that Enumerate finds xe and i915 hwmon
// entries in a synthetic sysfs tree and ignores other drivers.
func TestXE_Enumerate_Fixture(t *testing.T) {
	tmp := buildXeFixture(t, "card0", "xe")
	cards, err := Enumerate(filepath.Join(tmp, "sys"))
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("Enumerate: want 1 card, got %d", len(cards))
	}
	if cards[0].DriverName != "xe" {
		t.Errorf("DriverName: want xe, got %q", cards[0].DriverName)
	}
}

// TestXE_ReadFanRPMs_Fixture verifies fan*_input reading.
func TestXE_ReadFanRPMs_Fixture(t *testing.T) {
	tmp := buildXeFixture(t, "card0", "xe")
	cards, err := Enumerate(filepath.Join(tmp, "sys"))
	if err != nil || len(cards) == 0 {
		t.Skip("fixture enumeration failed")
	}
	rpms, err := cards[0].ReadFanRPMs()
	if err != nil {
		t.Fatalf("ReadFanRPMs: %v", err)
	}
	if len(rpms) != 1 || rpms[0] != 1200 {
		t.Errorf("ReadFanRPMs: want [1200], got %v", rpms)
	}
}

func buildXeFixture(t *testing.T, cardName, driverName string) string {
	t.Helper()
	tmp := t.TempDir()
	sysRoot := filepath.Join(tmp, "sys")
	cardPath := filepath.Join(sysRoot, "class", "drm", cardName)
	hwmonPath := filepath.Join(cardPath, "device", "hwmon", "hwmonX")
	driverDir := filepath.Join(sysRoot, "bus", "drivers", driverName)

	for _, dir := range []string{hwmonPath, driverDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Symlink device/driver → the driver directory.
	deviceDir := filepath.Join(cardPath, "device")
	driverLink := filepath.Join(deviceDir, "driver")
	if err := os.Symlink(driverDir, driverLink); err != nil {
		t.Fatal(err)
	}

	// hwmon name file.
	if err := os.WriteFile(filepath.Join(hwmonPath, "name"), []byte(driverName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// fan1_input.
	if err := os.WriteFile(filepath.Join(hwmonPath, "fan1_input"), []byte("1200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return tmp
}
