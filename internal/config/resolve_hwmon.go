package config

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
)

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
		if !e.IsDir() {
			continue
		}
		suffix := name[len("hwmon"):]
		if suffix == "" || !allDigits(suffix) {
			continue
		}
		data, err := fs.ReadFile(fsys, name+"/name")
		if err != nil {
			// hwmonN without a readable name is harmless — class entries
			// for virtual devices. Skip silently.
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
