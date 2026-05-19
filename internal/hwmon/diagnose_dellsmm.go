package hwmon

import (
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// MinDellSMMVentdFork is the minimum dell-smm-hwmon-dkms version ventd
// recommends. v7.0.0-ventd.3 is the first release that:
//   - adds the Latitude 7280 to i8k_whitelist_fan_control (state 2 HIGH now usable),
//   - makes pwm_enable readable on whitelist-matched machines (watchdog crash
//     recovery doesn't have to guess "PWM=255 as safe fallback"),
//   - gates pwm_enable=2 on non-whitelist Dells to a safe set_fan(i8k_fan_max)
//     fallback rather than the historical -EINVAL.
//
// Stale or in-tree dell-smm-hwmon installs produce the WARN diagnostic below
// with a pointer to the ventd fork release.
const MinDellSMMVentdFork = "7.0.0-ventd.3"

// DiagnoseDellSMMVersion checks whether dell_smm_hwmon is the ventd-fork
// build, and at the minimum recommended version. Strictly read-only: shells
// out to modinfo and parses the `version:` line. Called at daemon startup.
//
// Logs:
//   - INFO "dell-smm: ventd fork installed" on a satisfying version.
//   - WARN "dell-smm: in-tree driver detected" when version doesn't contain
//     "ventd" — operator should install the ventd-org DKMS package.
//   - WARN "dell-smm: ventd fork older than recommended" when version is
//     "X.Y.Z-ventd.N" but predates MinDellSMMVentdFork.
//   - silent if the module isn't loaded (not a Dell, or driver absent —
//     diagnose-hwmon will already have flagged "no PWM").
func DiagnoseDellSMMVersion(logger *slog.Logger) {
	diagnoseDellSMMVersion(logger, runModinfo)
}

// diagnoseDellSMMVersion is the dependency-injected form. modinfoFn returns
// the raw output bytes of `modinfo dell_smm_hwmon` (or an error). Tests pass
// a fake; production uses runModinfo.
func diagnoseDellSMMVersion(logger *slog.Logger, modinfoFn func(string) (string, error)) {
	out, err := modinfoFn("dell_smm_hwmon")
	if err != nil {
		// Module not loaded or modinfo absent — not necessarily a problem.
		// On non-Dell hosts this is the normal case. Log at DEBUG so the
		// signal isn't lost but the noise stays low.
		logger.Debug("dell-smm: modinfo dell_smm_hwmon not available, skipping version check",
			"err", err.Error())
		return
	}

	version := parseModinfoVersion(out)
	if version == "" {
		logger.Debug("dell-smm: modinfo emitted no `version:` line, skipping check")
		return
	}

	ventdFork := strings.Contains(version, "-ventd.")
	if !ventdFork {
		logger.Warn("dell-smm: in-tree driver detected; ventd fork v"+MinDellSMMVentdFork+" recommended",
			"installed_version", version,
			"recommended", "v"+MinDellSMMVentdFork,
			"why", "the in-tree driver returns -EINVAL on pwm_enable=2 for non-whitelist Dells and makes pwm_enable write-only on whitelist-matched machines — both cause spurious fan spikes on every ventd restart",
			"action", "install github.com/ventd/dell-smm-hwmon-dkms via `ventd --probe-modules` or the web UI driver-install flow")
		return
	}

	if compareVentdForkVersion(version, MinDellSMMVentdFork) < 0 {
		logger.Warn("dell-smm: ventd fork older than recommended",
			"installed_version", version,
			"recommended", "v"+MinDellSMMVentdFork,
			"action", "upgrade github.com/ventd/dell-smm-hwmon-dkms to v"+MinDellSMMVentdFork+" — fixes pwm_enable readability + adds Latitude 7280 to whitelist")
		return
	}

	logger.Info("dell-smm: ventd fork installed",
		"version", version,
		"minimum_recommended", "v"+MinDellSMMVentdFork)
}

// parseModinfoVersion extracts the `version:` value from modinfo output.
// Returns "" if no version line is present. Modinfo lines look like:
//
//	version:        7.0.0-ventd.3
func parseModinfoVersion(modinfoOut string) string {
	for _, line := range strings.Split(modinfoOut, "\n") {
		if !strings.HasPrefix(line, "version:") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "version:"))
	}
	return ""
}

// compareVentdForkVersion compares two strings of the form "X.Y.Z-ventd.N".
// Returns -1 / 0 / +1 in the strings.Compare sense. Both inputs MUST have
// the -ventd.N suffix; otherwise returns 0 (caller should already have
// checked ventdFork beforehand).
//
// Example: compareVentdForkVersion("7.0.0-ventd.2", "7.0.0-ventd.3") == -1.
func compareVentdForkVersion(a, b string) int {
	aBase, aN := splitVentdFork(a)
	bBase, bN := splitVentdFork(b)
	if aBase != bBase {
		// X.Y.Z portion differs — fall back to lexicographic compare.
		// In practice both will share the kernel-aligned base (7.0.0 etc.)
		// so this branch is for "should never happen but be safe" cases.
		if aBase < bBase {
			return -1
		}
		return 1
	}
	if aN < bN {
		return -1
	}
	if aN > bN {
		return 1
	}
	return 0
}

// splitVentdFork parses "X.Y.Z-ventd.N" into (X.Y.Z, N). Returns ("", 0) on
// malformed input.
func splitVentdFork(s string) (string, int) {
	idx := strings.Index(s, "-ventd.")
	if idx < 0 {
		return "", 0
	}
	base := s[:idx]
	n := 0
	for _, r := range s[idx+len("-ventd."):] {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return base, n
}

// runModinfo is the production modinfo-fn. Times out after 5s to avoid
// blocking startup on a hung modprobe.
func runModinfo(module string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "modinfo", module).CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
