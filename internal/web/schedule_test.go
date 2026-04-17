package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
)

// clockAt builds a fixed time in the local zone so tests don't carry
// an implicit "pass if the CI runner is in UTC" assumption.
func clockAt(t *testing.T, iso string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02T15:04", iso, time.Local)
	if err != nil {
		t.Fatalf("parse %q: %v", iso, err)
	}
	return parsed
}

func newScheduledTestServer(t *testing.T, profiles map[string]config.Profile, active string) *Server {
	t.Helper()
	srv := newVersionTestServer(t)
	live := *srv.cfg.Load()
	live.Profiles = profiles
	live.ActiveProfile = active
	// Give the scheduler something to bind; Controls are deep-copied
	// during an apply so the existing swap semantics are exercised
	// even though these tests don't read fan bindings back.
	live.Controls = []config.Control{{Fan: "cpu_fan", Curve: "cpu_linear_silent"}}
	srv.cfg.Store(&live)
	return srv
}

func TestScheduler_ComputeWinner_SpecificityTiebreak(t *testing.T) {
	parse := func(s string) *config.Schedule {
		t.Helper()
		sch, err := config.ParseSchedule(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return sch
	}
	// Thursday 2026-01-15 at 10:30 local — all three candidates match,
	// so the tiebreak must pick the most specific one.
	now := clockAt(t, "2026-01-15T10:30")
	scheds := map[string]*config.Schedule{
		"always":   parse("00:00-23:59 *"),
		"weekdays": parse("09:00-18:00 mon-fri"),
		"narrow":   parse("10:00-11:00 thu"),
	}
	if got := computeWinner(scheds, now); got != "narrow" {
		t.Errorf("winner = %q want narrow (tiebreak: fewer days → shorter duration)", got)
	}
}

func TestScheduler_ComputeWinner_NoMatch(t *testing.T) {
	sch, err := config.ParseSchedule("09:00-18:00 mon-fri")
	if err != nil {
		t.Fatal(err)
	}
	now := clockAt(t, "2026-01-17T12:00") // Saturday
	scheds := map[string]*config.Schedule{"weekdays": sch}
	if got := computeWinner(scheds, now); got != "" {
		t.Errorf("winner = %q want empty", got)
	}
}

func TestScheduler_ComputeWinner_LexicalFinalTiebreak(t *testing.T) {
	parse := func(s string) *config.Schedule {
		sch, _ := config.ParseSchedule(s)
		return sch
	}
	// Two identical schedules — only lexical name differs.
	scheds := map[string]*config.Schedule{
		"zeta":  parse("08:00-17:00 mon-fri"),
		"alpha": parse("08:00-17:00 mon-fri"),
	}
	now := clockAt(t, "2026-01-15T12:00")
	if got := computeWinner(scheds, now); got != "alpha" {
		t.Errorf("winner = %q want alpha (lexical tiebreak)", got)
	}
}

func TestScheduler_NextTransition_FindsBoundary(t *testing.T) {
	parse := func(s string) *config.Schedule {
		sch, _ := config.ParseSchedule(s)
		return sch
	}
	scheds := map[string]*config.Schedule{
		"silent":   parse("22:00-07:00 *"),
		"balanced": parse("07:00-22:00 *"),
	}
	cfg := &config.Config{Profiles: map[string]config.Profile{
		"silent":   {Schedule: "22:00-07:00 *"},
		"balanced": {Schedule: "07:00-22:00 *"},
	}}
	// Currently 21:30 on a Monday → silent takes over at 22:00.
	now := clockAt(t, "2026-01-12T21:30")
	at, next, ok := nextTransition(cfg, scheds, computeActiveProfile(cfg, scheds, now), now)
	if !ok {
		t.Fatal("expected a transition within 24h")
	}
	if next != "silent" {
		t.Errorf("next = %q want silent", next)
	}
	if at.Hour() != 22 || at.Minute() != 0 {
		t.Errorf("transition at %s, want 22:00", at.Format("15:04"))
	}
}

func TestScheduler_NextTransition_StableReturnsFalse(t *testing.T) {
	// No profiles at all → active is always "", no transition in any
	// forward scan. This is the "nothing to do" path the API must
	// report as a null next_transition.
	scheds := map[string]*config.Schedule{}
	cfg := &config.Config{}
	now := clockAt(t, "2026-01-12T10:00")
	if _, _, ok := nextTransition(cfg, scheds, computeActiveProfile(cfg, scheds, now), now); ok {
		t.Errorf("expected no transition when no profiles are configured")
	}
}

func TestScheduler_FallbackProfile_WinsWhenNoScheduleMatches(t *testing.T) {
	// "silent" covers only 22:00-07:00; at 10:00 no schedule matches,
	// so the unscheduled "balanced" profile wins as the default
	// fallback.
	srv := newScheduledTestServer(t, map[string]config.Profile{
		"silent":   {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}, Schedule: "22:00-07:00 *"},
		"balanced": {Bindings: map[string]string{"cpu_fan": "cpu_linear_balanced"}},
	}, "silent")

	srv.SetNowFn(func() time.Time { return clockAt(t, "2026-01-12T10:00") })
	srv.scheduleTick()
	if got := srv.cfg.Load().ActiveProfile; got != "balanced" {
		t.Errorf("active = %q want balanced (fallback)", got)
	}
}

func TestScheduler_FallbackProfile_EndOfWindowTransitionsBack(t *testing.T) {
	// At 22:00 silent wins; at 07:00 it ends and the unscheduled
	// fallback takes over. Verifies the fallback is part of the
	// transition-tracking path so end-of-window boundaries clear
	// manual overrides correctly.
	srv := newScheduledTestServer(t, map[string]config.Profile{
		"silent":   {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}, Schedule: "22:00-07:00 *"},
		"balanced": {Bindings: map[string]string{"cpu_fan": "cpu_linear_balanced"}},
	}, "balanced")

	srv.SetNowFn(func() time.Time { return clockAt(t, "2026-01-12T22:00") })
	srv.scheduleTick()
	if got := srv.cfg.Load().ActiveProfile; got != "silent" {
		t.Fatalf("22:00: active = %q want silent", got)
	}

	srv.SetNowFn(func() time.Time { return clockAt(t, "2026-01-13T07:00") })
	srv.scheduleTick()
	if got := srv.cfg.Load().ActiveProfile; got != "balanced" {
		t.Errorf("07:00 next day: active = %q want balanced (fallback)", got)
	}
}

func TestScheduler_TickSwitchesProfileAtBoundary(t *testing.T) {
	srv := newScheduledTestServer(t, map[string]config.Profile{
		"silent":  {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}, Schedule: "22:00-07:00 *"},
		"daytime": {Bindings: map[string]string{"cpu_fan": "cpu_linear_daytime"}, Schedule: "07:00-22:00 *"},
	}, "daytime")

	// 21:59 on Monday — still "daytime".
	srv.SetNowFn(func() time.Time { return clockAt(t, "2026-01-12T21:59") })
	srv.scheduleTick()
	if got := srv.cfg.Load().ActiveProfile; got != "daytime" {
		t.Fatalf("before boundary: active = %q want daytime", got)
	}

	// 22:00 on Monday — scheduler must switch to "silent".
	srv.SetNowFn(func() time.Time { return clockAt(t, "2026-01-12T22:00") })
	srv.scheduleTick()
	if got := srv.cfg.Load().ActiveProfile; got != "silent" {
		t.Fatalf("after boundary: active = %q want silent", got)
	}
	if got := srv.cfg.Load().Controls[0].Curve; got != "cpu_linear_silent" {
		t.Errorf("binding rewrite: curve = %q want cpu_linear_silent", got)
	}
}

func TestScheduler_ManualOverrideStaysUntilTransition(t *testing.T) {
	srv := newScheduledTestServer(t, map[string]config.Profile{
		"silent":  {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}, Schedule: "22:00-07:00 *"},
		"daytime": {Bindings: map[string]string{"cpu_fan": "cpu_linear_daytime"}, Schedule: "07:00-22:00 *"},
		"perf":    {Bindings: map[string]string{"cpu_fan": "cpu_linear_perf"}},
	}, "daytime")

	// At noon Monday, daytime is the scheduled winner. Seed lastScheduled.
	srv.SetNowFn(func() time.Time { return clockAt(t, "2026-01-12T12:00") })
	srv.scheduleTick()

	// Operator manually switches to "perf".
	body := bytes.NewBufferString(`{"name": "perf"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/profile/active", body)
	w := httptest.NewRecorder()
	srv.handleProfileActive(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("manual switch: status=%d", w.Code)
	}
	if got := srv.cfg.Load().ActiveProfile; got != "perf" {
		t.Fatalf("after manual switch: active = %q want perf", got)
	}

	// 13:00 Monday — schedule winner is still "daytime", override wins.
	srv.SetNowFn(func() time.Time { return clockAt(t, "2026-01-12T13:00") })
	srv.scheduleTick()
	if got := srv.cfg.Load().ActiveProfile; got != "perf" {
		t.Errorf("mid-window tick: active = %q want perf (override must hold)", got)
	}

	// 22:00 Monday — schedule winner transitions to "silent". Override clears.
	srv.SetNowFn(func() time.Time { return clockAt(t, "2026-01-12T22:00") })
	srv.scheduleTick()
	if got := srv.cfg.Load().ActiveProfile; got != "silent" {
		t.Errorf("post-transition: active = %q want silent (override must clear on boundary)", got)
	}
}

func TestScheduler_SkipsDuringPanic(t *testing.T) {
	srv := newScheduledTestServer(t, map[string]config.Profile{
		"silent":  {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}, Schedule: "22:00-07:00 *"},
		"daytime": {Bindings: map[string]string{"cpu_fan": "cpu_linear_daytime"}, Schedule: "07:00-22:00 *"},
	}, "daytime")

	// Fake-activate panic by flipping the field under lock.
	srv.panic.mu.Lock()
	srv.panic.active = true
	srv.panic.mu.Unlock()

	// 22:00 would normally switch to silent.
	srv.SetNowFn(func() time.Time { return clockAt(t, "2026-01-12T22:00") })
	srv.scheduleTick()
	if got := srv.cfg.Load().ActiveProfile; got != "daytime" {
		t.Errorf("active = %q want daytime (panic must suppress scheduler)", got)
	}
}

func TestScheduler_NoScheduledProfileLeavesActiveAlone(t *testing.T) {
	srv := newScheduledTestServer(t, map[string]config.Profile{
		"silent":   {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}, Schedule: "22:00-07:00 *"},
		"daytime":  {Bindings: map[string]string{"cpu_fan": "cpu_linear_daytime"}, Schedule: "07:00-22:00 *"},
		"fallback": {Bindings: map[string]string{"cpu_fan": "cpu_linear_fallback"}},
	}, "fallback")

	// There IS coverage 24/7 from silent+daytime; however, test that
	// when the clock sits exactly between transitions and one matches,
	// we don't toggle off the scheduled winner.
	srv.SetNowFn(func() time.Time { return clockAt(t, "2026-01-12T10:00") })
	srv.scheduleTick()
	if got := srv.cfg.Load().ActiveProfile; got != "daytime" {
		t.Errorf("active = %q want daytime", got)
	}
}

func TestScheduler_MalformedScheduleIsIgnored(t *testing.T) {
	// parsedSchedules must tolerate a malformed live-config entry
	// (only reachable if someone mutated the in-memory config outside
	// Save(), since validate would reject it on disk).
	srv := newVersionTestServer(t)
	live := *srv.cfg.Load()
	live.Profiles = map[string]config.Profile{
		"broken": {Schedule: "not a schedule"},
		"ok":     {Schedule: "09:00-10:00 *"},
	}
	srv.cfg.Store(&live)
	scheds := parsedSchedules(srv.cfg.Load(), srv.logger)
	if _, ok := scheds["broken"]; ok {
		t.Errorf("broken schedule should be skipped")
	}
	if _, ok := scheds["ok"]; !ok {
		t.Errorf("valid schedule should be kept")
	}
}

func TestHandleScheduleStatus_ReportsSourceAndNext(t *testing.T) {
	srv := newScheduledTestServer(t, map[string]config.Profile{
		"silent":  {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}, Schedule: "22:00-07:00 *"},
		"daytime": {Bindings: map[string]string{"cpu_fan": "cpu_linear_daytime"}, Schedule: "07:00-22:00 *"},
	}, "daytime")

	srv.SetNowFn(func() time.Time { return clockAt(t, "2026-01-12T21:00") })
	req := httptest.NewRequest(http.MethodGet, "/api/schedule/status", nil)
	w := httptest.NewRecorder()
	srv.handleScheduleStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var body scheduleStatus
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.ActiveProfile != "daytime" {
		t.Errorf("ActiveProfile = %q want daytime", body.ActiveProfile)
	}
	if body.Source != "schedule" {
		t.Errorf("Source = %q want schedule", body.Source)
	}
	if body.NextProfile != "silent" {
		t.Errorf("NextProfile = %q want silent", body.NextProfile)
	}
	if body.NextTransition == nil || body.NextTransition.Hour() != 22 {
		t.Errorf("NextTransition = %v; want 22:00", body.NextTransition)
	}
}

func TestHandleScheduleStatus_SourceManualAfterOverride(t *testing.T) {
	srv := newScheduledTestServer(t, map[string]config.Profile{
		"silent":  {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}, Schedule: "22:00-07:00 *"},
		"daytime": {Bindings: map[string]string{"cpu_fan": "cpu_linear_daytime"}, Schedule: "07:00-22:00 *"},
		"perf":    {Bindings: map[string]string{"cpu_fan": "cpu_linear_perf"}},
	}, "daytime")
	srv.SetNowFn(func() time.Time { return clockAt(t, "2026-01-12T12:00") })
	srv.scheduleTick()

	body := bytes.NewBufferString(`{"name": "perf"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/profile/active", body)
	srv.handleProfileActive(httptest.NewRecorder(), req)

	req = httptest.NewRequest(http.MethodGet, "/api/schedule/status", nil)
	w := httptest.NewRecorder()
	srv.handleScheduleStatus(w, req)
	var statusBody scheduleStatus
	_ = json.Unmarshal(w.Body.Bytes(), &statusBody)
	if statusBody.Source != "manual" {
		t.Errorf("Source = %q want manual", statusBody.Source)
	}
}

// newProfileScheduleTestServer builds a server whose config passes the
// validator — required because handleProfileSchedule runs the config
// through config.Save, which re-validates. Keeps Controls empty so we
// don't have to wire a matching Fan/Curve graph into every test.
func newProfileScheduleTestServer(t *testing.T, profiles map[string]config.Profile) *Server {
	t.Helper()
	srv := newVersionTestServer(t)
	live := *srv.cfg.Load()
	live.Profiles = profiles
	live.Controls = nil
	srv.cfg.Store(&live)
	srv.configPath = t.TempDir() + "/cfg.yaml"
	return srv
}

func TestHandleProfileSchedule_UpdatesAndPersists(t *testing.T) {
	srv := newProfileScheduleTestServer(t, map[string]config.Profile{
		"silent": {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}},
	})

	body := bytes.NewBufferString(`{"name":"silent","schedule":"22:00-07:00 *"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/profile/schedule", body)
	w := httptest.NewRecorder()
	srv.handleProfileSchedule(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", w.Code, w.Body)
	}
	if got := srv.cfg.Load().Profiles["silent"].Schedule; got != "22:00-07:00 *" {
		t.Errorf("schedule = %q want 22:00-07:00 *", got)
	}
	if _, err := config.Load(srv.configPath); err != nil {
		t.Errorf("reload saved config: %v", err)
	}
}

func TestHandleProfileSchedule_RejectsMalformedGrammar(t *testing.T) {
	srv := newProfileScheduleTestServer(t, map[string]config.Profile{
		"silent": {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}},
	})

	body := bytes.NewBufferString(`{"name":"silent","schedule":"25:00-99:99 nope"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/profile/schedule", body)
	w := httptest.NewRecorder()
	srv.handleProfileSchedule(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d want 400", w.Code)
	}
}

func TestHandleProfileSchedule_EmptyClearsSchedule(t *testing.T) {
	srv := newProfileScheduleTestServer(t, map[string]config.Profile{
		"silent": {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}, Schedule: "22:00-07:00 *"},
	})

	body := bytes.NewBufferString(`{"name":"silent","schedule":""}`)
	req := httptest.NewRequest(http.MethodPut, "/api/profile/schedule", body)
	w := httptest.NewRecorder()
	srv.handleProfileSchedule(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", w.Code, w.Body)
	}
	if got := srv.cfg.Load().Profiles["silent"].Schedule; got != "" {
		t.Errorf("schedule = %q want empty", got)
	}
}

func TestConfig_ProfileScheduleRoundTrip(t *testing.T) {
	cfg := config.Empty()
	cfg.Profiles = map[string]config.Profile{
		"silent": {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}, Schedule: "22:00-07:00 *"},
	}
	path := t.TempDir() + "/cfg.yaml"
	if _, err := config.Save(cfg, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := loaded.Profiles["silent"].Schedule; got != "22:00-07:00 *" {
		t.Errorf("schedule = %q after round-trip want 22:00-07:00 *", got)
	}
}

func TestConfig_MalformedScheduleFailsValidate(t *testing.T) {
	cfg := config.Empty()
	cfg.Profiles = map[string]config.Profile{
		"broken": {Bindings: map[string]string{"cpu_fan": "cpu_linear_silent"}, Schedule: "not a schedule"},
	}
	path := t.TempDir() + "/cfg.yaml"
	if _, err := config.Save(cfg, path); err == nil {
		t.Errorf("Save accepted malformed schedule; want validation error")
	}
}
