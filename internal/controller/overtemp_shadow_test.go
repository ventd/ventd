package controller

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/watchdog"
)

// TestTick_OvertempFailsafeObservedButSuppressedInShadowMode pins the shadow-
// mode contract for the over-temperature failsafe: shadow mode lets an operator
// evaluate ventd's decisions WITHOUT ceding control, so a thermal emergency must
// still ENGAGE the failsafe and be surfaced (the recent-decisions feed shows
// ventd WOULD force full speed) — but NO PWM is written to the hardware, the
// operator's existing controller stays in charge. A regression that short-
// circuited shadow mode before the failsafe ran would hide a thermal emergency
// from someone evaluating ventd; one that wrote the byte would violate the
// shadow contract.
func TestTick_OvertempFailsafeObservedButSuppressedInShadowMode(t *testing.T) {
	ff := newFakeFan(t)
	chipDir := filepath.Dir(ff.tempPath)
	writeTempAttr(t, chipDir, "temp1_crit", "90000") // engage 94 °C

	cfg := makeLinearCurveCfg(ff, "cpu", "cpu_curve", 40, 200)
	cfg.Apply.Shadow = true // evaluate-only: every PWM write is suppressed

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	wd := watchdog.New(logger)
	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(cfg)
	c := New("cpu", "cpu_curve", ff.pwmPath, "hwmon", cfgPtr, wd, &stubCal{}, logger)
	c.tjmaxFn = func() float64 { return 0 }

	// Mark the hardware with a sentinel PWM the controller must NOT overwrite.
	if err := os.WriteFile(ff.pwmPath, []byte("111\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Spike above engage, then backdate the dwell so the failsafe engages.
	writeTempAttr(t, chipDir, "temp1_input", "95000")
	c.tick()
	c.emergency["cpu"].overSince = time.Now().Add(-emergencyDebounce - time.Second)
	c.tick()

	log := buf.String()
	// The failsafe engaged (the emergency is observed, not hidden by shadow).
	if !c.emergency["cpu"].engaged {
		t.Error("over-temp failsafe did not engage in shadow mode — a thermal emergency must still be detected")
	}
	if !strings.Contains(log, "OVER-TEMPERATURE FAILSAFE engaged") {
		t.Errorf("failsafe engage not logged in shadow mode; log:\n%s", log)
	}
	// The would-write surfaced as 255 (full speed) via the shadow feed.
	if !strings.Contains(log, "shadow_write_suppressed") || !strings.Contains(log, "would_write=255") {
		t.Errorf("shadow feed must show would_write=255 for the failsafe; log:\n%s", log)
	}
	// But the hardware byte is untouched — shadow mode wrote nothing.
	if got := readPWMByte(t, ff.pwmPath); got != 111 {
		t.Errorf("PWM byte = %d, want 111 (shadow mode must not write — even the failsafe is suppressed)", got)
	}
}
