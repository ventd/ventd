package detection

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// CollectGPU gathers GPU detection items (§12.4 — GPU catalog §5.7).
func CollectGPU(ctx context.Context) CollectResult {
	var res CollectResult
	add := func(item Item) { res.Items = append(res.Items, item) }
	miss := func(m *MissingTool) {
		if m != nil {
			res.MissingTools = append(res.MissingTools, *m)
		}
	}

	// PCI display class (0300)
	if out, m := runCmd(ctx, "lspci", "-nn", "-d", "::0300"); m != nil {
		miss(m)
	} else {
		add(textItem("commands/gpu/lspci_class_03", out))
		add(symlinkItem("gpu", "commands/gpu/lspci_class_03"))
	}

	// NVIDIA: nvidia-smi query
	if out, m := runCmd(ctx, "nvidia-smi", "--query-gpu=name,uuid,driver_version,vbios_version", "--format=csv,noheader"); m != nil {
		miss(m)
	} else {
		add(textItem("commands/gpu/nvml_query", out))
	}

	// AMDGPU: modinfo + gpu_metrics sysfs
	if out, m := runCmd(ctx, "modinfo", "amdgpu"); m == nil {
		add(textItem("commands/gpu/amdgpu_modinfo", out))
	}
	walkAMDGPUMetrics(&res)

	// Intel xe / i915
	walkXeHwmon(&res)

	return res
}

func walkAMDGPUMetrics(res *CollectResult) {
	drm := "/sys/class/drm"
	entries, err := os.ReadDir(drm)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "card") || strings.Contains(e.Name(), "-") {
			continue
		}
		metricsPath := filepath.Join(drm, e.Name(), "device", "gpu_metrics")
		data := readFile(metricsPath)
		if len(data) == 0 {
			continue
		}
		bundlePath := filepath.Join("sys/class/drm", e.Name(), "device", "gpu_metrics")
		res.Items = append(res.Items, Item{Path: bundlePath, Content: data})
	}
}

func walkXeHwmon(res *CollectResult) {
	drm := "/sys/class/drm"
	entries, err := os.ReadDir(drm)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "card") || strings.Contains(e.Name(), "-") {
			continue
		}
		driverLink := filepath.Join(drm, e.Name(), "device", "driver")
		target, err := os.Readlink(driverLink)
		if err != nil {
			continue
		}
		driverName := filepath.Base(target)
		if driverName != "xe" && driverName != "i915" {
			continue
		}
		// Walk hwmon under this card.
		hwmonDir := filepath.Join(drm, e.Name(), "device", "hwmon")
		hwEntries, err := os.ReadDir(hwmonDir)
		if err != nil {
			continue
		}
		for _, hw := range hwEntries {
			attrs, _ := os.ReadDir(filepath.Join(hwmonDir, hw.Name()))
			for _, a := range attrs {
				if !isHwmonAttr(a.Name()) {
					continue
				}
				data := readFile(filepath.Join(hwmonDir, hw.Name(), a.Name()))
				if len(data) > 0 {
					bundlePath := filepath.Join("sys/class/drm", e.Name(), "device/hwmon", hw.Name(), a.Name())
					res.Items = append(res.Items, textItem(bundlePath, data))
				}
			}
		}
		add := textItem("commands/gpu/xe_hwmon_walk", []byte(driverName+": "+e.Name()+"\n"))
		res.Items = append(res.Items, add)
	}
}
