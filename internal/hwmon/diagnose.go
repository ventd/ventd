package hwmon

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultHwmonRoot is the production root for hwmon class enumeration.
// Re-declared here as a string constant so the diagnose path does not
// have to import enumerate.go's typed value into a different file.
const diagHwmonRoot = "/sys/class/hwmon"

// DiagnoseHwmon enumerates the live hwmon class directory and logs a
// summary of writable PWM channels. Strictly read-only — never
// modprobes, never writes.
//
// Called once at daemon startup so operators investigating "fans
// aren't being controlled" see at a glance whether the kernel
// surfaces any PWM at all, whether group-write was applied by the
// shipped udev rule, and which chips are present.
func DiagnoseHwmon(logger *slog.Logger) {
	DiagnoseHwmonAt(logger, diagHwmonRoot)
}

// DiagnoseHwmonAt is the test-friendly form: drive enumeration from
// an arbitrary filesystem root. Production callers use the unrooted
// DiagnoseHwmon wrapper above.
//
// On a healthy install this logs a single INFO with a count and
// example. On a "no PWM at all" system it logs a WARN with a
// remediation pointer to `ventd --probe-modules`. On a "PWM visible
// but not group-writable" system it logs a WARN explaining that the
// shipped udev rule has not applied (likely a missing udevadm
// trigger or a custom-built rule).
func DiagnoseHwmonAt(logger *slog.Logger, root string) {
	pwmPaths := findPWMPathsAt(root)
	if len(pwmPaths) == 0 {
		logger.Warn("hwmon: no PWM channels visible at startup",
			"root", root,
			"action", "run `sudo ventd --probe-modules` to load and persist the right kernel module, "+
				"or open the web UI for guided diagnostics")
		return
	}

	type chipState struct {
		name        string
		pwmCount    int
		writableCnt int
	}
	chips := map[string]*chipState{}
	for _, p := range pwmPaths {
		dir := filepath.Dir(p)
		name := readNameFile(dir)
		if name == "" {
			name = filepath.Base(dir)
		}
		st := chips[dir]
		if st == nil {
			st = &chipState{name: name}
			chips[dir] = st
		}
		st.pwmCount++
		if isGroupWritable(p) {
			st.writableCnt++
		}
	}

	dirs := make([]string, 0, len(chips))
	for d := range chips {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	totalWritable := 0
	chipSummaries := make([]string, 0, len(dirs))
	for _, d := range dirs {
		st := chips[d]
		totalWritable += st.writableCnt
		chipSummaries = append(chipSummaries,
			st.name+"="+filepath.Base(d)+
				" ("+itoaDiag(st.writableCnt)+"/"+itoaDiag(st.pwmCount)+" g+w)")
	}

	if totalWritable == 0 {
		logger.Warn("hwmon: PWM channels visible but none are group-writable for the ventd group",
			"chips", strings.Join(chipSummaries, ", "),
			"action", "verify /etc/udev/rules.d/90-ventd-hwmon.rules is installed; "+
				"run `sudo udevadm control --reload && sudo udevadm trigger --subsystem-match=hwmon`")
		return
	}

	logger.Info("hwmon: PWM channels visible",
		"writable", totalWritable,
		"total", len(pwmPaths),
		"chips", strings.Join(chipSummaries, ", "),
		"example", pwmPaths[0])
}

// findPWMPathsAt is the rooted variant of findPWMPaths. Globs every
// hwmonN/pwm<N> under root (real numeric pwm files only — pwmN_enable
// and friends are excluded the same way findPWMPaths does it).
func findPWMPathsAt(root string) []string {
	matches, err := filepath.Glob(filepath.Join(root, "hwmon*", "pwm[0-9]*"))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, p := range matches {
		base := filepath.Base(p)
		suffix := strings.TrimPrefix(base, "pwm")
		if !allDigits(suffix) {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// allDigits returns true when s is non-empty and all-numeric.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// readNameFile returns the trimmed contents of <hwmonDir>/name or "".
func readNameFile(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "name"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// isGroupWritable returns true when path's mode includes the group
// write bit (0020).
func isGroupWritable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().Perm()&0o020 != 0
}

// itoaDiag is a small int→string helper local to the diagnose path
// to avoid pulling strconv into this otherwise stdlib-light file.
func itoaDiag(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
