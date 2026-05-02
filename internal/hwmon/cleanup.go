package hwmon

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CleanupReport captures what an orphan-cleanup run actually removed. The
// wizard surfaces this as part of the ResetAndReinstall confirmation so
// the operator can see what state was discarded before the next install
// attempt runs. Fields are append-only; renaming a field is a JSON-shape
// breaking change.
type CleanupReport struct {
	BuildDirsRemoved  []string `json:"build_dirs_removed,omitempty"`
	ModulesRemoved    []string `json:"modules_removed,omitempty"`
	DKMSRemoved       []string `json:"dkms_removed,omitempty"`
	ModulesLoadDClean bool     `json:"modules_loadd_clean,omitempty"`
	BlacklistWritten  string   `json:"blacklist_written,omitempty"`
	NonFatalErrors    []string `json:"non_fatal_errors,omitempty"`
}

// CleanupOrphanInstall removes leftover state from a previous failed
// install attempt. Idempotent — calling it on a clean system is a no-op
// that returns an empty CleanupReport.
//
// The function is intentionally tolerant of every individual step: the
// goal is to leave the system in a state where a fresh InstallDriver can
// succeed, not to fail loudly on every missing path. Fatal failures (a
// path we cannot remove that would block reinstall) are returned as a
// non-nil error; cosmetic failures (DKMS not installed, etc.) accumulate
// in CleanupReport.NonFatalErrors.
func CleanupOrphanInstall(driver DriverNeed, release string, logger *slog.Logger) (*CleanupReport, error) {
	if logger == nil {
		logger = slog.Default()
	}
	report := &CleanupReport{}

	// 1. Remove /tmp/ventd-driver-* build temp dirs left behind by a
	// previous InstallDriver run that died before its `defer
	// os.RemoveAll(tmpDir)` fired (panic, kill -9, OOM-kill).
	if matches, err := filepath.Glob(filepath.Join(os.TempDir(), "ventd-driver-*")); err == nil {
		for _, dir := range matches {
			if err := os.RemoveAll(dir); err != nil {
				report.NonFatalErrors = append(report.NonFatalErrors,
					fmt.Sprintf("remove %s: %s", dir, err))
				continue
			}
			report.BuildDirsRemoved = append(report.BuildDirsRemoved, dir)
		}
	}

	// 2. Remove /lib/modules/<release>/extra/<module>.ko ONLY if the
	// module is not currently loaded. A loaded module's .ko file is
	// referenced by the kernel; removing it is harmless to the running
	// kernel but the next depmod -a would re-stat the missing file and
	// emit warnings, which scares operators.
	if release != "" && driver.Module != "" && !moduleLoaded(driver.Module) {
		extraKO := filepath.Join("/lib/modules", release, "extra", driver.Module+".ko")
		if fileExists(extraKO) {
			if err := os.Remove(extraKO); err != nil {
				report.NonFatalErrors = append(report.NonFatalErrors,
					fmt.Sprintf("remove %s: %s", extraKO, err))
			} else {
				report.ModulesRemoved = append(report.ModulesRemoved, extraKO)
			}
		}
	}

	// 3. Remove DKMS state for the module. `dkms remove --all <module>`
	// covers every version DKMS has registered; we don't try to enumerate
	// versions because the next InstallDriver run will register a fresh
	// version anyway. Skipped silently when dkms is not on PATH.
	// DKMS 3.0+ rejects the older `dkms remove --all <module>`
	// syntax with "Arguments <module> and <module-version> are
	// not specified" — caught on Phoenix's HIL desktop where
	// Ubuntu 24.04 ships dkms 3.0.11 and the cleanup silently
	// failed (Reset+Reinstall card "ran" but DKMS state stayed).
	// New syntax requires <module>/<version>; we parse `dkms
	// status -m <module>` to enumerate registered versions and
	// remove each one. The next InstallDriver run will register
	// a fresh version anyway.
	if driver.Module != "" {
		if _, err := exec.LookPath("dkms"); err == nil {
			versions := dkmsVersionsForModule(driver.Module)
			for _, v := range versions {
				spec := driver.Module + "/" + v
				n, a := rootArgv("dkms", []string{"remove", "--all", spec})
				out, err := exec.Command(n, a...).CombinedOutput()
				if err != nil {
					outStr := strings.ToLower(strings.TrimSpace(string(out)))
					if !strings.Contains(outStr, "not found") &&
						!strings.Contains(outStr, "is not in the dkms tree") {
						report.NonFatalErrors = append(report.NonFatalErrors,
							fmt.Sprintf("dkms remove --all %s: %s (%s)",
								spec, err, outStr))
						continue
					}
				}
				report.DKMSRemoved = append(report.DKMSRemoved, spec)
			}
		}
	}

	// 4. Clean /etc/modules-load.d/ventd.conf — only if the module
	// is not loaded. The autoload.go::persistModule writer's format is
	// "one module name per line"; we filter out the target module
	// without rewriting unrelated lines.
	if driver.Module != "" && !moduleLoaded(driver.Module) {
		if cleaned, err := stripModuleFromLoadConf(driver.Module); err != nil {
			report.NonFatalErrors = append(report.NonFatalErrors,
				fmt.Sprintf("strip module from modules-load.d: %s", err))
		} else if cleaned {
			report.ModulesLoadDClean = true
		}
	}

	logger.Info("cleanup-orphan-install complete",
		"module", driver.Module,
		"release", release,
		"build_dirs", len(report.BuildDirsRemoved),
		"modules_removed", len(report.ModulesRemoved),
		"dkms_removed", len(report.DKMSRemoved),
		"non_fatal_errors", len(report.NonFatalErrors))
	return report, nil
}

// CleanupInTreeConflict unbinds the conflicting in-tree driver and writes
// a blacklist drop-in so it doesn't reload at boot. Used as the
// remediation action for ClassInTreeConflict — typically alongside
// CleanupOrphanInstall in a single ResetAndReinstall flow.
//
// blacklistPath is the drop-in file path (typically
// DistroInfo.BlacklistDropInPath()).
func CleanupInTreeConflict(conflictModule, blacklistPath string, logger *slog.Logger) (*CleanupReport, error) {
	if logger == nil {
		logger = slog.Default()
	}
	report := &CleanupReport{}

	if conflictModule != "" && moduleLoaded(conflictModule) {
		n, a := rootArgv("modprobe", []string{"-r", conflictModule})
		if out, err := exec.Command(n, a...).CombinedOutput(); err != nil {
			return report, fmt.Errorf("unbind %s: %w (output: %s)",
				conflictModule, err, strings.TrimSpace(string(out)))
		}
		report.ModulesRemoved = append(report.ModulesRemoved, conflictModule)
	}

	if conflictModule != "" && blacklistPath != "" {
		if err := writeBlacklistDropIn(blacklistPath, conflictModule); err != nil {
			report.NonFatalErrors = append(report.NonFatalErrors,
				fmt.Sprintf("write blacklist: %s", err))
		} else {
			report.BlacklistWritten = blacklistPath
		}
	}

	logger.Info("cleanup-in-tree-conflict complete",
		"conflict_module", conflictModule,
		"blacklist_path", blacklistPath,
		"non_fatal_errors", len(report.NonFatalErrors))
	return report, nil
}

// moduleLoaded reports whether name appears in /proc/modules. The
// liveInTreeDriverConflict probe uses a similar walk; we duplicate the
// logic here (rather than refactoring into a shared helper) because the
// preflight probe is read-only and may run with a stubbed Probe set,
// while cleanup always reads the live system.
func moduleLoaded(name string) bool {
	data, err := os.ReadFile("/proc/modules")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) >= 1 && fields[0] == name {
			return true
		}
	}
	return false
}

// stripModuleFromLoadConf removes lines naming module from
// /etc/modules-load.d/ventd.conf. Returns (true, nil) when the file was
// modified, (false, nil) when the module was not present, and an error
// when reading or writing fails.
func stripModuleFromLoadConf(module string) (bool, error) {
	const path = "/etc/modules-load.d/ventd.conf"
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	dropped := false
	for _, line := range lines {
		if strings.TrimSpace(line) == module {
			dropped = true
			continue
		}
		out = append(out, line)
	}
	if !dropped {
		return false, nil
	}
	// If the file becomes empty (or just whitespace) remove it; otherwise
	// rewrite. Empty files in modules-load.d are harmless but operators
	// reading the dir expect every file to mean something.
	rewritten := strings.Join(out, "\n")
	if strings.TrimSpace(rewritten) == "" {
		if err := os.Remove(path); err != nil {
			return false, fmt.Errorf("remove %s: %w", path, err)
		}
		return true, nil
	}
	if err := os.WriteFile(path, []byte(rewritten), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// writeBlacklistDropIn writes (or appends to) a modprobe blacklist
// drop-in. The file format is `blacklist <module>` per line, with one
// trailing newline. Idempotent — re-blacklisting an already-blacklisted
// module is a no-op rewrite of the same file.
func writeBlacklistDropIn(path, module string) error {
	if module == "" {
		return fmt.Errorf("module name is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	want := "blacklist " + module
	existing, _ := os.ReadFile(path) // missing OK
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == want {
			return nil // idempotent
		}
	}
	body := strings.TrimRight(string(existing), "\n")
	if body != "" {
		body += "\n"
	}
	body += want + "\n"
	return os.WriteFile(path, []byte(body), 0o644)
}

// dkmsVersionsForModule parses `dkms status -m <module>` output to
// extract every registered version of the module. DKMS 3.0+'s
// status output format (one record per line):
//
//	<module>/<version>, <kernel>, <arch>: <status>
//
// We split on "/" then "," to extract just the version portion.
// Returns nil when dkms is not on PATH, the call fails, or the
// module has no registered versions.
func dkmsVersionsForModule(module string) []string {
	if module == "" {
		return nil
	}
	if _, err := exec.LookPath("dkms"); err != nil {
		return nil
	}
	out, err := exec.Command("dkms", "status", "-m", module).Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var versions []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// "it87/2026.05.02, 6.8.0-111-generic, x86_64: installed"
		head, _, _ := strings.Cut(line, ",")
		modAndVer := strings.TrimSpace(head)
		_, ver, ok := strings.Cut(modAndVer, "/")
		if !ok || ver == "" {
			continue
		}
		if !seen[ver] {
			seen[ver] = true
			versions = append(versions, ver)
		}
	}
	return versions
}
