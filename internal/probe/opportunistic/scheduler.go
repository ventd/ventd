package opportunistic

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/idle"
	"github.com/ventd/ventd/internal/observation"
	"github.com/ventd/ventd/internal/probe"
)

// DefaultTickInterval is the scheduler poll cadence (60 s). The tick
// is independent of the OpportunisticGate's 600 s durability — most
// ticks are cheap predicate evaluations that refuse without firing.
const DefaultTickInterval = 60 * time.Second

// SchedulerConfig collects every dependency the scheduler needs.
// Most fields are required; tests may inject mocks via the function
// hooks.
type SchedulerConfig struct {
	// Channels is the live controllable-channel set. Filtered to
	// non-phantom polarity by NewDetector.
	Channels []*probe.ControllableChannel
	// Detector identifies coverage gaps. Required.
	Detector *Detector
	// ProbeDeps is forwarded to FireOne. Required.
	ProbeDeps ProbeDeps
	// IdleCfg is forwarded to OpportunisticGate. ProcRoot, SysRoot,
	// and Clock should be set; durability/tick are overridden by the
	// scheduler if zero.
	IdleCfg idle.OpportunisticGateConfig
	// FirstInstallMarkerPath is the absolute path of the marker file.
	// Empty disables the 24-hour delay (test convenience).
	FirstInstallMarkerPath string
	// Disabled, when non-nil and returning true, refuses every tick
	// with ReasonOpportunisticDisabled. Wires the
	// Config.NeverActivelyProbeAfterInstall toggle into the scheduler
	// without taking a *config.Config dependency.
	Disabled func() bool
	// IsManualMode, when non-nil, returns true for channels in manual
	// mode. Manual-mode channels are skipped per RULE-OPP-PROBE-09.
	IsManualMode func(*probe.ControllableChannel) bool
	// ChannelKnowns maps observation.ChannelID to stall/min-spin
	// anchors. Forwarded to Detector at gap-computation time.
	Knowns map[uint16]ChannelKnowns
	// LastProbeAt persists the most-recent successful-or-aborted
	// probe timestamp per channel. Loaded at construction; saved on
	// every fire. Used by tie-break when multiple channels have
	// identical gap counts. nil disables persistence (test convenience).
	LastProbeAt LastProbeStore
	// TickInterval overrides DefaultTickInterval. Zero uses default.
	TickInterval time.Duration
	// Now is the clock function used for log records and last-probe
	// timestamps. nil uses time.Now.
	Now func() time.Time
	// Logger is the structured logger. nil uses slog.Default.
	Logger *slog.Logger
}

// LastProbeStore is the persistence interface for per-channel
// last-probe timestamps. Implementations may use spec-16 KV in
// production or an in-memory map in tests.
type LastProbeStore interface {
	GetLastProbe(channelID uint16) (time.Time, bool)
	SetLastProbe(channelID uint16, ts time.Time) error
}

// Status is the JSON-serializable view of the scheduler's runtime
// state, exposed via the v0.5.5 PR-B web endpoint.
type Status struct {
	Running    bool      `json:"running"`
	ChannelID  uint16    `json:"channel_id,omitempty"`
	GapPWM     uint8     `json:"gap_pwm,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	LastReason string    `json:"last_reason,omitempty"`
	TickCount  uint64    `json:"tick_count"`
}

// Scheduler is the long-running goroutine that arbitrates
// opportunistic probes. Construct via NewScheduler, then call Run
// from cmd/ventd/main.go alongside the controller goroutines.
type Scheduler struct {
	cfg SchedulerConfig

	// runState protects the Status and ensures one probe in flight
	// system-wide (RULE-OPP-PROBE-03).
	runMu      sync.Mutex
	runStatus  Status
	runActive  atomic.Bool
	tickCount  atomic.Uint64
	lastReason atomic.Value // string
}

// NewScheduler constructs a Scheduler. It does not start any
// goroutine; the caller must invoke Run.
func NewScheduler(cfg SchedulerConfig) (*Scheduler, error) {
	if cfg.Detector == nil {
		return nil, fmt.Errorf("opportunistic: SchedulerConfig.Detector is nil")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = DefaultTickInterval
	}
	return &Scheduler{cfg: cfg}, nil
}

// Status returns a snapshot of the scheduler's runtime state.
func (s *Scheduler) Status() Status {
	st := Status{
		Running:   s.runActive.Load(),
		TickCount: s.tickCount.Load(),
	}
	if r := s.lastReason.Load(); r != nil {
		st.LastReason = r.(string)
	}
	if st.Running {
		s.runMu.Lock()
		st.ChannelID = s.runStatus.ChannelID
		st.GapPWM = s.runStatus.GapPWM
		st.StartedAt = s.runStatus.StartedAt
		s.runMu.Unlock()
	}
	return st
}

// Run blocks until ctx is cancelled. Each tick:
//  1. Refuse if Disabled() (RULE-OPP-PROBE-08).
//  2. Refuse if the first-install marker is < 24 h old
//     (RULE-OPP-PROBE-07).
//  3. Refuse if OpportunisticGate refuses (RULE-OPP-PROBE-01).
//  4. Compute gaps; refuse if none.
//  5. Pick the lowest-PWM gap on the channel with the largest gap
//     set, tie-broken by oldest LastProbeAt.
//  6. Refuse if the chosen channel is in manual mode
//     (RULE-OPP-PROBE-09).
//  7. Fire one probe via FireOne. Persist LastProbeAt on either
//     outcome.
func (s *Scheduler) Run(ctx context.Context) error {
	// Ensure the first-install marker exists. Idempotent.
	if path := s.cfg.FirstInstallMarkerPath; path != "" {
		if _, err := EnsureMarker(path, s.cfg.Now()); err != nil {
			s.cfg.Logger.Warn("opportunistic install marker", "err", err)
		}
	}

	t := time.NewTicker(s.cfg.TickInterval)
	defer t.Stop()
	for {
		s.tick(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// tick runs one scheduler iteration. Exposed for tests.
func (s *Scheduler) tick(ctx context.Context) {
	s.tickCount.Add(1)

	// 1. Toggle.
	if s.cfg.Disabled != nil && s.cfg.Disabled() {
		s.lastReason.Store(string(idle.ReasonOpportunisticDisabled))
		return
	}

	// 2. First-install delay.
	if path := s.cfg.FirstInstallMarkerPath; path != "" {
		past, err := PastFirstInstallDelay(path, s.cfg.Now())
		if err != nil {
			s.cfg.Logger.Warn("opportunistic install delay check", "err", err)
		}
		if !past {
			s.lastReason.Store(string(idle.ReasonOpportunisticBootWindow))
			return
		}
	}

	// 3. Idle gate.
	ok, reason, _ := idle.OpportunisticGate(ctx, s.cfg.IdleCfg)
	if !ok {
		s.lastReason.Store(string(reason))
		return
	}

	// 4. Gaps.
	gaps, err := s.cfg.Detector.Gaps(s.cfg.Now())
	if err != nil {
		s.cfg.Logger.Warn("opportunistic gap detection", "err", err)
		s.lastReason.Store("gap_detection_error")
		return
	}
	if len(gaps) == 0 {
		s.lastReason.Store("no_gaps")
		return
	}

	// 5. Pick the channel with the largest gap set; tie-break on
	// oldest LastProbeAt.
	pickID, pickPWM, pickedCh := s.pickChannel(gaps)
	if pickedCh == nil {
		s.lastReason.Store("no_eligible_channels")
		return
	}

	// 6. Manual-mode refusal.
	if s.cfg.IsManualMode != nil && s.cfg.IsManualMode(pickedCh) {
		s.lastReason.Store(string("manual_mode"))
		return
	}

	// 7. Fire.
	s.runMu.Lock()
	s.runStatus = Status{
		Running:   true,
		ChannelID: pickID,
		GapPWM:    pickPWM,
		StartedAt: s.cfg.Now(),
	}
	s.runMu.Unlock()
	s.runActive.Store(true)
	defer func() {
		s.runActive.Store(false)
	}()

	probeErr := FireOne(ctx, pickedCh, pickPWM, s.cfg.ProbeDeps)
	if probeErr != nil {
		s.lastReason.Store("probe_error:" + probeErr.Error())
	} else {
		s.lastReason.Store("probe_complete")
	}

	if s.cfg.LastProbeAt != nil {
		if err := s.cfg.LastProbeAt.SetLastProbe(pickID, s.cfg.Now()); err != nil {
			s.cfg.Logger.Warn("opportunistic last-probe persist", "err", err)
		}
	}
}

// pickChannel implements the scheduler's choice rule:
//   - Among channels with non-empty gap sets, choose the one with the
//     largest set.
//   - Tie-break on oldest LastProbeAt (zero time = never probed = oldest).
//   - Within the chosen channel, return the lowest-PWM gap.
//
// Returns the picked observation.ChannelID, the picked PWM, and the
// matching ControllableChannel from cfg.Channels (nil if no match).
func (s *Scheduler) pickChannel(gaps map[uint16][]uint8) (uint16, uint8, *probe.ControllableChannel) {
	if len(gaps) == 0 {
		return 0, 0, nil
	}
	type cand struct {
		id     uint16
		pwms   []uint8
		oldest time.Time
		ch     *probe.ControllableChannel
	}
	cands := make([]cand, 0, len(gaps))
	for id, pwms := range gaps {
		if len(pwms) == 0 {
			continue
		}
		var ch *probe.ControllableChannel
		for _, c := range s.cfg.Channels {
			if observation.ChannelID(c.PWMPath) == id {
				ch = c
				break
			}
		}
		if ch == nil {
			continue
		}
		var oldest time.Time
		if s.cfg.LastProbeAt != nil {
			if ts, ok := s.cfg.LastProbeAt.GetLastProbe(id); ok {
				oldest = ts
			}
		}
		sort.Slice(pwms, func(i, j int) bool { return pwms[i] < pwms[j] })
		cands = append(cands, cand{id: id, pwms: pwms, oldest: oldest, ch: ch})
	}
	if len(cands) == 0 {
		return 0, 0, nil
	}
	sort.Slice(cands, func(i, j int) bool {
		// Largest gap set first.
		if len(cands[i].pwms) != len(cands[j].pwms) {
			return len(cands[i].pwms) > len(cands[j].pwms)
		}
		// Then oldest LastProbeAt (zero is oldest = wins).
		return cands[i].oldest.Before(cands[j].oldest)
	})
	winner := cands[0]
	return winner.id, winner.pwms[0], winner.ch
}
