package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/recovery"
	"github.com/ventd/ventd/internal/sysclass"
)

// DefaultConfigPath is the production location for ventd's config.
// ApplyPhase writes here unless an operator-supplied path override
// is wired in via the bridge.
const DefaultConfigPath = "/etc/ventd/config.yaml"

// ApplyArtifact is the structured result of the ApplyPhase. Carries
// the path the config was written to and a snapshot of what was
// configured so the wizard UI can render a "what just happened" panel.
type ApplyArtifact struct {
	ConfigPath  string `json:"config_path"`
	Fans        int    `json:"fans"`
	MonitorOnly bool   `json:"monitor_only"`

	// MonitorReason is a short machine-readable explanation of why
	// the apply landed in monitor-only mode despite the wizard's
	// initial_outcome being control_mode. Empty in the happy case
	// (active control achieved). The wizard UI surfaces this on
	// the "Wizard complete" screen so an operator who expected
	// active control isn't left guessing.
	//
	// Known values:
	//   no_cpu_sensor       — DiscoverCPUSensor returned no path
	//   no_admitted_fans    — every probed fan was excluded
	//                         (phantom polarity / calibrate Phantom /
	//                         calibration skipped / probe found zero)
	//   no_controls_built   — guard token for a buildConfig regression
	//                         that produced Fans without Controls
	MonitorReason string `json:"monitor_reason,omitempty"`

	// EnableRestored counts how many excluded channels had their
	// pwm<N>_enable restored to the probe-time value. Surfaced for
	// the doctor page so operators can see whether the cleanup ran.
	EnableRestored int `json:"enable_restored,omitempty"`
}

// ApplyPhase writes the daemon's config.yaml from prior phases'
// artifacts. This is the orchestrator's terminal phase — once
// ApplyPhase succeeds the wizard is done and the daemon can take
// control on next reload.
//
// Full-control contract: consumes ProbeArtifact + PolarityArtifact +
// CalibrateArtifact + a discovered CPU sensor to build a working
// ACTIVE-CONTROL config with Sensors + Fans + Curves + Controls. The
// daemon loads this and starts driving fans immediately — Goal 4 of
// the rework is met the moment Apply succeeds.
//
// Fallback to monitor-only: when no CPU sensor is discoverable, or
// when every probed fan is excluded (phantom polarity / phantom from
// calibrate's sustained-spin check), ApplyPhase writes a config with
// Fans listed but no Controls. The daemon still loads it and reports
// temps/RPMs in the dashboard; operators add curves later via the
// web UI.
//
// Fan exclusion rules:
//   - polarity == "phantom"          → exclude (no PWM surface)
//   - calibrate Phantom              → exclude (sustained-spin check
//     saw zero RPM AND the sweep
//     itself measured MaxRPM == 0)
//   - calibration skipped/failed     → include with safe defaults
//   - polarity == "unknown"          → include; daemon's polarity-
//     aware WritePWM refuses to
//     drive until polarity resolves
type ApplyPhase struct {
	// ConfigPath overrides DefaultConfigPath. Used by tests to write
	// to t.TempDir().
	ConfigPath string

	// HwmonRoot overrides "/sys/class/hwmon" for sensor discovery.
	// Tests inject a fixture root.
	HwmonRoot string
}

// Name identifies this phase in the checkpoint store and the wizard UI.
func (ApplyPhase) Name() string { return "apply" }

// Execute reads prior artifacts and writes config.yaml atomically.
func (p ApplyPhase) Execute(_ context.Context, rc *RunContext) Outcome {
	path := p.ConfigPath
	if path == "" {
		path = DefaultConfigPath
	}

	probeArt, err := loadProbeArtifact(rc)
	if err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "load probe artifact: " + err.Error(),
		}
	}

	polarityArt, polErr := loadPolarityArtifact(rc)
	// polarity is best-effort — if it didn't run, treat every fan as
	// "unknown" polarity and let the controller's WritePWM refuse
	// writes safely. The wizard will still write a usable config.
	if polErr != nil {
		rc.Log().Warn("apply: no polarity artifact, defaulting all fans to unknown polarity",
			"err", polErr)
		polarityArt = PolarityArtifact{}
	}
	polByPath := make(map[string]string, len(polarityArt.Results))
	for _, r := range polarityArt.Results {
		polByPath[r.PWMPath] = r.Polarity
	}

	// CalibrateArtifact is best-effort: missing/failed → use safe
	// heuristic PWM bounds for every fan rather than failing apply.
	// CalibrateFanResult.Phantom (set by the sweep's sustained-spin
	// check) now carries what loadVerifyArtifact previously did — a
	// fan flagged Phantom is excluded from the applied config.
	calByPath := map[string]CalibrateFanResult{}
	if calArt, calErr := loadCalibrateArtifact(rc); calErr == nil {
		for _, r := range calArt.Results {
			calByPath[r.PWMPath] = r
		}
	}

	// RPMDetectArtifact is best-effort: missing → ApplyPhase uses
	// ProbeArtifact's same-index pairing without overrides.
	rpmOverrides := map[string]string{}
	if rpmArt, rpmErr := loadRPMDetectArtifact(rc); rpmErr == nil {
		for _, r := range rpmArt.Results {
			if r.Improved && r.ResolvedRPM != "" {
				rpmOverrides[r.PWMPath] = r.ResolvedRPM
			}
		}
	}

	// NVMLArtifact is best-effort: missing or empty → no NVIDIA
	// fans in the config (non-NVIDIA host or no GPU fans visible).
	var nvmlFans []NVMLGPUFan
	if nvmlArt, nvmlErr := loadNVMLArtifact(rc); nvmlErr == nil {
		nvmlFans = nvmlArt.Fans
	}

	// Sensor discovery: a working active-control config needs at
	// least one temperature source. No sensor → fall back to
	// monitor-only (Controls remains empty).
	hwmonRoot := p.HwmonRoot
	if hwmonRoot == "" {
		hwmonRoot = rc.HwmonRoot
	}
	if hwmonRoot == "" {
		hwmonRoot = "/sys/class/hwmon"
	}
	cpuSensor := DiscoverCPUSensor(hwmonRoot)

	// CPU-model-derived Tjmax used as fallback when the active CPU
	// sensor doesn't surface tempN_crit — acpitz, some laptop ECs,
	// several ARM SoCs. The sysclass table maps Intel N-series →
	// 105°C, AMD HEDT → 95°C, etc. (#1276). Returns 0 on unknown
	// CPU model; buildConfig falls through to the 95°C blanket.
	sysclassTjmax := sysclass.TjmaxFromCPUInfo()

	cfg := buildConfig(probeArt, polByPath, calByPath, rpmOverrides, nvmlFans, cpuSensor, sysclassTjmax, probeArt.CPUTDPW)
	monitorOnly := len(cfg.Controls) == 0
	nonMonoFans := nonMonotonicSummary(calByPath, cfg.Fans)

	// Diagnose the monitor-only path so the artifact carries a
	// reason field the UI + doctor can surface. The two distinct
	// causes have very different operator implications: missing
	// CPU sensor is a hwmon discovery gap; no admitted fans is
	// usually a calibrate-phantom cascade (every probed fan failed
	// both the sweep and the sustained-spin check).
	monitorReason := ""
	if monitorOnly {
		switch {
		case cpuSensor.Path == "":
			monitorReason = "no_cpu_sensor"
		case len(cfg.Fans) == 0:
			monitorReason = "no_admitted_fans"
		default:
			// cfg.Fans>0 but cfg.Controls==0 — shouldn't happen
			// with the current buildConfig (Controls always
			// follows Fans when both prerequisites are met), but
			// be explicit so a future regression surfaces a
			// recognisable token rather than empty.
			monitorReason = "no_controls_built"
		}
	}

	if err := writeConfigAtomic(path, cfg); err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "write config: " + err.Error(),
		}
	}

	// Restore pwm<N>_enable on every channel that's NOT in the
	// applied config — phantom-classified, calibrate-phantom, and
	// (when we demoted to monitor-only) every probed channel.
	// Without this, the wizard would leave channels at pwm_enable=1
	// (manual) with whatever PWM the calibrate sweep left last, and
	// neither ventd (monitor-only) nor BIOS (manual mode active)
	// would drive the fan under load — a thermal safety regression
	// observed on Dell SMM after the calibrate→apply cascade.
	includedPWM := map[string]bool{}
	for _, f := range cfg.Fans {
		includedPWM[f.PWMPath] = true
	}
	enableRestored := 0
	for _, fan := range probeArt.Fans {
		// Monitor-only demotion: restore every probed channel,
		// regardless of whether it would have been included. The
		// daemon won't drive any of them so firmware control is
		// the safe default for all.
		if !monitorOnly && includedPWM[fan.PWMPath] {
			continue
		}
		if fan.EnablePath == "" || fan.InitialEnable == 0 {
			continue
		}
		if err := os.WriteFile(fan.EnablePath, []byte(strconv.Itoa(int(fan.InitialEnable))), 0o644); err != nil {
			rc.Log().Warn("apply: restore pwm_enable failed",
				"enable_path", fan.EnablePath,
				"target", fan.InitialEnable,
				"err", err)
			continue
		}
		enableRestored++
	}

	art := ApplyArtifact{
		ConfigPath:     path,
		Fans:           len(cfg.Fans),
		MonitorOnly:    monitorOnly,
		MonitorReason:  monitorReason,
		EnableRestored: enableRestored,
	}
	raw, _ := EncodeArtifact(art)

	rc.Sink().Emit("info", "apply",
		fmt.Sprintf("config written to %s (%d fan(s); monitor-only=%v)",
			path, art.Fans, art.MonitorOnly))
	if monitorOnly {
		rc.Log().Warn("apply complete: monitor-only fallback",
			"path", path,
			"fans", art.Fans,
			"reason", monitorReason,
			"pwm_enable_restored", enableRestored)
	} else {
		rc.Log().Info("apply complete",
			"path", path,
			"fans", art.Fans,
			"curves", len(cfg.Curves),
			"monitor_only", art.MonitorOnly,
			"pwm_enable_restored", enableRestored)
	}
	if len(nonMonoFans) > 0 {
		rc.Log().Warn("apply: calibration flagged non-monotonic PWM→RPM curves",
			"fans", nonMonoFans,
			"hint", "vendor-EC interference is the usual cause; see doctor")
		rc.Sink().Emit("warn", "apply",
			fmt.Sprintf("non-monotonic curves on %d fan(s): %s", len(nonMonoFans), strings.Join(nonMonoFans, ", ")))
	}

	return Outcome{Status: StatusSuccess, Artifact: raw}
}

// buildConfig assembles the daemon-loadable config from every prior
// phase's artifact. When all prerequisites are met (probe fans,
// polarity resolved, calibration data, CPU sensor discovered), the
// resulting config has Sensors + Fans + Curves + Controls and the
// daemon starts active control on next reload — meeting Goal 4 of
// the rework.
//
// Degraded paths:
//   - No CPU sensor              → no Curves/Controls (monitor-only)
//   - Calibration missing        → fan included with safe heuristic PWM
//     bounds (StartPWM=80, MaxPWM=255), curve still wired
//   - Polarity phantom OR        → fan excluded entirely
//     calibrate-flagged Phantom
//     (sustained-spin check)
func buildConfig(
	probeArt ProbeArtifact,
	polByPath map[string]string,
	calByPath map[string]CalibrateFanResult,
	rpmOverrides map[string]string,
	nvmlFans []NVMLGPUFan,
	cpuSensor DiscoveredSensor,
	sysclassTjmax float64,
	cpuTDPW int,
) *config.Config {
	cfg := &config.Config{
		Version:      1,
		PollInterval: config.Duration{Duration: 2 * time.Second},
		Web: config.Web{
			Listen:     "0.0.0.0:9999",
			SessionTTL: config.Duration{Duration: 24 * time.Hour},
		},
	}

	if cpuSensor.Path != "" {
		cfg.Sensors = append(cfg.Sensors, config.Sensor{
			Name:        "cpu_temp",
			Type:        "hwmon",
			Path:        cpuSensor.Path,
			ChipName:    cpuSensor.ChipName,
			HwmonDevice: resolveStableHwmonDevice(cpuSensor.Path),
		})
	}

	for _, fan := range probeArt.Fans {
		if polByPath[fan.PWMPath] == "phantom" {
			continue
		}
		if cal, ok := calByPath[fan.PWMPath]; ok && cal.Phantom {
			continue
		}

		minPWM := uint8(80)
		if cal, ok := calByPath[fan.PWMPath]; ok && cal.StartPWM > 0 {
			minPWM = cal.StartPWM
		}
		isPump := false
		var pumpMinimum uint8
		if cal, ok := calByPath[fan.PWMPath]; ok {
			isPump = cal.IsPump
			if isPump {
				// Pumps must never stop (config.Validate rule 6). Set
				// pump_minimum from the measured StartPWM, floored at
				// the package-wide MinPumpPWM (20). Without this auto-
				// derivation the wizard would emit is_pump=true with
				// pump_minimum=0 — which the validator rejects, leaving
				// the daemon in monitor-only on next reload (#1275).
				pumpMinimum = uint8(config.MinPumpPWM)
				if cal.StartPWM > pumpMinimum {
					pumpMinimum = cal.StartPWM
				}
				if minPWM < pumpMinimum {
					minPWM = pumpMinimum
				}
			}
		}

		// Prefer RPMDetectPhase's detected path when ProbePhase
		// couldn't pair a same-index tach (split-chip fan/tach
		// configurations on some AMD boards).
		rpmPath := fan.RPMPath
		if rpmPath == "" {
			if override, ok := rpmOverrides[fan.PWMPath]; ok {
				rpmPath = override
			}
		}

		cfg.Fans = append(cfg.Fans, config.Fan{
			Name:        fan.LabelHint,
			Type:        "hwmon",
			PWMPath:     fan.PWMPath,
			RPMPath:     rpmPath,
			ChipName:    fan.ChipName,
			HwmonDevice: resolveStableHwmonDevice(fan.PWMPath),
			MinPWM:      minPWM,
			MaxPWM:      255,
			IsPump:      isPump,
			PumpMinimum: pumpMinimum,
		})
	}

	// Append NVIDIA GPU fans (PWMPath is the GPU index encoded as
	// a string; the daemon's nvidia HAL backend uses it to address
	// the right GPU via NVML).
	for _, gf := range nvmlFans {
		cfg.Fans = append(cfg.Fans, config.Fan{
			Name:    gf.Label,
			Type:    "nvidia",
			PWMPath: fmt.Sprintf("%d", gf.Index),
			MinPWM:  30,  // GPU fans default min is safer than CPU fans (avoid stall)
			MaxPWM:  100, // NVML uses percent, not byte
		})
		// Emit matching NVIDIA sensors so the dashboard surfaces GPU
		// temperature + fan-speed alongside CPU + hwmon fans. Without
		// these, NVMLPhase discovers the GPU as a fan but the dashboard
		// shows `rpm: null` and no `gpu*_temp` entry under sensors[]
		// (#1226). The nvidia HAL backend already speaks the same
		// (Type:"nvidia", Path:"<index>", Metric:"<temp|fan_pct|...>")
		// schema the runtime validates; ApplyPhase just wasn't writing
		// the sensor side.
		cfg.Sensors = append(cfg.Sensors,
			config.Sensor{
				Name:   fmt.Sprintf("gpu%d_temp", gf.Index),
				Type:   "nvidia",
				Path:   strconv.Itoa(int(gf.Index)),
				Metric: "temp",
			},
			config.Sensor{
				Name:   fmt.Sprintf("gpu%d_fan_pct", gf.Index),
				Type:   "nvidia",
				Path:   strconv.Itoa(int(gf.Index)),
				Metric: "fan_pct",
			},
		)
	}

	// Active control only when we have BOTH a sensor and at least
	// one fan. Otherwise stay monitor-only.
	if cpuSensor.Path == "" || len(cfg.Fans) == 0 {
		return cfg
	}

	// Curve ceiling: prefer the chip's hwmon tempN_crit (TjMax
	// shutdown threshold) which coretemp/k10temp/k8temp populate per-
	// chip. When the active sensor is acpitz or another driver that
	// doesn't surface _crit, fall back to sysclass.Detection.Tjmax —
	// CPU-model regex lookup (Intel N-series 105°C, AMD HEDT 95°C,
	// etc.) — before the conservative 95°C blanket (#1276).
	maxTemp := cpuSensor.CritC
	if maxTemp <= 0 {
		maxTemp = sysclassTjmax
	}
	if maxTemp <= 0 {
		maxTemp = 95
	}
	maxTemp -= 10 // -10°C safety margin between top-anchor and shutdown
	minTemp := 40.0

	// Per-fan curves (#1272). Each admitted fan gets its own
	// config.CurveConfig keyed off its name, with:
	//
	//   - max_pwm_pct  capped at the fan's saturation knee — the highest
	//                  PWM whose RPM is ≥ kneeRPMFraction × MaxRPM in the
	//                  rising portion of the measured Curve[]. Above the
	//                  knee, additional duty cycle produces audible
	//                  whine + EC fight on vendor firmwares without
	//                  additional airflow.
	//   - min_pwm_pct  = StartPWM / 255 × 100 (or PumpMinimum-equivalent
	//                  for pumps), so the curve never asks for a duty
	//                  the fan won't honour.
	//   - anchor count derived from this fan's MinPWM-MaxPWM range, not
	//                  the widest fan in the system. A narrow-range fan
	//                  stays at 3 anchors; a wide-range desktop fan gets
	//                  up to 9.
	//   - PWM% spread  linear between the fan's clamped min and max
	//                  pwm_pct (no longer hard-coded 20→100).
	//
	// NVIDIA and other non-hwmon fans without calibration data fall back
	// to the generic minTemp→maxTemp / 20→100% shape.
	tdpGamma := tdpAggressivenessGamma(cpuTDPW)
	for _, f := range cfg.Fans {
		curveName := perFanCurveName(f.Name)
		var c config.CurveConfig
		if cal, ok := calByPath[f.PWMPath]; ok && f.Type == "hwmon" && len(cal.Curve) > 0 {
			c = buildPerFanCurve(curveName, f, cal, minTemp, maxTemp, tdpGamma)
		} else {
			c = buildGenericCurve(curveName, f, minTemp, maxTemp, tdpGamma)
		}
		cfg.Curves = append(cfg.Curves, c)
		cfg.Controls = append(cfg.Controls, config.Control{
			Fan:   f.Name,
			Curve: curveName,
		})
	}

	return cfg
}

// perFanCurveName produces a stable, schema-safe curve identifier from
// a fan's display name. Lowercased; non-alphanumeric collapsed to "-";
// prefixed with "fan-" so the names are distinguishable from operator-
// authored curves in the web UI. Duplicate normalised names collide on
// the first writer wins — buildConfig only emits one curve per Fan, so
// the only collision risk is two fans whose LabelHint normalises
// identically (unusual; HAL labels include the chip + channel index).
func perFanCurveName(fanName string) string {
	var b strings.Builder
	b.WriteString("fan-")
	prevHyphen := false
	for _, r := range strings.ToLower(fanName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen {
				b.WriteRune('-')
				prevHyphen = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// saturationKneePct returns the saturation-knee PWM byte expressed as a
// percentage in [0, 100]. The knee is the highest PWM in the rising
// portion (PWM ≥ startPWM, monotonic envelope) whose RPM is at least
// kneeRPMFraction × MaxRPM. Above the knee, additional duty cycle does
// not produce additional airflow — either the fan has hit its
// mechanical ceiling or vendor EC firmware is clamping it.
//
// Returns 100 when no useful knee can be detected (sparse or all-noise
// curve), letting the curve drive to PWM=255 as a safe fallback.
func saturationKneePct(curve []CalibrateCurvePoint, startPWM uint8, maxRPM int) uint8 {
	if maxRPM <= 0 || len(curve) == 0 {
		return 100
	}
	threshold := int(float64(maxRPM) * kneeRPMFraction)
	// Walk monotonic-rising envelope from the right edge. A non-monotonic
	// curve (e.g. EC clamping above PWM=200) shouldn't cap higher than
	// the last sample whose RPM was within threshold — anything past
	// that is duty cycle being burned without airflow.
	knee := uint8(255)
	envelope := -1
	// Build a running max-RPM envelope so a single low sample at high
	// PWM doesn't fool us into picking a too-low knee.
	for _, p := range curve {
		if p.PWM < startPWM {
			continue
		}
		if p.RPM > envelope {
			envelope = p.RPM
		}
		if p.RPM >= threshold {
			knee = p.PWM
		}
	}
	if envelope < threshold {
		// Whole rising portion below the knee threshold. Fan can't
		// hit MaxRPM at all (vendor-EC clamp throughout, broken
		// tach, etc). Fall through to full range.
		return 100
	}
	// Express byte as integer percent, rounded down so we don't
	// overshoot the measured knee.
	pct := int(knee) * 100 / 255
	if pct < int(kneePctMin) {
		pct = int(kneePctMin)
	}
	if pct > 100 {
		pct = 100
	}
	return uint8(pct)
}

// kneeRPMFraction is the fraction of MaxRPM a sample must reach for its
// PWM to qualify as a saturation-knee candidate. 0.95 keeps a 5% tach-
// noise margin: a fan that genuinely hits 98% of its max at PWM=240 and
// 100% at PWM=255 still gets PWM=240 as the knee (5% noise tolerance),
// and a fan whose Curve peaks at PWM=229 with RPM=2575 and drops to
// RPM=2307 at PWM=255 gets the knee capped at 229 — the daemon writes
// no higher and the user hears no rising-PWM-falling-RPM hunt.
const kneeRPMFraction = 0.95

// kneePctMin floors the saturation-knee percentage so a degenerate
// calibration (extreme stiction, EC clamping at very low PWM) can't
// emit a curve whose top anchor sits below the operator's idle floor.
const kneePctMin = 30

// minSpinPctFloor caps the curve's bottom anchor percentage low enough
// that we don't accidentally floor a fan above its natural idle (some
// fans spin freely from PWM=10), high enough that the curve top still
// has room to rise. Empirically 15%.
const minSpinPctFloor = 15

// curveAnchorCount derives the per-fan anchor count from the fan's PWM
// range, targeting ~30 PWM units per segment (= ~10-12% duty change per
// anchor) so the fan can absorb each step without overshoot.
//
//	range 60  → 3 anchors (clamped floor)
//	range 179 → 6 anchors (typical Dell SMM laptop blower)
//	range 243 → 8 anchors (typical NCT6687 desktop fan)
//
// nvidia fans use MaxPWM=100 (percent), which would give too few anchors
// (range = 70 → ~2). NVIDIA path falls back to a 6-anchor default so
// the curve has enough resolution for a 0-100% sweep.
func curveAnchorCount(f config.Fan) int {
	if f.Type != "hwmon" {
		return 6
	}
	r := int(f.MaxPWM) - int(f.MinPWM)
	if r <= 0 {
		return 3
	}
	a := (r + 15) / 30
	if a < 3 {
		a = 3
	}
	if a > 12 {
		a = 12
	}
	return a
}

// tdpAggressivenessGamma maps the host CPU TDP (watts) to the
// power-law exponent used to shape the per-fan curve's middle-band
// steepness (#1280). Higher TDP → lower gamma (more concave shape =
// PWM% climbs faster in the lower-middle band so a thermal transient
// gets head-room before it pins at the saturation knee). Lower TDP →
// higher gamma (more convex shape = PWM% climbs gently in the lower-
// middle band, surging only near the top — a 10 W mini-PC doesn't
// need to ramp at 60 °C).
//
// gamma=1 (the cpuTDPW=0 fallback for AMD without amd_energy or
// virtualised hosts) is the v1 linear shape — strict no-regression.
//
// The 35-250 W reference band targets the issue acceptance: a 13900K
// (PL1=125 W) rises sharply through 60-75 °C; a J4125 (PL1≈10 W)
// rises gently across the same band.
func tdpAggressivenessGamma(cpuTDPW int) float64 {
	if cpuTDPW <= 0 {
		return 1.0
	}
	const (
		lowTDP   = 35.0
		highTDP  = 250.0
		lowGamma = 2.0
		hiGamma  = 0.5
	)
	t := float64(cpuTDPW)
	if t <= lowTDP {
		return lowGamma
	}
	if t >= highTDP {
		return hiGamma
	}
	frac := (t - lowTDP) / (highTDP - lowTDP)
	return lowGamma + frac*(hiGamma-lowGamma)
}

// shapePWMPct maps the normalised temperature fraction [0,1] to a
// PWM percentage in [bottomPct, topPct] using the power-law gamma
// from tdpAggressivenessGamma. fraction=0 → bottomPct; fraction=1 →
// topPct regardless of gamma — both anchor endpoints stay pinned.
func shapePWMPct(fraction, gamma float64, bottomPct, topPct uint8) uint8 {
	if fraction <= 0 {
		return bottomPct
	}
	if fraction >= 1 {
		return topPct
	}
	if gamma <= 0 {
		gamma = 1
	}
	shape := math.Pow(fraction, gamma)
	pct := float64(bottomPct) + shape*float64(topPct-bottomPct)
	if pct < 0 {
		pct = 0
	}
	if pct > 255 {
		pct = 255
	}
	return uint8(math.Round(pct))
}

// buildPerFanCurve synthesises a config.CurveConfig from a fan's
// calibrate-measured PWM→RPM curve. Top anchor PWM% capped at the
// saturation knee; bottom anchor PWM% pinned at the fan's measured
// StartPWM (with a minSpinPctFloor safety floor). Anchors are
// uniform-temp in [minTemp, maxTemp]; PWM% follows shapePWMPct with
// the TDP-derived gamma so a high-TDP chip ramps faster than a low-
// TDP chip across the same temperature delta (#1280).
func buildPerFanCurve(name string, f config.Fan, cal CalibrateFanResult, minTemp, maxTemp, tdpGamma float64) config.CurveConfig {
	// Bottom-anchor PWM%: prefer the fan's measured StartPWM
	// expressed as a percentage. Floored at minSpinPctFloor so a
	// fan with a freak StartPWM=2 doesn't produce a 1% bottom
	// anchor that the operator can't audibly notice as "idle".
	bottomPct := uint8(minSpinPctFloor)
	if cal.StartPWM > 0 {
		p := int(cal.StartPWM) * 100 / 255
		if p > int(bottomPct) {
			bottomPct = uint8(p)
		}
	}
	if f.IsPump && f.PumpMinimum > 0 {
		// Pumps must never stop. Their bottom anchor is the
		// PumpMinimum byte, not the StartPWM (which would be 0 by
		// definition for a pump — they always spin).
		p := int(f.PumpMinimum) * 100 / 255
		if p > int(bottomPct) {
			bottomPct = uint8(p)
		}
	}
	topPct := saturationKneePct(cal.Curve, cal.StartPWM, cal.MaxRPM)
	if topPct <= bottomPct {
		topPct = 100 // pathological cal data; let the operator reshape in the UI
	}

	anchors := curveAnchorCount(f)
	pts := make([]config.CurvePoint, anchors)
	for i := 0; i < anchors; i++ {
		fraction := float64(i) / float64(anchors-1)
		temp := minTemp + fraction*(maxTemp-minTemp)
		pct := shapePWMPct(fraction, tdpGamma, bottomPct, topPct)
		pts[i] = config.CurvePoint{Temp: temp, PWMPct: ptrU8(pct)}
	}
	return config.CurveConfig{
		Name:    name,
		Type:    "points",
		Sensor:  "cpu_temp",
		MinTemp: minTemp,
		MaxTemp: maxTemp,
		Points:  pts,
	}
}

// buildGenericCurve is the fallback used for NVIDIA fans (no per-fan
// calibrate sweep) and any hwmon fan without calibration data.
// Bottom = fan.MinPWM expressed as a percentage, top = 100.
// PWM% follows the TDP-shaped curve so the generic fallback ramps
// match the calibrated fans' aggressiveness (#1280).
func buildGenericCurve(name string, f config.Fan, minTemp, maxTemp, tdpGamma float64) config.CurveConfig {
	bottomPct := uint8(minSpinPctFloor)
	if f.Type == "nvidia" {
		bottomPct = f.MinPWM // already a percentage for nvidia
	} else if f.MinPWM > 0 {
		p := int(f.MinPWM) * 100 / 255
		if p > int(bottomPct) {
			bottomPct = uint8(p)
		}
	}
	topPct := uint8(100)
	anchors := curveAnchorCount(f)
	pts := make([]config.CurvePoint, anchors)
	for i := 0; i < anchors; i++ {
		fraction := float64(i) / float64(anchors-1)
		temp := minTemp + fraction*(maxTemp-minTemp)
		pct := shapePWMPct(fraction, tdpGamma, bottomPct, topPct)
		pts[i] = config.CurvePoint{Temp: temp, PWMPct: ptrU8(pct)}
	}
	return config.CurveConfig{
		Name:    name,
		Type:    "points",
		Sensor:  "cpu_temp",
		MinTemp: minTemp,
		MaxTemp: maxTemp,
		Points:  pts,
	}
}

// nonMonotonicSummary returns the names of fans whose calibrate sweep
// flagged NonMonotonicCurve. Used by the apply log to surface the
// quality signal to operators reading journalctl, alongside the doctor
// surface that picks up the same data from the calibrate artifact.
func nonMonotonicSummary(calByPath map[string]CalibrateFanResult, fans []config.Fan) []string {
	out := []string{}
	for _, f := range fans {
		if cal, ok := calByPath[f.PWMPath]; ok && cal.NonMonotonicCurve {
			out = append(out, f.Name)
		}
	}
	return out
}

func ptrU8(v uint8) *uint8 { return &v }

// resolveStableHwmonDevice returns the canonical /sys/devices/... path
// for the hwmon chip whose pwm/temp file lives at sysfsPath. The result
// is the chip dir's `device` symlink resolved via EvalSymlinks. Used by
// config.ResolveHwmonPaths to disambiguate between two hwmonN entries
// that share a chip_name — common on boards where the in-kernel
// nct6683 and the OOT nct6687 both bind and both report `name=nct6687`.
//
// Best-effort: any failure (synthetic test path, virtual chip without
// a `device` symlink such as acpitz, partially-enumerated sysfs at cold
// boot) returns "". The daemon's resolver treats empty HwmonDevice as
// "fall back to chip_name alone", which is the pre-disambiguation
// behaviour — strictly weaker than failing the write.
var resolveStableHwmonDevice = func(sysfsPath string) string {
	if sysfsPath == "" {
		return ""
	}
	target, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(sysfsPath), "device"))
	if err != nil {
		return ""
	}
	return target
}

// writeConfigAtomic marshals cfg as YAML and writes to path via a
// tmp+fsync+rename so a crash mid-write never leaves a half-written
// file the daemon's loader would reject.
func writeConfigAtomic(path string, cfg *config.Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", tmp, err)
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// loadPolarityArtifact reads the PolarityPhase's checkpoint. Returns
// an error if the phase didn't run. The error is recoverable —
// ApplyPhase falls back to "unknown polarity for all fans" rather
// than failing.
func loadPolarityArtifact(rc *RunContext) (PolarityArtifact, error) {
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		return PolarityArtifact{}, err
	}
	prior, ok := state.Outcomes[(PolarityPhase{}).Name()]
	if !ok {
		return PolarityArtifact{}, errors.New("PolarityPhase has not run")
	}
	if prior.Status != StatusSuccess && prior.Status != StatusSkipped {
		return PolarityArtifact{}, fmt.Errorf(
			"PolarityPhase did not succeed (status=%q)", prior.Status)
	}
	if len(prior.Artifact) == 0 {
		return PolarityArtifact{}, nil // skipped → empty results, not an error
	}
	var art PolarityArtifact
	if err := json.Unmarshal(prior.Artifact, &art); err != nil {
		return PolarityArtifact{}, fmt.Errorf("decode PolarityArtifact: %w", err)
	}
	return art, nil
}
