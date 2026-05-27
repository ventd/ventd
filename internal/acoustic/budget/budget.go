package budget

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/acoustic/proxy"
	acrunner "github.com/ventd/ventd/internal/acoustic/runner"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/nvidia"
)

// Build assembles the per-tick AcousticBudget for the candidate channel
// using R33 (no-microphone psychoacoustic proxy):
//
//   - Target:     PresetDBATargets[preset] (or operator override) — the
//     dBA cap the gate must not exceed.
//   - CurrentDBA: proxy.Compose() over every configured hwmon fan whose
//     RPM is currently readable, plus R30's per-host K_cal
//     mic-calibration offset when /var/lib/ventd/acoustic/
//     k_cal.json is present. Without K_cal the value is in
//     within-host "au" (today's behaviour); with K_cal it is
//     dBA at the mic position. (#1281)
//   - DBAPerPWM:  proxy.CostRate() for the candidate channel using the
//     fan's measured RPM and a default-classified blade
//     count. Cost-rate is multiplied by the preset weight
//     (Silent 3x cost-averse, Performance 0.2x).
//
// Per-tick cost: one open+read+close per configured hwmon fan
// (typically 1-8) + one optional read of the K_cal file. At ~50 µs
// each that's well under the controller's 2 s tick budget. Returns a
// zero AcousticBudget when no RPMs are readable (host in early boot,
// every fan tach offline) — the gate treats Target=0 as "disabled"
// and the controller behaves identically to the v0.5.11 no-budget
// path.
func Build(live *config.Config, chID string, preset controller.Preset) controller.AcousticBudget {
	if live == nil {
		return controller.AcousticBudget{}
	}
	target := controller.DBATargetFor(preset, live.Smart.DBATarget)
	if target <= 0 {
		return controller.AcousticBudget{}
	}

	// Compose host loudness from every fan with a readable RPM.
	// hwmon fans read /sys; NVIDIA fans call nvmlDeviceGetFanSpeedRPM
	// (R535+); ErrFanRPMUnsupported / older driver / pre-Maxwell GPU
	// silently skips the fan rather than failing the whole budget.
	// (#1282)
	fans := make([]proxy.Fan, 0, len(live.Fans))
	var candidateRPM float64
	for _, f := range live.Fans {
		var (
			rpm        float64
			ok         bool
			class      proxy.FanClass
			diameterMM float64
			bladeCount int
		)
		switch f.Type {
		case "hwmon":
			if f.RPMPath == "" {
				continue
			}
			r, gotRPM := readRPMSafe(f.RPMPath)
			if !gotRPM || r <= 0 {
				continue
			}
			rpm, ok = float64(r), true
			// hwmon fans honour the curated hwdb fan_profiles entry
			// when present; otherwise resolveFanShape falls through
			// to the name-hint heuristic + 120mm default. (#1283)
			class, diameterMM, bladeCount = resolveFanShape(f)
		case "nvidia":
			r, err := readNvidiaFanRPM(f.PWMPath)
			if err != nil || r <= 0 {
				continue
			}
			rpm, ok = float64(r), true
			// GPU fans use ClassGPUShroud + a name-hint diameter
			// heuristic (#1282); the hwdb catalog doesn't carry
			// per-GPU FanProfiles today.
			class = proxy.ClassGPUShroud
			diameterMM = nvidiaShroudDiameterMM(f.Name)
		default:
			continue
		}
		if !ok {
			continue
		}
		fans = append(fans, proxy.Fan{
			Class:      class,
			DiameterMM: diameterMM,
			BladeCount: bladeCount,
			RPM:        rpm,
		})
		if f.Name == chID || f.PWMPath == chID {
			candidateRPM = rpm
		}
	}
	if len(fans) == 0 {
		return controller.AcousticBudget{}
	}

	current := proxy.Compose(fans) + loadKCalOffset()

	// CostRate for the candidate channel. When the candidate RPM is
	// unknown (chID didn't match any hwmon fan — typically because
	// chID is an nvidia path), DBAPerPWM stays 0 and the gate has
	// nothing per-PWM to refuse — Target alone still bounds via the
	// current-vs-target check.
	var dbaPerPWM float64
	if candidateRPM > 0 {
		dbaPerPWM = proxy.CostRate(
			proxy.ClassCase120140,
			candidateRPM,
			120,
			0, 0,
			5.0, // typical 4-pin PWM consumer fan slope
			presetMultiplierFor(preset),
		)
	}

	return controller.AcousticBudget{
		Target:     target,
		CurrentDBA: current,
		DBAPerPWM:  dbaPerPWM,
	}
}

// readRPMSafe reads a hwmon fan*_input file and returns the int RPM.
// Failure returns (0, false). Used by Build to compose per-tick host
// loudness without holding the controller hot path on blocking IO —
// sysfs reads are kernel-buffered single-page transfers, typically
// <50 µs each.
func readRPMSafe(path string) (int, bool) {
	if path == "" {
		return 0, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(raw))
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	if v < 0 {
		return 0, false
	}
	return v, true
}

// readNvidiaFanRPMFn is the package-level seam tests use to inject a
// deterministic RPM source without spinning up libnvidia-ml. Default
// is nvidia.ReadFanRPM; budget's nvidia_rpm_test.go points it at a
// fixture. (#1282)
var readNvidiaFanRPMFn = nvidia.ReadFanRPM

// readNvidiaFanRPM resolves a config.Fan{Type:"nvidia"} entry's
// PWMPath (encoded as the GPU index decimal string) into the live
// NVML-reported fan RPM. Failure surfaces (0, err) so the budget
// builder skips the fan rather than failing the host total. (#1282)
func readNvidiaFanRPM(pwmPath string) (uint32, error) {
	idx, err := strconv.ParseUint(strings.TrimSpace(pwmPath), 10, 32)
	if err != nil {
		return 0, err
	}
	return readNvidiaFanRPMFn(uint(idx))
}

// nvidiaShroudDiameterMM picks an axial-shroud diameter heuristic
// from the GPU fan's operator-visible name: triple-fan aftermarket
// AIBs (Aorus, Strix, TUF, Gaming X) ship 120mm shrouds; everything
// else defaults to the 80mm Founders-Edition-class shroud. The
// proxy's tip-speed math scales with diameter², so this distinction
// matters for multi-GPU workstations whose loudness is GPU-fan-
// dominated. (#1282)
func nvidiaShroudDiameterMM(name string) float64 {
	lname := strings.ToLower(name)
	for _, hint := range []string{"aorus", "strix", "tuf", "gaming x", "trinity", "amp", "triple"} {
		if strings.Contains(lname, hint) {
			return 120
		}
	}
	return 80
}

// fanProfileCatalogPtr is the catalog source of per-fan class +
// diameter metadata. The daemon's startup wires the matched
// BoardCatalogEntry here (when DMI / DT / chip-probe matched a
// tier-1/1.5 board); Build reads from it to replace the name-hint
// heuristics with the curated catalog template. Nil = no catalog match
// (no regression — falls back to defaults). (#1283)
var fanProfileCatalogPtr atomic.Pointer[hwdb.BoardCatalogEntry]

// SetFanProfileCatalog publishes the matched board entry so the
// smart-mode acoustic-budget builder can look up per-fan class +
// diameter overrides. Safe to call multiple times: lock-free atomic
// swap. Passing nil disables the catalog path (heuristics-only).
// (#1283)
func SetFanProfileCatalog(entry *hwdb.BoardCatalogEntry) {
	fanProfileCatalogPtr.Store(entry)
}

// resolveFanShape returns the (class, diameter_mm, blade_count) for
// a config fan. When the matched hwdb board entry has a FanProfile
// keyed on this fan's PWM channel (the basename of f.PWMPath), the
// catalog values take precedence; otherwise the name-hint heuristic
// + 120mm default + per-class blade-count default applies. (#1283)
func resolveFanShape(f config.Fan) (proxy.FanClass, float64, int) {
	entry := fanProfileCatalogPtr.Load()
	if entry != nil && f.Type == "hwmon" && f.PWMPath != "" {
		channel := filepath.Base(f.PWMPath)
		if fp, ok := hwdb.LookupFanProfile(entry, channel); ok {
			class := proxy.FanClass(fp.Class)
			diameter := float64(fp.DiameterMM)
			if diameter <= 0 {
				diameter = 120
			}
			return class, diameter, fp.DefaultBladeCount
		}
	}
	return DefaultFanClassFor(f), 120, 0
}

// DefaultFanClassFor returns the acoustic-proxy FanClass for a config
// fan entry. Without per-fan blade/diameter calibration data, we
// classify by chip-name + label heuristics: AIO pumps get ClassAIOPump,
// laptop blowers ClassLaptopBlower, everything else ClassCase120140.
// v0.6.x extension: read per-fan class from the wizard's catalog
// overlay once spec-15 sub-issue lands.
func DefaultFanClassFor(f config.Fan) proxy.FanClass {
	if f.IsPump {
		return proxy.ClassAIOPump
	}
	name := strings.ToLower(f.Name)
	switch {
	case strings.Contains(name, "blower"):
		return proxy.ClassLaptopBlower
	case strings.Contains(name, "pump"):
		return proxy.ClassAIOPump
	case strings.Contains(name, "gpu") || f.Type == "nvidia":
		return proxy.ClassGPUShroud
	default:
		return proxy.ClassCase120140
	}
}

// kCalPath is the persisted R30 microphone calibration JSON. Set as
// a package-level variable so tests can point at a fixture path
// without monkey-patching the global filesystem. (#1281)
var kCalPath = acrunner.DefaultKCalPath

// kcalCacheEntry memoises the parsed K_cal offset so the per-tick
// acoustic budget doesn't re-open and JSON-decode k_cal.json on every
// controller tick (Build runs once per fan per tick). The cache is
// gated on the file's path + mtime + size: a single stat syscall
// replaces the open+read+unmarshal on the hot path, while a
// recalibration that rewrites the file (changed mtime/size) is still
// picked up on the next call — so the observable result is identical to
// an uncached LoadResult, only cheaper.
type kcalCacheEntry struct {
	mu      sync.Mutex
	path    string
	modTime time.Time
	size    int64
	offset  float64
	valid   bool
}

// kcalCache is the process-wide K_cal memo. A value (not pointer) so it
// is zero-initialised cold; get() uses a pointer receiver, and the only
// access is through loadKCalOffset, so it is never copied.
var kcalCache kcalCacheEntry

// loadKCalOffset returns the K_cal offset (dB) when the per-host
// microphone calibration record at kCalPath is present and parseable,
// 0 otherwise. The offset is added to proxy.Compose() so the host
// loudness reported by /api/v1/smart/status.acoustic.current_dba is
// true dBA at the mic position when calibrated, and the within-host
// au scale otherwise. (#1281)
//
// The parse result is memoised behind an mtime/size gate (kcalCacheEntry)
// so repeated per-tick calls cost a single stat rather than a full
// open+read+unmarshal; a rewritten k_cal.json is detected by its changed
// mtime/size and reloaded on the next call.
func loadKCalOffset() float64 {
	return kcalCache.get(kCalPath)
}

// get returns the cached offset when the file at path is unchanged since
// the last parse (same path, mtime and size), otherwise reloads. A
// missing or unreadable file yields offset 0 and invalidates the cache,
// so a later-written file is picked up on the next call — matching the
// uncached LoadResult fallback exactly. The lock is held across the
// reload so concurrent per-tick callers collapse to a single re-parse.
func (c *kcalCacheEntry) get(path string) float64 {
	fi, statErr := os.Stat(path)

	c.mu.Lock()
	defer c.mu.Unlock()

	if statErr != nil {
		c.valid = false
		return 0
	}
	if c.valid && c.path == path && c.size == fi.Size() && c.modTime.Equal(fi.ModTime()) {
		return c.offset
	}

	r, present, err := acrunner.LoadResult(path)
	if err != nil || !present {
		c.valid = false
		return 0
	}
	c.path = path
	c.size = fi.Size()
	c.modTime = fi.ModTime()
	c.offset = r.KCalOffset
	c.valid = true
	return c.offset
}

// micCalibrated reports whether a K_cal calibration record is
// present and parseable at kCalPath. Surfaces a mic_calibrated boolean
// so the UI can render a "calibrate mic" hint when the displayed
// current_dba is still in au. (#1281)
func micCalibrated() bool {
	_, present, err := acrunner.LoadResult(kCalPath)
	return err == nil && present
}

// presetMultiplierFor maps the controller's Preset enum to the
// acoustic-proxy's PresetMultiplier so CostRate scales with the
// operator's quietness preference. R33-LOCK-09.
func presetMultiplierFor(p controller.Preset) proxy.PresetMultiplier {
	switch p {
	case controller.PresetSilent:
		return proxy.PresetSilent
	case controller.PresetPerformance:
		return proxy.PresetPerformance
	default:
		return proxy.PresetBalanced
	}
}
