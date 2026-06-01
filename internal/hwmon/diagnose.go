package hwmon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ventd/ventd/internal/hal"
)

// DiagnoseHwmon enumerates the live hwmon class directory and logs a
// summary of writable PWM channels. Strictly read-only — never
// modprobes, never writes.
//
// Called once at daemon startup so operators investigating "fans
// aren't being controlled" see at a glance whether the kernel
// surfaces any PWM at all, whether group-write was applied by the
// shipped udev rule, and which chips are present.
func DiagnoseHwmon(logger *slog.Logger) {
	// Honour VENTD_HWMON_ROOT: under the override the daemon controls the
	// synthetic tree, so the startup diagnostic must describe that, not the
	// host's real /sys (which the daemon is deliberately ignoring). EffectiveRoot
	// is the hwmon class dir — DefaultHwmonRoot by default.
	DiagnoseHwmonAt(logger, EffectiveRoot())
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
	pwmPaths := FindPWMPathsAt(root)
	if len(pwmPaths) == 0 {
		// #1163: on hosts where fan control lives in a non-hwmon HAL
		// backend (msi-ec, thinkpad, ipmi, …), the suggestion to run
		// `ventd --probe-modules` is misleading. If a non-hwmon backend
		// has already enumerated channels with CapWritePWM, downgrade
		// to INFO and name the backend instead.
		if backends := halBackendsExposingPWM(); len(backends) > 0 {
			logger.Info("hwmon: no PWM via sysfs; fan control will use HAL backend",
				"root", root,
				"backends", strings.Join(backends, ","))
			return
		}
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

// FindPWMPathsAt is the rooted variant of findPWMPaths. Globs every
// hwmonN/pwm<N> under root (real numeric pwm files only — pwmN_enable
// and friends are excluded the same way findPWMPaths does it).
//
// Exported for the v0.8.x orchestrator's ProbePhase, which scans the
// host's PWMs against the same shape the legacy Manager.run path uses
// so both paths converge on identical fan enumeration.
func FindPWMPathsAt(root string) []string {
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

// halBackendsExposingPWM returns the names of registered HAL backends
// (other than "hwmon") whose Enumerate reports at least one channel
// with CapWritePWM. Used by DiagnoseHwmonAt to decide whether the
// "no PWM via sysfs" condition is actually a problem.
func halBackendsExposingPWM() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Partial results are exactly what we want here: if one backend (e.g. a
	// flaky IPMI BMC) errors but another non-hwmon backend reports a writable
	// PWM channel, we must still see it — otherwise the caller wrongly treats
	// "no PWM via sysfs" as a real problem. hal.Enumerate isolates per-backend
	// failures, so the joined error is intentionally ignored.
	chs, _ := hal.Enumerate(ctx)
	seen := map[string]struct{}{}
	for _, ch := range chs {
		if ch.Caps&hal.CapWritePWM == 0 {
			continue
		}
		name, _, ok := strings.Cut(ch.ID, ":")
		if !ok || name == "hwmon" {
			continue
		}
		seen[name] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
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
