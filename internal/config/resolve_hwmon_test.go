package config

import (
	"errors"
	"fmt"
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

// TestResolveHwmonPaths_DisambiguateViaHwmonDevice covers the dual-
// nct6687 rig scenario where two distinct drivers (e.g. nct6683 and
// nct6687) load and both expose `name=nct6687` at their hwmonN/name
// attribute. ChipName alone can't disambiguate; the Sensor/Fan's
// configured HwmonDevice (stable /sys/devices/... path) picks the
// right hwmonN by EvalSymlinks equality.
func TestResolveHwmonPaths_DisambiguateViaHwmonDevice(t *testing.T) {
	root := t.TempDir()
	classDir := filepath.Join(root, "class", "hwmon")
	if err := os.MkdirAll(classDir, 0o755); err != nil {
		t.Fatalf("mkdir classDir: %v", err)
	}

	// Two chips, both report name=nct6687, distinct /sys/devices paths.
	type chip struct {
		hwmon, chipName, devicePath string
	}
	chips := []chip{
		{"hwmon5", "nct6687", "devices/platform/nct6683.2592"},
		{"hwmon6", "nct6687", "devices/platform/nct6687.2592"},
	}
	deviceFor := map[string]string{}
	for _, c := range chips {
		hwmonDir := filepath.Join(root, c.devicePath, "hwmon", c.hwmon)
		if err := os.MkdirAll(hwmonDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", hwmonDir, err)
		}
		if err := os.WriteFile(filepath.Join(hwmonDir, "name"), []byte(c.chipName+"\n"), 0o644); err != nil {
			t.Fatalf("write name: %v", err)
		}
		// class entry: symlink to the hwmonN dir
		target, err := filepath.Rel(classDir, hwmonDir)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		if err := os.Symlink(target, filepath.Join(classDir, c.hwmon)); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		// Resolved "device" path is the platform dir one up from hwmon/hwmonN.
		deviceFor[c.hwmon] = filepath.Join(root, c.devicePath)
	}

	// Override the resolver to map hwmonN -> the absolute platform path we
	// just built. Mirrors production: /sys/class/hwmon/hwmonN/device
	// EvalSymlinks to /sys/devices/platform/<chip>.<addr>.
	prev := SetHwmonDevicePathResolver(func(hwmonN string) (string, error) {
		p, ok := deviceFor[hwmonN]
		if !ok {
			return "", fmt.Errorf("unknown hwmon %q", hwmonN)
		}
		return filepath.EvalSymlinks(p)
	})
	t.Cleanup(func() { SetHwmonDevicePathResolver(prev) })

	fsys := os.DirFS(classDir)

	// 1. Multi-match + HwmonDevice pointing at nct6687 resolves to hwmon6.
	cfg := &Config{
		Fans: []Fan{
			{
				Name:        "cpu_fan",
				Type:        "hwmon",
				PWMPath:     "/sys/class/hwmon/hwmon99/pwm1",
				ChipName:    "nct6687",
				HwmonDevice: filepath.Join(root, "devices/platform/nct6687.2592"),
			},
		},
	}
	if err := ResolveHwmonPaths(cfg, fsys); err != nil {
		t.Fatalf("expected successful disambiguation; got %v", err)
	}
	if got, want := cfg.Fans[0].PWMPath, "/sys/class/hwmon/hwmon6/pwm1"; got != want {
		t.Errorf("PWMPath: got %q, want %q", got, want)
	}

	// 2. Multi-match + HwmonDevice pointing at nct6683 resolves to hwmon5.
	cfg.Fans[0].PWMPath = "/sys/class/hwmon/hwmon99/pwm1"
	cfg.Fans[0].HwmonDevice = filepath.Join(root, "devices/platform/nct6683.2592")
	if err := ResolveHwmonPaths(cfg, fsys); err != nil {
		t.Fatalf("expected successful disambiguation; got %v", err)
	}
	if got, want := cfg.Fans[0].PWMPath, "/sys/class/hwmon/hwmon5/pwm1"; got != want {
		t.Errorf("PWMPath (second): got %q, want %q", got, want)
	}

	// 3. Multi-match + empty HwmonDevice errors with a helpful message.
	cfg.Fans[0].HwmonDevice = ""
	cfg.Fans[0].PWMPath = "/sys/class/hwmon/hwmon99/pwm1"
	err := ResolveHwmonPaths(cfg, fsys)
	if err == nil {
		t.Fatal("expected error when HwmonDevice empty on multi-match")
	}
	for _, want := range []string{"multiple", "hwmon_device", "hwmon5", "hwmon6", "nct6687"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q; got %v", want, err)
		}
	}

	// 4. Multi-match + HwmonDevice pointing at a path that doesn't match
	// any candidate errors clearly.
	cfg.Fans[0].HwmonDevice = filepath.Join(root, "devices/platform/nct6999.0000")
	if err := os.MkdirAll(cfg.Fans[0].HwmonDevice, 0o755); err != nil {
		t.Fatalf("mkdir decoy: %v", err)
	}
	cfg.Fans[0].PWMPath = "/sys/class/hwmon/hwmon99/pwm1"
	err = ResolveHwmonPaths(cfg, fsys)
	if err == nil {
		t.Fatal("expected error when HwmonDevice matches no candidate")
	}
	if !strings.Contains(err.Error(), "does not match any") {
		t.Errorf("error should say 'does not match any'; got %v", err)
	}

	// 5. Multi-match + HwmonDevice that does not resolve on this system.
	cfg.Fans[0].HwmonDevice = "/definitely/not/here/platform/ghost.0"
	cfg.Fans[0].PWMPath = "/sys/class/hwmon/hwmon99/pwm1"
	err = ResolveHwmonPaths(cfg, fsys)
	if err == nil {
		t.Fatal("expected error when HwmonDevice unresolvable")
	}
	if !strings.Contains(err.Error(), "not resolvable") {
		t.Errorf("error should say 'not resolvable'; got %v", err)
	}
}

// TestResolveHwmonPaths_SingleMatchEmptyHwmonDevice — when there's
// only one hwmonN for a chip AND hwmon_device is empty, the resolver
// must still return the sole match. This preserves the behaviour of
// configs that pre-date PR #42 (which introduced hwmon_device) and
// haven't been re-saved through the current Save path.
func TestResolveHwmonPaths_SingleMatchEmptyHwmonDevice(t *testing.T) {
	fsys := hwmonFS(map[string]string{"hwmon4": "nct6687"}, nil)
	cfg := &Config{
		Sensors: []Sensor{
			{
				Name:     "cpu",
				Type:     "hwmon",
				Path:     "/sys/class/hwmon/hwmon3/temp1_input",
				ChipName: "nct6687",
			},
		},
	}
	if err := ResolveHwmonPaths(cfg, fsys); err != nil {
		t.Fatalf("single-match + empty HwmonDevice should succeed; got %v", err)
	}
	if got, want := cfg.Sensors[0].Path, "/sys/class/hwmon/hwmon4/temp1_input"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestResolveHwmonPaths_SingleMatchHonoursHwmonDevice covers issue #86:
// on a dual-Super-I/O board (two chips sharing a chip_name) where only
// one chip has enumerated at daemon start, the resolver previously
// returned the sole candidate unconditionally — silently binding every
// fan to the wrong chip for hours. After the fix, a configured
// hwmon_device must be honoured even when there is a single candidate:
//   - matching hwmon_device        → resolve to the sole candidate
//   - mismatched hwmon_device      → error ("does not match any")
func TestResolveHwmonPaths_SingleMatchHonoursHwmonDevice(t *testing.T) {
	root := t.TempDir()
	classDir := filepath.Join(root, "class", "hwmon")
	if err := os.MkdirAll(classDir, 0o755); err != nil {
		t.Fatalf("mkdir classDir: %v", err)
	}

	// Only one chip has enumerated at this point — the nct6683.2592
	// Super-I/O instance. The nct6687.2592 instance the operator
	// configured against has not yet appeared.
	realDir := filepath.Join(root, "devices/platform/nct6683.2592/hwmon/hwmon5")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", realDir, err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "name"), []byte("nct6687\n"), 0o644); err != nil {
		t.Fatalf("write name: %v", err)
	}
	target, err := filepath.Rel(classDir, realDir)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(classDir, "hwmon5")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	enumeratedDevice := filepath.Join(root, "devices/platform/nct6683.2592")
	configuredButMissing := filepath.Join(root, "devices/platform/nct6687.2592")
	if err := os.MkdirAll(configuredButMissing, 0o755); err != nil {
		t.Fatalf("mkdir decoy: %v", err)
	}

	prev := SetHwmonDevicePathResolver(func(hwmonN string) (string, error) {
		if hwmonN == "hwmon5" {
			return filepath.EvalSymlinks(enumeratedDevice)
		}
		return "", fmt.Errorf("unknown hwmon %q", hwmonN)
	})
	t.Cleanup(func() { SetHwmonDevicePathResolver(prev) })

	fsys := os.DirFS(classDir)

	cases := []struct {
		name        string
		hwmonDevice string
		wantErr     bool
		wantErrSub  string
		wantPath    string
	}{
		{
			name:        "matching_hwmon_device_resolves",
			hwmonDevice: enumeratedDevice,
			wantPath:    "/sys/class/hwmon/hwmon5/pwm1",
		},
		{
			name:        "mismatched_hwmon_device_errors",
			hwmonDevice: configuredButMissing,
			wantErr:     true,
			wantErrSub:  "does not match any",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Fans: []Fan{
					{
						Name:        "cpu_fan",
						Type:        "hwmon",
						PWMPath:     "/sys/class/hwmon/hwmon99/pwm1",
						ChipName:    "nct6687",
						HwmonDevice: tc.hwmonDevice,
					},
				},
			}
			err := ResolveHwmonPaths(cfg, fsys)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error (%s); got nil, PWMPath=%q", tc.wantErrSub, cfg.Fans[0].PWMPath)
				}
				if !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("error should contain %q; got %v", tc.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveHwmonPaths: %v", err)
			}
			if got := cfg.Fans[0].PWMPath; got != tc.wantPath {
				t.Errorf("PWMPath: got %q, want %q", got, tc.wantPath)
			}
		})
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
