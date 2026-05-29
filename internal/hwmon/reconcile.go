package hwmon

import (
	"log/slog"
	"path/filepath"
	"strings"
)

// ReconcileUnmanagedManual hands back to firmware any pwm channel left in
// manual mode (pwm_enable=1) on a hwmon chip ventd controls but that ventd does
// NOT control in the current config.
//
// Such a channel is a stranded leftover: a prior config controlled it (so the
// daemon flipped pwm_enable to 1), then a re-setup or a config edit dropped it
// — the fan is now frozen at the dead config's last PWM, unresponsive to
// temperature and strictly worse than firmware auto, because no controller
// drives it and the watchdog never registered it (so even exit-restore misses
// it). This is the root cause behind "some of my fans don't respond to temp"
// after a re-setup: the wizard admits a subset, and the rest sit stranded.
//
// controlledPWMPaths is the set of hwmon pwm paths (e.g.
// /sys/class/hwmon/hwmon9/pwm4) ventd actively controls this run. The function
// only scans hwmon chips that contain at least one controlled path — it never
// touches chips ventd has nothing to do with — and within those chips only
// restores channels reading manual (1) that aren't controlled. It hands them
// back via the {2,99,0} firmware-auto sequence (never the manual value 1), the
// same handback the crash-recovery path uses, so the fan returns to the BIOS
// curve. Returns the number of channels restored.
//
// RULE-CTRL-RECONCILE-STRANDED.
func ReconcileUnmanagedManual(controlledPWMPaths map[string]bool, logger *slog.Logger) (restored int) {
	if logger == nil {
		logger = slog.Default()
	}
	chips := make(map[string]struct{})
	for p := range controlledPWMPaths {
		chips[filepath.Dir(p)] = struct{}{}
	}
	for chip := range chips {
		enables, err := filepath.Glob(filepath.Join(chip, "pwm[0-9]*_enable"))
		if err != nil {
			continue
		}
		for _, enablePath := range enables {
			base := filepath.Base(enablePath)
			mid := strings.TrimSuffix(strings.TrimPrefix(base, "pwm"), "_enable")
			if !allDigits(mid) {
				continue // pwm_extra_freq_enable etc.
			}
			pwmPath := strings.TrimSuffix(enablePath, "_enable")
			if controlledPWMPaths[pwmPath] {
				continue // ventd controls this channel — leave it acquired
			}
			cur, err := ReadPWMEnablePath(enablePath)
			if err != nil || cur != 1 {
				continue // unreadable, or not stranded-manual (only 1 == manual)
			}
			chosen, err := restoreFirmwareControl(enablePath)
			if err != nil {
				logger.Warn("reconcile: stranded manual fan detected but firmware handback failed",
					"pwm_path", pwmPath, "err", err)
				continue
			}
			restored++
			logger.Info("reconcile: handed a stranded manual fan back to firmware auto (acquired by a prior config, no longer controlled)",
				"pwm_path", pwmPath, "value", chosen)
		}
	}
	return restored
}
