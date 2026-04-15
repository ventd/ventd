package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

// makeConfigYAMLWithHwmonDevice returns a minimal valid YAML config
// whose single fan entry carries both chip_name and hwmon_device, so
// Load() drives the lookupChip code path that can fail with
// ErrHwmonDeviceNotReady under a cold-boot udev race.
func makeConfigYAMLWithHwmonDevice(fanPWMPath, fanChip, fanHwmonDevice string) []byte {
	const tmpl = `
version: 1
poll_interval: 2s
web:
  listen: 127.0.0.1:9999
  password_hash: "$2a$04$abcdefghijklmnopqrstuO3z0fxpYkA1RQvLYbc2UE1tX6dGw.Uyq"
sensors:
  - name: cpu
    type: nvidia
    path: "0"
fans:
  - name: f1
    type: hwmon
    pwm_path: FAN_PATH
    chip_name: FAN_CHIP
    hwmon_device: FAN_DEVICE
    min_pwm: 20
    max_pwm: 255
controls:
  - fan: f1
    curve: cpu_curve
curves:
  - name: cpu_curve
    type: fixed
    value: 128
`
	out := strings.ReplaceAll(tmpl, "FAN_PATH", fanPWMPath)
	out = strings.ReplaceAll(out, "FAN_CHIP", fanChip)
	out = strings.ReplaceAll(out, "FAN_DEVICE", fanHwmonDevice)
	return []byte(out)
}

// TestReproLoadUnderUdevRace reproduces issue #103. Simulates cold
// boot where only one Super-I/O chip has enumerated and the
// configured hwmon_device points at a not-yet-created /sys/devices
// path. Before the fix, Load()'s error chain wrapped os.ErrNotExist
// via EvalSymlinks, and cmd/ventd used errors.Is(err, os.ErrNotExist)
// to detect missing-config — silently entering first-boot mode. The
// fix breaks that chain at lookupChip by wrapping ENOENT into
// ErrHwmonDeviceNotReady instead.
func TestReproLoadUnderUdevRace(t *testing.T) {
	// Only the wrong-but-same-chip_name chip has enumerated. Config
	// disambiguates via hwmon_device to a path that does not exist on
	// this test host (real EvalSymlinks, no override needed — the path
	// is under /sys/devices/platform/<fake>). Mirrors phoenix-MS-7D25's
	// cold-boot state on 2026-04-16 09:07.
	withHwmonRootFS(t, fstest.MapFS{
		"hwmon5/name": &fstest.MapFile{Data: []byte("nct6687\n")},
	})

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	missingDevice := "/sys/devices/platform/ventd-test-nct6687-not-yet-enumerated-103"
	if err := os.WriteFile(cfgPath, makeConfigYAMLWithHwmonDevice(
		"/sys/class/hwmon/hwmon5/pwm1", "nct6687", missingDevice,
	), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("Load unexpectedly succeeded despite unresolvable hwmon_device; cold-boot race would silently bind to wrong chip")
	}
	// Post-fix invariant (#103): Load()'s error for a hwmon race must
	// NOT wrap os.ErrNotExist. Before the fix, this assertion failed
	// because EvalSymlinks's ENOENT propagated through three layers of
	// %w-wrapping; cmd/ventd's errors.Is(err, os.ErrNotExist) then
	// matched and silently dropped the daemon into first-boot mode.
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load error wraps os.ErrNotExist, indistinguishable from a missing config file — cmd/ventd would silently enter first-boot mode. err = %v", err)
	}
	// The transient-race sentinel MUST be reachable via errors.Is so
	// LoadForStartup can detect it for retry. If this breaks, the
	// retry loop cannot distinguish "udev still running" from a
	// permanent misconfiguration.
	if !errors.Is(err, ErrHwmonDeviceNotReady) {
		t.Errorf("Load error should wrap ErrHwmonDeviceNotReady for retry detection; got %v", err)
	}
}

func TestLoadForStartup_FirstBootOnMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "does-not-exist.yaml")

	cfg, firstBoot, err := LoadForStartup(cfgPath, StartupOptions{})
	if err != nil {
		t.Fatalf("LoadForStartup on missing file: err=%v; want nil", err)
	}
	if !firstBoot {
		t.Fatal("LoadForStartup on missing file: firstBoot=false; want true")
	}
	if cfg == nil {
		t.Fatal("LoadForStartup on missing file: cfg=nil; want Empty()")
	}
}

func TestLoadForStartup_StartupErrorOnUnparseable(t *testing.T) {
	// A config that exists but cannot be YAML-parsed must NOT be
	// treated as first-boot. The setup-wizard guards one entry point;
	// crashlooping is the right answer for a corrupted config because
	// the operator needs to fix it, not have their existing state
	// silently wiped.
	withHwmonRootFS(t, fstest.MapFS{})

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("this: is: not: valid: yaml:\n  -  - nested"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, firstBoot, err := LoadForStartup(cfgPath, StartupOptions{})
	if err == nil {
		t.Fatal("LoadForStartup on garbage YAML: err=nil; want parse error")
	}
	if firstBoot {
		t.Error("LoadForStartup on garbage YAML signalled first-boot; should surface parse error instead")
	}
}

func TestLoadForStartup_NoRetryOnPermanentError(t *testing.T) {
	// A config that references a chip_name never seen on this host is
	// a permanent misconfiguration, not a udev race — must fail
	// immediately rather than burn the full retry budget.
	withHwmonRootFS(t, fstest.MapFS{
		"hwmon0/name": &fstest.MapFile{Data: []byte("coretemp\n")},
	})

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, makeConfigYAMLWithHwmonDevice(
		"/sys/class/hwmon/hwmon0/pwm1", "nct6687", "",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, firstBoot, err := LoadForStartup(cfgPath, StartupOptions{
		Timeout:      500 * time.Millisecond,
		PollInterval: 50 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("LoadForStartup on unknown-chip config: err=nil; want permanent resolver error")
	}
	if firstBoot {
		t.Error("LoadForStartup on unknown-chip config signalled first-boot; should surface resolver error")
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("permanent error should not have been retried; elapsed=%s, timeout=500ms", elapsed)
	}
}

// TestLoadForStartup_RetryEventuallySucceeds simulates the real
// cold-boot flow: at t=0 only the wrong-chip (first Super-I/O) has
// enumerated, and the configured hwmon_device platform path does not
// exist yet. Partway through the retry window, udev finishes the
// second-chip enumeration: its /sys/devices/platform/<configured>
// dir appears along with a new hwmon6 class entry. LoadForStartup's
// next tick resolves cleanly.
func TestLoadForStartup_RetryEventuallySucceeds(t *testing.T) {
	tmpDir := t.TempDir()

	// Build a real /sys-like directory tree the resolver can drive.
	// Using os.DirFS and on-disk files lets us extend the tree
	// mid-test to simulate udev completing enumeration.
	root := t.TempDir()
	classDir := filepath.Join(root, "class", "hwmon")
	if err := os.MkdirAll(classDir, 0o755); err != nil {
		t.Fatalf("mkdir classDir: %v", err)
	}
	// hwmon5 corresponds to the first-chip-to-enumerate (nct6683
	// platform, whose hwmon name file says "nct6687" — same-name
	// collision that motivates hwmon_device disambiguation).
	firstChipHwmonDir := filepath.Join(root, "devices/platform/nct6683.2592/hwmon/hwmon5")
	if err := os.MkdirAll(firstChipHwmonDir, 0o755); err != nil {
		t.Fatalf("mkdir first chip hwmon: %v", err)
	}
	if err := os.WriteFile(filepath.Join(firstChipHwmonDir, "name"), []byte("nct6687\n"), 0o644); err != nil {
		t.Fatalf("write hwmon5 name: %v", err)
	}
	if err := os.Symlink(firstChipHwmonDir, filepath.Join(classDir, "hwmon5")); err != nil {
		t.Fatalf("symlink hwmon5: %v", err)
	}

	// The second chip's platform dir is the hwmon_device the operator
	// configured. It does not exist yet — we create it mid-retry.
	secondChipDevice := filepath.Join(root, "devices/platform/nct6687.2592")
	secondChipHwmonDir := filepath.Join(secondChipDevice, "hwmon/hwmon6")

	// Route candidate hwmon5/hwmon6 to their real platform paths, the
	// way production's EvalSymlinks does. hwmon6 may not exist yet at
	// the time the resolver calls this — the returned error is fine,
	// lookupChip skips unresolvable candidates.
	prev := SetHwmonDevicePathResolver(func(hwmonN string) (string, error) {
		switch hwmonN {
		case "hwmon5":
			return filepath.EvalSymlinks(filepath.Join(root, "devices/platform/nct6683.2592"))
		case "hwmon6":
			return filepath.EvalSymlinks(secondChipDevice)
		}
		return "", errors.New("unknown hwmon " + hwmonN)
	})
	t.Cleanup(func() { SetHwmonDevicePathResolver(prev) })

	prevFS := SetHwmonRootFS(os.DirFS(classDir))
	t.Cleanup(func() { SetHwmonRootFS(prevFS) })

	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, makeConfigYAMLWithHwmonDevice(
		"/sys/class/hwmon/hwmon6/pwm1", "nct6687", secondChipDevice,
	), 0o600); err != nil {
		t.Fatal(err)
	}

	// Schedule the second chip to complete enumeration ~60ms in, well
	// within a 500ms retry budget at 25ms poll interval. We create
	// the platform dir, the hwmon6 subdir with a name file, and the
	// class symlink — the same three things udev creates.
	var scheduled atomic.Bool
	go func() {
		time.Sleep(60 * time.Millisecond)
		if err := os.MkdirAll(secondChipHwmonDir, 0o755); err != nil {
			t.Logf("mkdir secondChipHwmonDir: %v", err)
			return
		}
		if err := os.WriteFile(filepath.Join(secondChipHwmonDir, "name"), []byte("nct6687\n"), 0o644); err != nil {
			t.Logf("write hwmon6 name: %v", err)
			return
		}
		if err := os.Symlink(secondChipHwmonDir, filepath.Join(classDir, "hwmon6")); err != nil {
			t.Logf("symlink hwmon6: %v", err)
			return
		}
		scheduled.Store(true)
	}()

	cfg, firstBoot, err := LoadForStartup(cfgPath, StartupOptions{
		Timeout:      500 * time.Millisecond,
		PollInterval: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("LoadForStartup should have succeeded once udev completed; err=%v (scheduled=%v)", err, scheduled.Load())
	}
	if firstBoot {
		t.Error("LoadForStartup returned firstBoot=true after retry success; want false")
	}
	if cfg == nil {
		t.Fatal("LoadForStartup returned nil cfg after successful retry")
	}
	if got, want := cfg.Fans[0].PWMPath, "/sys/class/hwmon/hwmon6/pwm1"; got != want {
		t.Errorf("Fan.PWMPath: got %q, want %q", got, want)
	}
}

func TestLoadForStartup_RetryTimeout(t *testing.T) {
	// Device never appears → LoadForStartup returns a startup error
	// (NOT first-boot) so systemd's Restart=on-failure kicks us.
	withHwmonRootFS(t, fstest.MapFS{
		"hwmon5/name": &fstest.MapFile{Data: []byte("nct6687\n")},
	})

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, makeConfigYAMLWithHwmonDevice(
		"/sys/class/hwmon/hwmon5/pwm1", "nct6687",
		"/sys/devices/platform/ventd-test-never-appears-103",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, firstBoot, err := LoadForStartup(cfgPath, StartupOptions{
		Timeout:      100 * time.Millisecond,
		PollInterval: 25 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("LoadForStartup with permanently-missing device: err=nil; want timeout error")
	}
	if firstBoot {
		t.Error("LoadForStartup returned firstBoot=true on hwmon timeout; must be startup error so systemd restarts us")
	}
	if !strings.Contains(err.Error(), "hwmon not ready") {
		t.Errorf("error should mention 'hwmon not ready'; got %v", err)
	}
	if !errors.Is(err, ErrHwmonDeviceNotReady) {
		t.Errorf("timeout error must still wrap ErrHwmonDeviceNotReady so operators see the original cause; got %v", err)
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("retry exited before deadline: elapsed=%s, timeout=100ms", elapsed)
	}
}

func TestLoadForStartup_TimeoutZeroMeansNoRetry(t *testing.T) {
	withHwmonRootFS(t, fstest.MapFS{
		"hwmon5/name": &fstest.MapFile{Data: []byte("nct6687\n")},
	})

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, makeConfigYAMLWithHwmonDevice(
		"/sys/class/hwmon/hwmon5/pwm1", "nct6687",
		"/sys/devices/platform/ventd-test-never-appears-103",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, firstBoot, err := LoadForStartup(cfgPath, StartupOptions{Timeout: 0})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("LoadForStartup with Timeout=0 + missing device: err=nil; want immediate error")
	}
	if firstBoot {
		t.Error("LoadForStartup with Timeout=0 returned firstBoot=true; want startup error")
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("Timeout=0 should not have retried; elapsed=%s", elapsed)
	}
}
