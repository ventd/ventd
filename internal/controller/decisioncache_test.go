package controller

import (
	"sync"
	"testing"
)

// TestDecisionCache_StoreLoadRoundTrip verifies Store + Load returns
// the same value byte-equal — no copy/pointer aliasing bug that would
// surface as the dashboard occasionally seeing a stale OutputPWM.
func TestDecisionCache_StoreLoadRoundTrip(t *testing.T) {
	c := NewDecisionCache()
	want := Decision{
		ReactivePWM: 140,
		Result: BlendedResult{
			OutputPWM:        180,
			PredictivePWM:    200,
			WPred:            0.65,
			PathARefused:     true,
			DBABudgetRefused: false,
			PredictedDBA:     31.5,
			UIState:          "blended",
		},
	}
	c.Store("/sys/class/hwmon/hwmon0/pwm1", want)

	got, ok := c.Load("/sys/class/hwmon/hwmon0/pwm1")
	if !ok {
		t.Fatal("Load returned !ok after Store")
	}
	if got != want {
		t.Errorf("Load=%+v, want %+v", got, want)
	}
}

// TestDecisionCache_LoadEmpty returns (zero, false) for never-stored
// channel — no panic, no spurious zero value masquerading as a real
// decision.
func TestDecisionCache_LoadEmpty(t *testing.T) {
	c := NewDecisionCache()
	if r, ok := c.Load("nonexistent"); ok || r != (Decision{}) {
		t.Errorf("Load(nonexistent)=(%+v, %v), want (zero, false)", r, ok)
	}
}

// TestDecisionCache_NilSafe — both Store and Load on a nil receiver
// must be no-ops. The web layer's nil guard depends on this so the
// monitor-only daemon (no controller, no decisions) doesn't crash on
// every /smart/channels poll.
func TestDecisionCache_NilSafe(t *testing.T) {
	var c *DecisionCache
	c.Store("x", Decision{Result: BlendedResult{OutputPWM: 100}}) // must not panic
	if r, ok := c.Load("x"); ok || r != (Decision{}) {
		t.Errorf("nil-receiver Load=(%+v, %v), want (zero, false)", r, ok)
	}
	if all := c.LoadAll(); all != nil {
		t.Errorf("nil-receiver LoadAll=%v, want nil", all)
	}
}

// TestDecisionCache_OverwritePicksLatest pins the contract that Store
// overwrites — the controller fires every tick, so what the dashboard
// sees should ALWAYS be the most recent decision, never a stale one.
func TestDecisionCache_OverwritePicksLatest(t *testing.T) {
	c := NewDecisionCache()
	c.Store("ch", Decision{Result: BlendedResult{OutputPWM: 100}})
	c.Store("ch", Decision{Result: BlendedResult{OutputPWM: 150}})
	c.Store("ch", Decision{Result: BlendedResult{OutputPWM: 200}})
	got, _ := c.Load("ch")
	if got.Result.OutputPWM != 200 {
		t.Errorf("OutputPWM=%d, want 200 (latest)", got.Result.OutputPWM)
	}
}

// TestDecisionCache_LoadAllSnapshot returns every channel's most
// recent decision in one pass. The web /smart/channels handler
// calls LoadAll once per request; per-channel races between LoadAll
// and concurrent Stores must not panic.
func TestDecisionCache_LoadAllSnapshot(t *testing.T) {
	c := NewDecisionCache()
	c.Store("a", Decision{Result: BlendedResult{OutputPWM: 100}})
	c.Store("b", Decision{Result: BlendedResult{OutputPWM: 150}})
	c.Store("c", Decision{Result: BlendedResult{OutputPWM: 200}})

	all := c.LoadAll()
	if len(all) != 3 {
		t.Fatalf("got %d entries, want 3", len(all))
	}
	if all["a"].Result.OutputPWM != 100 || all["b"].Result.OutputPWM != 150 || all["c"].Result.OutputPWM != 200 {
		t.Errorf("snapshot mismatch: %+v", all)
	}
}

// TestDecisionCache_ConcurrentStoresAreRaceSafe — controller runs at
// 0.5 Hz per channel today but the contract is "no race regardless
// of cadence". 8 goroutines hammer Store + Load against shared
// channels for a brief window; -race must come back clean.
func TestDecisionCache_ConcurrentStoresAreRaceSafe(t *testing.T) {
	c := NewDecisionCache()
	channels := []string{"ch0", "ch1", "ch2", "ch3"}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			pwm := uint8(seed * 17)
			for {
				select {
				case <-stop:
					return
				default:
					ch := channels[seed%len(channels)]
					c.Store(ch, Decision{Result: BlendedResult{OutputPWM: pwm}})
					_, _ = c.Load(ch)
					_ = c.LoadAll()
					pwm++
				}
			}
		}(i)
	}
	doneCh := make(chan struct{})
	go func() {
		for j := 0; j < 1_000_000; j++ {
		}
		close(stop)
		wg.Wait()
		close(doneCh)
	}()
	<-doneCh
}
