package detectors

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// HwmonNamesFS is the read-only filesystem surface
// KmodLoadedDetector needs. Production wires the live /sys/class/hwmon;
// tests inject a synthetic dir tree.
type HwmonNamesFS interface {
	// ReadDir returns the immediate children of path.
	ReadDir(path string) ([]os.DirEntry, error)
	// ReadFile returns the bytes of name (typically the chip's "name"
	// attribute).
	ReadFile(name string) ([]byte, error)
}

// liveHwmonNamesFS reads from the real /sys.
type liveHwmonNamesFS struct{}

func (liveHwmonNamesFS) ReadDir(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }
func (liveHwmonNamesFS) ReadFile(name string) ([]byte, error)       { return os.ReadFile(name) }

// hwmonRoot is the canonical /sys path. Tests inject the FS rather
// than overriding this constant.
const hwmonRoot = "/sys/class/hwmon"

// KmodLoadedDetector verifies every expected kernel module has a live
// hwmon entry — i.e. the module is loaded AND the chip is responding.
// "Expected" comes from the resolved catalog match's required-modules
// set; production wiring populates ExpectedModules from the
// EffectiveControllerProfile, tests pass the slice directly.
//
// Surfaces a Blocker per missing module: the controller can't drive
// PWM if its module isn't loaded. Distinct from preflight_subset's
// "OOT module needs reinstall after kernel update" — kmod_loaded
// fires when the module is supposed to be available right now and
// isn't.
type KmodLoadedDetector struct {
	// ExpectedModules is the set of hwmon "name" attributes that
	// MUST be present. Production passes the resolved catalog
	// match's modules (e.g. ["nct6687", "coretemp"]); tests pass
	// the slice directly.
	ExpectedModules []string

	// FS is the read-only filesystem surface. Defaults to
	// liveHwmonNamesFS{} when nil.
	FS HwmonNamesFS
}

// NewKmodLoadedDetector constructs a detector. fs nil → live /sys.
func NewKmodLoadedDetector(modules []string, fs HwmonNamesFS) *KmodLoadedDetector {
	if fs == nil {
		fs = liveHwmonNamesFS{}
	}
	return &KmodLoadedDetector{ExpectedModules: modules, FS: fs}
}

// Name returns the stable detector ID.
func (d *KmodLoadedDetector) Name() string { return "kmod_loaded" }

// Probe walks /sys/class/hwmon, collects every chip's name, and
// emits a Blocker Fact per expected module that didn't appear.
func (d *KmodLoadedDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	loaded := loadedHwmonNames(d.FS)
	now := timeNowFromDeps(deps)

	var facts []doctor.Fact
	for _, mod := range d.ExpectedModules {
		if _, ok := loaded[mod]; ok {
			continue
		}
		facts = append(facts, doctor.Fact{
			Detector: d.Name(),
			Severity: doctor.SeverityBlocker,
			Class:    recovery.ClassMissingModule,
			Title:    fmt.Sprintf("Expected module %s is not loaded", mod),
			Detail: fmt.Sprintf(
				"No /sys/class/hwmon/hwmon*/name reports %q. The catalog match for this system requires %s for fan control; without it ventd cannot drive PWM channels backed by this chip. Run `sudo modprobe %s`; if that fails the module isn't installed (re-run the wizard) or its dependencies aren't loaded.",
				mod, mod, mod,
			),
			EntityHash: doctor.HashEntity("kmod_loaded", mod),
			Observed:   now,
			Journal:    []string{fmt.Sprintf("loaded hwmon names: %s", sortedKeys(loaded))},
		})
	}
	return facts, nil
}

// loadedHwmonNames walks /sys/class/hwmon/hwmonN/name files and
// returns the set of distinct names. Handles ENOENT and read errors
// by skipping the offending entry — a partial read is more useful
// than no read at all.
func loadedHwmonNames(fsys HwmonNamesFS) map[string]struct{} {
	out := map[string]struct{}{}
	entries, err := fsys.ReadDir(hwmonRoot)
	if err != nil {
		return out
	}
	for _, e := range entries {
		raw, err := fsys.ReadFile(filepath.Join(hwmonRoot, e.Name(), "name"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(raw))
		if name == "" {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

// sortedKeys turns a string-set into a stable comma-joined string
// for journal entries. Determinism matters because the diag bundle's
// self-check dedupes on Fact.Journal content.
func sortedKeys(m map[string]struct{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// Compile-time check.
var _ HwmonNamesFS = liveHwmonNamesFS{}
var _ fs.ReadDirFS = (osHwmonReadDirFS{})

// osHwmonReadDirFS adapts the real filesystem to fs.ReadDirFS. Kept
// as a documentation-only adapter to mirror the modules_load detector's
// pattern; the detector itself uses the narrower HwmonNamesFS
// interface so tests don't need to construct an fs.FS root.
type osHwmonReadDirFS struct{}

func (osHwmonReadDirFS) Open(name string) (fs.File, error) { return os.Open(name) }
func (osHwmonReadDirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return os.ReadDir(name)
}
