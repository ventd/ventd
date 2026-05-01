// Package aggregator implements v0.5.9's per-channel confidence
// aggregation chain (R12 §Q3 + §Q6 + §Q4). Per spec-v0_5_9 §2.5.
//
// Inputs (per channel, per tick):
//   - conf_A from internal/confidence/layer_a.Estimator.Read
//   - conf_B from internal/coupling.Snapshot.Confidence
//   - conf_C from internal/marginal.Runtime + active-signature
//     collapse (caller picks the right shard)
//   - drift flags per layer (R16; v0.5.9 always false)
//   - global gate boolean (R12 §Q6)
//
// Output: w_pred ∈ [0, 1] for the predictive controller blend, plus
// a 5-state UI label and the intermediate values for the doctor
// surface.
//
// Aggregation order (locked by R12 §Q3):
//
//  1. drift decay PER LAYER:
//     conf_X_decayed = conf_X · 0.5^(seconds_since_drift_set / 60)
//     when the layer's drift_flag is set; otherwise unchanged.
//
//  2. min collapse:
//     w_raw = clamp(min(conf_A_decayed, conf_B_decayed, conf_C_decayed), 0, 1)
//
//  3. LPF (wraps the MIN, not each component):
//     w_filt = w_filt_prev + (dt/τ_w) · (w_raw − w_filt_prev), τ_w = 30s
//
//  4. Lipschitz clamp on the per-tick LPF delta:
//     w_pred = w_filt_prev + clamp(w_filt − w_filt_prev, ±L_max·dt)
//     L_max = 0.05/s  (≤ 0.1 step per 2-s tick)
//
// Cold-start hard pin: first 5 min after Envelope C completion,
// w_pred = 0 regardless of the formula above.
//
// Global gate (R12 §Q6): when false, every channel's effective
// w_pred is forced to 0 — the ONLY path that bypasses the Lipschitz
// clamp. This matches R12's "instantly drop to safe" requirement.
//
// 5-state UI collapse: Refused > Drifting > Cold-start > Warming
// > Converged. Hysteresis ±0.02 around the 0.40 boundary between
// Warming and Converged so the UI doesn't flicker.
package aggregator

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Locked R12 parameters from spec-v0_5_9 §3.4.
const (
	// LPFTauW is the LPF time constant for the w_raw → w_filt step.
	// 30 seconds per R12 §Q3.
	LPFTauW = 30 * time.Second

	// LMax is the Lipschitz bound on w_pred per second. 0.05/s
	// translates to ≤ 0.1 PWM-blend-weight step per 2-s tick.
	LMax = 0.05

	// DriftHalfLife is the per-layer 0.5^(t/T_half) decay constant.
	// 60 seconds per R12 §Q5.
	DriftHalfLife = 60 * time.Second

	// ColdStartWindow is the hard-pin window after Envelope C
	// completion. Spec-v0_5_9 §3.3 locks 5 minutes uniformly;
	// telemetry-informed class-aware refinement is deferred.
	ColdStartWindow = 5 * time.Minute

	// UIBoundaryConverged is the conf threshold above which the UI
	// reports "Converged" rather than "Warming". Hysteresis band
	// ±UIBoundaryHysteresis suppresses flicker.
	UIBoundaryConverged  = 0.40
	UIBoundaryHysteresis = 0.02
)

// 5-state UI labels per spec-v0_5_9 §5.7 / RULE-UI-CONF-01.
const (
	UIStateRefused   = "refused"
	UIStateDrifting  = "drifting"
	UIStateColdStart = "cold-start"
	UIStateWarming   = "warming"
	UIStateConverged = "converged"
)

// LayerIndex labels for the drift_flags array.
const (
	LayerA = 0
	LayerB = 1
	LayerC = 2
)

// Snapshot is the lock-free per-tick output of the aggregator. The
// controller call site reads these via atomic.Pointer.
type Snapshot struct {
	ChannelID string

	// Inputs (snapshot of caller-supplied scalars for the doctor
	// surface — these are also exposed raw via the same channel).
	ConfA      float64
	ConfB      float64
	ConfC      float64
	DriftFlags [3]bool

	// Intermediate values (R12 §Q3 doctor depth surface).
	Wraw  float64 // post-drift, pre-LPF
	Wfilt float64 // post-LPF, pre-Lipschitz
	Wpred float64 // post-Lipschitz, this tick's blend gate

	// 5-state UI label per RULE-UI-CONF-01.
	UIState string
}

// Config drives Aggregator construction. All fields have sensible
// defaults; zero Config is valid.
type Config struct{}

// Aggregator owns per-channel state. Tick is the hot path; Read is
// lock-free via atomic.Pointer.
type Aggregator struct {
	mu       sync.Mutex
	channels map[string]*channelState

	// envelopeCDoneAt is the wall-clock at which the daemon's
	// Envelope C calibration completed; used as the cold-start
	// pin's t0. Zero time.Time disables the pin (test fixtures).
	envelopeCDoneAt time.Time
}

type channelState struct {
	wFiltPrev    float64
	wPredPrev    float64
	lastTick     time.Time
	driftSetTime [3]time.Time // wall-clock when each layer's drift flag was set
	snapshot     atomic.Pointer[Snapshot]
}

// New constructs an empty Aggregator. Channels are admitted lazily
// by the first Tick call.
func New(cfg Config) *Aggregator {
	return &Aggregator{channels: make(map[string]*channelState)}
}

// SetEnvelopeCDoneAt records the wall-clock at which the daemon's
// Envelope C calibration completed. Used to gate the cold-start
// hard pin (RULE-AGG-COLDSTART-01). Pass time.Time{} to disable.
func (a *Aggregator) SetEnvelopeCDoneAt(t time.Time) {
	a.mu.Lock()
	a.envelopeCDoneAt = t
	a.mu.Unlock()
}

// SetDrift records that a layer's drift_flag has just been set (or
// cleared). The aggregator decays that layer's confidence input by
// 0.5^(t/T_half) starting from `now` until the flag is cleared.
//
// `set=false` clears the flag (no decay).
func (a *Aggregator) SetDrift(channelID string, layer int, set bool, now time.Time) {
	if layer < 0 || layer > 2 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.channels[channelID]
	if !ok {
		c = &channelState{}
		a.channels[channelID] = c
	}
	if set {
		c.driftSetTime[layer] = now
	} else {
		c.driftSetTime[layer] = time.Time{}
	}
}

// Tick is the hot path. Caller passes raw conf_A/B/C scalars,
// per-layer drift flags, and the global gate boolean. Returns the
// post-aggregation snapshot.
//
// driftFlags is indexed by LayerA/LayerB/LayerC.
//
// wPredSystem == false forces the channel's w_pred to 0 immediately
// (bypassing Lipschitz). Returns a snapshot with UIStateRefused.
func (a *Aggregator) Tick(channelID string, confA, confB, confC float64,
	driftFlags [3]bool, wPredSystem bool, now time.Time) *Snapshot {

	a.mu.Lock()
	c, ok := a.channels[channelID]
	if !ok {
		c = &channelState{}
		a.channels[channelID] = c
	}

	// SetDrift is the canonical setter, but Tick also records the
	// initial set time when callers pass driftFlags directly without
	// having called SetDrift first. Idempotent: set time is recorded
	// only the first time the flag transitions false→true.
	for i := 0; i < 3; i++ {
		if driftFlags[i] && c.driftSetTime[i].IsZero() {
			c.driftSetTime[i] = now
		} else if !driftFlags[i] {
			c.driftSetTime[i] = time.Time{}
		}
	}

	// Step 1: per-layer drift decay BEFORE min collapse.
	confADecayed := applyDriftDecay(confA, driftFlags[LayerA], c.driftSetTime[LayerA], now)
	confBDecayed := applyDriftDecay(confB, driftFlags[LayerB], c.driftSetTime[LayerB], now)
	confCDecayed := applyDriftDecay(confC, driftFlags[LayerC], c.driftSetTime[LayerC], now)

	// Step 2: min collapse (clamp to [0, 1]).
	wRaw := minOfThree(confADecayed, confBDecayed, confCDecayed)
	wRaw = clamp01(wRaw)

	// Compute dt for LPF + Lipschitz. First tick uses wFiltPrev=0
	// implicitly (zero-value channelState).
	var dt time.Duration
	if !c.lastTick.IsZero() {
		dt = now.Sub(c.lastTick)
		if dt < 0 {
			dt = 0
		}
	}

	// Step 3: LPF wraps the min (single-pole exponential).
	wFilt := lpfStep(c.wFiltPrev, wRaw, dt)

	// Step 4: Lipschitz clamp on the LPF delta.
	wPred := lipschitzClamp(c.wPredPrev, wFilt, dt)

	// Cold-start hard pin: first ColdStartWindow after envelopeC
	// completion → w_pred = 0.
	coldStartActive := !a.envelopeCDoneAt.IsZero() && now.Sub(a.envelopeCDoneAt) < ColdStartWindow
	if coldStartActive {
		wPred = 0
	}

	// Global gate: forces w_pred = 0 immediately, bypassing
	// Lipschitz (RULE-AGG-GLOBAL-01). The cold-start pin is also
	// short-circuited because either path reaches the same w_pred=0
	// outcome.
	if !wPredSystem {
		wPred = 0
	}

	c.wFiltPrev = wFilt
	c.wPredPrev = wPred
	c.lastTick = now

	uiState := classifyUIState(wPred, driftFlags, coldStartActive, wPredSystem)

	s := &Snapshot{
		ChannelID:  channelID,
		ConfA:      confA,
		ConfB:      confB,
		ConfC:      confC,
		DriftFlags: driftFlags,
		Wraw:       wRaw,
		Wfilt:      wFilt,
		Wpred:      wPred,
		UIState:    uiState,
	}
	c.snapshot.Store(s)
	a.mu.Unlock()
	return s
}

// Read returns the most recent snapshot for channelID without
// blocking. Returns nil when the channel has never ticked.
func (a *Aggregator) Read(channelID string) *Snapshot {
	a.mu.Lock()
	c, ok := a.channels[channelID]
	a.mu.Unlock()
	if !ok {
		return nil
	}
	return c.snapshot.Load()
}

// SnapshotAll returns a copy of every channel's most recent
// snapshot. Order is unspecified.
func (a *Aggregator) SnapshotAll() []*Snapshot {
	a.mu.Lock()
	out := make([]*Snapshot, 0, len(a.channels))
	for _, c := range a.channels {
		if s := c.snapshot.Load(); s != nil {
			out = append(out, s)
		}
	}
	a.mu.Unlock()
	return out
}

// applyDriftDecay multiplies x by 0.5^(seconds_since_drift_set / DriftHalfLife)
// when set is true and setTime is non-zero. Otherwise returns x unchanged.
func applyDriftDecay(x float64, set bool, setTime, now time.Time) float64 {
	if !set || setTime.IsZero() {
		return x
	}
	age := now.Sub(setTime)
	if age <= 0 {
		return x
	}
	return x * math.Exp2(-age.Seconds()/DriftHalfLife.Seconds())
}

// lpfStep is one tick of single-pole exponential LPF:
//
//	y[n] = y[n-1] + (dt/τ) · (x[n] − y[n-1])
//
// dt ≥ τ saturates the response (full step toward x). dt = 0 holds.
func lpfStep(prev, x float64, dt time.Duration) float64 {
	if dt <= 0 {
		return prev
	}
	alpha := dt.Seconds() / LPFTauW.Seconds()
	if alpha > 1 {
		alpha = 1
	}
	return prev + alpha*(x-prev)
}

// lipschitzClamp limits |y[n] − y[n-1]| to L_max · dt. This caps the
// per-tick rate of change in w_pred so a sudden conf drop cannot
// reduce cooling faster than 0.1 weight units per 2-s tick.
func lipschitzClamp(prev, candidate float64, dt time.Duration) float64 {
	if dt <= 0 {
		return prev
	}
	maxDelta := LMax * dt.Seconds()
	delta := candidate - prev
	if delta > maxDelta {
		delta = maxDelta
	} else if delta < -maxDelta {
		delta = -maxDelta
	}
	return prev + delta
}

// classifyUIState collapses the four conditions into the 5-state
// label per RULE-UI-CONF-01.
//
// Priority: Refused > Drifting > Cold-start > Warming > Converged.
func classifyUIState(wPred float64, drift [3]bool, coldStart, gateOn bool) string {
	if !gateOn {
		return UIStateRefused
	}
	if drift[LayerA] || drift[LayerB] || drift[LayerC] {
		return UIStateDrifting
	}
	if coldStart {
		return UIStateColdStart
	}
	if wPred >= UIBoundaryConverged-UIBoundaryHysteresis {
		return UIStateConverged
	}
	return UIStateWarming
}

func minOfThree(a, b, c float64) float64 {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
