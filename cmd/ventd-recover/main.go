// ventd-recover: emergency fan pwm_enable restore.
//
// Fired by ventd-recover.service via OnFailure= on the main ventd.service
// unit when the daemon exits unexpectedly (SIGKILL, OOM, hardware-watchdog
// timeout, or a panic that escapes the defer chain).
//
// Walks /sys/class/hwmon/hwmon*/pwm<N>_enable and writes "1" (kernel-defined
// "automatic" / firmware-controlled mode) to every file it can open. Best
// effort: a file that cannot be written is logged to stderr and skipped; the
// loop continues so every reachable channel is reset. Exit 0 always — a
// non-zero exit would mark the OnFailure oneshot as failed and could loop
// systemd back into the OnFailure chain.
//
// Keep this binary small and allocation-light. The write hot path
// (writeToFD) uses a package-level array so it makes zero heap allocations
// on the happy path; see TestVentdRecover_NoAllocs.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// one is the byte sequence written to every pwm_enable file. Package-level
// so writeToFD can reference it without a per-call heap allocation.
var one = [2]byte{'1', '\n'}

func main() {
	restoreAll("/sys/class/hwmon")
}

// restoreAll walks root/hwmon*/pwm<N>_enable and writes "1\n" to each.
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

// writeToFD writes "1\n" to an already-open file descriptor.
// Zero heap allocations: one[:] slices a package-level array (static data).
func writeToFD(fd int) {
	syscall.Write(fd, one[:]) //nolint:errcheck
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
