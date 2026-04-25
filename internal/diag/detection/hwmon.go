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
	hwmonDir := "/sys/class/hwmon"
	entries, err := os.ReadDir(hwmonDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		chipDir := filepath.Join(hwmonDir, e.Name())
		attrs, err := os.ReadDir(chipDir)
		if err != nil {
			continue
		}
		for _, a := range attrs {
			if a.IsDir() {
				continue
			}
			name := a.Name()
			// Only capture fan/temp/pwm/in attributes and driver metadata.
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
	}
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
