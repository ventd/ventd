package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/config"
)

// Scheduled profile switching — Session D 3e.
//
// A single goroutine (runScheduler) ticks every schedInterval, parses
// each Profile's `schedule:` grammar, and picks the "scheduled winner"
// for the current local time. On every transition (the winning profile
// name changes) the scheduler:
//
//   - applies the winner via applyProfile (same atomic-pointer swap the
//     manual /api/profile/active POST uses), and
//   - clears any active manual override.
//
// Manual overrides: a POST to /api/profile/active flips
// scheduleState.manualOverride. While the flag is set the scheduler
// does NOT apply the scheduled winner — the operator's last pick
// sticks. The flag is cleared when the scheduled winner changes, which
// is the "next transition boundary" semantic from the spec.
//
// Overlap tiebreak (documented here because the parser doesn't reject
// overlaps):
//
//   1. fewer matching days beats more (mon,wed,fri beats mon-fri beats *)
//   2. shorter duration beats longer (08:00-09:00 beats 07:00-12:00)
//   3. lexical profile name (deterministic final tiebreak)
//
// Scheduled switches are suppressed during an active panic: pinning
// every fan to max is the whole point of panic, and a profile swap
// mid-panic would violate that invariant.

const defaultScheduleInterval = 60 * time.Second

// scheduleState tracks the scheduler's view of what profile the
// schedule says should be active and whether a manual override is
// suppressing scheduled switches. Zero value is a ready-to-use mutex.
type scheduleState struct {
	mu             sync.Mutex
	manualOverride bool
	lastScheduled  string
}

func (st *scheduleState) markManualOverride() {
	st.mu.Lock()
	st.manualOverride = true
	st.mu.Unlock()
}

// observe records `winner` as the latest scheduled candidate and
// returns whether a manual override is currently suppressing the
// scheduler. If the winner changed since the last observation, the
// override is cleared — that's the "next transition boundary" rule.
func (st *scheduleState) observe(winner string) (suppressed bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if winner != st.lastScheduled {
		st.lastScheduled = winner
		st.manualOverride = false
	}
	return st.manualOverride
}

func (st *scheduleState) snapshot() (lastScheduled string, override bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.lastScheduled, st.manualOverride
}

// parsedSchedules returns name→parsed for every profile with a
// non-empty schedule string. Malformed schedules are logged and
// skipped: the config validator already rejects malformed grammar on
// load, so hitting this path means either the in-memory config was
// mutated outside Save() (shouldn't happen) or a future code path
// accepted looser input than validate does.
func parsedSchedules(cfg *config.Config, logger *slog.Logger) map[string]*config.Schedule {
	out := make(map[string]*config.Schedule, len(cfg.Profiles))
	for name, p := range cfg.Profiles {
		if p.Schedule == "" {
			continue
		}
		sch, err := config.ParseSchedule(p.Schedule)
		if err != nil {
			if logger != nil {
				logger.Error("schedule: skipping malformed schedule",
					"profile", name, "schedule", p.Schedule, "err", err)
			}
			continue
		}
		out[name] = sch
	}
	return out
}

// computeWinner picks the profile whose schedule matches `now`. On
// overlap, the tiebreak order is documented at the top of this file.
// Returns "" when no scheduled profile matches — callers that want
// the default-fallback semantics should use computeActiveProfile.
func computeWinner(scheds map[string]*config.Schedule, now time.Time) string {
	var matches []string
	for name, sch := range scheds {
		if sch.Matches(now) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := scheds[matches[i]], scheds[matches[j]]
		if adc, bdc := a.DayCount(), b.DayCount(); adc != bdc {
			return adc < bdc
		}
		if ad, bd := a.DurationMin(), b.DurationMin(); ad != bd {
			return ad < bd
		}
		return matches[i] < matches[j]
	})
	return matches[0]
}

// computeActiveProfile returns the profile the scheduler wants active
// right now. When a scheduled profile matches `now` it wins; otherwise
// the first (lex-sorted) profile with an empty Schedule is the default
// fallback. Returns "" only when the config has no profiles at all, or
// every profile carries a schedule and none match — in which case the
// scheduler leaves the live ActiveProfile untouched.
func computeActiveProfile(cfg *config.Config, scheds map[string]*config.Schedule, now time.Time) string {
	if w := computeWinner(scheds, now); w != "" {
		return w
	}
	var fallbacks []string
	for name, p := range cfg.Profiles {
		if p.Schedule == "" {
			fallbacks = append(fallbacks, name)
		}
	}
	if len(fallbacks) == 0 {
		return ""
	}
	sort.Strings(fallbacks)
	return fallbacks[0]
}

// nextTransition scans forward one minute at a time until the active
// profile (scheduled winner or fallback) differs from `current`.
// Capped at 24h (1440 iterations): if nothing changes in the next day,
// the schedule is effectively stable (one profile always wins, or
// nothing does) and the UI renders "no upcoming transition." 1440
// map lookups per call is trivial — the expensive parse already
// happened on the caller's parsedSchedules map build.
func nextTransition(cfg *config.Config, scheds map[string]*config.Schedule, current string, from time.Time) (time.Time, string, bool) {
	from = from.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 1440; i++ {
		t := from.Add(time.Duration(i) * time.Minute)
		if w := computeActiveProfile(cfg, scheds, t); w != current {
			return t, w, true
		}
	}
	return time.Time{}, "", false
}

// applyProfile is the shared mutation path used by both the manual
// /api/profile/active handler and the scheduler goroutine. It looks up
// `name`, deep-copies the live config (so readers through cfg.Load
// keep seeing a coherent snapshot), rewrites every Control whose Fan
// is bound in the chosen profile, and atomically swaps the pointer.
//
// Unknown profile returns an error — callers decide whether to return
// 400 (manual handler) or just log (scheduler). The swap is the only
// side effect; persistence is the operator's explicit Apply flow.
func (s *Server) applyProfile(name string) (*config.Config, error) {
	live := s.cfg.Load()
	profile, ok := live.Profiles[name]
	if !ok {
		return nil, fmt.Errorf("unknown profile: %s", name)
	}
	// Issue #978: Clone before mutating. The pre-Clone shallow copy
	// only re-allocated Controls and silently aliased Fans/Curves/
	// Profiles/etc. with the live pointer — any later writer racing
	// a concurrent reader would have corrupted live state.
	next := live.Clone()
	next.ActiveProfile = name
	for i := range next.Controls {
		if curve, hit := profile.Bindings[next.Controls[i].Fan]; hit {
			next.Controls[i].Curve = curve
		}
	}
	s.cfg.Store(next)
	return next, nil
}

// now returns the server's notion of the current instant in the
// scheduler's configured timezone (#624). The atomic pointer test
// seam (SetNowFn) wins when set; production leaves it nil and falls
// back to wall time converted into the configured timezone before
// returning. config.Schedule.Matches() reads t.Hour()/t.Minute()/
// t.Weekday() against whatever zone the time carries, so the
// time.In() conversion here is the single point that pins the
// scheduler's "now" semantics across the codebase.
//
// Default timezone is Local for back-compat with pre-#624 configs;
// new configs should set `scheduler.timezone: utc` (or an explicit
// IANA name) to avoid drift across geographic moves + DST.
func (s *Server) now() time.Time {
	var t time.Time
	if p := s.nowFn.Load(); p != nil && p.fn != nil {
		t = p.fn()
	} else {
		t = time.Now()
	}
	if live := s.cfg.Load(); live != nil {
		if loc, ok := live.Scheduler.Location(); ok {
			return t.In(loc)
		}
		// LoadLocation failed for a non-empty / non-utc / non-local
		// Timezone setting. Falling back to Local matches the zero-
		// value default; the warning is logged at startup
		// (scheduleTZWarnOnce) so we don't spam every tick.
		return t.In(time.Local)
	}
	return t
}

// scheduleTZWarnOnce logs a single-shot warning when the configured
// scheduler.timezone is non-empty but doesn't resolve to a known
// location. Fires from runScheduler the first tick so the warning
// surfaces near daemon startup rather than only when an operator
// queries the schedule API.
//
// Idempotent on a per-daemon basis via tzWarnFired.
func (s *Server) scheduleTZWarnOnce() {
	if s.tzWarnFired.Load() {
		return
	}
	live := s.cfg.Load()
	if live == nil {
		return
	}
	tz := strings.TrimSpace(live.Scheduler.Timezone)
	if tz == "" {
		s.tzWarnFired.Store(true)
		return
	}
	if _, ok := live.Scheduler.Location(); ok {
		s.tzWarnFired.Store(true)
		return
	}
	s.logger.Warn("scheduler: configured timezone not recognised; falling back to Local",
		"scheduler.timezone", tz)
	s.tzWarnFired.Store(true)
}

// runScheduler is the scheduler goroutine's main loop. It exits when
// s.ctx cancels. The first tick fires immediately after launch so the
// scheduler claims ownership of the active profile without waiting a
// full interval — otherwise a restart during a scheduled window would
// leave the previous session's ActiveProfile in place for up to 60s.
// The interval is re-read each tick via schedulerInterval so tests
// that lower the cadence after New() see it take effect within one
// current-period wait — avoiding a race on a captured local.
func (s *Server) runScheduler() {
	s.scheduleTZWarnOnce()
	s.scheduleTick()
	for {
		interval := s.schedulerInterval()
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(interval):
			s.scheduleTick()
		case <-s.schedWake:
			// SetSchedulerInterval fired; loop around to re-read the
			// new cadence immediately. No tick here — the caller is
			// the one supposed to seed state, and a spurious tick
			// could re-clear a freshly-set manual override.
		}
	}
}

// scheduleTick runs one pass of the scheduler: compute winner,
// maybe-apply. Broken out from runScheduler so tests can drive ticks
// deterministically without reaching into the goroutine.
//
// Holds schedTickMu for the full load-compute-store sequence so a
// second concurrent tick (test goroutine + production goroutine
// overlap, the issue #812 race) cannot interleave its winner-
// computation with another tick's apply. Production has only one
// scheduler goroutine so contention is zero; the mutex is defence
// against any caller that drives a tick from a second goroutine.
func (s *Server) scheduleTick() {
	s.schedTickMu.Lock()
	defer s.schedTickMu.Unlock()
	now := s.now()
	live := s.cfg.Load()
	scheds := parsedSchedules(live, s.logger)
	// The scheduler's transition tracking is keyed off the full
	// active-profile computation — including the default fallback —
	// so an end-of-window transition from a scheduled profile back to
	// the fallback counts as a transition that clears manual override.
	winner := computeActiveProfile(live, scheds, now)
	suppressed := s.schedState.observe(winner)
	if suppressed {
		return
	}
	if winner == "" || winner == live.ActiveProfile {
		return
	}
	if s.IsPanicked("") {
		// Panic pins every fan to max; a profile swap would undo it.
		return
	}
	if _, err := s.applyProfile(winner); err != nil {
		s.logger.Error("schedule: apply profile failed", "profile", winner, "err", err)
		return
	}
	s.logger.Info("schedule: switched profile", "profile", winner)
}

// schedulerTZString returns the operator-readable timezone label the
// scheduler is currently evaluating against. Empty / "local" config
// resolves to "Local" (capitalised so it reads as a name); "utc" to
// "UTC"; an explicit IANA name (e.g. "Australia/Sydney") is echoed
// back. If LoadLocation failed (warned on first tick) the string is
// "Local (fallback from <bad-value>)" so the API consumer can show
// the operator that ventd ignored the config setting.
func schedulerTZString(cfg *config.Config) string {
	if cfg == nil {
		return "Local"
	}
	tz := strings.TrimSpace(cfg.Scheduler.Timezone)
	switch strings.ToLower(tz) {
	case "":
		return "Local"
	case "local":
		return "Local"
	case "utc":
		return "UTC"
	default:
		if _, ok := cfg.Scheduler.Location(); ok {
			return tz
		}
		return "Local (fallback from " + tz + ")"
	}
}

// scheduleStatus is the JSON body returned by GET /api/schedule/status.
// NextTransition is a pointer so the field serialises as null when the
// schedule is stable (no transition within 24h).
//
// Timezone is the resolved IANA name (or "Local" / "UTC") the
// scheduler is evaluating schedule strings against (#624). UI surfaces
// this next to the active profile so an operator at a glance knows
// whether a 22:00 schedule will fire at their wall clock's 22:00 or
// at 22:00 UTC.
type scheduleStatus struct {
	ActiveProfile  string     `json:"active_profile"`
	Source         string     `json:"source"` // "schedule" | "manual"
	Timezone       string     `json:"timezone"`
	NextTransition *time.Time `json:"next_transition,omitempty"`
	NextProfile    string     `json:"next_profile,omitempty"`
}

// handleScheduleStatus GET /api/schedule/status.
//
// Source is "manual" when a manual override is currently suppressing
// scheduled switches, "schedule" otherwise. The active_profile always
// reflects the live config (what the controllers are actually running)
// rather than the theoretical scheduled winner — during an override
// those can differ, which is the whole point of the override.
func (s *Server) handleScheduleStatus(w http.ResponseWriter, r *http.Request) {
	live := s.cfg.Load()
	scheds := parsedSchedules(live, s.logger)
	now := s.now()
	active := computeActiveProfile(live, scheds, now)
	_, override := s.schedState.snapshot()
	source := "schedule"
	if override {
		source = "manual"
	}
	resp := scheduleStatus{
		ActiveProfile: live.ActiveProfile,
		Source:        source,
		Timezone:      schedulerTZString(live),
	}
	if t, next, ok := nextTransition(live, scheds, active, now); ok {
		resp.NextTransition = &t
		resp.NextProfile = next
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, resp)
}

// handleProfileSchedule PUT /api/profile/schedule updates the schedule
// string on an existing profile.
//
// Body: {"name": "silent", "schedule": "22:00-07:00 *"}
//
// An empty schedule clears the field (the profile becomes manual-only).
// Malformed grammar returns 400 with the parser error so the UI can
// surface it inline without a separate validate round-trip.
//
// The change is persisted to disk — saving the whole config is the
// daemon's only atomic-write primitive, and a schedule edit without
// persistence would evaporate on restart.
func (s *Server) handleProfileSchedule(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r, 4<<10)
	var req struct {
		Name     string `json:"name"`
		Schedule string `json:"schedule"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isMaxBytesErr(err) {
			s.writeJSONError(w, http.StatusRequestEntityTooLarge, "request too large")
			return
		}
		s.writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Name == "" {
		s.writeJSONError(w, http.StatusBadRequest, "name required")
		return
	}
	if req.Schedule != "" {
		if _, err := config.ParseSchedule(req.Schedule); err != nil {
			s.writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	live := s.cfg.Load()
	p, ok := live.Profiles[req.Name]
	if !ok {
		s.writeJSONError(w, http.StatusBadRequest, "unknown profile: "+req.Name)
		return
	}

	next := *live
	next.Profiles = make(map[string]config.Profile, len(live.Profiles))
	for k, v := range live.Profiles {
		next.Profiles[k] = v
	}
	p.Schedule = req.Schedule
	next.Profiles[req.Name] = p

	validated, err := config.Save(&next, s.configPath)
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.cfg.Store(validated)
	s.writeJSON(r, w, map[string]string{"status": "ok"})
}
