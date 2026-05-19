package platformprofile

import (
	"fmt"
	"sort"
)

// HardwareSummary captures one-shot hardware capabilities the selector
// needs. Populated once at controller startup from coretemp, RAPL, and
// hwmon discovery.
type HardwareSummary struct {
	// CPUModel is the human-readable CPU model string from
	// /proc/cpuinfo (e.g. "Intel(R) Core(TM) i7-6600U CPU @ 2.60GHz").
	CPUModel string
	// TJmaxC is the thermal junction maximum in degrees Celsius, read
	// from temp1_crit on the coretemp hwmon chip. Used as the temperature
	// ceiling — the BIOS thermal control aims to avoid this.
	TJmaxC int
	// TDPWatts is the package power limit (PL1) in whole watts, read from
	// /sys/class/powercap/intel-rapl/intel-rapl:0/constraint_0_power_limit_uw
	// (microwatts) and converted. Helps interpret current draw as a
	// fraction of the design envelope.
	TDPWatts int
	// FanMaxRPM is the maximum RPM the dell-smm-hwmon (or other) driver
	// reports via fan1_max. Used to compute fan saturation.
	FanMaxRPM int
	// FanCount is the number of fan channels visible to ventd. 1 on
	// most laptops, 2-4 on towers / workstations.
	FanCount int
	// ChassisClass is "laptop", "desktop", or "server" — derived from
	// DMI chassis_type. Influences how aggressive the selector is
	// (laptops have stricter thermal envelopes and benefit more from
	// power tuning).
	ChassisClass string
	// StateQuantizedN, when > 0, signals the underlying hwmon driver
	// exposes a state-quantized fan surface (see hwdb v1.4). The
	// selector reasons differently about fan saturation on these
	// because RPM jumps are discrete, not continuous.
	StateQuantizedN int
}

// Inputs are the live readings sampled each tick.
type Inputs struct {
	// CurrentTempC is the package temperature in degrees Celsius (read
	// from coretemp temp1_input).
	CurrentTempC float64
	// CurrentRPM is the live fan RPM (read from fan1_input).
	CurrentRPM int
	// CurrentTDPWatts is the live RAPL package power draw in watts.
	// Zero or negative if RAPL is unavailable.
	CurrentTDPWatts float64
	// CPULoadPct is the 1-minute load average expressed as a percentage
	// of available logical CPUs (e.g. 50 means half the cores are busy).
	CPULoadPct float64
}

// Decision is the selector's output: which profile to set, plus a
// human-readable reason a future learning loop can persist + revisit.
type Decision struct {
	Profile string
	Reason  string
	// PressureScore is a derived 0..1 metric the controller logs
	// alongside the decision. >= 0.85 → "performance", <= 0.35 → quiet
	// tier (the lowest-aggressive available), middle → balanced.
	PressureScore float64
}

// Selector turns Hardware + Inputs into a profile choice. Policy is
// intentionally simple in v1 — the learning store records outcomes so a
// future version can refine the thresholds from observed data.
type Selector struct {
	hw       HardwareSummary
	choices  []string
	quietest string // lowest-aggressive profile available
	mid      string // typically "balanced"
	hottest  string // typically "performance"
}

// NewSelector validates the available-profile list, picks canonical
// "quietest" / "mid" / "hottest" anchors, and returns a configured
// Selector. Returns error if the profile list doesn't contain at least
// one anchor we recognise.
func NewSelector(hw HardwareSummary, available []string) (*Selector, error) {
	if len(available) == 0 {
		return nil, fmt.Errorf("platform_profile: empty available list")
	}
	// Canonical anchors. Order matters: most-quiet → most-aggressive.
	// We prefer specific names but fall back to whatever's present so
	// vendors that ship non-standard choice strings still work.
	canonical := []string{"low-power", "cool", "quiet", "balanced", "balanced-performance", "performance"}
	avail := map[string]bool{}
	for _, c := range available {
		avail[c] = true
	}
	pick := func(preferred []string, fallback string) string {
		for _, p := range preferred {
			if avail[p] {
				return p
			}
		}
		return fallback
	}
	sel := &Selector{hw: hw, choices: append([]string(nil), available...)}
	sort.Strings(sel.choices)
	sel.quietest = pick([]string{"low-power", "cool", "quiet"}, sel.choices[0])
	sel.hottest = pick([]string{"performance", "balanced-performance"}, sel.choices[len(sel.choices)-1])
	// Mid: prefer "balanced" specifically (canonical centre on Dell /
	// Lenovo / HP), then "balanced-performance" (HP), then fall back to
	// whichever remaining canonical entry sits between quietest and
	// hottest. We DON'T just walk the canonical list in order because
	// "quiet" would win over "balanced" on a machine that exposes both.
	midPrefs := []string{"balanced", "balanced-performance"}
	mid := ""
	for _, p := range midPrefs {
		if avail[p] && p != sel.quietest && p != sel.hottest {
			mid = p
			break
		}
	}
	if mid == "" {
		// No canonical balanced choice — walk remaining canonical names.
		for _, c := range canonical {
			if avail[c] && c != sel.quietest && c != sel.hottest {
				mid = c
				break
			}
		}
	}
	if mid == "" {
		mid = sel.choices[len(sel.choices)/2]
	}
	sel.mid = mid
	return sel, nil
}

// Pick returns the recommended profile and a reason string. Pressure is
// computed from thermal headroom, fan saturation, and power draw —
// each weighted by hardware-class heuristics — and clamped to [0,1].
func (s *Selector) Pick(in Inputs) Decision {
	score := s.pressure(in)
	switch {
	case score >= 0.85:
		return Decision{
			Profile:       s.hottest,
			Reason:        fmt.Sprintf("pressure=%.2f >= 0.85: thermal/power/load high — pushing BIOS to %s envelope", score, s.hottest),
			PressureScore: score,
		}
	case score <= 0.35:
		return Decision{
			Profile:       s.quietest,
			Reason:        fmt.Sprintf("pressure=%.2f <= 0.35: thermal/power/load low — relaxing BIOS to %s envelope", score, s.quietest),
			PressureScore: score,
		}
	default:
		return Decision{
			Profile:       s.mid,
			Reason:        fmt.Sprintf("pressure=%.2f in [0.35, 0.85): %s envelope", score, s.mid),
			PressureScore: score,
		}
	}
}

// pressure produces a 0..1 stress signal combining thermal headroom, fan
// saturation, CPU load, and (when known) RAPL draw. Each subcomponent is
// individually clamped before averaging so a single saturated input can't
// dominate the others — we want pressure to rise gracefully and the
// learning loop to disambiguate later if any one weight needs tuning.
func (s *Selector) pressure(in Inputs) float64 {
	parts := []float64{}

	// Thermal component: 0 when far below TJmax, 1 when at TJmax.
	if s.hw.TJmaxC > 0 && in.CurrentTempC > 0 {
		t := (in.CurrentTempC - 35.0) / float64(s.hw.TJmaxC-35)
		parts = append(parts, clamp01(t))
	}

	// Fan saturation: 0 when fan off, 1 when at max. On state-quantized
	// fans we treat any non-zero as "fan is responding" — discrete
	// jumps don't tell us much about pressure, so the contribution is
	// flatter. On continuous fans the curve is linear.
	if s.hw.FanMaxRPM > 0 && in.CurrentRPM > 0 {
		f := float64(in.CurrentRPM) / float64(s.hw.FanMaxRPM)
		if s.hw.StateQuantizedN > 0 {
			// Compress to [0.4, 0.8] band — discrete RPM shifts shouldn't
			// dominate pressure either way.
			f = 0.4 + 0.4*clamp01(f)
		}
		parts = append(parts, clamp01(f))
	}

	// CPU load component: 0 at idle, 1 at full-tilt all-cores.
	if in.CPULoadPct > 0 {
		parts = append(parts, clamp01(in.CPULoadPct/100.0))
	}

	// Power draw component: 0 well below TDP, 1 at TDP.
	if s.hw.TDPWatts > 0 && in.CurrentTDPWatts > 0 {
		parts = append(parts, clamp01(in.CurrentTDPWatts/float64(s.hw.TDPWatts)))
	}

	if len(parts) == 0 {
		// No usable inputs — default to mid pressure (balanced choice).
		return 0.5
	}
	sum := 0.0
	for _, p := range parts {
		sum += p
	}
	return sum / float64(len(parts))
}

// Anchors returns the three canonical profile names chosen at construction.
// Useful for log messages + the learning store's per-profile statistics.
func (s *Selector) Anchors() (quietest, mid, hottest string) {
	return s.quietest, s.mid, s.hottest
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
