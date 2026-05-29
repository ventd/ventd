// ventd-recover: emergency fan pwm_enable restore.
//
// Fired by ventd-recover.service via OnFailure= on the main ventd.service
// unit when the daemon exits unexpectedly (SIGKILL, OOM, hardware-watchdog
// timeout, or a panic that escapes the defer chain).
//
// Walks /sys/class/hwmon/hwmon*/pwm<N>_enable and hands every channel it can
// open back to firmware control by walking {2, 99, 0} — the first value the
// chip accepts without EINVAL wins. It deliberately never writes pwm_enable=1
// (manual): on most super-I/O chips that pins the fan at the dead daemon's
// last PWM instead of returning control to firmware (#1434; the residual-
// manual bug #1039 fixed on the in-daemon path). Best effort: a file that
// cannot be written is logged to stderr and skipped; the loop continues so
// every reachable channel is reset. Exit 0 always — a non-zero exit would
// mark the OnFailure oneshot as failed and could loop systemd back into the
// OnFailure chain.
//
// Keep this binary small and allocation-light. The write hot path (writeToFD)
// uses package-level byte arrays so it makes zero heap allocations on the
// happy path; see TestVentdRecover_NoAllocs.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// enableHandbackSequence is the ordered set of pwm_enable values written to
// hand a channel back to firmware control, mirroring
// watchdog.SafePreDaemonEnableSequence and hwmon.enableHandbackSequence.
//
// It MUST NOT contain 1: on most super-I/O chips (NCT6687, ITE, Nuvoton)
// pwm_enable=1 is MANUAL mode, which pins the fan at the dead daemon's last
// PWM instead of handing back to firmware (#1434 / #1039). 2 = automatic,
// 99 = SuperIO auto placeholder (NCT6687D pre-#169), 0 = ABI full-speed last
// resort. This binary must not import internal packages (binary-size budget,
// TestVentdRecover_BinarySize), so the sequence is a local copy; the byte
// forms are package-level so writeToFD allocates nothing on the happy path.
var (
	valTwo      = [...]byte{'2', '\n'}
	valNineNine = [...]byte{'9', '9', '\n'}
	valZero     = [...]byte{'0', '\n'}

	enableHandbackSequence = [][]byte{valTwo[:], valNineNine[:], valZero[:]}
)

// sysWrite and sysSeek are indirection seams so the EINVAL-fallback walk is
// testable without a chip that rejects values. Calling through a func value
// allocates nothing, so the zero-alloc guarantee is preserved.
var (
	sysWrite = syscall.Write
	sysSeek  = syscall.Seek
)

func main() {
	restoreAll("/sys/class/hwmon")
}

// restoreAll walks root/hwmon*/pwm<N>_enable and hands each channel back to
// firmware control via writeToFD (the {2, 99, 0} walk).
func restoreAll(root string) {
	hwmons, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range hwmons {
		if !strings.HasPrefix(e.Name(), "hwmon") {
			continue
		}
		hwmonDir := filepath.Join(root, e.Name())
		files, err := os.ReadDir(hwmonDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !isPWMEnable(f.Name()) {
				continue
			}
			path := filepath.Join(hwmonDir, f.Name())
			fd, errno := syscall.Open(path, syscall.O_WRONLY, 0)
			if errno != nil {
				_, _ = os.Stderr.WriteString("ventd-recover: open " + path + ": " + errno.Error() + "\n")
				continue
			}
			writeToFD(fd)
			syscall.Close(fd) //nolint:errcheck
		}
	}
}

// writeToFD walks enableHandbackSequence on an already-open fd, rewinding to
// offset 0 before each attempt and writing the next candidate until one lands
// without EINVAL. A non-EINVAL error aborts — if the file itself is unwritable
// no other value will land either. Zero heap allocations on the happy path:
// the candidates slice static data and the syscalls go through func-value seams.
func writeToFD(fd int) {
	for _, b := range enableHandbackSequence {
		_, _ = sysSeek(fd, 0, 0)
		if _, err := sysWrite(fd, b); err == nil {
			return // landed on this value
		} else if err != syscall.EINVAL {
			return // hard failure; the next candidate won't land either
		}
		// EINVAL: chip rejected this value; try the next.
	}
}

// isPWMEnable returns true for names matching pwm[0-9]+_enable.
func isPWMEnable(name string) bool {
	const pre, suf = "pwm", "_enable"
	if !strings.HasPrefix(name, pre) || !strings.HasSuffix(name, suf) {
		return false
	}
	mid := name[len(pre) : len(name)-len(suf)]
	if len(mid) == 0 {
		return false
	}
	for i := 0; i < len(mid); i++ {
		if mid[i] < '0' || mid[i] > '9' {
			return false
		}
	}
	return true
}
