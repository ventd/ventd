package detection

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildMsiECFixture lays out a synthetic /sys tree shaped like a
// real msi-ec host: hwmon entry under /sys/class/hwmon/hwmon3 is a
// symlink into /sys/devices/platform/msi-ec/hwmon/hwmon3; the
// platform-device root has cpu/ and leds/ subdirs that the flat
// hwmon walk misses, plus a power/ and uevent that the bounded
// recursion must skip.
func buildMsiECFixture(t *testing.T) (hwmonRoot, platformRoot string) {
	t.Helper()
	root := t.TempDir()

	platformRoot = filepath.Join(root, "sys", "devices", "platform")
	driverRoot := filepath.Join(platformRoot, "msi-ec")
	hwmonChip := filepath.Join(driverRoot, "hwmon", "hwmon3")

	if err := os.MkdirAll(hwmonChip, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(hwmonChip, "name"), "msi_ec\n")
	mustWrite(t, filepath.Join(hwmonChip, "pwm1"), "128\n")
	mustWrite(t, filepath.Join(hwmonChip, "temp1_input"), "42000\n")

	// cpu/ subdir — the value that triggered #1170 (Hudson's bundle).
	cpuDir := filepath.Join(driverRoot, "cpu")
	if err := os.MkdirAll(cpuDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(cpuDir, "realtime_temperature"), "47\n")
	mustWrite(t, filepath.Join(cpuDir, "realtime_fan_speed"), "30\n")

	// leds/ subdir nested two deep — at depth 2 from the platform
	// root, still inside the depth-3 bound.
	ledDir := filepath.Join(driverRoot, "leds", "msi::kbd_backlight")
	if err := os.MkdirAll(ledDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(ledDir, "brightness"), "255\n")

	// uevent and power/ — both must be skipped by walkPlatformDevice.
	mustWrite(t, filepath.Join(driverRoot, "uevent"), "DRIVER=msi-ec\n")
	powerDir := filepath.Join(driverRoot, "power")
	if err := os.MkdirAll(powerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(powerDir, "control"), "auto\n")

	// hwmon class entry as a symlink into the platform-device tree.
	hwmonRoot = filepath.Join(root, "sys", "class", "hwmon")
	if err := os.MkdirAll(hwmonRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hwmonChip, filepath.Join(hwmonRoot, "hwmon3")); err != nil {
		t.Fatal(err)
	}
	// `device` symlink from chip → platform-device root (one level up
	// from hwmon/hwmon3 into the driver dir). Mirrors the real kernel
	// layout that EvalSymlinks resolves on a live msi-ec host.
	if err := os.Symlink(driverRoot, filepath.Join(hwmonChip, "device")); err != nil {
		t.Fatal(err)
	}
	return hwmonRoot, platformRoot
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itemPaths(items []Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.IsSymlink {
			continue
		}
		out = append(out, it.Path)
	}
	return out
}

func itemByPath(items []Item, path string) (Item, bool) {
	for _, it := range items {
		if it.Path == path {
			return it, true
		}
	}
	return Item{}, false
}

func TestWalkHwmonAt_RecursesIntoPlatformDeviceSubdirs(t *testing.T) {
	hwmonRoot, platformRoot := buildMsiECFixture(t)
	var res CollectResult
	walkHwmonAt(&res, hwmonRoot, platformRoot)

	paths := itemPaths(res.Items)
	mustHave := []string{
		"sys/class/hwmon/hwmon3/name",
		"sys/class/hwmon/hwmon3/pwm1",
		"sys/class/hwmon/hwmon3/temp1_input",
		"sys/devices/platform/msi-ec/cpu/realtime_temperature",
		"sys/devices/platform/msi-ec/cpu/realtime_fan_speed",
		"sys/devices/platform/msi-ec/leds/msi::kbd_backlight/brightness",
	}
	for _, want := range mustHave {
		if !containsString(paths, want) {
			t.Errorf("missing %q in:\n  %s", want, strings.Join(paths, "\n  "))
		}
	}
}

func TestWalkHwmonAt_SkipsUeventAndPower(t *testing.T) {
	hwmonRoot, platformRoot := buildMsiECFixture(t)
	var res CollectResult
	walkHwmonAt(&res, hwmonRoot, platformRoot)

	for _, p := range itemPaths(res.Items) {
		if strings.HasSuffix(p, "/uevent") {
			t.Errorf("uevent must be skipped, got %q", p)
		}
		if strings.Contains(p, "/power/") {
			t.Errorf("power/ subtree must be skipped, got %q", p)
		}
	}
}

func TestWalkHwmonAt_RespectsDepthBound(t *testing.T) {
	root := t.TempDir()
	platformRoot := filepath.Join(root, "sys", "devices", "platform")
	driverRoot := filepath.Join(platformRoot, "deep-ec")
	// Build d1/d2/d3/d4 where d4 contains a leaf — beyond the depth bound (3).
	deep := filepath.Join(driverRoot, "d1", "d2", "d3", "d4")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(deep, "secret"), "should-not-appear\n")
	// And a leaf at exactly depth 3 (d1/d2/d3/leaf) — must appear.
	allowed := filepath.Join(driverRoot, "d1", "d2", "d3")
	mustWrite(t, filepath.Join(allowed, "allowed"), "yes\n")

	hwmonRoot := filepath.Join(root, "sys", "class", "hwmon")
	chip := filepath.Join(driverRoot, "hwmon", "hwmon9")
	if err := os.MkdirAll(chip, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(chip, "name"), "deep_ec\n")
	if err := os.MkdirAll(hwmonRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(chip, filepath.Join(hwmonRoot, "hwmon9")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(driverRoot, filepath.Join(chip, "device")); err != nil {
		t.Fatal(err)
	}

	var res CollectResult
	walkHwmonAt(&res, hwmonRoot, platformRoot)
	paths := itemPaths(res.Items)
	if !containsString(paths, "sys/devices/platform/deep-ec/d1/d2/d3/allowed") {
		t.Errorf("file at depth 3 (boundary) must appear; paths: %v", paths)
	}
	for _, p := range paths {
		if strings.HasSuffix(p, "/secret") {
			t.Errorf("file at depth 4 must be skipped, got %q", p)
		}
	}
}

func TestWalkHwmonAt_TruncatesLargeLeafTo4KiB(t *testing.T) {
	root := t.TempDir()
	platformRoot := filepath.Join(root, "sys", "devices", "platform")
	driverRoot := filepath.Join(platformRoot, "big-ec")
	if err := os.MkdirAll(driverRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// 10 KiB of 'A'.
	big := bytes.Repeat([]byte{'A'}, 10*1024)
	mustWrite(t, filepath.Join(driverRoot, "blob"), string(big))

	chip := filepath.Join(driverRoot, "hwmon", "hwmon0")
	if err := os.MkdirAll(chip, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(chip, "name"), "big_ec\n")
	hwmonRoot := filepath.Join(root, "sys", "class", "hwmon")
	if err := os.MkdirAll(hwmonRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(chip, filepath.Join(hwmonRoot, "hwmon0")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(driverRoot, filepath.Join(chip, "device")); err != nil {
		t.Fatal(err)
	}

	var res CollectResult
	walkHwmonAt(&res, hwmonRoot, platformRoot)

	it, ok := itemByPath(res.Items, "sys/devices/platform/big-ec/blob")
	if !ok {
		t.Fatal("expected truncated leaf in bundle")
	}
	// textItem trims trailing \n and re-adds one, so payload is 4096 + 1.
	if len(it.Content) > platformWalkMaxLeafLen+1 {
		t.Errorf("leaf not capped: got %d bytes, want <=%d", len(it.Content), platformWalkMaxLeafLen+1)
	}
}

func TestWalkHwmonAt_NonPlatformDeviceDoesNotRecurse(t *testing.T) {
	// A hwmon chip backed by a fake PCI device (path NOT under
	// platformRoot) must NOT trigger the recursive platform walk.
	root := t.TempDir()
	platformRoot := filepath.Join(root, "sys", "devices", "platform")
	if err := os.MkdirAll(platformRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	pciDev := filepath.Join(root, "sys", "devices", "pci0000:00", "0000:00:1f.3")
	if err := os.MkdirAll(pciDev, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(pciDev, "noisy_attr"), "do-not-collect\n")

	chip := filepath.Join(pciDev, "hwmon", "hwmon7")
	if err := os.MkdirAll(chip, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(chip, "name"), "amdgpu\n")
	hwmonRoot := filepath.Join(root, "sys", "class", "hwmon")
	if err := os.MkdirAll(hwmonRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(chip, filepath.Join(hwmonRoot, "hwmon7")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pciDev, filepath.Join(chip, "device")); err != nil {
		t.Fatal(err)
	}

	var res CollectResult
	walkHwmonAt(&res, hwmonRoot, platformRoot)

	for _, p := range itemPaths(res.Items) {
		if strings.HasSuffix(p, "/noisy_attr") {
			t.Errorf("PCI-backed device must not be recursively walked; got %q", p)
		}
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
