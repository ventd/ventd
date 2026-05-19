package platformprofile

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

func TestSelector_PickHotProfileUnderPressure(t *testing.T) {
	hw := HardwareSummary{TJmaxC: 100, FanMaxRPM: 6600, TDPWatts: 15}
	sel, err := NewSelector(hw, []string{"cool", "quiet", "balanced", "performance"})
	if err != nil {
		t.Fatal(err)
	}
	d := sel.Pick(Inputs{
		CurrentTempC:    95, // ~95% of TJmax
		CurrentRPM:      6000,
		CPULoadPct:      90,
		CurrentTDPWatts: 14,
	})
	if d.Profile != "performance" {
		t.Errorf("under high pressure want performance, got %q (score=%.2f reason=%s)", d.Profile, d.PressureScore, d.Reason)
	}
}

func TestSelector_PickQuietProfileWhenIdle(t *testing.T) {
	hw := HardwareSummary{TJmaxC: 100, FanMaxRPM: 6600, TDPWatts: 15}
	sel, _ := NewSelector(hw, []string{"cool", "quiet", "balanced", "performance"})
	d := sel.Pick(Inputs{
		CurrentTempC:    40,
		CurrentRPM:      0,
		CPULoadPct:      2,
		CurrentTDPWatts: 1,
	})
	if d.Profile != "cool" && d.Profile != "quiet" && d.Profile != "low-power" {
		t.Errorf("idle should pick quietest tier, got %q (score=%.2f reason=%s)", d.Profile, d.PressureScore, d.Reason)
	}
}

func TestSelector_PickBalancedInMiddle(t *testing.T) {
	hw := HardwareSummary{TJmaxC: 100, FanMaxRPM: 6600, TDPWatts: 15}
	sel, _ := NewSelector(hw, []string{"cool", "quiet", "balanced", "performance"})
	d := sel.Pick(Inputs{
		CurrentTempC:    70,
		CurrentRPM:      3000,
		CPULoadPct:      50,
		CurrentTDPWatts: 8,
	})
	if d.Profile != "balanced" {
		t.Errorf("middle pressure should pick balanced, got %q (score=%.2f)", d.Profile, d.PressureScore)
	}
}

func TestSelector_HandlesNonStandardChoices(t *testing.T) {
	// HP-style: "low-power", "balanced-performance", "performance" -- no "balanced"
	hw := HardwareSummary{TJmaxC: 100, FanMaxRPM: 6600, TDPWatts: 15}
	sel, err := NewSelector(hw, []string{"low-power", "balanced-performance", "performance"})
	if err != nil {
		t.Fatal(err)
	}
	quietest, mid, hottest := sel.Anchors()
	if quietest != "low-power" {
		t.Errorf("quietest: got %q, want low-power", quietest)
	}
	if hottest != "performance" {
		t.Errorf("hottest: got %q, want performance", hottest)
	}
	if mid != "balanced-performance" {
		t.Errorf("mid: got %q, want balanced-performance", mid)
	}
}

// fakeReaders builds a controller with deterministic input readers + a fake
// sysfs snapshot + an in-memory writeFn so the test can assert which profile
// was applied.
type fakeReaders struct {
	temp, load, power float64
	rpm               int
	snap              *Snapshot
	writes            []string
	mu                atomic.Int32
}

func TestController_WritesSwitchWhenHysteresisMet(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := NewLearningStore(t.TempDir() + "/lrn.json")
	hw := HardwareSummary{TJmaxC: 100, FanMaxRPM: 6600, TDPWatts: 15}
	sel, _ := NewSelector(hw, []string{"cool", "quiet", "balanced", "performance"})

	fr := &fakeReaders{
		temp: 95, rpm: 6000, load: 90, power: 14,
		snap: &Snapshot{Present: true, Available: []string{"cool", "quiet", "balanced", "performance"}, Current: "balanced"},
	}

	written := []string{}
	c := NewController(ControllerOptions{
		Logger:               logger,
		Selector:             sel,
		Store:                store,
		Hardware:             hw,
		PollInterval:         10 * time.Millisecond,
		MinDwell:             0, // no dwell for the test
		BackoffAfterExternal: 0,
		TempReader:           func() (float64, error) { return fr.temp, nil },
		RPMReader:            func() (int, error) { return fr.rpm, nil },
		LoadReader:           func() (float64, error) { return fr.load, nil },
		PowerReader:          func() (float64, error) { return fr.power, nil },
		SnapReader: func() (*Snapshot, error) {
			snapCopy := *fr.snap
			return &snapCopy, nil
		},
		WriteFn: func(p string) error {
			written = append(written, p)
			fr.snap.Current = p
			return nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	c.Run(ctx)

	if len(written) == 0 {
		t.Fatal("controller did not write any profile under high pressure")
	}
	if written[0] != "performance" {
		t.Errorf("first write: got %q, want performance", written[0])
	}
}

func TestController_ExternalWriteTriggersBackoff(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := NewLearningStore(t.TempDir() + "/lrn.json")
	hw := HardwareSummary{TJmaxC: 100, FanMaxRPM: 6600, TDPWatts: 15}
	sel, _ := NewSelector(hw, []string{"cool", "balanced", "performance"})

	snap := &Snapshot{Present: true, Available: []string{"cool", "balanced", "performance"}, Current: "performance"}

	c := NewController(ControllerOptions{
		Logger:               logger,
		Selector:             sel,
		Store:                store,
		Hardware:             hw,
		PollInterval:         5 * time.Millisecond,
		MinDwell:             0,
		BackoffAfterExternal: 200 * time.Millisecond,
		TempReader:           func() (float64, error) { return 95, nil },
		RPMReader:            func() (int, error) { return 6000, nil },
		LoadReader:           func() (float64, error) { return 90, nil },
		PowerReader:          func() (float64, error) { return 14, nil },
		SnapReader: func() (*Snapshot, error) {
			c := *snap
			return &c, nil
		},
		WriteFn: func(p string) error { snap.Current = p; return nil },
	})
	// First tick: nothing to do (already at performance).
	c.tick(context.Background())
	// Simulate operator changing it externally.
	snap.Current = "cool"
	// Next tick: should detect the external change and not immediately re-write.
	c.tick(context.Background())
	if snap.Current != "cool" {
		t.Errorf("after external write, controller wrote %q; expected to back off and leave 'cool' alone", snap.Current)
	}
}

// Snapshot used by the absent-interface guard.
func TestReadAt_AbsentInterfaceCausesNoControllerAction(t *testing.T) {
	fsys := fstest.MapFS{}
	snap, _ := ReadAt(fsys, "sys/class/platform-profile")
	if snap.Present {
		t.Fatal("expected absent snapshot")
	}
}
