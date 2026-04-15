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

// errFS is an fs.FS stub that fails every Open with a fixed error. Used to
// exercise the buildChipMap error-wrap branch without relying on host sysfs.
type errFS struct{ err error }

func (e errFS) Open(name string) (fs.File, error) { return nil, e.err }

// hwmonFS is a convenience builder for MapFS fixtures. Each chipName becomes
// a `hwmonN/name` file containing that chip's name; passing an empty string
// creates the directory (via a placeholder file) but skips the name file so
// buildChipMap exercises its "name unreadable" branch.
func hwmonFS(chips map[string]string, extra fstest.MapFS) fstest.MapFS {
	fs := fstest.MapFS{}
	for hwmon, chip := range chips {
		if chip == "" {
			fs[hwmon+"/.keep"] = &fstest.MapFile{Data: []byte{}}
			continue
		}
		fs[hwmon+"/name"] = &fstest.MapFile{Data: []byte(chip + "\n")}
	}
	for k, v := range extra {
		fs[k] = v
	}
	return fs
}

func TestResolveHwmonPaths_HappyPath(t *testing.T) {
	fsys := hwmonFS(map[string]string{
		"hwmon0": "coretemp",
		"hwmon4": "nct6687",
	}, nil)

	cfg := &Config{
		Sensors: []Sensor{
			{
				Name:     "cpu_temp",
				Type:     "hwmon",
				Path:     "/sys/class/hwmon/hwmon3/temp1_input",
				ChipName: "nct6687",
			},
			{
				Name:     "pkg_temp",
				Type:     "hwmon",
				Path:     "/sys/class/hwmon/hwmon99/temp1_input",
				ChipName: "coretemp",
			},
		},
		Fans: []Fan{
			{
				Name:     "cpu_fan",
				Type:     "hwmon",
				PWMPath:  "/sys/class/hwmon/hwmon3/pwm1",
				RPMPath:  "/sys/class/hwmon/hwmon3/fan1_input",
				ChipName: "nct6687",
			},
		},
	}

	if err := ResolveHwmonPaths(cfg, fsys); err != nil {
		t.Fatalf("ResolveHwmonPaths: %v", err)
	}

	if got, want := cfg.Sensors[0].Path, "/sys/class/hwmon/hwmon4/temp1_input"; got != want {
		t.Errorf("nct6687 sensor path: got %q, want %q", got, want)
	}
	if got, want := cfg.Sensors[1].Path, "/sys/class/hwmon/hwmon0/temp1_input"; got != want {
		t.Errorf("coretemp sensor path: got %q, want %q", got, want)
	}
	if got, want := cfg.Fans[0].PWMPath, "/sys/class/hwmon/hwmon4/pwm1"; got != want {
		t.Errorf("fan PWMPath: got %q, want %q", got, want)
	}
	if got, want := cfg.Fans[0].RPMPath, "/sys/class/hwmon/hwmon4/fan1_input"; got != want {
		t.Errorf("fan RPMPath: got %q, want %q", got, want)
	}
}

func TestResolveHwmonPaths_EmptyChipNameLeftUntouched(t *testing.T) {
	fsys := hwmonFS(map[string]string{"hwmon4": "nct6687"}, nil)

	origSensor := "/sys/class/hwmon/hwmon3/temp1_input"
	origPWM := "/sys/class/hwmon/hwmon3/pwm1"

	cfg := &Config{
		Sensors: []Sensor{{Name: "legacy_temp", Type: "hwmon", Path: origSensor}},
		Fans:    []Fan{{Name: "legacy_fan", Type: "hwmon", PWMPath: origPWM}},
	}

	if err := ResolveHwmonPaths(cfg, fsys); err != nil {
		t.Fatalf("ResolveHwmonPaths: %v", err)
	}
	if cfg.Sensors[0].Path != origSensor {
		t.Errorf("sensor path mutated despite empty ChipName: %q", cfg.Sensors[0].Path)
	}
	if cfg.Fans[0].PWMPath != origPWM {
		t.Errorf("fan PWMPath mutated despite empty ChipName: %q", cfg.Fans[0].PWMPath)
	}
}

func TestResolveHwmonPaths_NvidiaWithChipNameSkipped(t *testing.T) {
	fsys := hwmonFS(map[string]string{"hwmon0": "coretemp"}, nil)

	orig := "0"
	cfg := &Config{
		Sensors: []Sensor{
			// ChipName is set but Type != "hwmon"; must not resolve or error.
			{Name: "gpu_temp", Type: "nvidia", Path: orig, Metric: "temp", ChipName: "coretemp"},
		},
		Fans: []Fan{
			{Name: "gpu_fan", Type: "nvidia", PWMPath: orig, ChipName: "coretemp"},
		},
	}

	if err := ResolveHwmonPaths(cfg, fsys); err != nil {
		t.Fatalf("ResolveHwmonPaths: %v", err)
	}
	if cfg.Sensors[0].Path != orig {
		t.Errorf("nvidia sensor path rewritten: %q", cfg.Sensors[0].Path)
	}
	if cfg.Fans[0].PWMPath != orig {
		t.Errorf("nvidia fan PWMPath rewritten: %q", cfg.Fans[0].PWMPath)
	}
}

func TestResolveHwmonPaths_FanWithoutRPMPath(t *testing.T) {
	fsys := hwmonFS(map[string]string{"hwmon4": "nct6687"}, nil)

	cfg := &Config{
		Fans: []Fan{
			{
				Name:     "open_loop_fan",
				Type:     "hwmon",
				PWMPath:  "/sys/class/hwmon/hwmon3/pwm2",
				ChipName: "nct6687",
			},
		},
	}

	if err := ResolveHwmonPaths(cfg, fsys); err != nil {
		t.Fatalf("ResolveHwmonPaths: %v", err)
	}
	if got, want := cfg.Fans[0].PWMPath, "/sys/class/hwmon/hwmon4/pwm2"; got != want {
		t.Errorf("PWMPath: got %q, want %q", got, want)
	}
	if cfg.Fans[0].RPMPath != "" {
		t.Errorf("empty RPMPath spuriously populated: %q", cfg.Fans[0].RPMPath)
	}
}

func TestResolveHwmonPaths_UnknownChip(t *testing.T) {
	fsys := hwmonFS(map[string]string{"hwmon0": "coretemp"}, nil)

	cfg := &Config{
		Sensors: []Sensor{
			{Name: "mb", Type: "hwmon", Path: "/sys/class/hwmon/hwmon3/temp1_input", ChipName: "nct6687"},
		},
	}

	err := ResolveHwmonPaths(cfg, fsys)
	if err == nil {
		t.Fatal("ResolveHwmonPaths accepted unknown chip_name")
	}
	if !strings.Contains(err.Error(), "nct6687") {
		t.Errorf("error should name missing chip; got %v", err)
	}
	if !strings.Contains(err.Error(), "mb") {
		t.Errorf("error should name the offending sensor; got %v", err)
	}
}

func TestResolveHwmonPaths_AmbiguousChip(t *testing.T) {
	fsys := hwmonFS(map[string]string{
		"hwmon1": "amdgpu",
		"hwmon2": "amdgpu",
	}, nil)

	cfg := &Config{
		Fans: []Fan{
			{
				Name:     "gpu_fan",
				Type:     "hwmon",
				PWMPath:  "/sys/class/hwmon/hwmon1/pwm1",
				ChipName: "amdgpu",
			},
		},
	}

	err := ResolveHwmonPaths(cfg, fsys)
	if err == nil {
		t.Fatal("ResolveHwmonPaths accepted ambiguous chip_name")
	}
	for _, want := range []string{"amdgpu", "hwmon1", "hwmon2", "hwmon_device"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q; got %v", want, err)
		}
	}
}

func TestResolveHwmonPaths_PathWithoutHwmonSegment(t *testing.T) {
	fsys := hwmonFS(map[string]string{"hwmon4": "nct6687"}, nil)

	cfg := &Config{
		Sensors: []Sensor{
			// Path somehow lacks /hwmonN/ — config corruption or a future
			// rewrite that replaced the segment with something else. Must error.
			{Name: "bogus", Type: "hwmon", Path: "/tmp/fake/temp1_input", ChipName: "nct6687"},
		},
	}

	err := ResolveHwmonPaths(cfg, fsys)
	if err == nil {
		t.Fatal("ResolveHwmonPaths accepted path with no /hwmonN segment")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should name the sensor; got %v", err)
	}
}

func TestResolveHwmonPaths_HwmonEntryWithoutName(t *testing.T) {
	// hwmon7 directory exists but has no readable `name` file — buildChipMap
	// must skip it instead of erroring out, so hwmon4 still resolves cleanly.
	fsys := hwmonFS(map[string]string{
		"hwmon4": "nct6687",
		"hwmon7": "",
	}, nil)

	cfg := &Config{
		Sensors: []Sensor{
			{Name: "cpu_temp", Type: "hwmon", Path: "/sys/class/hwmon/hwmon3/temp1_input", ChipName: "nct6687"},
		},
	}

	if err := ResolveHwmonPaths(cfg, fsys); err != nil {
		t.Fatalf("ResolveHwmonPaths: %v", err)
	}
	if got, want := cfg.Sensors[0].Path, "/sys/class/hwmon/hwmon4/temp1_input"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveHwmonPaths_DevicePathStyle(t *testing.T) {
	// Some hwmon paths live under /sys/devices/... via the hwmon_device
	// symlink target rather than under /sys/class/hwmon. The regex must
	// still locate the /hwmonN segment without being confused by the
	// preceding `/hwmon/` directory.
	fsys := hwmonFS(map[string]string{"hwmon4": "nct6687"}, nil)

	cfg := &Config{
		Fans: []Fan{
			{
				Name:     "cpu_fan",
				Type:     "hwmon",
				PWMPath:  "/sys/devices/platform/nct6687.2592/hwmon/hwmon3/pwm1",
				ChipName: "nct6687",
			},
		},
	}

	if err := ResolveHwmonPaths(cfg, fsys); err != nil {
		t.Fatalf("ResolveHwmonPaths: %v", err)
	}
	if got, want := cfg.Fans[0].PWMPath, "/sys/devices/platform/nct6687.2592/hwmon/hwmon4/pwm1"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveHwmonPaths_NilArguments(t *testing.T) {
	fsys := hwmonFS(map[string]string{"hwmon0": "coretemp"}, nil)
	if err := ResolveHwmonPaths(nil, fsys); err == nil {
		t.Error("nil config accepted")
	}
	cfg := &Config{}
	if err := ResolveHwmonPaths(cfg, nil); err == nil {
		t.Error("nil fsys accepted")
	}
}

func TestResolveHwmonPaths_FanPWMPathWithoutHwmonSegment(t *testing.T) {
	fsys := hwmonFS(map[string]string{"hwmon4": "nct6687"}, nil)
	cfg := &Config{
		Fans: []Fan{{Name: "bogus", Type: "hwmon", PWMPath: "/tmp/fake/pwm1", ChipName: "nct6687"}},
	}
	err := ResolveHwmonPaths(cfg, fsys)
	if err == nil {
		t.Fatal("ResolveHwmonPaths accepted bogus PWMPath")
	}
	if !strings.Contains(err.Error(), "pwm_path") {
		t.Errorf("error should mention pwm_path; got %v", err)
	}
}

func TestResolveHwmonPaths_FanRPMPathWithoutHwmonSegment(t *testing.T) {
	fsys := hwmonFS(map[string]string{"hwmon4": "nct6687"}, nil)
	cfg := &Config{
		Fans: []Fan{{
			Name:     "bogus",
			Type:     "hwmon",
			PWMPath:  "/sys/class/hwmon/hwmon3/pwm1",
			RPMPath:  "/tmp/fake/fan1_input",
			ChipName: "nct6687",
		}},
	}
	err := ResolveHwmonPaths(cfg, fsys)
	if err == nil {
		t.Fatal("ResolveHwmonPaths accepted bogus RPMPath")
	}
	if !strings.Contains(err.Error(), "rpm_path") {
		t.Errorf("error should mention rpm_path; got %v", err)
	}
}

func TestResolveHwmonPaths_ReadDirError(t *testing.T) {
	sentinel := errors.New("sysfs gone")
	cfg := &Config{
		Sensors: []Sensor{
			{Name: "cpu", Type: "hwmon", Path: "/sys/class/hwmon/hwmon3/temp1_input", ChipName: "nct6687"},
		},
	}
	err := ResolveHwmonPaths(cfg, errFS{err: sentinel})
	if err == nil {
		t.Fatal("ResolveHwmonPaths swallowed fs ReadDir error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error should wrap sentinel via %%w; got %v", err)
	}
}

// TestResolveHwmonPaths_SymlinkedHwmonEntries exercises the production
// sysfs layout where every `/sys/class/hwmon/hwmonN` is a symlink into
// `/sys/devices/.../hwmon/hwmonN`. DirEntry.IsDir() returns false for
// the symlink (it reads the dirent type, not the target), so any filter
// that skips non-directories breaks buildChipMap on real hardware. The
// existing MapFS-based tests cannot catch this because fstest.MapFS
// synthesizes implicit directory entries whose IsDir() returns true.
//
// Regression guard for the phoenix-MS-7D25 rig failure where every
// hwmon chip (coretemp, nct6687, nvme, …) was invisible to the resolver
// and ventd refused to start with any real config.
func TestResolveHwmonPaths_SymlinkedHwmonEntries(t *testing.T) {
	root := t.TempDir()

	classDir := filepath.Join(root, "class", "hwmon")
	if err := os.MkdirAll(classDir, 0o755); err != nil {
		t.Fatalf("mkdir classDir: %v", err)
	}

	// Mirror the live sysfs layout: real chip directories under
	// devices/.../hwmon/, class entries are symlinks to them.
	type chip struct {
		hwmon, chipName, devicePath string
	}
	chips := []chip{
		{"hwmon0", "acpitz", "devices/virtual/thermal/thermal_zone0"},
		{"hwmon4", "nct6687", "devices/platform/nct6687.2592/hwmon"},
		{"hwmon5", "coretemp", "devices/platform/coretemp.0/hwmon"},
		{"hwmon6", "nct6687", "devices/platform/nct6687.2593/hwmon"},
	}
	for _, c := range chips {
		realDir := filepath.Join(root, c.devicePath, c.hwmon)
		if err := os.MkdirAll(realDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", realDir, err)
		}
		if err := os.WriteFile(filepath.Join(realDir, "name"), []byte(c.chipName+"\n"), 0o644); err != nil {
			t.Fatalf("write name: %v", err)
		}
		// Class entry is a relative symlink, same shape as real
		// /sys/class/hwmon/hwmonN -> ../../devices/...
		target, err := filepath.Rel(classDir, realDir)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		link := filepath.Join(classDir, c.hwmon)
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("symlink %s -> %s: %v", link, target, err)
		}
	}

	// Sanity check: the class dir should list 4 entries, all symlinks.
	entries, err := os.ReadDir(classDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != len(chips) {
		t.Fatalf("fixture built %d entries, want %d", len(entries), len(chips))
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Fatalf("fixture entry %q reports IsDir()=true; symlink regression won't reproduce", e.Name())
		}
	}

	fsys := os.DirFS(classDir)

	cfg := &Config{
		Sensors: []Sensor{
			{Name: "cpu_package", Type: "hwmon", ChipName: "coretemp",
				Path: "/sys/class/hwmon/hwmon99/temp1_input"},
		},
		Fans: []Fan{
			{Name: "cpu_fan", Type: "hwmon", ChipName: "nct6687",
				PWMPath: "/sys/class/hwmon/hwmon99/pwm1"},
		},
	}

	if err := ResolveHwmonPaths(cfg, fsys); err == nil {
		// Ambiguous nct6687 is expected — two chips share the name.
		t.Fatal("ResolveHwmonPaths accepted ambiguous nct6687 without error")
	} else if !strings.Contains(err.Error(), "nct6687") ||
		!strings.Contains(err.Error(), "multiple") {
		t.Fatalf("expected ambiguity error for nct6687; got %v", err)
	}

	// Drop the ambiguous fan, re-run: coretemp should resolve to hwmon5.
	cfg.Fans = nil
	if err := ResolveHwmonPaths(cfg, fsys); err != nil {
		t.Fatalf("ResolveHwmonPaths: %v", err)
	}
	if got, want := cfg.Sensors[0].Path, "/sys/class/hwmon/hwmon5/temp1_input"; got != want {
		t.Errorf("coretemp sensor path: got %q, want %q", got, want)
	}
}

func TestResolveHwmonPaths_IgnoresNonHwmonDirs(t *testing.T) {
	// Exercised branches in buildChipMap:
	//   - `hwmonFoo/name` — non-numeric suffix, skipped (allDigits)
	//   - `README`        — doesn't start with `hwmon`, skipped
	//   - `hwmon9` as a regular file — IsDir() false branch
	//   - `hwmon6/name` whitespace-only — trimmed empty, skipped
	fsys := fstest.MapFS{
		"hwmon4/name":   {Data: []byte("nct6687\n")},
		"hwmonFoo/name": {Data: []byte("bogus\n")},
		"hwmon6/name":   {Data: []byte("   \n")},
		"hwmon9":        {Data: []byte("not a directory")},
		"README":        {Data: []byte("not a hwmon entry")},
	}

	cfg := &Config{
		Sensors: []Sensor{
			{Name: "bogus", Type: "hwmon", Path: "/sys/class/hwmon/hwmon3/temp1_input", ChipName: "bogus"},
		},
	}
	if err := ResolveHwmonPaths(cfg, fsys); err == nil {
		t.Error("ResolveHwmonPaths accepted non-numeric hwmonFoo entry as a match")
	}

	cfg2 := &Config{
		Sensors: []Sensor{
			{Name: "cpu", Type: "hwmon", Path: "/sys/class/hwmon/hwmon3/temp1_input", ChipName: "nct6687"},
		},
	}
	if err := ResolveHwmonPaths(cfg2, fsys); err != nil {
		t.Fatalf("ResolveHwmonPaths: %v", err)
	}
	if got, want := cfg2.Sensors[0].Path, "/sys/class/hwmon/hwmon4/temp1_input"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
