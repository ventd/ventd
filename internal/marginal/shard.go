// Package marginal implements v0.5.8's Layer-C per-(channel, signature)
// marginal-benefit RLS estimator. Per spec-v0_5_8-marginal-benefit.md.
//
// Layer C learns ΔT_per_+1_PWM = β_0 + β_1·load (d_C=2 per R10 §10.1)
// and exposes a saturation flag the v0.5.9 confidence-gated controller
// uses to refuse ramps that pay full acoustic cost for zero thermal
// benefit.
package marginal

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"gonum.org/v1/gonum/mat"

	"github.com/ventd/ventd/internal/coupling"
)

// Locked dimensions and thresholds. Tests pin every constant against
// its R-bundle source (R10 §10.1 d_C=2; R11 §0 saturation; R12
// bounded covariance).
const (
	// DimC is the per-(channel, signature) parameter vector dimension
	// per R10 §10.1: φ = [1, load], θ = [β_0, β_1].
	DimC = 2

	// SaturationDeltaT is R11 §0's locked saturation threshold (°C).
	// Below this, ΔT is indistinguishable from sensor noise on
	// coretemp/nct/it87 1°C-quantized chips.
	SaturationDeltaT = 2.0

	// SaturationNWritesFastLoop / SaturationNReadsSlowLoop are R11 §0
	// locked window sizes for Path-B observed-saturation detection.
	SaturationNWritesFastLoop = 20
	SaturationNReadsSlowLoop  = 3

	// NMinR12 is R12 §Q1's sample_count_term saturation point —
	// where conf_C's sample-count input reaches 1.0.
	NMinR12 = 50

	// EWMAResidualAlpha is R12 §Q1's α=0.95 EWMA decay.
	EWMAResidualAlpha = 0.95

	// EFloor is the residual-term denominator: residual_term =
	// clamp(1 − √E_k / √E_floor, 0, 1). Set to (2°C)² so that an
	// EWMA at the noise floor (R11 §0) yields residual_term = 0.
	EFloor = SaturationDeltaT * SaturationDeltaT // 4.0

	// MinLambda / MaxLambda are R12's locked forgetting bounds.
	MinLambda = 0.95
	MaxLambda = 0.999

	// TrPCap is R12's tr(P) ceiling. Same as v0.5.7 Layer B.
	TrPCap = 100.0
)

// Config drives Shard construction. Lambda is required; InitialP
// defaults to 1000 if zero. ChannelID and SignatureLabel must both
// be non-empty.
type Config struct {
	ChannelID      string
	SignatureLabel string
	InitialP       float64 // P_0 = α·I, α=1000 typical (R10 §10.4)
	Lambda         float64

	// Layer-B prior seeding inputs. When LayerBConfirmed is true
	// AND PWMUnitMax > 0, β_0 is initialised to b_ii / PWMUnitMax
	// per R10 §10.7 / RULE-CMB-PRIOR-01. Otherwise β_0 starts at 0.
	LayerBPriorBii  float64 // diagonal coupling coefficient from Layer-B
	LayerBConfirmed bool    // true iff signguard confirmed sign
	PWMUnitMax      int     // 255 for duty_0_255 channels
}

// DefaultConfig returns R12-locked defaults: α=1000, λ=0.99.
func DefaultConfig(channelID, signatureLabel string) Config {
	return Config{
		ChannelID:      channelID,
		SignatureLabel: signatureLabel,
		InitialP:       1000.0,
		Lambda:         0.99,
	}
}

// Shard is one (channel, signature) RLS estimator. Concurrent
// Update/ObserveOutcome calls are serialised by mu; Read is
// lock-free via atomic.Pointer (RULE-CMB-RUNTIME-03).
type Shard struct {
	cfg    Config
	pInit  float64 // tr(P_0) cached for tr(P̂) in ConfidenceComponents
	lambda float64

	mu    sync.Mutex
	theta *mat.VecDense // [β_0, β_1]
	p     *mat.SymDense // 2x2 covariance

	nSamples     uint64
	ewmaResidual float64
	lastLoad     float64

	// Path-B observed-saturation tracking
	observedZeroDeltaTRun int
	observedSaturationPWM uint8

	// Set true after Layer-B parent confirms warmup-cleared at
	// admission. RULE-CMB-WARMUP-01.
	parentLayerBOutOfWarmup bool

	// Lock-free Snapshot read.
	snapshot atomic.Pointer[Snapshot]
}

// SnapshotKind aliases coupling.SnapshotKind so v0.5.7 and v0.5.8
// share a single classification surface.
type SnapshotKind = coupling.SnapshotKind

// Re-export the Kind constants for consumer convenience.
const (
	KindWarmup         = coupling.KindWarmup
	KindHealthy        = coupling.KindHealthy
	KindMarginal       = coupling.KindMarginal
	KindUnidentifiable = coupling.KindUnidentifiable
)

// Snapshot is the Read()-returned state for a Shard. Lock-free
// via atomic.Pointer; safe for the controller hot loop in v0.5.9.
type Snapshot struct {
	ChannelID      string
	SignatureLabel string
	Kind           SnapshotKind
	Theta          []float64

	// Identifiability state.
	TrP          float64
	InitialP     float64
	NSamples     uint64
	EWMAResidual float64

	// Saturation surface — TWO flags, consumed differently by
	// v0.5.9 (Path A → "refuse this ramp"; Path B → "drop conf_C").
	Saturated             bool
	SaturationAdmitR11    bool
	ObservedZeroDeltaTRun int
	ObservedSaturationPWM uint8

	MarginalSlope float64 // β_0 + β_1·lastLoad
	WarmingUp     bool

	Confidence ConfidenceComponents
}

// ConfidenceComponents are the four R12 §Q1 input terms emitted
// by v0.5.8. v0.5.9 owns aggregation, decay, Lipschitz, LPF.
type ConfidenceComponents struct {
	SaturationAdmit bool
	ResidualTerm    float64
	CovarianceTerm  float64
	SampleCountTerm float64
}

// New constructs a Shard with the given config. Returns an error
// if Lambda is outside R12 bounds [0.95, 0.999] or if ChannelID /
// SignatureLabel are empty.
func New(cfg Config) (*Shard, error) {
	if cfg.ChannelID == "" {
		return nil, errors.New("marginal: empty ChannelID")
	}
	if cfg.SignatureLabel == "" {
		return nil, errors.New("marginal: empty SignatureLabel")
	}
	if cfg.InitialP <= 0 {
		cfg.InitialP = 1000.0
	}
	if cfg.Lambda < MinLambda || cfg.Lambda > MaxLambda {
		return nil, errors.New("marginal: Lambda outside R12 bounds [0.95, 0.999]")
	}
	s := &Shard{
		cfg:    cfg,
		pInit:  cfg.InitialP * float64(DimC), // tr(P_0) = α·d
		lambda: cfg.Lambda,
		theta:  mat.NewVecDense(DimC, nil),
		p:      mat.NewSymDense(DimC, nil),
	}
	for i := 0; i < DimC; i++ {
		s.p.SetSym(i, i, cfg.InitialP)
	}
	// Layer-B prior seeding (gated by signguard confirmation).
	if cfg.LayerBConfirmed && cfg.PWMUnitMax > 0 && cfg.LayerBPriorBii != 0 {
		s.theta.SetVec(0, cfg.LayerBPriorBii/float64(cfg.PWMUnitMax))
	}
	s.publish()
	return s, nil
}

// SetParentOutOfWarmup is called by the runtime once it has observed
// the parent Layer-B shard out-of-warmup (RULE-CMB-WARMUP-01). Once
// true the Layer-C warmup gate can clear independently.
func (s *Shard) SetParentOutOfWarmup(b bool) {
	s.mu.Lock()
	s.parentLayerBOutOfWarmup = b
	s.publish()
	s.mu.Unlock()
}

// Update folds one (φ, y) observation into the RLS estimate.
// Sherman-Morrison rank-1 via gonum mat.SymRankOne (R10 §10.3),
// followed by R12 bounded-covariance clamp.
func (s *Shard) Update(now time.Time, phi []float64, y float64) error {
	if len(phi) != DimC {
		return errors.New("marginal: phi length != DimC")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	phiVec := mat.NewVecDense(DimC, append([]float64(nil), phi...))

	// Predicted ŷ = θᵀφ; residual e = y − ŷ.
	yHat := mat.Dot(s.theta, phiVec)
	e := y - yHat

	// EWMA residual (R12 §Q1).
	s.ewmaResidual = EWMAResidualAlpha*s.ewmaResidual + (1-EWMAResidualAlpha)*e*e

	// Sherman-Morrison: P_new = (P − P φ φᵀ P / (λ + φᵀ P φ)) / λ.
	var pPhi mat.VecDense
	pPhi.MulVec(s.p, phiVec)
	denom := s.lambda + mat.Dot(phiVec, &pPhi)
	if denom < 1e-12 {
		// Pathological denominator; skip to keep numerical stability.
		s.lastLoad = phi[1]
		s.publish()
		return nil
	}

	scale := -1.0 / denom
	s.p.SymRankOne(s.p, scale, &pPhi)
	for r := 0; r < DimC; r++ {
		for c := r; c < DimC; c++ {
			s.p.SetSym(r, c, s.p.At(r, c)/s.lambda)
		}
	}

	// θ_new = θ + (P_new φ) e.
	var pNewPhi mat.VecDense
	pNewPhi.MulVec(s.p, phiVec)
	s.theta.AddScaledVec(s.theta, e, &pNewPhi)

	// R12 bounded-covariance clamp (RULE-CMB-SHARD-03).
	tr := mat.Trace(s.p)
	if tr > TrPCap {
		k := TrPCap / tr
		for r := 0; r < DimC; r++ {
			for c := r; c < DimC; c++ {
				s.p.SetSym(r, c, s.p.At(r, c)*k)
			}
		}
	}

	s.nSamples++
	s.lastLoad = phi[1]
	s.publish()
	return nil
}

// MarginalSlope returns β_0 + β_1·load — the predicted ΔT-per-+1-PWM
// at the given load. RULE-CMB-CONF-01: this is what v0.5.9 multiplies
// by the candidate ΔPWM to predict ΔT.
func (s *Shard) MarginalSlope(load float64) float64 {
	snap := s.snapshot.Load()
	if snap == nil || len(snap.Theta) != DimC {
		return 0
	}
	return snap.Theta[0] + snap.Theta[1]*load
}

// PredictDT returns the predicted ΔT for a candidate ΔPWM at the given
// load. Path-A saturation rule: ΔT < SaturationDeltaT → saturated.
func (s *Shard) PredictDT(deltaPWM int, load float64) float64 {
	return s.MarginalSlope(load) * float64(deltaPWM)
}

// IsSaturated implements the dual-path saturation rule:
// - Path A (RLS-driven): predicted ΔT for the candidate ΔPWM < 2°C.
// - Path B (R11-locked): observed run of 20 consecutive sub-2°C writes.
// During warmup the flag is forced false (RULE-CMB-SAT-03).
func (s *Shard) IsSaturated(deltaPWM int, load float64) bool {
	snap := s.snapshot.Load()
	if snap == nil {
		return false
	}
	if snap.WarmingUp {
		return false
	}
	if deltaPWM > 0 {
		predDT := snap.MarginalSlope*float64(deltaPWM) + (snap.Theta[1]-snap.Theta[1])*load
		_ = predDT
		// MarginalSlope on snapshot is at lastLoad; recompute live.
		live := snap.Theta[0] + snap.Theta[1]*load
		if live*float64(deltaPWM) < SaturationDeltaT {
			return true
		}
	}
	if !snap.SaturationAdmitR11 {
		return true
	}
	return false
}

// ObserveOutcome consumes the actual observed ΔT after a controller
// write, driving the Path-B observed-saturation gate (R11 §0).
//
// currentPWM is recorded as ObservedSaturationPWM the first time a
// 20-write streak completes; reset to 0 when the streak breaks.
func (s *Shard) ObserveOutcome(deltaT float64, currentPWM uint8) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if math.Abs(deltaT) < SaturationDeltaT {
		s.observedZeroDeltaTRun++
		if s.observedZeroDeltaTRun == SaturationNWritesFastLoop {
			s.observedSaturationPWM = currentPWM
		}
	} else {
		s.observedZeroDeltaTRun = 0
		s.observedSaturationPWM = 0
	}
	s.publish()
}

// Read returns the current Snapshot via atomic.Pointer load. Safe to
// call concurrently with Update / ObserveOutcome (RULE-CMB-RUNTIME-03).
func (s *Shard) Read() *Snapshot { return s.snapshot.Load() }

// publish (caller holds mu) computes the new Snapshot and atomically
// installs it. A nil receiver write is impossible because Shard is
// constructed via New().
func (s *Shard) publish() {
	tr := mat.Trace(s.p)
	const nMinWarmup = uint64(5 * DimC * DimC) // = 20

	// Three-condition gate (R10 §10.4) plus parent Layer-B clearance.
	warmingUp := s.nSamples < nMinWarmup ||
		tr > 0.5*s.pInit ||
		!s.parentLayerBOutOfWarmup

	theta := []float64{s.theta.AtVec(0), s.theta.AtVec(1)}
	marginalSlope := theta[0] + theta[1]*s.lastLoad

	// Path-B (R11) admit: streak below 20 → admit; ≥20 → refuse.
	saturationAdmit := s.observedZeroDeltaTRun < SaturationNWritesFastLoop

	// Path-A: predicted saturation at lastLoad + ΔPWM=1 (doctor view;
	// controller calls IsSaturated(deltaPWM, load) for hot-loop use).
	pathA := false
	if !warmingUp && marginalSlope*1.0 < SaturationDeltaT {
		pathA = true
	}

	// ConfidenceComponents per R12 §Q1.
	residualTerm := clamp01(1.0 - math.Sqrt(s.ewmaResidual)/math.Sqrt(EFloor))
	pHat := tr / s.pInit
	covarTerm := clamp01(1.0 - pHat/float64(DimC))
	sampleTerm := clamp01(float64(s.nSamples) / float64(NMinR12))

	kind := KindHealthy
	if warmingUp {
		kind = KindWarmup
	}

	snap := &Snapshot{
		ChannelID:             s.cfg.ChannelID,
		SignatureLabel:        s.cfg.SignatureLabel,
		Kind:                  kind,
		Theta:                 theta,
		TrP:                   tr,
		InitialP:              s.pInit,
		NSamples:              s.nSamples,
		EWMAResidual:          s.ewmaResidual,
		Saturated:             pathA,
		SaturationAdmitR11:    saturationAdmit,
		ObservedZeroDeltaTRun: s.observedZeroDeltaTRun,
		ObservedSaturationPWM: s.observedSaturationPWM,
		MarginalSlope:         marginalSlope,
		WarmingUp:             warmingUp,
		Confidence: ConfidenceComponents{
			SaturationAdmit: saturationAdmit,
			ResidualTerm:    residualTerm,
			CovarianceTerm:  covarTerm,
			SampleCountTerm: sampleTerm,
		},
	}
	s.snapshot.Store(snap)
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
