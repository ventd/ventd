package asus

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// embedded holds the vendored g-helper curve presets. Synced from
// github.com/seerge/g-helper — see UPSTREAM for the commit SHA and the GPL-3.0
// license note.
//
//go:embed configs/*.json UPSTREAM LICENSE.upstream
var embedded embed.FS

// Entry is one vendored config file: the parsed Config plus the source
// identifier (the filename stem, e.g. "g-helper").
type Entry struct {
	Config   *Config
	Source   string
	Filename string
}

// Catalog is the parsed in-memory set of vendored g-helper presets, indexed by
// source file and by performance-mode name.
type Catalog struct {
	Entries  []*Entry
	bySource map[string]*Entry
	byMode   map[string]Preset
}

// Lookup returns the entry for a source name (filename stem), or nil.
func (c *Catalog) Lookup(source string) *Entry {
	if c == nil {
		return nil
	}
	return c.bySource[source]
}

// Mode returns the preset for a performance-mode name (silent/balanced/turbo)
// and whether it was found. Modes are deduplicated across source files: the
// first file to define a mode wins (there is one source file today).
func (c *Catalog) Mode(mode string) (Preset, bool) {
	if c == nil {
		return Preset{}, false
	}
	p, ok := c.byMode[mode]
	return p, ok
}

// Modes returns the available performance-mode names in deterministic
// (sorted) order.
func (c *Catalog) Modes() []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.byMode))
	for name := range c.byMode {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Size returns the number of parsed config files.
func (c *Catalog) Size() int {
	if c == nil {
		return 0
	}
	return len(c.Entries)
}

// LoadCatalog parses the vendored embedded presets.
func LoadCatalog() (*Catalog, error) {
	return LoadCatalogFS(embedded, "configs")
}

// LoadCatalogFS is the test-friendly variant: parses every *.json file in the
// named subdirectory of fsys. g-helper configs are strict JSON, so they decode
// directly — no comment-stripping pipeline like nbfc needs. A malformed or
// invalid config aborts the load with the offending filename named, mirroring
// the nbfc / framework fail-closed contract (RULE-ASUS-CATALOG-01) — a
// half-loaded corpus is worse than a clear error.
func LoadCatalogFS(fsys fs.FS, dir string) (*Catalog, error) {
	matches, err := fs.Glob(fsys, dir+"/*.json")
	if err != nil {
		return nil, fmt.Errorf("asus: glob %s: %w", dir, err)
	}
	cat := &Catalog{
		bySource: make(map[string]*Entry, len(matches)),
		byMode:   make(map[string]Preset),
	}
	for _, p := range matches {
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("asus: read %s: %w", p, err)
		}
		cfg := &Config{}
		if err := json.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("asus: parse %s: %w", p, err)
		}
		if err := validate(cfg); err != nil {
			return nil, fmt.Errorf("asus: %s: %w", p, err)
		}
		source := strings.TrimSuffix(path.Base(p), ".json")
		entry := &Entry{Config: cfg, Source: source, Filename: p}
		cat.Entries = append(cat.Entries, entry)
		cat.bySource[source] = entry
		for _, preset := range cfg.Presets {
			if _, exists := cat.byMode[preset.Mode]; !exists {
				cat.byMode[preset.Mode] = preset
			}
		}
	}
	sort.Slice(cat.Entries, func(i, j int) bool {
		return cat.Entries[i].Source < cat.Entries[j].Source
	})
	return cat, nil
}

// validate enforces the corpus invariants (RULE-ASUS-CATALOG-02): at least one
// preset, every preset names a mode and carries both a CPU and a GPU curve, and
// every curve is monotonic-by-temperature with duty percentages in [0,100]. A
// vendored preset with an inverted curve or an out-of-range duty is a sync
// error, not something to surface to an operator as a usable curve.
func validate(cfg *Config) error {
	if len(cfg.Presets) == 0 {
		return fmt.Errorf("no presets defined")
	}
	seen := make(map[string]bool, len(cfg.Presets))
	for i, p := range cfg.Presets {
		if strings.TrimSpace(p.Mode) == "" {
			return fmt.Errorf("preset %d: empty mode", i)
		}
		if seen[p.Mode] {
			return fmt.Errorf("preset %q: duplicate mode", p.Mode)
		}
		seen[p.Mode] = true
		if err := validateCurve(p.Mode, "cpu", p.CPU); err != nil {
			return err
		}
		if err := validateCurve(p.Mode, "gpu", p.GPU); err != nil {
			return err
		}
	}
	return nil
}

// validateCurve checks one device curve: non-empty, ascending temperatures,
// duty in [0,100].
func validateCurve(mode, device string, pts []CurvePoint) error {
	if len(pts) == 0 {
		return fmt.Errorf("preset %q %s: empty curve", mode, device)
	}
	prevTemp := pts[0].TempC
	for i, pt := range pts {
		if pt.Pct < 0 || pt.Pct > 100 {
			return fmt.Errorf("preset %q %s point %d: duty %d out of [0,100]", mode, device, i, pt.Pct)
		}
		if i > 0 && pt.TempC < prevTemp {
			return fmt.Errorf("preset %q %s point %d: temperature %d below previous %d (must be ascending)", mode, device, i, pt.TempC, prevTemp)
		}
		prevTemp = pt.TempC
	}
	return nil
}
