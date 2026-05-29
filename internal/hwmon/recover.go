package hwmon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// enableHandbackSequence is the ordered set of pwm_enable values the crash-
// recovery path writes to hand a channel back to firmware control. The first
// value the chip accepts without EINVAL wins.
//
// It MUST NOT contain 1. On most super-I/O chips (NCT6687, ITE IT87xx,
// Nuvoton) pwm_enable=1 is MANUAL mode: writing it after a crash pins the fan
// at whatever PWM byte the dead daemon last wrote — often near-zero mid-spin-
// down or mid-calibration — instead of returning control to the BIOS curve.
// That is the residual-manual bug #1039 fixed on the in-daemon Register path
// (RULE-WD-PRIOR-CRASH-FALLBACK); the recovery path must not reintroduce it.
//
// The sequence mirrors watchdog.SafePreDaemonEnableSequence. It is duplicated
// here rather than imported because internal/watchdog imports internal/hwmon
// (importing it back would be a cycle); TestEnableHandbackSequence_NeverManual
// guards the value so the local copy cannot drift to include the manual value.
//   - 2  — "automatic": de-facto userspace convention, hits ~all in-tree drivers.
//   - 99 — SuperIO auto placeholder some vendor drivers use (NCT6687D pre-#169);
//     the kernel ABI defines 2+ as a range, not a single value.
//   - 0  — ABI "no fan speed control / full speed": noisy but mechanically safe
//     last resort.
//
// RULE-WD-RECOVER-HANDBACK.
var enableHandbackSequence = []int{2, 99, 0}

// RecoverAllPWM walks /sys/class/hwmon for every pwm<N>_enable file and hands
// each channel back to firmware control by walking enableHandbackSequence
// (2 → 99 → 0), taking the first value the chip accepts without EINVAL. It
// deliberately never writes pwm_enable=1 (manual) — see enableHandbackSequence.
//
// Used by the ventd-recover.service systemd OnFailure= oneshot:
// when the long-running daemon exits unexpectedly (SIGKILL, OOM,
// segfault, hardware-watchdog timeout, panic that escapes the defer
// chain), this function runs as a separate process and resets every
// fan to a conservative known-safe state. Pairs with the daemon's
// graceful-exit Restore path to deliver the README's "any exit path
// within two seconds" promise.
//
// Idempotent: writing the automatic value to a channel already in automatic
// mode is a no-op. Tolerant: a file we cannot write (permission denied, EIO,
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

		chosen, err := restoreFirmwareControl(enablePath)
		if err != nil {
			failed++
			logger.Warn("recover: failed to hand pwm_enable back to firmware",
				"path", enablePath, "err", err)
			continue
		}
		succeeded++
		logger.Info("recover: handed pwm_enable back to firmware",
			"path", enablePath, "value", chosen)
	}

	logger.Info("recover: complete",
		"succeeded", succeeded,
		"failed", failed,
		"total", len(enables))
	return succeeded, failed
}

// restoreFirmwareControl walks enableHandbackSequence against path, writing
// each candidate until one lands without EINVAL. Returns the value that stuck.
func restoreFirmwareControl(path string) (int, error) {
	return pickEnableValue(enableHandbackSequence, func(v int) error {
		return writePWMEnable(path, strconv.Itoa(v))
	})
}

// pickEnableValue tries each candidate via attempt and returns the first that
// succeeds. attempt returns nil on success, an error wrapping syscall.EINVAL
// to advance to the next candidate, or any other error to abort: a value the
// driver rejects as out-of-range (EINVAL) means try the next; a file we cannot
// write at all (EACCES, EIO, device gone) means no other value will land
// either. Kept pure (no I/O) so the fallback-walk logic is unit-testable
// without a chip that rejects values.
func pickEnableValue(seq []int, attempt func(int) error) (int, error) {
	var lastErr error
	for _, v := range seq {
		err := attempt(v)
		if err == nil {
			return v, nil
		}
		lastErr = err
		if !errors.Is(err, syscall.EINVAL) {
			break // hard failure; a different value won't land either
		}
	}
	if lastErr == nil {
		lastErr = errors.New("recover: empty enable sequence")
	}
	return 0, lastErr
}

// writePWMEnable writes value+"\n" to the given pwm_enable path. Separate
// from internal/hwmon/hwmon.go's WritePWMEnable so the recover path
// has no dependency on the package's main public API surface — keeps
// the recover binary surface as small as possible. Errors wrap the
// underlying syscall error with %w so pickEnableValue can detect EINVAL.
func writePWMEnable(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write([]byte(value + "\n")); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
