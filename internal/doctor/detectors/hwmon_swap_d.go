package detectors

import (
	"context"
	"fmt"
	"sort"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// HwmonSwapDetector watches for hwmon enumeration changing since
// daemon start. /sys/class/hwmon/hwmonN numbers are kernel-assigned
// at boot AND at module reload — RULE-HWMON-INDEX-UNSTABLE pins the
// invariant that paths must be resolved via the chip's "name"
// attribute, not the index. This detector is the runtime watchdog:
// if the hwmon0/hwmon1 mapping flipped underneath us, the controller's
// cached PWM paths now point at the wrong chip and need re-resolution.
//
// Production wires the daemon's startup-time map (chip-name → hwmonN
// index) and the live HwmonNamesFS reader. Discrepancy → Blocker
// because the controller may be writing to the wrong chip.
type HwmonSwapDetector struct {
	// Baseline is the chip-name → hwmon-dir map captured at daemon
	// start. Set by the wiring layer.
	Baseline map[string]string

	// FS is the live filesystem reader. Reuses the kmod_loaded
	// detector's HwmonNamesFS interface.
	FS HwmonNamesFS
}

// NewHwmonSwapDetector constructs a detector with the given baseline
// map and filesystem reader. fs nil → live /sys.
func NewHwmonSwapDetector(baseline map[string]string, fs HwmonNamesFS) *HwmonSwapDetector {
	if fs == nil {
		fs = liveHwmonNamesFS{}
	}
	cp := make(map[string]string, len(baseline))
	for k, v := range baseline {
		cp[k] = v
	}
	return &HwmonSwapDetector{Baseline: cp, FS: fs}
}

// Name returns the stable detector ID.
func (d *HwmonSwapDetector) Name() string { return "hwmon_swap" }

// Probe re-walks /sys/class/hwmon and compares each chip's current
// hwmonN dir to the baseline. Emits a Blocker per chip whose dir
// shifted.
func (d *HwmonSwapDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(d.Baseline) == 0 {
		return nil, nil
	}

	live := liveHwmonNameToDir(d.FS)
	now := timeNowFromDeps(deps)

	// Stable-order diff. Iterate baseline keys in sorted order so
	// the JSON output is reproducible run-to-run.
	names := make([]string, 0, len(d.Baseline))
	for k := range d.Baseline {
		names = append(names, k)
	}
	sort.Strings(names)

	var facts []doctor.Fact
	for _, name := range names {
		baseDir := d.Baseline[name]
		liveDir, ok := live[name]
		if !ok {
			facts = append(facts, doctor.Fact{
				Detector:   d.Name(),
				Severity:   doctor.SeverityBlocker,
				Class:      recovery.ClassUnknown,
				Title:      fmt.Sprintf("Chip %s no longer enumerated under /sys/class/hwmon", name),
				Detail:     fmt.Sprintf("At daemon start the chip %q was at %s. It's no longer present — the kernel module unloaded, the device hot-removed, or sysfs lost the entry. The controller's cached path is stale; PWM writes will fail with ENOENT.", name, baseDir),
				EntityHash: doctor.HashEntity("hwmon_swap_disappeared", name, baseDir),
				Observed:   now,
			})
			continue
		}
		if liveDir != baseDir {
			facts = append(facts, doctor.Fact{
				Detector:   d.Name(),
				Severity:   doctor.SeverityBlocker,
				Class:      recovery.ClassUnknown,
				Title:      fmt.Sprintf("Chip %s moved from %s to %s", name, baseDir, liveDir),
				Detail:     fmt.Sprintf("hwmon enumeration changed underneath the running daemon. The controller's cached PWM paths still reference %s; writes will hit the WRONG chip. Restart ventd so it re-resolves paths from /sys/class/hwmon/<dir>/name.", baseDir),
				EntityHash: doctor.HashEntity("hwmon_swap_moved", name, baseDir, liveDir),
				Observed:   now,
			})
		}
	}
	return facts, nil
}

// liveHwmonNameToDir walks /sys/class/hwmon and returns a map of
// (name attribute → hwmonN dir). When two chips share a name (e.g.
// two drivetemp drives), only the first wins — that's a known
// limitation of name-only resolution and is RULE-HWMON-INDEX-UNSTABLE
// territory; the swap detector's purpose is the singleton case.
func liveHwmonNameToDir(fs HwmonNamesFS) map[string]string {
	out := map[string]string{}
	entries, err := fs.ReadDir(hwmonRoot)
	if err != nil {
		return out
	}
	for _, e := range entries {
		raw, err := fs.ReadFile(hwmonRoot + "/" + e.Name() + "/name")
		if err != nil {
			continue
		}
		name := trimNL(string(raw))
		if name == "" {
			continue
		}
		if _, exists := out[name]; !exists {
			out[name] = e.Name()
		}
	}
	return out
}

// trimNL trims trailing newline + spaces. Local helper to avoid
// pulling strings.TrimSpace into hot-path map operations (the
// /sys/class/hwmon/N/name files always end in '\n').
func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
