package detection

import (
	"context"
	"strings"
)

// CollectSystem gathers platform identity items (§12.1).
func CollectSystem(ctx context.Context) CollectResult {
	var res CollectResult
	add := func(item Item) { res.Items = append(res.Items, item) }
	miss := func(m *MissingTool) {
		if m != nil {
			res.MissingTools = append(res.MissingTools, *m)
		}
	}

	// uname -a
	if out, m := runCmd(ctx, "uname", "-a"); m != nil {
		miss(m)
	} else {
		add(textItem("commands/system/uname_-a", out))
	}

	// /etc/os-release
	if data := readFile("/etc/os-release"); len(data) > 0 {
		add(textItem("commands/system/lsb_release", data))
	} else if out, m := runCmd(ctx, "lsb_release", "-a"); m != nil {
		miss(m)
	} else {
		add(textItem("commands/system/lsb_release", out))
	}

	// dmidecode — baseboard + chassis + processor only (no system-uuid)
	if out, m := runCmd(ctx, "dmidecode", "-t", "baseboard", "-t", "chassis", "-t", "processor"); m != nil {
		miss(m)
	} else {
		add(textItem("commands/system/dmidecode_filtered", filterDMIDecode(out)))
	}

	// /proc/cmdline
	if data := readFile("/proc/cmdline"); len(data) > 0 {
		add(textItem("commands/system/kernel_cmdline", data))
	}

	// lsmod filtered to fan-related modules
	if out, m := runCmd(ctx, "lsmod"); m != nil {
		miss(m)
	} else {
		add(textItem("commands/system/modules_loaded", filterLsmod(out)))
	}

	// /proc/sys/kernel/tainted
	if data := readFile("/proc/sys/kernel/tainted"); len(data) > 0 {
		add(textItem("commands/system/tainted", data))
	}

	// Symlinks at bundle root for fast triage.
	add(symlinkItem("version", "commands/ventd/version"))

	return res
}

// filterDMIDecode strips the system-uuid and system-serial from dmidecode
// output — those are captured by P2DMI but the architectural exclusion is
// belt-and-suspenders here.
func filterDMIDecode(raw []byte) []byte {
	lines := strings.Split(string(raw), "\n")
	var out []string
	for _, l := range lines {
		lower := strings.ToLower(strings.TrimSpace(l))
		if strings.HasPrefix(lower, "uuid:") {
			continue
		}
		if strings.HasPrefix(lower, "serial number:") && strings.Contains(strings.ToLower(l), "system") {
			continue
		}
		out = append(out, l)
	}
	return []byte(strings.Join(out, "\n"))
}

// filterLsmod returns only fan/thermal-relevant module lines.
func filterLsmod(raw []byte) []byte {
	relevant := []string{
		"nct", "it87", "fintek", "w83", "asus", "dell", "hp_wmi",
		"thinkpad", "applesmc", "surface", "gigabyte", "corsair",
		"nzxt", "amdgpu", "nvidia", "coretemp", "k10temp", "peci",
		"hwmon", "fan", "thermal", "pwm",
	}
	lines := strings.Split(string(raw), "\n")
	var out []string
	out = append(out, lines[0]) // header
	for _, l := range lines[1:] {
		lower := strings.ToLower(l)
		for _, kw := range relevant {
			if strings.Contains(lower, kw) {
				out = append(out, l)
				break
			}
		}
	}
	return []byte(strings.Join(out, "\n"))
}
