package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/hal"
	hwhal "github.com/ventd/ventd/internal/hal/hwmon"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/probe"
)

// fakeClock collapses the polarity prober's multi-second settle waits to
// nothing: sleep() advances fake time without blocking, and now() reports it.
// The polarity scenario recomputes the fan's RPM synchronously on each pwm
// write (see TestScenario_PolarityClassification), so the prober never has to
// wait for a model to settle — the only thing the clock must do is let
// readRPMMean's window deadline elapse over a bounded number of iterations.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) sleep(d time.Duration) {
	// Advance fake time only — no real sleep. The polarity scenario drives the
	// fan model SYNCHRONOUSLY (RPM is recomputed on each pwm write, before the
	// prober reads the tach), so there is nothing to wait for. Advancing time
	// lets readRPMMean's window deadline be reached in a bounded number of
	// iterations. This makes the probe deterministic and race-free — the prior
	// real-sleep-against-a-background-ticker version flaked under -race on slow
	// runners when a tach read landed before the ticker recomputed RPM.
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// startSim materialises a one-device tree and runs the live model in the
// background, returning the device dir and a stop func.
func startSim(t *testing.T, cfg config) (dir string, stop func()) {
	t.Helper()
	root := t.TempDir()
	dev := filepath.Join(root, "hwmon0")
	if err := materialise(dev, cfg.chip, cfg.fans, cfg.temps); err != nil {
		t.Fatal(err)
	}
	sig := make(chan os.Signal, 1)
	done := make(chan struct{})
	devices := []device{{dir: dev, chip: cfg.chip, fans: cfg.fans, temps: cfg.temps}}
	go func() { run(devices, cfg, sig); close(done) }()
	return dev, func() {
		sig <- os.Interrupt
		<-done
	}
}

// TestScenario_PolarityClassification drives the REAL polarity prober
// (internal/polarity.HwmonProber) against each hwmonsim fault model and asserts
// the verdict. This is fake-HIL: no hardware, but the production classification
// code reads tach and writes pwm through the live sim exactly as it would on a
// real board.
func TestScenario_PolarityClassification(t *testing.T) {
	cases := []struct {
		model       string
		wantClass   string
		wantPhantom string // only checked when non-empty
	}{
		{model: "spinup", wantClass: polarity.PolarityNormal},
		{model: "linear", wantClass: polarity.PolarityNormal},
		{model: "inverted", wantClass: polarity.PolarityInverted},
		{model: "phantom", wantClass: polarity.PolarityPhantom, wantPhantom: polarity.PhantomReasonNoResponse},
		{model: "stuck", wantClass: polarity.PolarityPhantom, wantPhantom: polarity.PhantomReasonNoResponse},
		// sentinel: the fan IS controllable and spinning, but its tach reports
		// the 65535 driver sentinel. A correct probe must NOT read 65535 as a
		// real RPM. Documented expectation: phantom (unusable tach), never
		// normal/inverted on garbage.
		{model: "sentinel", wantClass: polarity.PolarityPhantom},
		// sentinelhigh: real RPM at low duty, 65535 sentinel at high duty. A
		// prober that reads raw tach computes delta = 65535 - realLow and
		// mislabels the channel "normal" with an implausible RPM. The correct
		// verdict treats the sentinel sample as no-reading → phantom.
		{model: "sentinelhigh", wantClass: polarity.PolarityPhantom, wantPhantom: polarity.PhantomReasonImplausibleTach},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.model, func(t *testing.T) {
			cfg := baseCfg()
			cfg.fans = 1
			cfg.model = tc.model
			// Materialise the tree, but drive the model SYNCHRONOUSLY rather
			// than from a background ticker: recompute fan1_input from the
			// current duty on every pwm write the prober makes. The prober's
			// next tach read therefore always observes the settled RPM for the
			// duty it just wrote — deterministic, with no real sleeps and no
			// goroutine to race under -race.
			root := t.TempDir()
			dev := filepath.Join(root, "hwmon0")
			if err := materialise(dev, cfg.chip, cfg.fans, cfg.temps); err != nil {
				t.Fatal(err)
			}
			pwmPath := filepath.Join(dev, "pwm1")
			tachPath := filepath.Join(dev, "fan1_input")
			var spinning bool
			recompute := func() {
				rpm := rpmFor(clampByte(readInt(pwmPath, 0)), 1, cfg, &spinning, 0)
				_ = writeVal(tachPath, strconv.Itoa(rpm))
			}
			recompute() // seed the tach from the materialised duty (0)

			clk := &fakeClock{t: time.Now()}
			pr := &polarity.HwmonProber{
				Clock: clk.sleep,
				Now:   clk.now,
				WriteFile: func(path string, data []byte, mode os.FileMode) error {
					if err := os.WriteFile(path, data, mode); err != nil {
						return err
					}
					if filepath.Base(path) == "pwm1" {
						recompute()
					}
					return nil
				},
			}
			ch := &probe.ControllableChannel{
				SourceID: "hwmon0",
				PWMPath:  pwmPath,
				TachPath: tachPath,
				Driver:   "nct6687",
			}
			res, err := pr.ProbeChannel(context.Background(), ch)
			if err != nil {
				t.Fatalf("ProbeChannel(%s): %v", tc.model, err)
			}
			t.Logf("model=%s → polarity=%s phantom_reason=%q delta=%.0f low=%.0f high=%.0f",
				tc.model, res.Polarity, res.PhantomReason, res.Delta, res.Baseline, res.Observed)
			if res.Polarity != tc.wantClass {
				t.Errorf("model=%s: polarity=%s, want %s (delta=%.0f low=%.0f high=%.0f)",
					tc.model, res.Polarity, tc.wantClass, res.Delta, res.Baseline, res.Observed)
			}
			if tc.wantPhantom != "" && res.PhantomReason != tc.wantPhantom {
				t.Errorf("model=%s: phantom_reason=%q, want %q", tc.model, res.PhantomReason, tc.wantPhantom)
			}
		})
	}
}

// TestScenario_BackendRuntimeReads drives the REAL runtime control path — the
// hal/hwmon backend's Enumerate / Write / Read — against each fault model via
// the live sim, asserting the runtime read contract: a driver sentinel is
// rejected (OK=false), a plausible reading is surfaced, and a fan that drops
// out reads zero. This is the production read path the control loop depends on,
// exercised against fake-HIL hardware faults.
func TestScenario_BackendRuntimeReads(t *testing.T) {
	// waitRead polls Read until pred holds or the deadline, returning the last
	// reading seen. Lets the live sim's ticker settle to the written duty.
	waitRead := func(t *testing.T, be *hwhal.Backend, ch hal.Channel, pred func(hal.Reading) bool) hal.Reading {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		var last hal.Reading
		for time.Now().Before(deadline) {
			r, err := be.Read(ch)
			if err == nil {
				last = r
				if pred(r) {
					return r
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		return last
	}

	t.Run("sentinel_read_rejected", func(t *testing.T) {
		cfg := baseCfg()
		cfg.fans = 1
		cfg.model = "sentinel"
		cfg.tick = 5 * time.Millisecond
		dev, stop := startSim(t, cfg)
		defer stop()
		t.Setenv(hwmon.RootOverrideEnv, filepath.Dir(dev))
		be := hwhal.NewBackend(nil)
		chans, err := be.Enumerate(context.Background())
		if err != nil || len(chans) != 1 {
			t.Fatalf("Enumerate: err=%v n=%d", err, len(chans))
		}
		ch := chans[0]
		if err := be.Write(ch, 200); err != nil { // spin it up → tach emits 65535
			t.Fatalf("Write: %v", err)
		}
		// The runtime read must NOT surface 65535 as a real RPM: OK=false.
		r := waitRead(t, be, ch, func(r hal.Reading) bool { return !r.OK })
		if r.OK {
			t.Errorf("sentinel: Read returned OK=true RPM=%d; runtime must reject the 0xFFFF sentinel", r.RPM)
		}
	})

	t.Run("stuck_reads_fixed_rpm", func(t *testing.T) {
		cfg := baseCfg()
		cfg.fans = 1
		cfg.model = "stuck"
		cfg.tick = 5 * time.Millisecond
		dev, stop := startSim(t, cfg)
		defer stop()
		t.Setenv(hwmon.RootOverrideEnv, filepath.Dir(dev))
		be := hwhal.NewBackend(nil)
		chans, _ := be.Enumerate(context.Background())
		ch := chans[0]
		_ = be.Write(ch, 200)
		r := waitRead(t, be, ch, func(r hal.Reading) bool { return r.OK && r.RPM > 0 })
		// A stuck tach reads a fixed, plausible RPM that doesn't track duty —
		// the runtime should surface it (OK=true), not reject it as a sentinel.
		if !r.OK || r.RPM == 0 {
			t.Errorf("stuck: Read OK=%v RPM=%d; want a plausible fixed RPM surfaced", r.OK, r.RPM)
		}
	})

	t.Run("noisy_reads_stay_plausible", func(t *testing.T) {
		cfg := baseCfg()
		cfg.fans = 1
		cfg.model = "noisy"
		cfg.tick = 5 * time.Millisecond
		dev, stop := startSim(t, cfg)
		defer stop()
		t.Setenv(hwmon.RootOverrideEnv, filepath.Dir(dev))
		be := hwhal.NewBackend(nil)
		chans, _ := be.Enumerate(context.Background())
		ch := chans[0]
		_ = be.Write(ch, 200)
		// Sample repeatedly; every surfaced reading must be plausible (never a
		// sentinel, never an absurd RPM) despite the jitter.
		deadline := time.Now().Add(1 * time.Second)
		seen := 0
		for time.Now().Before(deadline) {
			r, err := be.Read(ch)
			if err == nil && r.OK {
				seen++
				if r.RPM > 25000 {
					t.Fatalf("noisy: surfaced implausible RPM=%d", r.RPM)
				}
			}
			time.Sleep(15 * time.Millisecond)
		}
		if seen == 0 {
			t.Errorf("noisy: no plausible readings surfaced at all")
		}
	})

	t.Run("disconnect_reads_zero_after_fault", func(t *testing.T) {
		cfg := baseCfg()
		cfg.fans = 1
		cfg.model = "disconnect"
		cfg.faultAfter = 20
		cfg.tick = 5 * time.Millisecond
		dev, stop := startSim(t, cfg)
		defer stop()
		t.Setenv(hwmon.RootOverrideEnv, filepath.Dir(dev))
		be := hwhal.NewBackend(nil)
		chans, _ := be.Enumerate(context.Background())
		ch := chans[0]
		_ = be.Write(ch, 200)
		// Before the fault the fan spins; after faultAfter ticks the tach drops
		// to 0 while duty stays high. The runtime must read the drop-out as 0
		// (a plausible reading), the signal the stall/low-temp paths key on.
		r := waitRead(t, be, ch, func(r hal.Reading) bool { return r.OK && r.RPM == 0 })
		if !r.OK || r.RPM != 0 {
			t.Errorf("disconnect: after fault Read OK=%v RPM=%d; want OK=true RPM=0", r.OK, r.RPM)
		}
	})
}
