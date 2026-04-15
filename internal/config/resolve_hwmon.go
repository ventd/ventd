package config

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────
// REVIEW NOTES — phoenixdnb
// ─────────────────────────────────────────────────────────────────────
//
// Pre-merge review of the resolver. Tests cover the resolver in
// isolation (100% line coverage per PR #20 body); the concerns below
// are about production-path integration, not the resolver logic.
//
// STATUS
//   UNTESTED ON RIG (phoenix-MS-7D25, nct6687 + RTX 4090).
//   Live verification still pending:
//     1. Force hwmon renumber: `rmmod nct6687; modprobe nct6687` and
//        restart ventd. Confirm Sensor.Path / Fan.PWMPath / Fan.RPMPath
//        all point at the new hwmonN after re-read.
//     2. Ambiguous-chip path (two hwmonN entries with matching `name`)
//        needs a dual-superIO board to reproduce live. Synthesizable
//        via the MapFS fixture in tests, so the error path is covered;
//        just no bare-metal confirmation yet.
//
// CROSS-REF securitytodo.md item #2 ("resolve hwmon paths by chip
// name"). This PR closes the RESOLVER half only. Item #2 is not fully
// retired until the follow-up Load()-wiring PR lands AND the config
// writer in internal/setup/setup.go starts populating ChipName (see
// concern #4 below).
//
// CONCERNS
//
// 1. Partial mutation on error.
//    Doc contract: "on error callers must assume partial mutation and
//    reject the Config". Easy footgun at the call site. Consider a
//    two-pass design: pass 1 resolves every (entry, hwmonDir, new path)
//    tuple with no mutation, pass 2 applies them. Either every entry
//    is migrated or the config is untouched — no "half-rewritten"
//    state for the caller to police.
//
// 2. Case sensitivity of ChipName.
//    Kernel hwmon names are always lowercase (nct6687, it87, amdgpu),
//    but operator-authored YAML may use uppercase. buildChipMap and
//    lookupChip are case-sensitive, so "NCT6687" silently misses.
//    Either strings.ToLower on both sides, or document the lowercase
//    requirement next to the YAML field in internal/config/config.go.
//    (No doc edit in this review pass — leaving it for the author.)
//
// 3. No existence check on rewrite target.
//    After rewriting /hwmon3/pwm1 → /hwmon4/pwm1, the new path is not
//    stat'd. If chip name matches but the specific pwm/sensor node is
//    absent on the new hwmonN (firmware/driver revision skew), the
//    failure surfaces later as a generic open() EACCES/ENOENT with no
//    indication the resolver was involved. An optional fs.Stat on the
//    rewritten path would fail earlier with a clearer message.
//    Trade-off: couples the resolver to the live fs when today it is
//    pure.
//
// 4. ChipName is not populated by the config writer.
//    internal/setup/setup.go constructs config.Sensor{}/config.Fan{}
//    at multiple sites (setup.go:478, :531, :691, :701, :709, :832,
//    :871) and none set ChipName. This means:
//      (a) existing deployments that regenerated a config before this
//          PR lands will see zero benefit from the resolver — every
//          entry has empty ChipName and is skipped by design.
//      (b) the follow-up Load()-wiring PR is NOT sufficient by itself
//          to close securitytodo.md item #2. The config writer must
//          also be taught to read hwmonN/name at enumeration time and
//          populate the field.
//    Flag this in the Load()-wiring PR's test plan, or (preferable)
//    fold the writer change in alongside the wiring so the promise
//    "survive hwmon renumbering" is true for first-boot users, not
//    only operators who hand-edit ChipName after upgrade.
//
// 5. Zero-terminal goal.
//    ventd's README promises "zero terminal after install". For the
//    renumber-survival feature to honour that, the operator must never
//    have to author ChipName themselves. Tied to concern #4 — the
//    writer must do it automatically. Any flow that requires manual
//    config edits to unlock renumber survival breaks the zero-terminal
//    promise.
//
// 6. Caller concurrency.
//    ResolveHwmonPaths mutates cfg in place. Expected to be called
//    once at Load(), pre-controllers, single-threaded. Worth a
//    doc-comment assertion on the public function; an accidental
//    runtime call concurrent with reads is a data race.
//
// README-DRIFT
//   No direct contradiction yet — README.md does not mention chip-name
//   resolution. It WILL drift once the renumber-survival promise is
//   made to users. Address in a follow-up doc PR, not here (rule:
//   no README edits in code review).
//
// ─────────────────────────────────────────────────────────────────────

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
