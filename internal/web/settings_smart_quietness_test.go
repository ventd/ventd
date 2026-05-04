package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// TestSettingsSmartQuietness_PresetRoundTripsViaConfigPUT verifies the
// settings.html → /api/v1/config PUT path the operator uses for the
// new Smart-mode quietness preset (#789, v0.6 prereq #3). PUT the full
// config with smart.preset = "silent" and smart.dba_target = 28; expect
// the validated config back from the handler with those fields preserved.
//
// Backstop for the hand-wired settings.js patch path — if the server
// ever drops the smart sub-object on round-trip (#483-style omission)
// the operator's preset choice would silently fail to persist.
func TestSettingsSmartQuietness_PresetRoundTripsViaConfigPUT(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	live := srv.cfg.Load()
	if live == nil {
		t.Fatal("harness has no live config loaded")
	}
	dba := 28.0
	next := *live
	next.Smart = config.SmartConfig{Preset: "silent", DBATarget: &dba}

	body, err := json.Marshal(next)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PUT /config: status=%d body=%q", w.Code, w.Body.String())
	}
	var got config.Config
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Smart.Preset != "silent" {
		t.Errorf("smart.preset = %q, want %q", got.Smart.Preset, "silent")
	}
	if got.Smart.DBATarget == nil {
		t.Fatal("smart.dba_target was dropped on round-trip")
	}
	if *got.Smart.DBATarget != 28 {
		t.Errorf("smart.dba_target = %v, want 28", *got.Smart.DBATarget)
	}

	// And the live cache should reflect the validated config the handler
	// stored — the dashboard's smart-mode pill reads s.cfg.Load() to
	// resolve the active preset, so the cache and the response must agree.
	cached := srv.cfg.Load()
	if cached == nil || cached.Smart.Preset != "silent" {
		t.Errorf("cached config didn't pick up new preset: %+v", cached.Smart)
	}
}

// TestSettingsSmartQuietness_UnknownPresetIsAcceptedAndResolvesToBalanced
// pins the documented behaviour from RULE-CTRL-PRESET-02: unknown
// preset strings are NON-FATAL — they persist as-is in the YAML so
// the operator can see what they typed, but SmartPreset() (the
// canonical resolver consumed by the controller and the dashboard
// pill) normalises to "balanced". This is the same forgiveness
// pattern as the experimental warn-once schema.
//
// The settings UI's preset segment buttons only ever PUT one of the
// three canonical names so this path is mostly defensive — but a
// hand-edited config.yaml with a typo shouldn't crash the daemon.
func TestSettingsSmartQuietness_UnknownPresetIsAcceptedAndResolvesToBalanced(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	live := srv.cfg.Load()
	next := *live
	next.Smart = config.SmartConfig{Preset: "ultra-quiet"}
	body, _ := json.Marshal(next)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PUT unknown preset: status=%d body=%q (want 200)", w.Code, w.Body.String())
	}
	cached := srv.cfg.Load()
	if cached == nil {
		t.Fatal("nil cached config")
	}
	if cached.Smart.Preset != "ultra-quiet" {
		t.Errorf("raw preset string = %q, want %q (verbatim persistence)", cached.Smart.Preset, "ultra-quiet")
	}
	resolved, ok := cached.Smart.SmartPreset()
	if ok {
		t.Errorf("SmartPreset() ok = true; expected false for unknown preset")
	}
	if resolved != "balanced" {
		t.Errorf("SmartPreset() resolved = %q, want %q", resolved, "balanced")
	}
}

// TestSettingsSmartQuietness_OutOfRangeDBATargetRejected verifies the
// [10, 80] dBA bounds check from RULE-CTRL-PRESET-03 fires through the
// HTTP path. A 5 dBA value is below the room-ambient floor (impossible
// to honour); silently accepting it would produce a controller that
// refuses every ramp regardless of the preset.
func TestSettingsSmartQuietness_OutOfRangeDBATargetRejected(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	live := srv.cfg.Load()
	next := *live
	tooLow := 5.0
	next.Smart = config.SmartConfig{Preset: "balanced", DBATarget: &tooLow}
	body, _ := json.Marshal(next)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT 5 dBA: status=%d body=%q (want 400)", w.Code, w.Body.String())
	}
}
