package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/hal"
	hwhal "github.com/ventd/ventd/internal/hal/hwmon"
	"github.com/ventd/ventd/internal/hwmon"
)

// TestE2E_BackendDrivesLiveSim closes the loop end-to-end through the real
// production code path: the hwmon HAL backend enumerates the synthetic tree
// (via VENTD_HWMON_ROOT), writes a duty byte (manual mode + pwm), the live sim
// model reads that back and recomputes RPM, and the backend reads the new RPM.
// This is the proof that a daemon pointed at hwmonsim controls fake fans for
// real — no hardware, no daemon process, no ports.
func TestE2E_BackendDrivesLiveSim(t *testing.T) {
	root := t.TempDir()
	dev := filepath.Join(root, "hwmon0")
	cfg := baseCfg()
	cfg.fans = 1
	cfg.tick = 5 * time.Millisecond
	if err := materialise(dev, cfg.chip, cfg.fans, cfg.temps); err != nil {
		t.Fatal(err)
	}

	stop := make(chan os.Signal, 1)
	done := make(chan struct{})
	devices := []device{{dir: dev, chip: cfg.chip, fans: cfg.fans, temps: cfg.temps}}
	go func() { run(devices, cfg, stop); close(done) }()
	t.Cleanup(func() {
		stop <- os.Interrupt
		<-done
	})

	t.Setenv(hwmon.RootOverrideEnv, root)
	be := hwhal.NewBackend(nil)
	chans, err := be.Enumerate(context.Background())
	if err != nil || len(chans) != 1 {
		t.Fatalf("Enumerate: err=%v n=%d", err, len(chans))
	}
	ch := chans[0]

	// Full duty → RPM climbs toward max.
	if err := be.Write(ch, 255); err != nil {
		t.Fatalf("Write(255): %v", err)
	}
	if rpm := waitRPM(t, be, ch, func(r int) bool { return r >= cfg.maxRPM-50 }); rpm < cfg.maxRPM-50 {
		t.Fatalf("after Write(255), RPM=%d, want near %d", rpm, cfg.maxRPM)
	}

	// Below the stall threshold → fan stops.
	if err := be.Write(ch, 10); err != nil {
		t.Fatalf("Write(10): %v", err)
	}
	if rpm := waitRPM(t, be, ch, func(r int) bool { return r == 0 }); rpm != 0 {
		t.Fatalf("after Write(10) (< stopPWM=%d), RPM=%d, want 0 (stalled)", cfg.stopPWM, rpm)
	}
}

func waitRPM(t *testing.T, be *hwhal.Backend, ch hal.Channel, ok func(int) bool) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last int
	for time.Now().Before(deadline) {
		r, err := be.Read(ch)
		if err == nil && r.OK {
			last = int(r.RPM)
			if ok(last) {
				return last
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return last
}
