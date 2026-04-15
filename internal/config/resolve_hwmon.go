package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// EnrichChipName fills in ChipName for any hwmon Sensor or Fan that
// lacks it by reading `dirname(path)/name`. Idempotent: entries with
// a non-empty ChipName are left alone.
//
// Called from two sites:
//
//   - Save (before marshal) so every on-disk config carries the chip
//     identifier ResolveHwmonPaths needs to survive a hwmonN renumber
//     on a future boot. Operator authoring via the web UI never has
//     to think about ChipName.
//
//   - Load (before ResolveHwmonPaths) as a self-heal for upgraded
//     configs that pre-date the ChipName field. If the existing
//     hwmon path is still valid (no renumber happened), the read
//     succeeds and ChipName is populated; the next ResolveHwmonPaths
//     call is a no-op for that entry. If the path is stale, the read
//     fails, ChipName stays empty, and the resolver does nothing for
//     this entry — matching the documented "empty ChipName ⇒ leave
//     untouched" semantics.
//
// Failures are silent. An unreadable name file is the same outcome
// as no name file: ChipName is left empty and the resolver no-ops.
//
// Reads happen via os.ReadFile, not the fsys argument, because:
//   - Sysfs paths like /sys/devices/platform/nct6687.2592/hwmon/hwmonN
//     do not live under /sys/class/hwmon and would not be reachable
//     through the class-rooted fsys used by ResolveHwmonPaths.
//   - Tests for EnrichChipName drive a real os.TempDir tree (see
//     enrich_test.go) so this path is exercised end-to-end without
//     touching production /sys.
func EnrichChipName(cfg *Config) {
	if cfg == nil {
		return
	}
	for i := range cfg.Sensors {
		s := &cfg.Sensors[i]
		if s.Type == "hwmon" && s.ChipName == "" && s.Path != "" {
			s.ChipName = chipNameFromSysfsPath(s.Path)
		}
	}
	for i := range cfg.Fans {
		f := &cfg.Fans[i]
		if f.Type == "hwmon" && f.ChipName == "" && f.PWMPath != "" {
			f.ChipName = chipNameFromSysfsPath(f.PWMPath)
		}
	}
}

// chipNameFromSysfsPath reads `dirname(path)/name` and returns the
// trimmed contents. Returns "" if the file is unreadable for any
// reason (missing path, permission denied, hwmon device removed).
func chipNameFromSysfsPath(path string) string {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(path), "name"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// hwmonIndexRe matches a `/hwmonN` path segment (N digits) followed by either a
// forward slash or end-of-string. The second capture preserves whichever
// terminator matched so the rewrite does not drop it.
var hwmonIndexRe = regexp.MustCompile(`/hwmon(\d+)(/|$)`)

// ResolveHwmonPaths re-anchors every hwmon sensor/fan path in cfg to the
// hwmonN index currently reported by fsys.
//
// fsys must be rooted at the hwmon class directory (in production,
// os.DirFS("/sys/class/hwmon")), so that its top-level entries are the
// per-chip hwmon0, hwmon1, … directories. For every entry with a readable
// `name` file, the trimmed chip name is mapped to its hwmonN directory. Any
// Sensor or Fan with a non-empty ChipName then has its Path / PWMPath / RPMPath
// rewritten so the `/hwmonN/` segment points at the current index.
//
// Entries with an empty ChipName are left untouched, preserving the hand-pinned
// path behaviour of older configs. Non-hwmon types (e.g. nvidia) are skipped
// regardless of ChipName.
//
// Returns an error if any ChipName has no match or multiple matches, or if a
// path to be rewritten lacks a `/hwmonN` segment. The Config is mutated in
// place; on error callers must assume partial mutation and reject the Config.
func ResolveHwmonPaths(cfg *Config, fsys fs.FS) error {
	if cfg == nil {
		return fmt.Errorf("resolve hwmon paths: nil config")
	}
	if fsys == nil {
		return fmt.Errorf("resolve hwmon paths: nil filesystem")
	}

	chipToHwmon, err := buildChipMap(fsys)
	if err != nil {
		return fmt.Errorf("resolve hwmon paths: %w", err)
	}

	for i := range cfg.Sensors {
		s := &cfg.Sensors[i]
		if s.Type != "hwmon" || s.ChipName == "" {
			continue
		}
		hwmonDir, err := lookupChip("sensor", s.Name, s.ChipName, chipToHwmon)
		if err != nil {
			return err
		}
		newPath, err := rewriteHwmonPath(s.Path, hwmonDir)
		if err != nil {
			return fmt.Errorf("sensor %q: %w", s.Name, err)
		}
		s.Path = newPath
	}

	for i := range cfg.Fans {
		f := &cfg.Fans[i]
		if f.Type != "hwmon" || f.ChipName == "" {
			continue
		}
		hwmonDir, err := lookupChip("fan", f.Name, f.ChipName, chipToHwmon)
		if err != nil {
			return err
		}
		newPWM, err := rewriteHwmonPath(f.PWMPath, hwmonDir)
		if err != nil {
			return fmt.Errorf("fan %q: pwm_path: %w", f.Name, err)
		}
		f.PWMPath = newPWM
		if f.RPMPath != "" {
			newRPM, err := rewriteHwmonPath(f.RPMPath, hwmonDir)
			if err != nil {
				return fmt.Errorf("fan %q: rpm_path: %w", f.Name, err)
			}
			f.RPMPath = newRPM
		}
	}
	return nil
}

// buildChipMap walks fsys's top level for hwmonN directories and returns a
// map from the trimmed contents of each hwmonN/name to the matching hwmonN
// directory names. A chip with multiple hwmonN matches keeps all names so the
// caller can surface the ambiguity.
func buildChipMap(fsys fs.FS) (map[string][]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read hwmon root: %w", err)
	}
	m := map[string][]string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "hwmon") {
			continue
		}
		// Do NOT filter on e.IsDir(): in real sysfs every entry under
		// /sys/class/hwmon is a symlink into /sys/devices/..., and
		// DirEntry.IsDir() reads the dirent type (not the symlink
		// target) so it returns false for every live hwmon chip. The
		// fs.ReadFile below is the source of truth: if `name` is
		// missing or unreadable the entry is skipped, which covers
		// both "not a hwmon device" and "virtual class entry" cases.
		suffix := name[len("hwmon"):]
		if suffix == "" || !allDigits(suffix) {
			continue
		}
		data, err := fs.ReadFile(fsys, name+"/name")
		if err != nil {
			// hwmonN without a readable name is harmless — class entries
			// for virtual devices, or a plain file named `hwmonN` that
			// isn't a chip directory. Skip silently.
			continue
		}
		chip := strings.TrimSpace(string(data))
		if chip == "" {
			continue
		}
		m[chip] = append(m[chip], name)
	}
	// Stable order for deterministic error messages on ambiguous matches.
	for chip := range m {
		sort.Strings(m[chip])
	}
	return m, nil
}

func lookupChip(kind, entryName, chip string, chipToHwmon map[string][]string) (string, error) {
	matches := chipToHwmon[chip]
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%s %q: no hwmon device with chip_name %q", kind, entryName, chip)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%s %q: chip_name %q matches multiple hwmon devices (%s); disambiguate with hwmon_device",
			kind, entryName, chip, strings.Join(matches, ", "))
	}
}

// rewriteHwmonPath replaces the `/hwmonN` segment in path with `/newHwmon`,
// preserving the segment's trailing slash (or end-of-string) terminator.
// Returns an error if path has no `/hwmonN` segment; that signals either a
// malformed config or a path already rewritten to a non-sysfs location.
func rewriteHwmonPath(path, newHwmon string) (string, error) {
	if !hwmonIndexRe.MatchString(path) {
		return "", fmt.Errorf("path %q has no /hwmonN segment to rewrite", path)
	}
	return hwmonIndexRe.ReplaceAllString(path, "/"+newHwmon+"$2"), nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
