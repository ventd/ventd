package detection

import (
	"context"
)

// CollectUserspace gathers running-process and conflict-detection items (§12.3).
func CollectUserspace(ctx context.Context) CollectResult {
	var res CollectResult
	add := func(item Item) { res.Items = append(res.Items, item) }
	miss := func(m *MissingTool) {
		if m != nil {
			res.MissingTools = append(res.MissingTools, *m)
		}
	}

	// Processes with /dev/hidraw* open.
	if out, m := runCmd(ctx, "lsof", "-n", "/dev/hidraw0", "/dev/hidraw1", "/dev/hidraw2", "/dev/hidraw3"); m != nil {
		miss(&MissingTool{Name: "lsof-hidraw", Reason: m.Reason})
	} else {
		add(textItem("commands/userspace/processes_with_hidraw_open", out))
	}

	// Processes with /dev/dri/* open.
	if out, m := runCmd(ctx, "lsof", "-n", "/dev/dri/card0", "/dev/dri/renderD128"); m != nil {
		miss(&MissingTool{Name: "lsof-dri", Reason: m.Reason})
	} else {
		add(textItem("commands/userspace/lsof_dev_dri", out))
	}

	// amdgpu ppfeaturemask.
	if data := readFile("/sys/module/amdgpu/parameters/ppfeaturemask"); len(data) > 0 {
		add(textItem("commands/userspace/ppfeaturemask", data))
	}

	// Kernel tainted state.
	if data := readFile("/proc/sys/kernel/tainted"); len(data) > 0 {
		add(textItem("commands/userspace/tainted", data))
	}

	// Running fan-control-related services.
	if out, m := runCmd(ctx, "systemctl", "list-units", "--state=running", "--no-legend", "--plain"); m != nil {
		miss(m)
	} else {
		add(textItem("commands/userspace/running_fan_services", filterFanServices(out)))
	}

	return res
}

func filterFanServices(raw []byte) []byte {
	// Return only lines matching fan/cooling-related daemon names.
	keywords := []string{
		"fan", "liquid", "cool", "thinkfan", "nbfc", "lact",
		"corectrl", "fan2go", "ventd", "corsair", "liquidctl",
	}
	var out []byte
	for _, line := range splitLines(raw) {
		lower := toLower(line)
		for _, kw := range keywords {
			if contains(lower, kw) {
				out = append(out, line...)
				out = append(out, '\n')
				break
			}
		}
	}
	return out
}

// Minimal helpers to avoid importing extra packages.
func splitLines(b []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			lines = append(lines, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		lines = append(lines, b[start:])
	}
	return lines
}

func toLower(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 32
		} else {
			out[i] = c
		}
	}
	return out
}

func contains(b []byte, s string) bool {
	needle := []byte(s)
	for i := range b {
		if i+len(needle) <= len(b) {
			match := true
			for j := range needle {
				if b[i+j] != needle[j] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}
