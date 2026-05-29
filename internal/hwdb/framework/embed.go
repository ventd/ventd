package framework

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// embedded holds the vendored fw-fanctrl presets. Synced from
// github.com/TamtamHero/fw-fanctrl — see UPSTREAM for the commit SHA, the
// fw-fanctrl-AMD fork provenance, and the BSD-3-Clause license.
//
//go:embed configs/*.json UPSTREAM LICENSE.upstream
var embedded embed.FS

// Entry is one vendored config file: the parsed Config plus the source
// identifier (the filename stem, e.g. "fw-fanctrl" / "fw-fanctrl-amd").
type Entry struct {
	Config   *Config
	Source   string
	Filename string
}

// Catalog is the parsed in-memory set of vendored fw-fanctrl presets.
type Catalog struct {
	Entries  []*Entry
	bySource map[string]*Entry
}

// Lookup returns the entry for a source name (filename stem), or nil.
func (c *Catalog) Lookup(source string) *Entry {
	if c == nil {
		return nil
	}
	return c.bySource[source]
}

// Size returns the number of parsed config files.
func (c *Catalog) Size() int {
	if c == nil {
		return 0
	}
	return len(c.Entries)
}

// LoadCatalog parses the vendored embedded presets. A malformed or invalid
// config aborts the load with the offending filename named, mirroring nbfc's
// fail-closed contract (RULE-FRAMEWORK-CATALOG-01) — a half-loaded corpus is
// worse than a clear error.
func LoadCatalog() (*Catalog, error) {
	return LoadCatalogFS(embedded, "configs")
}

// LoadCatalogFS is the test-friendly variant: parses every *.json file in the
// named subdirectory of fsys. fw-fanctrl configs are strict JSON (no JSONC), so
// they decode directly — no comment-stripping pipeline like nbfc needs.
func LoadCatalogFS(fsys fs.FS, dir string) (*Catalog, error) {
	matches, err := fs.Glob(fsys, dir+"/*.json")
	if err != nil {
		return nil, fmt.Errorf("framework: glob %s: %w", dir, err)
	}
	cat := &Catalog{bySource: make(map[string]*Entry, len(matches))}
	for _, p := range matches {
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("framework: read %s: %w", p, err)
		}
		cfg := &Config{}
		if err := json.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("framework: parse %s: %w", p, err)
		}
		if err := validate(cfg); err != nil {
			return nil, fmt.Errorf("framework: %s: %w", p, err)
		}
		source := strings.TrimSuffix(path.Base(p), ".json")
		entry := &Entry{Config: cfg, Source: source, Filename: p}
		cat.Entries = append(cat.Entries, entry)
		cat.bySource[source] = entry
	}
	sort.Slice(cat.Entries, func(i, j int) bool {
		return cat.Entries[i].Source < cat.Entries[j].Source
	})
	return cat, nil
}

// validate enforces the corpus invariants (RULE-FRAMEWORK-CATALOG-02): at least
// one strategy, a defaultStrategy that resolves to a defined strategy, an
// on-discharging strategy that resolves when present, and every speedCurve
// monotonic-by-temperature with percentages in [0,100]. A vendored preset that
// names a missing strategy or an inverted curve is a sync error, not something
// to surface to an operator as a usable curve.
func validate(cfg *Config) error {
	if len(cfg.Strategies) == 0 {
		return fmt.Errorf("no strategies defined")
	}
	if strings.TrimSpace(cfg.DefaultStrategy) == "" {
		return fmt.Errorf("empty defaultStrategy")
	}
	if _, ok := cfg.Strategies[cfg.DefaultStrategy]; !ok {
		return fmt.Errorf("defaultStrategy %q is not a defined strategy", cfg.DefaultStrategy)
	}
	if d := cfg.dischargeStrategy(); d != "" {
		if _, ok := cfg.Strategies[d]; !ok {
			return fmt.Errorf("strategyOnDischarging %q is not a defined strategy", d)
		}
	}
	for name, s := range cfg.Strategies {
		if len(s.SpeedCurve) == 0 {
			return fmt.Errorf("strategy %q: empty speedCurve", name)
		}
		prevTemp := s.SpeedCurve[0].TempC
		for i, p := range s.SpeedCurve {
			if p.SpeedPct < 0 || p.SpeedPct > 100 {
				return fmt.Errorf("strategy %q point %d: speed %d out of [0,100]", name, i, p.SpeedPct)
			}
			if i > 0 && p.TempC < prevTemp {
				return fmt.Errorf("strategy %q point %d: temperature %d below previous %d (must be ascending)", name, i, p.TempC, prevTemp)
			}
			prevTemp = p.TempC
		}
	}
	return nil
}
