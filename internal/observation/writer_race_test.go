package observation

import (
	"sync"
	"testing"

	"github.com/ventd/ventd/internal/probe"
)

// TestWriter_ConcurrentAppendRaceSafe — bug-hunt iteration 2 caught
// that the prior Writer struct comment ("NOT safe for concurrent
// use") was being violated in production: both the controller tick
// goroutine AND the opportunistic prober goroutine call Append
// against the same Writer concurrently.
//
// The race manifests on the rotation-trigger check: two goroutines
// observing bytesWritten >= maxActiveSize simultaneously would each
// call Rotate(), and the second's Header write would land in the
// FIRST one's brand-new file (the underlying log.Append is goroutine-
// safe but the per-Writer counters aren't).
//
// This test runs 8 goroutines × 1000 Append calls each against the
// same Writer. -race must come back clean.
func TestWriter_ConcurrentAppendRaceSafe(t *testing.T) {
	ch1 := &probe.ControllableChannel{PWMPath: "/sys/test/pwm1", Driver: "nct6775"}
	ch2 := &probe.ControllableChannel{PWMPath: "/sys/test/pwm2", Driver: "nct6775"}
	ch3 := &probe.ControllableChannel{PWMPath: "/sys/test/pwm3", Driver: "nct6775"}
	w, _ := newTestWriter(t, []*probe.ControllableChannel{ch1, ch2, ch3})

	const goroutines = 8
	const perGoroutine = 1000
	id1 := ChannelID(ch1.PWMPath)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				rec := &Record{
					Ts:        int64(seed*perGoroutine + i + 1),
					ChannelID: id1,
				}
				if err := w.Append(rec); err != nil {
					// Fatal would race other goroutines; record + skip.
					t.Errorf("Append: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
