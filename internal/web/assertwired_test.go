package web

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// TestAssertWired covers the fail-fast wiring guard added with the Deps
// refactor (R3 / P2). It is the safety net that turns a forgotten Set* in the
// daemon main from a silently-degraded endpoint into an immediate boot error.
func TestAssertWired(t *testing.T) {
	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(config.Empty())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// RequireWiring=false: assertWired is a no-op even with nothing wired,
	// so the existing test harnesses (which skip every Set*) keep working.
	lax := New(Deps{Ctx: ctx, Cfg: &cfgPtr, Logger: logger})
	if err := lax.assertWired(); err != nil {
		t.Fatalf("RequireWiring=false must never error, got %v", err)
	}

	// RequireWiring=true with nothing wired: every production-required dep is
	// named so the operator/maintainer sees exactly what was dropped.
	strict := New(Deps{Ctx: ctx, Cfg: &cfgPtr, Logger: logger, RequireWiring: true})
	err := strict.assertWired()
	if err == nil {
		t.Fatal("RequireWiring=true with no wiring must error")
	}
	for _, want := range []string{
		"ReadyState", "KVWiper", "CalibrationWiper",
		"FactoryResetHook", "CoolingCapacityFn", "VersionInfo",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q, got: %v", want, err)
		}
	}

	// The conditional smart-mode / opportunistic setters are deliberately
	// excluded — wire only the unconditional six and the guard passes.
	strict.SetReadyState(NewReadyState())
	strict.SetKVWiper(func() error { return nil })
	strict.SetCalibrationWiper(func() error { return nil })
	strict.SetFactoryResetHook(func(context.Context) error { return nil })
	strict.SetCoolingCapacityFn(func() CoolingStatus { return CoolingStatus{} })
	strict.SetVersionInfo(VersionInfo{Version: "test"})
	if err := strict.assertWired(); err != nil {
		t.Fatalf("fully wired server must pass assertWired, got %v", err)
	}
}
