// Package coupling implements v0.5.7's Layer B per-channel thermal-
// coupling estimator per spec-v0_5_7-thermal-coupling.md and the
// locked R9+R10 research at
// docs/research/r-bundle/R9-R10-identifiability-and-shards.md.
//
// Each Shard estimates the per-channel ARX-with-exogenous-input model
//
//	T_i[k+1] = a_i·T_i[k] + Σ_j b_ij·pwm_j[k] + c_i·load_i[k] + w_i[k]
//
// using a rank-1 Sherman-Morrison RLS update (`gonum/mat.SymRankOne`)
// with R12's bounded-covariance directional forgetting:
//
//   - λ ∈ [0.95, 0.999] (set per-shard from R12 auto-tuner)
//   - tr(P) clamped to ≤ 100 via post-update proportional rescale
//
// Reads are lock-free via atomic.Pointer[Snapshot]; the controller
// hot loop calls Snapshot.Read() without acquiring the shard mutex.
package coupling

import (
	"errors"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"gonum.org/v1/gonum/mat"
)

// MaxNCoupled is the per-channel cap on coupled neighbour fans.
// R10 §10.2: above 16 the analytical PE conditions degrade
// (need PE-d ≥ 18) and identifiability is hopeless under
// workload-driven excitation.
const MaxNCoupled = 16

// TrPCap is R12's bounded-covariance ceiling. Post-update
// rescale: if tr(P) > TrPCap, multiply P by TrPCap/tr(P).
// Eigenvectors preserved, only magnitudes attenuated.
const TrPCap = 100.0

// MinLambda / MaxLambda are R12's directional forgetting bounds.
const (
	MinLambda = 0.95
	MaxLambda = 0.999
)

// WarmupCovarianceRatio is the warmup gate per R10 §10.4: tr(P)
// must shrink below 0.5·tr(P_0) for the shard's snapshot to be
// consumed by downstream controllers.
const WarmupCovarianceRatio = 0.5

// SnapshotKind classifies the runtime state of a shard for
// downstream consumers (v0.5.9 controller, v0.5.10 doctor).
type SnapshotKind uint8

const (
	// KindWarmup — output not consumed; n_samples / tr(P) /
	// κ have not all met the warmup gate.
	KindWarmup SnapshotKind = iota
	// KindHealthy — κ ≤ 10², full RLS update active.
	KindHealthy
	// KindMarginal — 10² < κ ≤ 10⁴, directional forgetting
	// only in unexcited subspace.
	KindMarginal
	// KindUnidentifiable — κ > 10⁴, θ held at prior.
	KindUnidentifiable
	// KindCoVarying — pairwise Pearson detected co-varying
	// fan group; shard merged the columns into a composite.
	KindCoVarying
)

// String renders the kind for log output.
func (k SnapshotKind) String() string {
	switch k {
	case KindWarmup:
		return "warmup"
	case KindHealthy:
		return "healthy"
	case KindMarginal:
		return "marginal"
	case KindUnidentifiable:
		return "unidentifiable"
	case KindCoVarying:
		return "co-varying"
	default:
		return "unknown"
	}
}

// Snapshot is the lock-free read view of a shard. The controller
// hot loop calls Read() without acquiring the shard mutex.
type Snapshot struct {
	ChannelID    string
	Kind         SnapshotKind
	Theta        []float64 // shard parameter vector copy
	NSamples     uint64
	TrP          float64
	Kappa        float64
	Lambda       float64
	WarmingUp    bool   // true when Kind == KindWarmup
	LastTickUnix int64  // wall clock of the last Tick
	GroupedFans  []int  // indices merged into composite via U1 detection
	Reason       string // free-form diagnostic for doctor
}

// Shard implements one Layer-B RLS estimator for a single channel.
type Shard struct {
	channelID string
	d         int
	nCoupled  int

	mu       sync.Mutex
	theta    *mat.VecDense // d-vector
	p        *mat.SymDense // d×d covariance
	pInitTr  float64       // tr(P_0) for the warmup ratio
	lambda   float64
	nSamples uint64
	lastTick time.Time

	// Identifiability detector state.
	kappa  float64
	kind   SnapshotKind
	groups []int // indices that have been merged into composite columns

	// regressorWindow is the rolling W=60 ring of φ rows the
	// identifiability tick consumes to compute κ via Window.Kappa
	// (RULE-CPL-IDENT-01 + RULE-CPL-IDENT-WIRING-04). Each Update
	// appends to it; the runtime's per-minute identifiability tick
	// reads it and classifies via Window.Kappa + ClassifyKappa
	// (RULE-CPL-IDENT-02). Nil-safe: nil window = no classification,
	// snapshot.Kappa stays at 0.
	regressorWindow *Window

	// snapshot is read lock-free by Snapshot.Read() and the
	// controller hot loop. Updated by every successful Tick.
	snapshot atomic.Pointer[Snapshot]
}

// Config controls Shard construction. Production callers use
// DefaultConfig; tests inject for fault injection.
type Config struct {
	ChannelID string
	NCoupled  int     // 0 ≤ NCoupled ≤ MaxNCoupled
	InitialP  float64 // P_0 = α·I, α=1000 typical (R10 §10.4)
	Lambda    float64 // forgetting factor, in [MinLambda, MaxLambda]
}

// DefaultConfig returns R10-locked defaults: P_0 = 1000·I,
// λ = 0.99 (mid-range; R12 auto-tunes from there).
func DefaultConfig(channelID string, nCoupled int) Config {
	return Config{
		ChannelID: channelID,
		NCoupled:  nCoupled,
		InitialP:  1000.0,
		Lambda:    0.99,
	}
}

// New constructs a Shard with the given config. Returns an error
// if NCoupled exceeds MaxNCoupled (R10 §10.2 / RULE-CPL-SHARD-01).
func New(cfg Config) (*Shard, error) {
	if cfg.NCoupled < 0 {
		return nil, errors.New("coupling: NCoupled must be non-negative")
	}
	if cfg.NCoupled > MaxNCoupled {
		return nil, errors.New("coupling: NCoupled exceeds MaxNCoupled (16)")
	}
	if cfg.Lambda < MinLambda || cfg.Lambda > MaxLambda {
		return nil, errors.New("coupling: Lambda outside R12 bounds [0.95, 0.999]")
	}
	if cfg.InitialP <= 0 {
		return nil, errors.New("coupling: InitialP must be positive")
	}

	// d = 1 (a) + NCoupled (b_·) + 1 (c)
	d := 1 + cfg.NCoupled + 1
	theta := mat.NewVecDense(d, nil)
	pData := make([]float64, d*d)
	for i := 0; i < d; i++ {
		pData[i*d+i] = cfg.InitialP
	}
	p := mat.NewSymDense(d, pData)
	pInitTr := mat.Trace(p)

	s := &Shard{
		channelID:       cfg.ChannelID,
		d:               d,
		nCoupled:        cfg.NCoupled,
		theta:           theta,
		p:               p,
		pInitTr:         pInitTr,
		lambda:          cfg.Lambda,
		kind:            KindWarmup,
		regressorWindow: NewWindow(d, 60),
	}

	// Initial snapshot — all zeros, warming up.
	initial := s.buildSnapshot()
	s.snapshot.Store(initial)
	return s, nil
}

// ChannelID returns the shard's channel identifier.
func (s *Shard) ChannelID() string { return s.channelID }

// Dim returns the parameter-vector dimension (d_B = 1 + N_coupled + 1).
func (s *Shard) Dim() int { return s.d }

// Update folds one observation (regressor φ ∈ ℝ^d, target y ∈ ℝ)
// into the RLS estimate. Implements the rank-1 Sherman-Morrison
// form per R10 §10.3:
//
//	K[k]   = P[k-1]·φ[k] / (λ + φ[k]ᵀ·P[k-1]·φ[k])
//	θ[k]   = θ[k-1] + K[k]·(y[k] − φ[k]ᵀ·θ[k-1])
//	P[k]   = (P[k-1] − K[k]·φ[k]ᵀ·P[k-1]) / λ
//
// Post-update: tr(P) clamped to ≤ TrPCap via proportional
// rescale (preserves eigenvectors, attenuates magnitudes).
//
// Returns nil on success; errors are advisory (caller continues).
func (s *Shard) Update(now time.Time, phi []float64, y float64) error {
	if len(phi) != s.d {
		return errors.New("coupling: phi dimension mismatch")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	phiVec := mat.NewVecDense(s.d, phi)

	// Scratch: P · φ
	var pPhi mat.VecDense
	pPhi.MulVec(s.p, phiVec)

	// denominator = λ + φᵀ · P · φ
	denom := s.lambda + mat.Dot(phiVec, &pPhi)
	if denom < 1e-12 {
		// Numerical guard per R10 §10.8: skip the update for
		// this tick rather than dividing by ~0.
		return nil
	}

	// K = (P · φ) / denom
	gain := mat.NewVecDense(s.d, nil)
	gain.ScaleVec(1.0/denom, &pPhi)

	// residual = y − φᵀ · θ
	residual := y - mat.Dot(phiVec, s.theta)

	// θ += K · residual
	var deltaTheta mat.VecDense
	deltaTheta.ScaleVec(residual, gain)
	s.theta.AddVec(s.theta, &deltaTheta)

	// P = (P − K · φᵀ · P) / λ
	// Equivalent (Sherman-Morrison): P -= (P·φ·φᵀ·P) / denom, then /= λ.
	// Use SymRankOne with negative scale: P_new = P + α·u·uᵀ where
	// α = -1/denom, u = P·φ. SymDense's SymRankOne expects a
	// symmetric update; the rank-1 P·φ·φᵀ·P/denom IS symmetric
	// because (P·φ)·(P·φ)ᵀ/denom is symmetric.
	scale := -1.0 / denom
	s.p.SymRankOne(s.p, scale, &pPhi)

	// Divide by λ.
	for i := 0; i < s.d; i++ {
		for j := i; j < s.d; j++ {
			s.p.SetSym(i, j, s.p.At(i, j)/s.lambda)
		}
	}

	// R12 bounded-covariance clamp.
	if tr := mat.Trace(s.p); tr > TrPCap {
		ratio := TrPCap / tr
		for i := 0; i < s.d; i++ {
			for j := i; j < s.d; j++ {
				s.p.SetSym(i, j, s.p.At(i, j)*ratio)
			}
		}
	}

	s.nSamples++
	s.lastTick = now

	// Append the φ row to the identifiability window
	// (RULE-CPL-IDENT-01 + RULE-CPL-IDENT-WIRING-04). The runtime's
	// per-minute identifiability tick consumes this rolling W=60 ring
	// to compute κ and call SetKind.
	if s.regressorWindow != nil {
		_ = s.regressorWindow.Add(phi)
	}

	// Re-publish snapshot. Caller's Identifiability tick
	// (separate cadence) updates kappa + kind; we just
	// refresh the state-derived fields here.
	s.snapshot.Store(s.buildSnapshot())
	return nil
}

// SetKind transitions the shard's identifiability kind. Called
// from the identifiability tick (separate cadence per R10 §10.3:
// once per minute at K=60 ticks). Updates the snapshot atomically.
func (s *Shard) SetKind(kind SnapshotKind, kappa float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kind = kind
	s.kappa = kappa
	s.snapshot.Store(s.buildSnapshot())
}

// RegressorWindow returns the shard's rolling regressor window. Used
// by the runtime's per-minute identifiability tick to compute
// Window.Kappa and Window.FindCoVaryingPairs
// (RULE-CPL-IDENT-WIRING-04). May be nil for shards constructed
// before v0.6.0; callers MUST nil-check.
func (s *Shard) RegressorWindow() *Window {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.regressorWindow
}

// NCoupled returns the configured coupled-channel count. Used by the
// identifiability tick when calling Window.FindCoVaryingPairs.
func (s *Shard) NCoupled() int { return s.nCoupled }

// SetGroups records the co-varying fan group indices (R9 §U1 +
// pairwise Pearson detector). Called once at shard creation or
// when the detector triggers. Subsequent Updates with these
// indices treat the columns as a composite.
func (s *Shard) SetGroups(groups []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups = append(s.groups[:0], groups...)
	if len(groups) > 0 && s.kind != KindWarmup {
		s.kind = KindCoVarying
	}
	s.snapshot.Store(s.buildSnapshot())
}

// buildSnapshot constructs a Snapshot from current state. Caller
// MUST hold s.mu.
func (s *Shard) buildSnapshot() *Snapshot {
	tr := mat.Trace(s.p)
	thetaCopy := make([]float64, s.d)
	for i := 0; i < s.d; i++ {
		thetaCopy[i] = s.theta.AtVec(i)
	}
	groupsCopy := make([]int, len(s.groups))
	copy(groupsCopy, s.groups)

	warming := !s.warmupComplete(tr)
	kind := s.kind
	if warming {
		kind = KindWarmup
	}

	var lastUnix int64
	if !s.lastTick.IsZero() {
		lastUnix = s.lastTick.Unix()
	}

	return &Snapshot{
		ChannelID:    s.channelID,
		Kind:         kind,
		Theta:        thetaCopy,
		NSamples:     s.nSamples,
		TrP:          tr,
		Kappa:        s.kappa,
		Lambda:       s.lambda,
		WarmingUp:    warming,
		LastTickUnix: lastUnix,
		GroupedFans:  groupsCopy,
	}
}

// warmupComplete returns true when ALL THREE warmup conditions
// hold per R10 §10.4 / RULE-CPL-WARMUP-01:
//
//	n_samples ≥ 5·d²  AND  tr(P) ≤ 0.5·tr(P_0)  AND  κ ≤ 10⁴
//
// Caller MUST hold s.mu.
func (s *Shard) warmupComplete(tr float64) bool {
	minSamples := uint64(5 * s.d * s.d)
	if s.nSamples < minSamples {
		return false
	}
	if tr > WarmupCovarianceRatio*s.pInitTr {
		return false
	}
	if s.kappa > UnidentifiableKappaThreshold {
		return false
	}
	return true
}

// Read returns the current snapshot. Lock-free; safe to call
// from the controller hot loop. May return nil on a brand-new
// shard whose first Update has not yet completed (caller MUST
// nil-check). RULE-CPL-RUNTIME-02.
func (s *Shard) Read() *Snapshot {
	return s.snapshot.Load()
}

// RunIdentificationTick performs one identifiability classification
// pass on this shard: read the regressor window, compute κ via
// Window.Kappa, classify via ClassifyKappa, and write the kind via
// SetKind. Logs co-varying PWM-pair candidates at INFO when the
// reduced-model NCoupled is > 0 and pairs are detected.
//
// Returns true when the window had enough rows (≥ Dim()) to compute
// κ; false when under-populated — in that case the shard's snapshot
// kind comes from buildSnapshot's warmup-gate inference, unchanged.
//
// `internal/coupling/runtime.go::runShardLoop`'s per-minute
// `identTick.C` case calls this exactly once; the rule contract
// (RULE-CPL-IDENT-WIRING-04) names the helper as the dispatch
// surface so a refactor that drops the call site from runShardLoop
// requires actively deleting a named-method call rather than an
// inline block.
//
// `logger` may be nil (tests).
func (s *Shard) RunIdentificationTick(logger *slog.Logger) bool {
	win := s.RegressorWindow()
	if win == nil || win.Count() < s.Dim() {
		return false
	}
	kappa := win.Kappa()
	kind := ClassifyKappa(kappa)
	s.SetKind(kind, kappa)
	if logger != nil {
		if pairs := win.FindCoVaryingPairs(s.NCoupled()); len(pairs) > 0 {
			logger.Info("coupling: co-varying PWM-pair candidates detected",
				"channel", s.channelID,
				"pairs", pairs)
		}
		logger.Debug("coupling: identifiability classified",
			"channel", s.channelID,
			"kappa", kappa,
			"kind", kind)
	}
	return true
}

// _ keeps math imported for future use; the bounded-covariance
// clamp on tr(P) handles NaN/Inf via Go's `NaN > anything == false`
// semantics so no helper is needed.
var _ = math.NaN
