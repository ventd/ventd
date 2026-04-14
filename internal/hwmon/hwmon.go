// Package hwmon provides low-level read/write access to Linux hwmon sysfs entries.
// All sysfs I/O in the daemon must go through this package — it is the single
// choke point that can be audited to confirm no raw PWM writes escape the
// controller's safety clamps.
package hwmon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ReadTemp reads a hwmon temp*_input sysfs file and returns degrees Celsius.
// hwmon temp files report values in millidegrees Celsius.
func ReadTemp(path string) (float64, error) {
	return ReadValue(path)
}

// ReadValue reads any hwmon *_input sysfs file and returns a scaled float64.
// Divisors are derived from the file name prefix:
//
//	temp*  →  millidegrees → ÷1000  (°C)
//	in*    →  millivolts   → ÷1000  (V)
//	power* →  microwatts   → ÷1e6   (W)
//	fan*   →  raw RPM      → ÷1     (RPM)
//	*      →  raw          → ÷1
func ReadValue(path string) (float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("hwmon: read %s: %w", path, err)
	}
	raw, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("hwmon: parse %s: %w", path, err)
	}
	base := filepath.Base(path)
	switch {
	case strings.HasPrefix(base, "temp"):
		return float64(raw) / 1000.0, nil
	case strings.HasPrefix(base, "in"):
		return float64(raw) / 1000.0, nil
	case strings.HasPrefix(base, "power"):
		return float64(raw) / 1000000.0, nil
	default:
		return float64(raw), nil
	}
}

// WritePWM writes a PWM duty cycle value (0–255) to a hwmon pwm* sysfs file.
// The caller is responsible for clamping value to the channel's configured
// [min_pwm, max_pwm] range before calling this function.
func WritePWM(path string, value uint8) error {
	data := strconv.AppendUint(nil, uint64(value), 10)
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0); err != nil {
		return fmt.Errorf("hwmon: write pwm %s=%d: %w", path, value, err)
	}
	return nil
}

// ReadPWMEnable reads the current pwm*_enable value for a PWM path.
// Values: 0 = full speed (no PWM), 1 = manual (software), 2 = auto (BIOS/kernel).
func ReadPWMEnable(pwmPath string) (int, error) {
	p := enablePath(pwmPath)
	data, err := os.ReadFile(p)
	if err != nil {
		return 0, fmt.Errorf("hwmon: read pwm_enable %s: %w", p, err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("hwmon: parse pwm_enable %s: %w", p, err)
	}
	return v, nil
}

// WritePWMEnable sets the pwm*_enable value for a PWM path.
// Always call WritePWMEnable(path, 2) before the daemon exits to hand control
// back to the BIOS/kernel.
//
// Returns a wrapped fs.ErrNotExist if the pwm*_enable file is absent.
// Some drivers (e.g. nct6683 for NCT6687D) do not expose this file;
// callers should use errors.Is(err, fs.ErrNotExist) to detect that case
// and fall back to writing a safe PWM value directly.
func WritePWMEnable(pwmPath string, value int) error {
	p := enablePath(pwmPath)
	// Stat first: sysfs returns EACCES (not ENOENT) when you try to O_CREATE
	// a file that doesn't exist, which would obscure "file missing" vs "truly
	// denied". Checking existence up front gives callers a reliable ErrNotExist.
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("hwmon: pwm_enable not supported at %s: %w", p, fs.ErrNotExist)
	}
	data := strconv.AppendInt(nil, int64(value), 10)
	data = append(data, '\n')
	if err := os.WriteFile(p, data, 0); err != nil {
		return fmt.Errorf("hwmon: write pwm_enable %s=%d: %w", p, value, err)
	}
	return nil
}

// ReadPWM reads the current PWM duty cycle (0–255) from a hwmon pwm* file.
func ReadPWM(path string) (uint8, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("hwmon: read pwm %s: %w", path, err)
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 8)
	if err != nil {
		return 0, fmt.Errorf("hwmon: parse pwm %s: %w", path, err)
	}
	return uint8(v), nil
}

// ReadRPM reads the fan speed in RPM from the fan*_input file that corresponds
// to pwmPath. The fan index is auto-derived: pwm1 → fan1_input.
func ReadRPM(pwmPath string) (int, error) {
	return ReadRPMPath(rpmPath(pwmPath))
}

// ReadRPMPath reads the fan speed in RPM from an explicit sysfs path.
func ReadRPMPath(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("hwmon: read rpm %s: %w", path, err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("hwmon: parse rpm %s: %w", path, err)
	}
	return v, nil
}

// enablePath derives the pwm*_enable path from a pwm* path.
// e.g. /sys/class/hwmon/hwmon6/pwm1 → /sys/class/hwmon/hwmon6/pwm1_enable
func enablePath(pwmPath string) string {
	return filepath.Join(filepath.Dir(pwmPath), filepath.Base(pwmPath)+"_enable")
}

// rpmPath derives the fan*_input path from a pwm* path.
// e.g. /sys/class/hwmon/hwmon6/pwm1 → /sys/class/hwmon/hwmon6/fan1_input
func rpmPath(pwmPath string) string {
	num := strings.TrimPrefix(filepath.Base(pwmPath), "pwm")
	return filepath.Join(filepath.Dir(pwmPath), "fan"+num+"_input")
}

// WriteFanTarget writes an RPM setpoint to a fan*_target sysfs file.
// Used for pre-RDNA AMD cards where the amdgpu driver accepts RPM setpoints
// via fan*_target rather than duty cycles via pwm*.
func WriteFanTarget(path string, rpm int) error {
	data := strconv.AppendInt(nil, int64(rpm), 10)
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0); err != nil {
		return fmt.Errorf("hwmon: write fan target %s=%d RPM: %w", path, rpm, err)
	}
	return nil
}

// ReadFanMaxRPM reads the maximum RPM for a fan*_target sysfs channel by
// reading the companion fan*_max file in the same hwmon directory. Returns
// 2000 if the file is absent — a conservative default for AMD GPU fans.
func ReadFanMaxRPM(targetPath string) int {
	base := filepath.Base(targetPath)
	num := strings.TrimSuffix(strings.TrimPrefix(base, "fan"), "_target")
	maxPath := filepath.Join(filepath.Dir(targetPath), "fan"+num+"_max")
	data, err := os.ReadFile(maxPath)
	if err != nil {
		return 2000
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || v <= 0 {
		return 2000
	}
	return v
}

// RPMTargetEnablePath derives the pwm*_enable path for a fan*_target sysfs
// file. Taking manual control of an RPM-target channel requires setting its
// companion pwm*_enable file, not a (non-existent) fan*_target_enable file.
// e.g. /sys/class/hwmon/hwmon0/fan1_target → /sys/class/hwmon/hwmon0/pwm1_enable
func RPMTargetEnablePath(targetPath string) string {
	dir := filepath.Dir(targetPath)
	num := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(targetPath), "fan"), "_target")
	return filepath.Join(dir, "pwm"+num+"_enable")
}

// WritePWMEnablePath writes a value to an explicit pwm*_enable sysfs path.
// Unlike WritePWMEnable, the caller provides the full _enable path directly;
// no "_enable" suffix is appended. Used for rpm_target fan channels where
// the enable file is pwm*_enable but the write target is fan*_target.
func WritePWMEnablePath(path string, value int) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("hwmon: pwm_enable not supported at %s: %w", path, fs.ErrNotExist)
	}
	data := strconv.AppendInt(nil, int64(value), 10)
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0); err != nil {
		return fmt.Errorf("hwmon: write pwm_enable %s=%d: %w", path, value, err)
	}
	return nil
}
