package hwmon

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// RecoverAllPWM walks /sys/class/hwmon for every pwm<N>_enable file
// and writes "1" to it (kernel-defined "automatic" mode), handing
// fan control back to the firmware/BIOS curve.
//
// Used by the ventd-recover.service systemd OnFailure= oneshot:
// when the long-running daemon exits unexpectedly (SIGKILL, OOM,
// segfault, hardware-watchdog timeout, panic that escapes the defer
// chain), this function runs as a separate process and resets every
// fan to a conservative known-safe state. Pairs with the daemon's
// graceful-exit Restore path to deliver the README's "any exit path
// within two seconds" promise.
//
// Idempotent: writing 1 to a pwm<N>_enable that's already 1 is a
// no-op. Tolerant: a file we cannot write (permission denied, EIO,
// device gone) is logged and skipped — never aborts the loop.
//
// Returns the number of files successfully reset and the number of
// failures. Caller (cmd/ventd) decides exit code; non-zero failures
// are not fatal because ventd-recover's whole purpose is best-effort
// recovery, and exiting non-zero would mark the OnFailure oneshot
// as failed and could trigger systemd's restart spiral.
func RecoverAllPWM(logger *slog.Logger) (succeeded, failed int) {
	return RecoverAllPWMAt(logger, "/sys/class/hwmon")
}

// RecoverAllPWMAt is the test-friendly form: drive recovery from an
// arbitrary filesystem root. Production wraps this with the live
// /sys/class/hwmon path.
func RecoverAllPWMAt(logger *slog.Logger, root string) (succeeded, failed int) {
	enables, err := filepath.Glob(filepath.Join(root, "hwmon*", "pwm[0-9]*_enable"))
	if err != nil {
		// Glob's only failure is malformed pattern, which would be a
		// programming error here. Log and return zeros.
		logger.Error("recover: glob failed", "root", root, "err", err)
		return 0, 0
	}

	if len(enables) == 0 {
		logger.Info("recover: no pwm_enable files found, nothing to reset",
			"root", root)
		return 0, 0
	}

	for _, enablePath := range enables {
		// Defensive: skip files whose pwm<N>_enable basename does not
		// resolve to a digit suffix. Filters pwm_extra_freq_enable etc.
		base := filepath.Base(enablePath)
		mid := strings.TrimSuffix(strings.TrimPrefix(base, "pwm"), "_enable")
		if !allDigits(mid) {
			continue
		}

		if err := writePWMEnable(enablePath, "1"); err != nil {
			failed++
			logger.Warn("recover: failed to reset pwm_enable",
				"path", enablePath, "err", err)
			continue
		}
		succeeded++
		logger.Info("recover: reset pwm_enable to automatic", "path", enablePath)
	}

	logger.Info("recover: complete",
		"succeeded", succeeded,
		"failed", failed,
		"total", len(enables))
	return succeeded, failed
}

// writePWMEnable writes "1\n" to the given pwm_enable path. Separate
// from internal/hwmon/hwmon.go's WritePWMEnable so the recover path
// has no dependency on the package's main public API surface — keeps
// the recover binary surface as small as possible.
func writePWMEnable(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write([]byte(value + "\n")); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
