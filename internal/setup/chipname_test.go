package setup

import (
	"os"
	"path/filepath"
	"testing"
)

// TestChipNameOf exercises the small helper used by the setup config
// writer to populate config.Sensor.ChipName / config.Fan.ChipName from
// the live hwmon name file. The function must:
//   - return the trimmed contents of <dirname(path)>/name on success
//   - return "" for missing/unreadable name files
//   - return "" for an empty input path (defensive)
//   - tolerate trailing whitespace/newlines that some chip drivers
//     write (single TrimSpace pass)
//   - work for both /sys/class/hwmon/hwmonN/* paths and the
//     /sys/devices/.../hwmon/hwmonN/* device-style equivalents (any
//     path whose `dirname()/name` resolves to a readable file)

func TestChipNameOf_ReadsAndTrimsName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "name"),
		[]byte("nct6687\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pwm1"),
		[]byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, want := chipNameOf(filepath.Join(dir, "pwm1")), "nct6687"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestChipNameOf_EmptyPathReturnsEmpty(t *testing.T) {
	if got := chipNameOf(""); got != "" {
		t.Errorf("empty path: got %q, want empty", got)
	}
}

func TestChipNameOf_MissingNameFile(t *testing.T) {
	dir := t.TempDir()
	// Create the path target but no name file alongside it.
	if err := os.WriteFile(filepath.Join(dir, "pwm1"), []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := chipNameOf(filepath.Join(dir, "pwm1")); got != "" {
		t.Errorf("missing name file: got %q, want empty", got)
	}
}

func TestChipNameOf_TrimsSurroundingWhitespace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "name"),
		[]byte("  amdgpu  \n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pwm1"), []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, want := chipNameOf(filepath.Join(dir, "pwm1")), "amdgpu"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestChipNameOf_DeviceStylePath(t *testing.T) {
	// /sys/devices/platform/.../hwmon/hwmonN/ paths must enrich
	// just as cleanly as /sys/class/hwmon/* paths because we use
	// dirname(path)/name uniformly.
	root := t.TempDir()
	devDir := filepath.Join(root, "platform", "nct6687.2592", "hwmon", "hwmon4")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devDir, "name"),
		[]byte("nct6687\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devDir, "pwm3"),
		[]byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, want := chipNameOf(filepath.Join(devDir, "pwm3")), "nct6687"; got != want {
		t.Errorf("device-style path: got %q, want %q", got, want)
	}
}

func TestChipNameOf_PathPointsAtNameDirectly(t *testing.T) {
	// Defensive: if the caller passed the name file itself instead
	// of a sibling like pwm1, dirname(path)/name resolves to the
	// same file. Should still work.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "name"),
		[]byte("it87\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := chipNameOf(filepath.Join(dir, "name")), "it87"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
