package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// makeMinimalConfigYAML returns a minimal valid YAML config for Parse,
// embedding the supplied sensor + fan paths.
func makeMinimalConfigYAML(sensorPath, sensorChip, fanPWMPath, fanChip string) []byte {
	const tmpl = `
version: 1
poll_interval: 2s
web:
  listen: 127.0.0.1:9999
  password_hash: "$2a$04$abcdefghijklmnopqrstuO3z0fxpYkA1RQvLYbc2UE1tX6dGw.Uyq"
sensors:
  - name: cpu
    type: hwmon
    path: SENSOR_PATH
    chip_name: SENSOR_CHIP
fans:
  - name: f1
    type: hwmon
    pwm_path: FAN_PATH
    chip_name: FAN_CHIP
    min_pwm: 20
    max_pwm: 255
controls:
  - fan: f1
    curve: cpu_curve
curves:
  - name: cpu_curve
    type: linear
    sensor: cpu
    min_temp: 40
    max_temp: 80
    min_pwm: 50
    max_pwm: 200
`
	out := strings.ReplaceAll(tmpl, "SENSOR_PATH", sensorPath)
	out = strings.ReplaceAll(out, "SENSOR_CHIP", sensorChip)
	out = strings.ReplaceAll(out, "FAN_PATH", fanPWMPath)
	out = strings.ReplaceAll(out, "FAN_CHIP", fanChip)
	return []byte(out)
}

// withHwmonRootFS swaps the package-level hwmon root for the duration
// of t and restores it afterwards. Critical for isolating Load tests
// from each other when they share a process.
func withHwmonRootFS(t *testing.T, fsys fs.FS) {
	t.Helper()
	prev := SetHwmonRootFS(fsys)
	t.Cleanup(func() { SetHwmonRootFS(prev) })
}

func TestLoad_RewritesHwmonPathsAcrossRenumber(t *testing.T) {
	// Config was written when nct6687 was at hwmon3; after a reboot
	// the kernel renumbered it to hwmon4. ResolveHwmonPaths must
	// rewrite Sensor.Path and Fan.PWMPath to point at hwmon4 so the
	// daemon writes to the right device.
	withHwmonRootFS(t, fstest.MapFS{
		"hwmon0/name": &fstest.MapFile{Data: []byte("coretemp\n")},
		"hwmon4/name": &fstest.MapFile{Data: []byte("nct6687\n")},
	})

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, makeMinimalConfigYAML(
		"/sys/class/hwmon/hwmon3/temp1_input", "nct6687",
		"/sys/class/hwmon/hwmon3/pwm1", "nct6687",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.Sensors[0].Path, "/sys/class/hwmon/hwmon4/temp1_input"; got != want {
		t.Errorf("Sensor.Path: got %q, want %q", got, want)
	}
	if got, want := cfg.Fans[0].PWMPath, "/sys/class/hwmon/hwmon4/pwm1"; got != want {
		t.Errorf("Fan.PWMPath: got %q, want %q", got, want)
	}
}

func TestLoad_NoChipNameNoOpsBackwardCompat(t *testing.T) {
	// Pre-existing config without ChipName must Load cleanly. Paths
	// stay as written; ResolveHwmonPaths's "empty ChipName ⇒ leave
	// alone" contract preserves backward compatibility for hand-pinned
	// paths (and for upgrades where Save hasn't enriched yet).
	//
	// Use hwmon999 rather than hwmon3 because EnrichChipName reads
	// dirname(path)/name via os.ReadFile against REAL /sys (see
	// resolve_hwmon.go:EnrichChipName — it deliberately bypasses the
	// swappable hwmonRootFS because sysfs paths from /sys/devices/...
	// are absolute and not rooted at the class dir). On hosts whose
	// /sys/class/hwmon/hwmon3 points at a real chip (e.g. a Logitech
	// hidpp_battery on some Linux desktops), the read would succeed
	// and populate ChipName, defeating the no-op assertion. hwmon999
	// is guaranteed absent on any real host.
	withHwmonRootFS(t, fstest.MapFS{
		"hwmon999/name": &fstest.MapFile{Data: []byte("nct6687\n")},
	})

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, makeMinimalConfigYAML(
		"/sys/class/hwmon/hwmon999/temp1_input", "",
		"/sys/class/hwmon/hwmon999/pwm1", "",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.Sensors[0].Path, "/sys/class/hwmon/hwmon999/temp1_input"; got != want {
		t.Errorf("Sensor.Path: got %q, want %q (must not rewrite without ChipName)", got, want)
	}
}

func TestLoad_FailsLoudOnUnknownChip(t *testing.T) {
	// Config asserts ChipName=nct6687 but no such chip is in fsys.
	// Daemon must REFUSE to start rather than write to a stale
	// /sys/class/hwmon/hwmon3/pwm1 (which on the live host might
	// belong to a different chip after BIOS revisions).
	withHwmonRootFS(t, fstest.MapFS{
		"hwmon0/name": &fstest.MapFile{Data: []byte("coretemp\n")},
	})

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, makeMinimalConfigYAML(
		"/sys/class/hwmon/hwmon3/temp1_input", "nct6687",
		"/sys/class/hwmon/hwmon3/pwm1", "nct6687",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Load with unknown chip succeeded; want error")
	} else if !strings.Contains(err.Error(), "no hwmon device with chip_name") {
		t.Errorf("Load error doesn't mention unknown-chip cause: %v", err)
	}
}

func TestLoad_FailsLoudOnAmbiguousChip(t *testing.T) {
	// Two hwmonN entries both named nct6687 (dual-Super-I/O board).
	// ResolveHwmonPaths must refuse to guess; daemon refuses to
	// start with a clear remediation pointer.
	withHwmonRootFS(t, fstest.MapFS{
		"hwmon3/name": &fstest.MapFile{Data: []byte("nct6687\n")},
		"hwmon7/name": &fstest.MapFile{Data: []byte("nct6687\n")},
	})

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, makeMinimalConfigYAML(
		"/sys/class/hwmon/hwmon3/temp1_input", "nct6687",
		"/sys/class/hwmon/hwmon3/pwm1", "nct6687",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Load with ambiguous chip succeeded; want error")
	} else if !strings.Contains(err.Error(), "matches multiple hwmon devices") ||
		!strings.Contains(err.Error(), "hwmon_device") {
		t.Errorf("Load error doesn't surface ambiguity + remediation hint: %v", err)
	}
}

// (Self-heal upgrade scenario — EnrichChipName populates ChipName from
// the live name file at Load when an upgraded config is path-valid —
// is covered in two parts:
//   - TestEnrichChipName_PopulatesMissing exercises the population
//   - TestLoad_NoChipNameNoOpsBackwardCompat exercises the Load path
// Composing them in a single integration test would require a
// production-locked /sys/class/hwmon/* path on the test host, which
// would couple tests to environment.)

func TestLoad_NonExistentConfigSurfacesErrNotExist(t *testing.T) {
	// First-boot path: no config file. Load returns os.ErrNotExist
	// so cmd/ventd's first-boot branch can fall through to creating
	// an empty config.
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "no-such-config.yaml")

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("Load on missing file succeeded; want error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load error doesn't unwrap to os.ErrNotExist: %v", err)
	}
}

func TestSetHwmonRootFS_RoundTrip(t *testing.T) {
	// Setter must return the previous fsys so callers can restore.
	prev := SetHwmonRootFS(fstest.MapFS{"foo": nil})
	defer SetHwmonRootFS(prev)

	got := SetHwmonRootFS(nil)
	if got == nil {
		t.Fatal("SetHwmonRootFS(nil) returned nil prev — should be the fixture")
	}
}
