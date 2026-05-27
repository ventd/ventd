package config

import (
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// hwmonRootFS is the fs.FS used by Load to re-anchor hwmon paths via
// ResolveHwmonPaths. Defaults to the live /sys/class/hwmon class
// directory; tests override via SetHwmonRootFS so the resolver can be
// driven from a fstest.MapFS fixture without touching real sysfs.
var hwmonRootFS fs.FS = os.DirFS("/sys/class/hwmon")

// SetHwmonRootFS overrides the fs.FS that Load passes to
// ResolveHwmonPaths. Tests use it to drive the resolver from a fixture.
// Pass nil to restore the default. Returns the previous root so tests
// can restore it via t.Cleanup.
func SetHwmonRootFS(fsys fs.FS) fs.FS {
	prev := hwmonRootFS
	if fsys == nil {
		hwmonRootFS = os.DirFS("/sys/class/hwmon")
	} else {
		hwmonRootFS = fsys
	}
	return prev
}

// Empty returns a minimal valid config with no fans, sensors, or controls.
// Used when starting the daemon before first-boot setup is complete.
//
// Collection fields are initialised to non-nil empty slices so /api/config
// renders them as `[]` rather than `null`. The UI iterates these lists
// without a null-guard; a JSON null would throw TypeError mid-render.
func Empty() *Config {
	return &Config{
		Version:      CurrentVersion,
		PollInterval: Duration{Duration: DefaultPollInterval},
		Web: Web{
			Listen:     "127.0.0.1:9999",
			SessionTTL: Duration{Duration: DefaultSessionTTL},
		},
		Sensors:  []Sensor{},
		Fans:     []Fan{},
		Curves:   []CurveConfig{},
		Controls: []Control{},
	}
}

// Load reads the YAML config at path, validates it, and re-anchors any
// hwmon Sensor / Fan paths whose ChipName is set so they survive a
// hwmonN renumbering across reboots. Re-anchor failures (chip missing,
// chip ambiguous, malformed path) are fatal — refusing to start is
// safer than writing PWM to a wrong-chip sysfs file. Entries with empty
// ChipName are left untouched.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, err
	}
	// One-shot compatibility repairs for configs written by earlier
	// ventd versions. Currently only repopulates web.tls_cert/tls_key
	// from a sibling first-boot keypair, so post-F2 installs stop
	// crashlooping on configs that pre-date the relevant Save() fix.
	// Mutations are persisted here so the next boot is idempotent.
	if mutated, mErr := Migrate(cfg, path, nil); mErr != nil {
		return nil, fmt.Errorf("migrate config %s: %w", path, mErr)
	} else if mutated {
		if _, sErr := Save(cfg, path); sErr != nil {
			return nil, fmt.Errorf("persist migrated config %s: %w", path, sErr)
		}
	}
	// Self-heal upgrade case: if the on-disk config pre-dates the
	// ChipName field and the hwmon paths are still valid (no
	// renumber happened), populate ChipName from the live name file
	// so future renumbers can be re-anchored. If a renumber DID
	// happen, the read fails silently and the resolver call below
	// will surface the misconfiguration loudly.
	EnrichChipName(cfg)
	if err := ResolveHwmonPaths(cfg, hwmonRootFS); err != nil {
		return nil, fmt.Errorf("resolve hwmon paths in %s: %w", path, err)
	}
	return cfg, nil
}

func Parse(data []byte) (*Config, error) {
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Migrate legacy raw-PWM curve fields to / from their `_pct` siblings
	// BEFORE validate so the MinPWM<=MaxPWM and points-ordering checks
	// see the populated raw values regardless of which form the YAML
	// used. Warnings ride slog.Default() so CLI and tests still see them
	// without having to plumb a logger through every Parse call site.
	MigrateCurvePWMFields(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
