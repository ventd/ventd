package probe

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
)

// defaultWriteCheck attempts to open the sysfs path O_WRONLY and immediately
// closes it. No data is written — this is a permission check only (RULE-PROBE-01).
func defaultWriteCheck(sysPath string) bool {
	f, err := os.OpenFile(sysPath, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// enumerateChannels discovers controllable fan channels from hwmon sysfs (§4.3).
// All file reads go through p.cfg.SysFS. The WriteCheck helper tests
// writability without writing any data.
func (p *prober) enumerateChannels(_ context.Context) ([]ControllableChannel, []Diagnostic) {
	var channels []ControllableChannel
	var diags []Diagnostic

	if p.cfg.SysFS == nil {
		return channels, diags
	}

	hwmonEntries, err := fs.ReadDir(p.cfg.SysFS, "class/hwmon")
	if err != nil {
		return channels, diags
	}

	for _, entry := range hwmonEntries {
		if !entry.IsDir() && entry.Type()&fs.ModeSymlink == 0 {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "hwmon") {
			continue
		}
		chs, chDiags := p.enumerateHwmonChannels(name)
		channels = append(channels, chs...)
		diags = append(diags, chDiags...)
	}

	return channels, diags
}

// enumerateHwmonChannels finds all pwmN files within one hwmonN directory and
// checks each for controllability.
func (p *prober) enumerateHwmonChannels(hwmonName string) ([]ControllableChannel, []Diagnostic) {
	base := path.Join("class/hwmon", hwmonName)
	driver, _ := readTrimmed(p.cfg.SysFS, path.Join(base, "name"))

	entries, err := fs.ReadDir(p.cfg.SysFS, base)
	if err != nil {
		return nil, nil
	}

	var channels []ControllableChannel
	var diags []Diagnostic

	for _, e := range entries {
		fname := e.Name()
		// Match pwmN (exactly, not pwmN_enable etc.)
		if !isPWMFile(fname) {
			continue
		}
		idx := strings.TrimPrefix(fname, "pwm")

		// pwmN_enable must exist for this to be a controllable channel.
		enableFile := fname + "_enable"
		if _, err := fs.Stat(p.cfg.SysFS, path.Join(base, enableFile)); err != nil {
			continue
		}

		// Writability check (no actual write, RULE-PROBE-01).
		// In production, defaultWriteCheck opens the real /sys path O_WRONLY.
		// In tests, an injected stub is used.
		sysPWMPath := "/sys/" + path.Join(base, fname)
		if !p.cfg.WriteCheck(sysPWMPath) {
			diags = append(diags, Diagnostic{
				Severity: "info",
				Code:     "PROBE-CHANNEL-NOT-WRITABLE",
				Message:  fmt.Sprintf("%s/%s: not writable; skipping", hwmonName, fname),
			})
			continue
		}

		ch := ControllableChannel{
			SourceID: hwmonName,
			PWMPath:  sysPWMPath,
			Driver:   driver,
			Polarity: "unknown", // v0.5.2 disambiguates (RULE-PROBE-06)
		}

		// Read initial PWM value.
		if raw, err := readTrimmed(p.cfg.SysFS, path.Join(base, fname)); err == nil {
			if v, err := strconv.Atoi(raw); err == nil {
				ch.InitialPWM = v
			}
		}

		// Look for companion fan*_input (tach — optional).
		tachFile := "fan" + idx + "_input"
		if _, err := fs.Stat(p.cfg.SysFS, path.Join(base, tachFile)); err == nil {
			ch.TachPath = "/sys/" + path.Join(base, tachFile)
			if raw, err := readTrimmed(p.cfg.SysFS, path.Join(base, tachFile)); err == nil {
				if v, err := strconv.Atoi(raw); err == nil {
					ch.InitialRPM = v
				}
			}
		}

		channels = append(channels, ch)
	}
	return channels, diags
}

// isPWMFile returns true for "pwmN" filenames where N is a decimal digit sequence,
// but not for "pwmN_enable", "pwmN_mode" etc.
func isPWMFile(name string) bool {
	if !strings.HasPrefix(name, "pwm") {
		return false
	}
	rest := name[3:]
	if len(rest) == 0 {
		return false
	}
	if strings.ContainsRune(rest, '_') {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
