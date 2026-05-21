package ec

import (
	"os"
	"strings"
)

// lockdownPath is the kernel's Lockdown LSM sysfs surface. Format is
// space-separated mode names with the active one bracketed, e.g.
//
//	none [integrity] confidentiality
//
// "none" means lockdown is inactive — userspace /dev/port and iopl
// work normally. "integrity" and "confidentiality" both block raw I/O
// port access, which means our /dev/port transport (and the ec_sys
// write_support module parameter on a signed kernel) cannot be used
// from userspace, regardless of root privilege.
const lockdownPath = "/sys/kernel/security/lockdown"

// readLockdownFile is the test injection seam. Production reads
// lockdownPath; tests substitute a fixture string.
var readLockdownFile = func() (string, error) {
	b, err := os.ReadFile(lockdownPath)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// LockdownActive reports whether the kernel's Lockdown LSM is in
// "integrity" or "confidentiality" mode. Returns false on systems
// without the Lockdown LSM (no /sys/kernel/security/lockdown file)
// or when the file contains "[none]".
//
// When this returns true, userspace EC transports (ec_sys debugfs +
// /dev/port port I/O) are blocked by the kernel even for root. The
// only viable EC R/W path is via a signed kernel module that uses
// the in-kernel acpi_ec_read / acpi_ec_write exports.
func LockdownActive() bool {
	s, err := readLockdownFile()
	if err != nil {
		// File absent on kernels without Lockdown LSM, or on hosts
		// where /sys/kernel/security/ isn't mounted. Treat as "not
		// active" — Available() will then attempt the transports
		// and surface any other failure cause.
		return false
	}
	// The active mode is bracketed.
	return strings.Contains(s, "[integrity]") || strings.Contains(s, "[confidentiality]")
}
