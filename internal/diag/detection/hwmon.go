package detection

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// CollectHwmon gathers lm_sensors output and the /sys/class/hwmon tree (§12.2).
func CollectHwmon(ctx context.Context) CollectResult {
	var res CollectResult
	add := func(item Item) { res.Items = append(res.Items, item) }
	miss := func(m *MissingTool) {
		if m != nil {
			res.MissingTools = append(res.MissingTools, *m)
		}
	}

	// sensors -u (machine-readable)
	if out, m := runCmd(ctx, "sensors", "-u"); m != nil {
		miss(m)
	} else {
		add(textItem("commands/hwmon/sensors_-u", out))
		// Symlink for fast triage.
		add(symlinkItem("hwmon", "commands/hwmon/sensors_-u"))
	}

	// sensors -u -A (without adapter prefix)
	if out, m := runCmd(ctx, "sensors", "-u", "-A"); m != nil {
		miss(m)
	} else {
		add(textItem("commands/hwmon/sensors_-u_-A", out))
	}

	// sensors -v (version)
	if out, m := runCmd(ctx, "sensors", "-v"); m != nil {
		miss(m)
	} else {
		add(textItem("commands/hwmon/lm_sensors_version", out))
	}

	// Mirror /sys/class/hwmon/* into sys/class/hwmon/
	walkHwmon(&res)

	return res
}

func walkHwmon(res *CollectResult) {
	walkHwmonAt(res, "/sys/class/hwmon", "/sys/devices/platform")
}

// Caps on the platform-device recursion (#1170). Vendor EC drivers
// (msi-ec, asus-wmi-ec, …) park interesting state in subdirs like
// `cpu/realtime_temperature` and `leds/`, which the flat hwmon walk
// missed. Bounds are chosen to avoid runaway walks on pathological
// trees while still pulling everything a triage agent needs.
const (
	platformWalkMaxDepth   = 3
	platformWalkMaxLeafLen = 4 * 1024
)

// walkHwmonAt is the test-friendly form. hwmonRoot is /sys/class/hwmon
// in production; platformRoot is /sys/devices/platform — the recursion
// only fires for chip device-paths that live under platformRoot, so a
// hwmon entry backed by a PCI GPU doesn't drag the whole PCI tree in.
func walkHwmonAt(res *CollectResult, hwmonRoot, platformRoot string) {
	entries, err := os.ReadDir(hwmonRoot)
	if err != nil {
		return
	}
	platformRootAbs, _ := filepath.Abs(platformRoot)
	seenPlatform := map[string]struct{}{}
	for _, e := range entries {
		chipDir := filepath.Join(hwmonRoot, e.Name())
		attrs, err := os.ReadDir(chipDir)
		if err != nil {
			continue
		}
		for _, a := range attrs {
			if a.IsDir() {
				continue
			}
			name := a.Name()
			if !isHwmonAttr(name) {
				continue
			}
			data := readFile(filepath.Join(chipDir, name))
			if len(data) == 0 {
				continue
			}
			bundlePath := filepath.Join("sys/class/hwmon", e.Name(), name)
			res.Items = append(res.Items, textItem(bundlePath, data))
		}

		devPath, err := filepath.EvalSymlinks(filepath.Join(chipDir, "device"))
		if err != nil {
			continue
		}
		devAbs, err := filepath.Abs(devPath)
		if err != nil {
			continue
		}
		if platformRootAbs == "" || !strings.HasPrefix(devAbs, platformRootAbs+string(filepath.Separator)) {
			continue
		}
		if _, dup := seenPlatform[devAbs]; dup {
			continue
		}
		seenPlatform[devAbs] = struct{}{}
		relRoot, err := filepath.Rel(platformRootAbs, devAbs)
		if err != nil {
			continue
		}
		walkPlatformDevice(res, devAbs, filepath.Join("sys/devices/platform", relRoot))
	}
}

// walkPlatformDevice recursively reads non-dir attributes under
// devAbs (the resolved /sys/devices/platform/<driver>/ root) and
// emits them at bundlePathRoot. Bounded by platformWalkMaxDepth and
// platformWalkMaxLeafLen. Skips uevent and the power/ subsystem tree
// because both are sysfs boilerplate that costs bytes without
// informing diagnosis.
func walkPlatformDevice(res *CollectResult, devAbs, bundlePathRoot string) {
	var walk func(curDir, curBundle string, depth int)
	walk = func(curDir, curBundle string, depth int) {
		entries, err := os.ReadDir(curDir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if name == "uevent" || name == "power" || name == "subsystem" || name == "driver" {
				continue
			}
			full := filepath.Join(curDir, name)
			bundle := filepath.Join(curBundle, name)
			if e.IsDir() {
				if depth >= platformWalkMaxDepth {
					continue
				}
				walk(full, bundle, depth+1)
				continue
			}
			// Skip symlinks — following them from sysfs leads back
			// into /sys/class/* and the bus tree, which we walk
			// (or won't walk) explicitly.
			if fi, ferr := os.Lstat(full); ferr != nil || fi.Mode()&os.ModeSymlink != 0 {
				continue
			}
			data := readFileCapped(full, platformWalkMaxLeafLen)
			if len(data) == 0 {
				continue
			}
			res.Items = append(res.Items, textItem(bundle, data))
		}
	}
	walk(devAbs, bundlePathRoot, 0)
}

// readFileCapped reads up to max bytes from path. Returns nil on any
// open error so callers can ignore the file silently — same contract
// as readFile.
func readFileCapped(path string, max int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, max)
	n, _ := f.Read(buf)
	if n <= 0 {
		return nil
	}
	return buf[:n]
}

func isHwmonAttr(name string) bool {
	prefixes := []string{
		"fan", "temp", "pwm", "in", "name", "device",
		"subsystem_vendor", "subsystem_device",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
