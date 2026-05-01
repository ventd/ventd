// blended.go — v0.5.9 PR-A.3: confidence-gated IMC-PI blended controller.
//
// Per spec-v0_5_9-confidence-controller.md §2.1–§2.8. The controller
// produces a `predictive_output` PWM via IMC-PI gains derived from
// Layer-B's per-channel RLS estimate, and blends it with the
// existing v0.4.x reactive curve under the per-tick weight `w_pred`
// supplied by the aggregator (R12 §Q3 chain):
//
//	output = w_pred · predictive + (1 − w_pred) · reactive
//
// Three independent refusal gates protect against running predictive
// control on bad data:
//
//   - **Instability guards** (RULE-CTRL-PI-05) refuse the predictive
//     arm entirely when the Layer-B estimate is divergent / unidentifiable
//     / numerically unsafe.
//   - **Path-A saturation** (Layer-C predicted ΔT < 2 °C) refuses the
//     specific candidate ramp; the controller falls through to reactive
//     and freezes the integrator (anti-windup hook).
//   - **Acoustic cost gate** refuses ramps where the operator's preset
//     says the predicted cooling benefit doesn't justify the noise.
//
// SPEC DIVERGENCE — IMC-PI sign convention:
//
// The earlier draft of spec §2.2 derived `K = b_ii/(1−a)` directly,
// giving `K_p, K_i < 0` for cooling fans (b<0). With error =
// sensor−setpoint and that signed gain, error > 0 (too hot) drives
// `u = K_p·error + I` more negative each tick, pushing predictive
// PWM BELOW reactive ⇒ less cooling ⇒ hotter ⇒ positive feedback
// instability. The spec claimed "the math carries through via
// bumpless init" but the spec's bumpless formula
// `I[0] = (reactive − K_p·error)/K_i` is dimensionally inconsistent
// (yields seconds, not PWM-units).
//
// Correct formulation (this implementation, and the amended spec):
// take the magnitude `K = |b_ii/(1−a)|`. With `K_p, K_i > 0` and
// error = sensor − setpoint, error > 0 drives `u > 0` ⇒ predictive =
// baseline + u > baseline ⇒ more cooling. Polarity (cooling vs
// heating direction) is already handled in `polarity.WritePWM`
// (RULE-POLARITY-05). Bumpless: `I[0] = −K_p·error` so `u[0] = 0`
// and predictive = reactive at the handoff tick.

package controller

import (
	"math"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/coupling"
	"github.com/ventd/ventd/internal/marginal"
)

// Preset is the operator's smart-mode aggressiveness selector. The
// enum lives in the controller package so PR-A.3 is hermetic; the
// `config.Smart.Preset` field that maps to this enum lands in
// PR-A.4 alongside the operator-visible Settings UI.
type Preset uint8

const (
	// PresetSilent — λ = 2τ (slow, cautious), cost factor 3× Balanced.
	// Operator opt-in to acoustic-first behaviour.
	PresetSilent Preset = iota
	// PresetBalanced — λ = τ, cost factor 1× (default).
	PresetBalanced
	// PresetPerformance — λ = τ/2 (fast, aggressive), cost factor 0.2×.
	// Operator opt-in to thermal-first behaviour.
	PresetPerformance
)

// IMC-PI tuning constants per spec-v0_5_9 §3.2.
const (
	// TauMinSeconds bounds the thermal time constant from below.
	// `a → 0` would make τ collapse to ~dt; the floor keeps the
	// controller from producing absurdly aggressive gains during
	// the first few ticks of a fresh shard.
	TauMinSeconds = 50.0

	// TauMaxSeconds bounds τ from above. NAS-class drives have
	// time constants in the 15–25 minute range; the cap covers
	// that without letting `a → 1` explode the formula.
	TauMaxSeconds = 1800.0

	// KappaUnidentifiable — Layer-B condition number above which
	// the PI is refused even if WarmingUp is false (R10 §10.2
	// defence-in-depth).
	KappaUnidentifiable = 1e4

	// PathASaturationDeltaT is the absolute predicted ΔT below
	// which the Layer-C Path-A gate refuses the candidate ramp.
	// Re-exported from `marginal.SaturationDeltaT` so the controller
	// package stays self-contained for tests.
	PathASaturationDeltaT = 2.0

	// CostFactorBalanced is the per-PWM-unit cost factor for
	// the Balanced preset (R18 stub linear cost). 0.01 °C-equiv
	// per PWM unit.
	CostFactorBalanced = 0.01

	// MinKpForBumpless is the floor below which we skip bumpless
	// init (set I[0] = 0 instead). Avoids pathological cases where
	// near-zero gain makes the magnitude term meaningless.
	MinKpForBumpless = 1e-9
)

// lambdaForPreset returns the IMC λ multiplier for τ.
//
// Spec §3.1: Silent: 2τ, Balanced: τ, Performance: τ/2.
func lambdaForPreset(p Preset) float64 {
	switch p {
	case PresetSilent:
		return 2.0
	case PresetPerformance:
		return 0.5
	default:
		return 1.0 // Balanced (and any unrecognised value).
	}
}

// costFactorForPreset returns the linear cost-factor multiplier
// applied to |ΔPWM| in the cost gate (spec §2.7).
//
// Silent: 3× Balanced (cost-averse). Balanced: 1×. Performance: 0.2×
// (cost-tolerant). The base factor `CostFactorBalanced` (0.01 °C/PWM)
// is from R18 stub and will be refined when the full psychoacoustic
// model lands in v0.7+.
func costFactorForPreset(p Preset) float64 {
	switch p {
	case PresetSilent:
		return 3.0 * CostFactorBalanced
	case PresetPerformance:
		return 0.2 * CostFactorBalanced
	default:
		return CostFactorBalanced
	}
}

// BlendedConfig drives controller construction.
type BlendedConfig struct {
	Preset Preset
	// PWMUnitMax is the maximum raw PWM byte (255 for hwmon).
	// Reserved for future per-driver scaling; v0.5.9 always 255.
	PWMUnitMax uint8
}

// BlendedController owns per-channel PI integrator state and gain
// caches. Hot-path safe — `Compute` takes `mu` for the duration of
// per-channel state updates only; reads of upstream Snapshots happen
// outside the lock.
type BlendedController struct {
	cfg BlendedConfig

	mu    sync.Mutex
	state map[string]*piState
}

// piState holds per-channel integrator + gain cache. The cache is
// invalidated when `Coupling.NSamples` advances past the cached
// snapshot point by `gainRefreshSamples`; that dampens noise from
// re-deriving gains every tick on a still-warming shard.
type piState struct {
	integrator     float64
	bumplessArmed  bool // false ⇒ next w_pred>0 tick will set bumpless I[0]
	cachedKp       float64
	cachedKi       float64
	cachedTau      float64
	cachedNSamples uint64 // NSamples at cache time
	lastTick       time.Time
}

// gainRefreshSamples is the cache-invalidation cadence: re-derive
// IMC-PI gains every ~60 NSamples (≈2 minutes at the v0.5.7 RLS
// tick rate). Spec §3.7.
const gainRefreshSamples uint64 = 60

// BlendedInputs is the per-tick input bundle. PR-B's wiring layer
// loads each Snapshot from the corresponding runtime; PR-A.3 tests
// pass synthetic inputs directly.
type BlendedInputs struct {
	ChannelID   string
	SensorTemp  float64 // °C
	Setpoint    float64 // °C; from Config.Smart.Setpoints
	ReactivePWM uint8   // curve-evaluated PWM, the v0.4.x output
	WPred       float64 // [0, 1] from aggregator

	// Upstream Snapshots — any may be nil.
	Coupling *coupling.Snapshot
	Marginal *marginal.Snapshot
	LayerA   *layer_a.Snapshot

	// LoadFraction is the current system load proxy (CPU-some PSI
	// or load-avg fraction), in [0, 1]. Feeds Path-A re-derive.
	LoadFraction float64

	DT  time.Duration
	Now time.Time

	// PWM clamps from the fan config.
	MinPWM uint8
	MaxPWM uint8
}

// BlendedResult is the hot-path output. The wiring layer writes
// `OutputPWM` to the fan and persists telemetry (PathARefused etc.)
// for the doctor surface.
type BlendedResult struct {
	OutputPWM     uint8
	PredictivePWM uint8 // raw predictive arm before blending
	WPred         float64

	// Refusal flags — multiple may be true on the same tick.
	PathARefused      bool
	CostRefused       bool
	FirstContactClamp bool
	PIRefused         bool
	IntegratorFrozen  bool

	UIState          string // "reactive" | "blended" | "refused-pi" | "refused-pathA" | "refused-cost"
	DiagnosticReason string // single-line explanation for telemetry/doctor
}

// NewBlended constructs an empty controller. State is populated
// lazily on first Compute per channel.
func NewBlended(cfg BlendedConfig) *BlendedController {
	if cfg.PWMUnitMax == 0 {
		cfg.PWMUnitMax = 255
	}
	return &BlendedController{
		cfg:   cfg,
		state: make(map[string]*piState),
	}
}

// Compute is the per-tick hot path. Pure-math: every dependency
// is supplied via `BlendedInputs`. Returns a populated
// `BlendedResult`; never panics; never blocks.
func (b *BlendedController) Compute(in BlendedInputs) BlendedResult {
	b.mu.Lock()
	defer b.mu.Unlock()

	st, ok := b.state[in.ChannelID]
	if !ok {
		st = &piState{}
		b.state[in.ChannelID] = st
	}
	st.lastTick = in.Now

	// Default result: pass-through to reactive. Mutated below
	// when the predictive arm is admitted.
	res := BlendedResult{
		OutputPWM:     in.ReactivePWM,
		PredictivePWM: in.ReactivePWM,
		WPred:         in.WPred,
		UIState:       "reactive",
	}

	// Step 0: w_pred ≤ 0 ⇒ pure reactive. Re-arm bumpless for
	// the next ramp-up so the first w_pred>0 tick lands on
	// I[0] = -K_p·error.
	if in.WPred <= 0 {
		st.bumplessArmed = false
		res.WPred = 0
		res.DiagnosticReason = "w_pred=0"
		return res
	}

	// Step 1: instability guards. Any failure → reactive only.
	if reason, ok := piRefuseReason(in.Coupling); !ok {
		res.PIRefused = true
		res.WPred = 0
		res.UIState = "refused-pi"
		res.DiagnosticReason = "pi-refused: " + reason
		// Re-arm bumpless: when the shard recovers, we want a
		// clean handoff to predictive on its first valid tick.
		st.bumplessArmed = false
		return res
	}

	// Step 2: derive (or reuse cached) IMC-PI gains.
	a := in.Coupling.Theta[0]
	bii := in.Coupling.Theta[1] // self-coupling for v0.5.9 (NCoupled=0)
	dtSec := in.DT.Seconds()
	if dtSec <= 0 {
		// Defensive: Compute called with zero/negative dt would
		// blow up the integrator. Fall through to reactive.
		res.PIRefused = true
		res.WPred = 0
		res.UIState = "refused-pi"
		res.DiagnosticReason = "pi-refused: dt<=0"
		return res
	}

	var Kp, Ki, tau float64
	if st.cachedKp != 0 && in.Coupling.NSamples-st.cachedNSamples < gainRefreshSamples {
		Kp = st.cachedKp
		Ki = st.cachedKi
		tau = st.cachedTau
	} else {
		var ok bool
		Kp, Ki, tau, ok = deriveIMCPIGains(a, bii, dtSec, b.cfg.Preset)
		if !ok {
			res.PIRefused = true
			res.WPred = 0
			res.UIState = "refused-pi"
			res.DiagnosticReason = "pi-refused: gain-derivation"
			st.bumplessArmed = false
			return res
		}
		st.cachedKp = Kp
		st.cachedKi = Ki
		st.cachedTau = tau
		st.cachedNSamples = in.Coupling.NSamples
	}

	// Step 3: PI computation with bumpless transfer + anti-windup.
	errSig := in.SensorTemp - in.Setpoint

	// Bumpless init on first w_pred>0 tick: I[0] = -K_p·error
	// so u[0] = K_p·error + I[0] = 0 ⇒ predictive[0] = baseline.
	if !st.bumplessArmed {
		if math.Abs(Kp) < MinKpForBumpless {
			st.integrator = 0
		} else {
			st.integrator = -Kp * errSig
		}
		st.bumplessArmed = true
	}

	// Candidate integrator + correction. We compute these BEFORE
	// committing, so we can detect saturation conditions and skip
	// the integrator update (anti-windup).
	candidateI := st.integrator + Ki*errSig*dtSec
	candidateU := Kp*errSig + candidateI
	candidatePredictive := saturateF64(float64(in.ReactivePWM)+candidateU,
		float64(in.MinPWM), float64(in.MaxPWM))

	// Anti-windup trigger A: PWM-clamp saturation in the direction
	// the integrator would push.
	pwmSatPositive := candidatePredictive >= float64(in.MaxPWM) &&
		candidateU > 0 && errSig > 0
	pwmSatNegative := candidatePredictive <= float64(in.MinPWM) &&
		candidateU < 0 && errSig < 0

	// Anti-windup trigger B: Path-A saturation refusal (computed
	// next; we set the flag BEFORE committing the integrator).
	candidateDeltaPWM := candidatePredictive - float64(in.ReactivePWM)
	pathARefused, predictedDeltaT := evalPathA(in.Marginal, candidateDeltaPWM, in.LoadFraction)

	integratorFrozen := pwmSatPositive || pwmSatNegative || pathARefused
	if !integratorFrozen {
		st.integrator = candidateI
	}

	// Final u/predictive after committing (or freezing) integrator.
	u := Kp*errSig + st.integrator
	predictiveF := saturateF64(float64(in.ReactivePWM)+u,
		float64(in.MinPWM), float64(in.MaxPWM))
	predictivePWM := uint8(math.Round(predictiveF))

	// Step 4: First-contact clamp (RULE-CTRL-FIRST-CONTACT-01).
	// On the first w_pred>0 tick of this channel's lifetime, never
	// reduce cooling below reactive — protects against a stale or
	// miscalibrated estimate driving the fan down on first engage.
	firstContactClamp := false
	if in.LayerA != nil && !in.LayerA.SeenFirstContact && predictivePWM < in.ReactivePWM {
		predictivePWM = in.ReactivePWM
		firstContactClamp = true
	}

	res.PredictivePWM = predictivePWM
	res.PathARefused = pathARefused
	res.IntegratorFrozen = integratorFrozen
	res.FirstContactClamp = firstContactClamp

	// Step 5: Cost gate. Only relevant when Path-A didn't refuse
	// (otherwise we're already on reactive).
	costRefused := false
	if !pathARefused {
		costRefused = evalCostGate(in.Marginal, predictedDeltaT,
			float64(predictivePWM)-float64(in.ReactivePWM), b.cfg.Preset)
	}
	res.CostRefused = costRefused

	// Step 6: Apply blend (or refuse).
	if pathARefused || costRefused {
		// Refusal: stay on reactive; effective w_pred = 0 in the
		// snapshot but the per-channel piState integrator is
		// preserved (frozen by anti-windup if pathARefused).
		res.OutputPWM = in.ReactivePWM
		res.WPred = 0
		switch {
		case pathARefused:
			res.UIState = "refused-pathA"
			res.DiagnosticReason = "path-A: predicted ΔT < 2°C"
		case costRefused:
			res.UIState = "refused-cost"
			res.DiagnosticReason = "cost-gate: cost > benefit"
		}
		return res
	}

	// Healthy blend: linear mix, post-clamp.
	blendF := in.WPred*float64(predictivePWM) + (1-in.WPred)*float64(in.ReactivePWM)
	res.OutputPWM = uint8(math.Round(saturateF64(blendF,
		float64(in.MinPWM), float64(in.MaxPWM))))
	res.UIState = "blended"
	res.DiagnosticReason = ""
	return res
}

// piRefuseReason centralises the RULE-CTRL-PI-05 instability guards.
// Returns ("", true) when all guards pass; ("<reason>", false) otherwise.
func piRefuseReason(s *coupling.Snapshot) (string, bool) {
	if s == nil {
		return "no coupling snapshot", false
	}
	if s.WarmingUp {
		return "Layer-B warming up", false
	}
	if len(s.Theta) < 2 {
		return "Theta too small", false
	}
	a := s.Theta[0]
	bii := s.Theta[1]
	if math.IsNaN(a) || math.IsInf(a, 0) || math.IsNaN(bii) || math.IsInf(bii, 0) {
		return "non-finite theta", false
	}
	if a <= 0 || a >= 1 {
		return "a outside (0,1)", false
	}
	if bii == 0 {
		return "b_ii=0", false
	}
	if s.Kappa > KappaUnidentifiable {
		return "kappa>1e4", false
	}
	return "", true
}

// deriveIMCPIGains returns (K_p, K_i, τ, ok) from Layer-B's RLS
// estimate (a, b_ii) and the configured preset. Caller must have
// already passed `piRefuseReason` checks.
//
// SPEC DIVERGENCE: K = |b/(1−a)|, NOT b/(1−a) directly. See file
// header comment for the correctness argument.
func deriveIMCPIGains(a, bii, dtSec float64, preset Preset) (Kp, Ki, tau float64, ok bool) {
	// τ = -dt / ln(a). a ∈ (0, 1) ⇒ ln(a) < 0 ⇒ τ > 0.
	lnA := math.Log(a)
	if lnA == 0 || math.IsNaN(lnA) || math.IsInf(lnA, 0) {
		return 0, 0, 0, false
	}
	tau = -dtSec / lnA
	if tau < TauMinSeconds {
		tau = TauMinSeconds
	}
	if tau > TauMaxSeconds {
		tau = TauMaxSeconds
	}

	// K = |b/(1-a)| — magnitude of the steady-state process gain.
	// Polarity is handled in polarity.WritePWM downstream.
	K := math.Abs(bii / (1 - a))
	if K == 0 || math.IsNaN(K) || math.IsInf(K, 0) {
		return 0, 0, 0, false
	}

	lambda := lambdaForPreset(preset) * tau
	theta := dtSec
	if lambda+theta < 1e-6 {
		return 0, 0, 0, false
	}
	Kp = tau / (K * (lambda + theta))
	Ki = Kp / tau
	return Kp, Ki, tau, true
}

// evalPathA computes the Path-A saturation refusal flag (Layer-C
// predicted-ΔT < 2°C check). Returns (refused, predictedDeltaT).
//
// Spec §2.6:
//
//	margin       = marginal.Theta[0] + marginal.Theta[1] * load
//	predicted_ΔT = margin · candidate_ΔPWM
//	refuse if !WarmingUp && |predicted_ΔT| < 2°C
//
// Nil snapshot or short Theta ⇒ no refusal (Layer-C absent ⇒ defer
// to anti-windup PWM-saturation check alone).
func evalPathA(m *marginal.Snapshot, candidateDeltaPWM, loadFraction float64) (refused bool, predictedDeltaT float64) {
	if m == nil || m.WarmingUp || len(m.Theta) < 2 {
		return false, 0
	}
	margin := m.Theta[0] + m.Theta[1]*loadFraction
	predictedDeltaT = margin * candidateDeltaPWM
	if math.Abs(predictedDeltaT) < PathASaturationDeltaT {
		return true, predictedDeltaT
	}
	return false, predictedDeltaT
}

// evalCostGate is the linear acoustic cost gate (R18 stub).
//
// Spec §2.7:
//
//	cost(ΔPWM)    = k_factor[preset] · |ΔPWM|
//	benefit       = -predictedDeltaT       // positive when cooling
//	refuse iff benefit < cost
func evalCostGate(m *marginal.Snapshot, predictedDeltaT, deltaPWM float64, preset Preset) bool {
	if m == nil || m.WarmingUp {
		return false
	}
	cost := costFactorForPreset(preset) * math.Abs(deltaPWM)
	benefit := -predictedDeltaT // sign flip: cooling is "benefit"
	return benefit < cost
}

// saturateF64 clamps v to [lo, hi].
func saturateF64(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
