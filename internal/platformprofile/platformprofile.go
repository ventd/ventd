// Package platformprofile observes AND drives the Linux kernel's generic
// platform_profile interface (/sys/class/platform-profile/platform-profile-N/)
// on hardware that exposes it (Dell, Lenovo, HP, ASUS, …). Each profile
// (typical choices: "cool", "quiet", "balanced", "performance") changes the
// firmware's thermal/fan envelope without ventd writing a single pwm byte.
//
// Per ventd's zero-config-smart design philosophy, the controller is ACTIVE
// by default: detect → decide → drive → observe → learn. There is no
// observe-only mode behind a flag; ventd is supposed to figure out the
// right envelope for the specific hardware and adjust over time. Operators
// can disable the controller entirely (config: platform_profile.disable),
// but the default is on.
//
// Inputs to the selector:
//   - TJmax (from coretemp temp1_crit) — the upper bound the BIOS thermal
//     control aims to avoid.
//   - Current package temperature.
//   - Fan max RPM + current RPM — saturation tells us how hard the BIOS is
//     already pushing the fan within the current envelope.
//   - CPU TDP (RAPL package power limit) + current draw — pressure tells us
//     whether the workload is sustained or transient.
//
// Decision policy is a layered heuristic plus a persisted learning store
// that records (decision, observed outcome) so a future revision can refine
// thresholds without code changes. v1 ships with the static rules below and
// the observation pipeline already wired.
package platformprofile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultSysfsRoot is the production root for platform-profile enumeration.
const DefaultSysfsRoot = "/sys/class/platform-profile"

// Snapshot captures one point-in-time read of the platform_profile interface.
type Snapshot struct {
	// Available is the kernel-reported list of allowed profile values, e.g.
	// ["cool", "quiet", "balanced", "performance"]. Sorted lexicographically
	// for stable diffs.
	Available []string `json:"available"`
	// Current is the active profile, e.g. "balanced". Empty if unset.
	Current string `json:"current"`
	// Path is the sysfs directory the snapshot was read from. Useful for
	// debugging multi-profile systems.
	Path string `json:"path"`
	// Present reports whether the platform-profile interface was found at
	// all. False means the kernel/firmware doesn't expose one (most
	// pre-2020 hardware, or vendors who never wired it up).
	Present bool `json:"present"`
}

// Read scans DefaultSysfsRoot for the first platform-profile-N directory and
// returns its Snapshot. Returns a Snapshot with Present:false when no
// platform_profile interface is found — this is a normal condition on
// hardware that doesn't expose one, not an error.
func Read() (*Snapshot, error) {
	return ReadAt(os.DirFS("/"), strings.TrimPrefix(DefaultSysfsRoot, "/"))
}

// ReadAt is the test-injectable form: scan a caller-supplied fs.FS rooted at
// the provided sysfs path (e.g. "sys/class/platform-profile"). Production
// callers use Read which roots at /sys/class/platform-profile.
func ReadAt(fsys fs.FS, sysfsDir string) (*Snapshot, error) {
	entries, err := fs.ReadDir(fsys, sysfsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Snapshot{Present: false}, nil
		}
		return nil, err
	}

	// Find the first platform-profile-N directory. Most systems expose
	// exactly one; multi-profile hosts (rare) require explicit selection
	// by index, which we don't implement here. Sort for determinism.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "platform-profile-") {
			continue
		}
		dir := filepath.Join(sysfsDir, e.Name())
		return readProfileDir(fsys, dir)
	}

	return &Snapshot{Present: false}, nil
}

func readProfileDir(fsys fs.FS, dir string) (*Snapshot, error) {
	choicesRaw, err := fs.ReadFile(fsys, filepath.Join(dir, "choices"))
	if err != nil {
		return nil, err
	}
	current, err := fs.ReadFile(fsys, filepath.Join(dir, "profile"))
	if err != nil {
		// 'profile' may not be readable on some kernels but 'choices' is;
		// surface what we have rather than failing the whole snapshot.
		current = nil
	}

	choices := strings.Fields(string(choicesRaw))
	sort.Strings(choices)

	return &Snapshot{
		Available: choices,
		Current:   strings.TrimSpace(string(current)),
		Path:      "/" + dir,
		Present:   true,
	}, nil
}

// Write sets the active profile via the standard sysfs interface. Returns an
// error if profile is not in the current Available set or if the sysfs write
// fails. Production code uses WriteAt(os.DirFS("/"), ...).
func Write(profile string) error {
	snap, err := Read()
	if err != nil {
		return err
	}
	if !snap.Present {
		return errors.New("platform_profile: no interface present")
	}
	if !contains(snap.Available, profile) {
		return errors.New("platform_profile: " + profile + " not in available choices")
	}
	return os.WriteFile(snap.Path+"/profile", []byte(profile+"\n"), 0o644)
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
