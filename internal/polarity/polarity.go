// Package polarity implements the v0.5.2 polarity disambiguation probe.
// It resolves each ControllableChannel.Polarity from "unknown" to one of
// "normal", "inverted", or "phantom" across all backend types (hwmon, NVML,
// IPMI, EC). Results are persisted to the spec-16 KV store under the
// "calibration" namespace.
package polarity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ventd/ventd/internal/probe"
)

// Polarity verdicts. The string values are persisted in the KV store
// and read back by the orchestrator bridge into the wizard UI's
// FanState.PolarityPhase, so any new value here must round-trip
// through the WebUI surfaces in web/calibration.js and the badge CSS
// in web/calibration.css.
const (
	PolarityNormal      = "normal"
	PolarityInverted    = "inverted"
	PolarityPhantom     = "phantom"
	PolarityProbational = "probational"
	PolarityUnknown     = "unknown"
)

// Phantom reason constants surfaced in diagnostics and doctor output.
const (
	PhantomReasonNoTach         = "no_tach"
	PhantomReasonNoResponse     = "no_response"
	PhantomReasonFirmwareLocked = "firmware_locked"
	PhantomReasonProfileOnly    = "profile_only"
	PhantomReasonDriverTooOld   = "driver_too_old"
	PhantomReasonWriteFailed    = "write_failed"

	// PhantomReasonImplausibleTach is recorded when a probe pulse yields no
	// plausible tach samples — every reading in the window was a driver
	// sentinel (0xFFFF) or above the plausibility ceiling
	// (hal/hwmon.IsSentinelRPM). The channel may well accept PWM writes, but
	// its tach can't be trusted, so direction can't be resolved and the
	// bipolar delta would be garbage (e.g. a 65535 sample at the high pulse
	// reads as a +64000 RPM "normal" verdict). The conservative, correct
	// outcome is phantom (monitor-only) rather than a fabricated direction.
	// Calibration already rejects sentinel samples (calibrate.go); this keeps
	// the polarity probe — which runs first and gates controllability —
	// consistent with that guard.
	PhantomReasonImplausibleTach = "implausible_tach"

	// PhantomReasonColdECSuspected is the reason recorded when a no-
	// response probe outcome hit a backend whose EC is known to veto
	// manual writes under a thermal floor (BackendCaps.EcCanThermalVeto).
	// Paired with PolarityProbational, not PolarityPhantom: the
	// channel is admitted with conservative defaults and the wizard
	// UI explains the verdict to the operator so they aren't left
	// staring at "monitor-only" with no context.
	PhantomReasonColdECSuspected = "cold_ec_suspected"

	// PhantomReasonMonotonicByConstruction is recorded when the probe
	// is skipped entirely because the backend driver's kernel API
	// cannot present an inverted channel. The verdict is always
	// PolarityNormal in this path; the reason string ships alongside
	// for diagnostics.
	PhantomReasonMonotonicByConstruction = "monotonic_by_construction"
)

// Threshold constants (spec §3.1, §3.2).
const (
	ThresholdRPM   = 150 // hwmon/EC: minimum RPM delta for non-phantom classification
	ThresholdPct   = 10  // NVML: minimum percentage-point delta
	HoldDuration   = 3 * time.Second
	RestoreDelay   = 500 * time.Millisecond
	BaselineWindow = time.Second

	// Bipolar-probe pulse values (RULE-POLARITY-13). The pre-#1110
	// algorithm wrote a single midpoint (128 / 50%) and compared
	// observed RPM against the pre-write baseline. That misclassified
	// every normal fan whose baseline PWM was above midpoint — a
	// fan running at PWM=255 / 2300 RPM slowed to ~1500 RPM when 128
	// was written, producing a negative delta and a false "inverted"
	// label. The 13900K / NCT6687 wizard incident on 2026-05-15
	// landed every controlled channel in that misclassification
	// because BIOS auto held fans at high PWM going into the probe.
	//
	// The bipolar test writes LOW then HIGH (matching the validity
	// probe at internal/validity, RULE-CALIB-PR2B-01) and classifies
	// on the delta between the two PULSES, not against baseline.
	// Polarity classification becomes baseline-PWM-invariant.
	//
	// BipolarPulseHold was originally 2 s. Issue #1221 HIL on the
	// 13900K / NCT6687 board found that 2 s is well short of the
	// spin-down time-constant of NCT6687-class case fans on splitter
	// cables (τ_down ≈ 2.2 s, settling time ≈ 6-8 s). Sampling at
	// t=2 s on a fan coasting down from BIOS auto baseline produced
	// deltas of 43-407 RPM across pwm1/3/7/8 — straddling the 150 RPM
	// phantom threshold, giving random run-to-run misclassification
	// (1-4 false phantoms per run on the same box). Raising the hold
	// to 6 s puts every channel within 2 % of asymptote, producing
	// deltas of 1474-1827 RPM — an order of magnitude above the
	// threshold. The sample window was simultaneously decoupled from
	// RestoreDelay into BipolarSampleWindow (1 s) so that at low RPM
	// (~600 RPM, period ≈ 100 ms) the mean averages ≥10 tach edges
	// rather than ~5.
	BipolarLowPWM       uint8         = 51  // ~20% of 255
	BipolarHighPWM      uint8         = 204 // ~80% of 255
	BipolarLowPct       uint8         = 20
	BipolarHighPct      uint8         = 80
	BipolarPulseHold    time.Duration = 6 * time.Second
	BipolarSampleWindow time.Duration = 1 * time.Second
)

// Sentinel errors.
var (
	ErrChannelNotControllable = errors.New("polarity: channel is phantom; writes not permitted")
	ErrPolarityNotResolved    = errors.New("polarity: polarity not yet resolved (still unknown)")
)

// ChannelResult is the resolved polarity for one channel after probing.
type ChannelResult struct {
	Backend       string   // "hwmon" | "nvml" | "ipmi" | "ec"
	Identity      Identity // backend-specific channel identity
	Polarity      string   // "normal" | "inverted" | "phantom"
	PhantomReason string   // non-empty when Polarity == "phantom"
	Baseline      float64  // baseline reading (RPM for hwmon, pct for NVML, vendor for IPMI)
	Observed      float64  // reading after midpoint write
	Delta         float64  // Observed - Baseline
	Unit          string   // "rpm" | "pct" | "vendor"
	ProbedAt      time.Time
	// AcousticStallSuspected is set by the v0.5.12 acoustic stall
	// detector (R31) when the post-calibration soak phase observed
	// a 2-of-3 stall signature on this channel. Purely informational —
	// surfaces in `ventd doctor` output and the dashboard. Does NOT
	// affect WritePWM or any control path. False on every channel
	// when no microphone calibration was performed.
	AcousticStallSuspected bool `json:"acoustic_stall_suspected,omitempty"`
}

// Identity encodes the backend-specific information needed to match a
// persisted ChannelResult to a live ControllableChannel on daemon restart.
type Identity struct {
	// hwmon / EC
	PWMPath  string `json:"pwm_path,omitempty"`
	TachPath string `json:"tach_path,omitempty"`
	// NVML
	PCIAddress string `json:"pci_address,omitempty"`
	FanIndex   int    `json:"fan_index,omitempty"`
	// IPMI
	BMCAddress string `json:"bmc_address,omitempty"`
	Vendor     string `json:"vendor,omitempty"`
	ChannelID  string `json:"channel_id,omitempty"`
}

// ApplyToChannel updates ch.Polarity and ch.PhantomReason from a resolved
// ChannelResult. Used by the daemon-start match path (RULE-POLARITY-08).
func ApplyToChannel(ch *probe.ControllableChannel, r ChannelResult) {
	ch.Polarity = r.Polarity
	ch.PhantomReason = r.PhantomReason
}

// WritePWM is the polarity-aware write helper (spec §3.4 / RULE-POLARITY-05).
// It inverts value for inverted channels and refuses writes to phantom/unknown.
// The actual write is dispatched via fn, which must forward the adjusted byte
// to the backend.
//
// Probational channels (BackendCaps.EcCanThermalVeto backends whose probe
// returned no_response, typically a cold-chassis Dell SMM EC) are written
// straight through as if normal: the EC will start honouring writes once
// thermals rise and the runtime closed-loop will recover. The conservative
// default curve baked in by ApplyPhase keeps the channel safe in the
// meantime.
func WritePWM(ch *probe.ControllableChannel, value uint8, fn func(uint8) error) error {
	var actual uint8
	switch ch.Polarity {
	case PolarityNormal, PolarityProbational:
		actual = value
	case PolarityInverted:
		actual = 255 - value
	case PolarityPhantom:
		return ErrChannelNotControllable
	case PolarityUnknown:
		return ErrPolarityNotResolved
	default:
		return fmt.Errorf("polarity: invalid polarity %q for channel %s", ch.Polarity, ch.PWMPath)
	}
	return fn(actual)
}

// IsControllable reports whether ch has a resolved non-phantom polarity.
// Probational channels count as controllable — see WritePWM.
func IsControllable(ch *probe.ControllableChannel) bool {
	return ch.Polarity == PolarityNormal ||
		ch.Polarity == PolarityInverted ||
		ch.Polarity == PolarityProbational
}

// Prober probes a single ControllableChannel and returns its resolved polarity.
// Each backend type implements its own Prober.
type Prober interface {
	ProbeChannel(ctx context.Context, ch *probe.ControllableChannel) (ChannelResult, error)
}
