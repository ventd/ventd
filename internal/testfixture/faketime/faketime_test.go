package faketime_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/testfixture/faketime"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestNow(t *testing.T) {
	c := faketime.New(t, epoch)
	if got := c.Now(); !got.Equal(epoch) {
		t.Errorf("Now() = %v, want %v", got, epoch)
	}
}

func TestAdvanceSingleTimer(t *testing.T) {
	c := faketime.New(t, epoch)
	tm := c.NewTimer(5 * time.Second)
	defer tm.Stop()

	// Not yet fired.
	select {
	case <-tm.C:
		t.Fatal("timer fired before Advance")
	default:
	}

	c.Advance(3 * time.Second)
	select {
	case <-tm.C:
		t.Fatal("timer fired at 3s, deadline is 5s")
	default:
	}

	c.Advance(3 * time.Second) // total 6s
	select {
	case v := <-tm.C:
		want := epoch.Add(5 * time.Second)
		if !v.Equal(want) {
			t.Errorf("timer value = %v, want %v", v, want)
		}
	default:
		t.Fatal("timer did not fire after advancing past deadline")
	}
}

func TestAdvanceMultiTimerOrdering(t *testing.T) {
	c := faketime.New(t, epoch)
	t3 := c.NewTimer(3 * time.Second)
	t1 := c.NewTimer(1 * time.Second)
	t2 := c.NewTimer(2 * time.Second)
	defer t1.Stop()
	defer t2.Stop()
	defer t3.Stop()

	c.Advance(5 * time.Second)

	// All three should have fired. Read them and verify ordering by value.
	var got []time.Time
	for _, tm := range []*faketime.Timer{t1, t2, t3} {
		select {
		case v := <-tm.C:
			got = append(got, v)
		default:
			t.Fatal("expected timer to have fired")
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i].Before(got[i-1]) {
			t.Errorf("timer %d (%v) fired before timer %d (%v)", i, got[i], i-1, got[i-1])
		}
	}
}

func TestTickerAcrossMultipleAdvances(t *testing.T) {
	c := faketime.New(t, epoch)
	tk := c.NewTicker(10 * time.Second)
	defer tk.Stop()

	// Advance 25s: should produce ticks at 10s, 20s. The third (30s) hasn't
	// been reached. Channel buffer is 1, so we drain between Advances.
	c.Advance(15 * time.Second) // tick at 10s
	select {
	case v := <-tk.C:
		want := epoch.Add(10 * time.Second)
		if !v.Equal(want) {
			t.Errorf("tick 1 = %v, want %v", v, want)
		}
	default:
		t.Fatal("expected tick at 10s")
	}

	c.Advance(10 * time.Second) // now at 25s; tick at 20s
	select {
	case v := <-tk.C:
		want := epoch.Add(20 * time.Second)
		if !v.Equal(want) {
			t.Errorf("tick 2 = %v, want %v", v, want)
		}
	default:
		t.Fatal("expected tick at 20s")
	}

	// No tick at 30s yet.
	select {
	case <-tk.C:
		t.Fatal("unexpected tick before 30s")
	default:
	}
}

func TestTickerMultipleTicksPerAdvance(t *testing.T) {
	c := faketime.New(t, epoch)
	tk := c.NewTicker(1 * time.Second)
	defer tk.Stop()

	// Advance 3s: should produce ticks at 1s, 2s, 3s. Channel buffer is 1,
	// so at most 1 is buffered. The rest are dropped (real Ticker semantics).
	c.Advance(3 * time.Second)
	select {
	case <-tk.C:
		// Got 1 tick — the others were dropped.
	default:
		t.Fatal("expected at least one tick")
	}
}

func TestAfter(t *testing.T) {
	c := faketime.New(t, epoch)
	ch := c.After(2 * time.Second)

	c.Advance(1 * time.Second)
	select {
	case <-ch:
		t.Fatal("After fired early")
	default:
	}

	c.Advance(2 * time.Second)
	select {
	case <-ch:
		// OK
	default:
		t.Fatal("After did not fire after advancing past deadline")
	}
}

func TestTimerStop(t *testing.T) {
	c := faketime.New(t, epoch)
	tm := c.NewTimer(1 * time.Second)

	if !tm.Stop() {
		t.Error("Stop returned false on unfired timer")
	}
	// Second stop should return false.
	if tm.Stop() {
		t.Error("Stop returned true on already-stopped timer")
	}

	c.Advance(5 * time.Second)
	select {
	case <-tm.C:
		t.Fatal("stopped timer fired")
	default:
	}
}

func TestTickerStop(t *testing.T) {
	c := faketime.New(t, epoch)
	tk := c.NewTicker(1 * time.Second)
	tk.Stop()

	c.Advance(5 * time.Second)
	select {
	case <-tk.C:
		t.Fatal("stopped ticker ticked")
	default:
	}
}

func TestConcurrentAdvanceAndNewTimer(t *testing.T) {
	c := faketime.New(t, epoch)
	var wg sync.WaitGroup
	var fired atomic.Int32

	// Spawn goroutines that concurrently create timers and advance the clock.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tm := c.NewTimer(time.Duration(i+1) * time.Millisecond)
			defer tm.Stop()
			// Drain if it fired.
			select {
			case <-tm.C:
				fired.Add(1)
			default:
			}
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Advance(5 * time.Millisecond)
		}()
	}

	wg.Wait()
	// Under -race, this must not flag any data races. The exact number of
	// fired timers is non-deterministic because Advance and NewTimer are
	// interleaved, so we just check for no panics/races.
}

func TestWaitUntilHappy(t *testing.T) {
	var ready atomic.Bool
	go func() {
		time.Sleep(10 * time.Millisecond)
		ready.Store(true)
	}()
	faketime.WaitUntil(t, func() bool { return ready.Load() }, 1*time.Second)
}

func TestWaitUntilTimeout(t *testing.T) {
	// Run in a sub-test so the Fatal doesn't kill this test.
	inner := &testing.T{}
	done := make(chan struct{})
	go func() {
		defer func() { close(done) }()
		// This will call inner.Fatal, which panics inside a goroutine.
		defer func() { recover() }()
		faketime.WaitUntil(inner, func() bool { return false }, 20*time.Millisecond)
	}()
	select {
	case <-done:
		// WaitUntil terminated — either via Fatal panic or timeout.
	case <-time.After(2 * time.Second):
		t.Fatal("WaitUntil did not terminate within 2s")
	}
}
