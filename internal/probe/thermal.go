package probe

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/ventd/ventd/internal/hwmon"
)

// enumerateThermal discovers thermal sources from hwmon and thermal_zone sysfs
// entries (§4.2). All reads are through p.cfg.SysFS — no writes.
func (p *prober) enumerateThermal(_ context.Context) ([]ThermalSource, []Diagnostic) {
	var sources []ThermalSource
	var diags []Diagnostic

	if p.cfg.HwmonClassFS == nil {
		return sources, diags
	}

	// hwmon entries: the hwmon class dir (HwmonClassFS — /sys/class/hwmon, or
	// the VENTD_HWMON_ROOT override tree).
	hwmonEntries, err := fs.ReadDir(p.cfg.HwmonClassFS, ".")
	if err != nil {
		diags = append(diags, Diagnostic{
			Severity: "warning",
			Code:     "PROBE-THERMAL-HWMON-UNAVAIL",
			Message:  "cannot read hwmon class dir: " + err.Error(),
		})
	}
	for _, entry := range hwmonEntries {
		if !entry.IsDir() && entry.Type()&fs.ModeSymlink == 0 {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "hwmon") {
			continue
		}
		src, srcDiags := p.enumerateHwmonThermal(name)
		if len(src.Sensors) > 0 {
			sources = append(sources, src)
		}
		diags = append(diags, srcDiags...)
	}

	// thermal_zone entries: /sys/class/thermal/thermal_zone*/ — read through the
	// real SysFS, not the hwmon override. When VENTD_HWMON_ROOT redirects hwmon
	// to a synthetic tree, skip thermal zones entirely: the sim models its
	// temperatures as hwmon temp*_input (already enumerated above), so reading
	// the host's real ACPI zones would leak real sensors into an otherwise
	// hardware-free sim run.
	thermalEntries, err := fs.ReadDir(p.cfg.SysFS, "class/thermal")
	if err == nil && !hwmon.RootIsOverridden() {
		for _, entry := range thermalEntries {
			name := entry.Name()
			if !strings.HasPrefix(name, "thermal_zone") {
				continue
			}
			src, srcDiags := p.enumerateThermalZone(name)
			if len(src.Sensors) > 0 {
				sources = append(sources, src)
			}
			diags = append(diags, srcDiags...)
		}
	}

	return sources, diags
}

// enumerateHwmonThermal reads temp*_input files from one hwmonN directory.
func (p *prober) enumerateHwmonThermal(hwmonName string) (ThermalSource, []Diagnostic) {
	// base is relative to HwmonClassFS; stored sensor paths are stamped from
	// HwmonClassDir so they follow the VENTD_HWMON_ROOT override.
	base := hwmonName
	driver, _ := readTrimmed(p.cfg.HwmonClassFS, path.Join(base, "name"))
	src := ThermalSource{SourceID: hwmonName, Driver: driver}

	var diags []Diagnostic
	entries, err := fs.ReadDir(p.cfg.HwmonClassFS, base)
	if err != nil {
		diags = append(diags, Diagnostic{
			Severity: "warning",
			Code:     "PROBE-THERMAL-HWMON-READ",
			Message:  fmt.Sprintf("%s: readdir: %s", hwmonName, err),
		})
		return src, diags
	}

	for _, e := range entries {
		fname := e.Name()
		if !strings.HasSuffix(fname, "_input") || !strings.HasPrefix(fname, "temp") {
			continue
		}
		// e.g. "temp1_input" → index prefix "temp1"
		prefix := strings.TrimSuffix(fname, "_input")
		labelPath := path.Join(base, prefix+"_label")
		label, _ := readTrimmed(p.cfg.HwmonClassFS, labelPath)

		valPath := path.Join(base, fname)
		rawVal, readErr := readTrimmed(p.cfg.HwmonClassFS, valPath)
		ch := SensorChannel{
			Name:  fname,
			Path:  path.Join(p.cfg.HwmonClassDir, valPath),
			Label: label,
		}
		if readErr != nil {
			diags = append(diags, Diagnostic{
				Severity: "warning",
				Code:     "PROBE-SENSOR-READ-FAIL",
				Message:  fmt.Sprintf("%s/%s: %s", hwmonName, fname, readErr),
			})
		} else {
			if milliC, err := strconv.ParseFloat(rawVal, 64); err == nil {
				ch.InitialRead = milliC / 1000.0
				ch.ReadOK = true
			}
		}
		src.Sensors = append(src.Sensors, ch)
	}
	return src, diags
}

// enumerateThermalZone reads from one thermal_zoneN entry.
func (p *prober) enumerateThermalZone(zoneName string) (ThermalSource, []Diagnostic) {
	base := path.Join("class/thermal", zoneName)
	zoneType, _ := readTrimmed(p.cfg.SysFS, path.Join(base, "type"))
	src := ThermalSource{SourceID: zoneName, Driver: zoneType}

	valPath := path.Join(base, "temp")
	rawVal, err := readTrimmed(p.cfg.SysFS, valPath)
	ch := SensorChannel{
		Name:  "temp",
		Path:  "/sys/" + valPath,
		Label: zoneType,
	}
	if err == nil {
		if milliC, err := strconv.ParseFloat(rawVal, 64); err == nil {
			ch.InitialRead = milliC / 1000.0
			ch.ReadOK = true
		}
	}
	src.Sensors = append(src.Sensors, ch)
	return src, nil
}
