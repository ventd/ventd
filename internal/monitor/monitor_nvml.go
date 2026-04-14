package monitor

import (
	"fmt"

	"github.com/ventd/ventd/internal/nvidia"
)

// scanNVML returns a Device entry per NVML-visible GPU with readings for
// temperature, fan speed, utilisation, power, and clocks. Uses the
// runtime-loaded NVML shim in internal/nvidia; silently returns nil when
// the library is absent or initialisation fails.
func scanNVML() []Device {
	// Idempotent; NVML library load happens at most once per process.
	// When the daemon/setup already called Init, this just increments
	// the refcount. Shutdown decrements; real release on final call.
	if err := nvidia.Init(nil); err != nil {
		return nil
	}
	defer nvidia.Shutdown()

	count := nvidia.CountGPUs()
	if count == 0 {
		return nil
	}

	var devices []Device
	for i := 0; i < count; i++ {
		gpuIdx := fmt.Sprintf("%d", i)
		d := Device{Name: nvidia.GPUName(uint(i)), Path: fmt.Sprintf("gpu%d", i)}
		if v, err := nvidia.ReadMetric(uint(i), "temp"); err == nil {
			d.Readings = append(d.Readings, Reading{Label: "Temperature", Value: v, Unit: "°C", SensorType: "nvidia", SensorPath: gpuIdx, Metric: "temp"})
		}
		if v, err := nvidia.ReadMetric(uint(i), "fan_pct"); err == nil {
			d.Readings = append(d.Readings, Reading{Label: "Fan Speed", Value: v, Unit: "%", SensorType: "nvidia", SensorPath: gpuIdx, Metric: "fan_pct"})
		}
		if v, err := nvidia.ReadMetric(uint(i), "util"); err == nil {
			d.Readings = append(d.Readings, Reading{Label: "GPU Util", Value: v, Unit: "%", SensorType: "nvidia", SensorPath: gpuIdx, Metric: "util"})
		}
		if v, err := nvidia.ReadMetric(uint(i), "mem_util"); err == nil {
			d.Readings = append(d.Readings, Reading{Label: "Mem Util", Value: v, Unit: "%", SensorType: "nvidia", SensorPath: gpuIdx, Metric: "mem_util"})
		}
		if v, err := nvidia.ReadMetric(uint(i), "power"); err == nil {
			d.Readings = append(d.Readings, Reading{Label: "Power", Value: v, Unit: "W", SensorType: "nvidia", SensorPath: gpuIdx, Metric: "power"})
		}
		if v, err := nvidia.ReadMetric(uint(i), "clock_gpu"); err == nil {
			d.Readings = append(d.Readings, Reading{Label: "GPU Clock", Value: v, Unit: "MHz", SensorType: "nvidia", SensorPath: gpuIdx, Metric: "clock_gpu"})
		}
		if v, err := nvidia.ReadMetric(uint(i), "clock_mem"); err == nil {
			d.Readings = append(d.Readings, Reading{Label: "Mem Clock", Value: v, Unit: "MHz", SensorType: "nvidia", SensorPath: gpuIdx, Metric: "clock_mem"})
		}
		devices = append(devices, d)
	}
	return devices
}
