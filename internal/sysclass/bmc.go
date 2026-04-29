package sysclass

import (
	"os/exec"
	"strings"
)

// detectBMC returns true when /dev/ipmi* exists or dmidecode reports a System
// Management Controller (SMBIOS type 38).
func detectBMC(d deps) bool {
	// Fast path: IPMI device node.
	matches, err := devGlob(d, "dev/ipmi*")
	if err == nil && len(matches) > 0 {
		return true
	}

	// Slow path: dmidecode -t 38 (only when /dev/ipmi* absent).
	out, err := d.execDmidecode("-t", "38")
	if err != nil {
		return false
	}
	// dmidecode outputs a handle block starting with "Handle 0x..." when
	// SMBIOS type 38 (IPMI Device Information) is present.
	return strings.Contains(out, "IPMI Device Information")
}

// runDmidecode is the production implementation of deps.execDmidecode.
// It calls the system dmidecode binary.
func runDmidecode(args ...string) (string, error) {
	out, err := exec.Command("dmidecode", args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
