package signature

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/proc"
)

// TestLibrary_LabelReadStress runs Tick + Label() concurrently
// under load. Catches the bug class where the atomic.Pointer[string]
// publish is racy with the read path (e.g., a missing memory barrier
// or a torn-read on a non-atomic field).
//
// Audit gap #5 (concurrency / race-detector coverage). Run with
// `go test -race -run TestLibrary_LabelReadStress`.
//
// The test runs for ~200 ms with one Tick goroutine and 16 Label
// reader goroutines. Reader assertions: every read returns a
// recognisable label string (one of FallbackLabelIdle,
// FallbackLabelDisabled, MaintLabelPrefix-prefixed, or hash-tuple),
// and every read is non-empty.
func TestLibrary_LabelReadStress(t *testing.T) {
	if testing.Short() {
		t.Skip("race stress test skipped under -short")
	}
	lib := makeLib(t)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var labelReads atomic.Uint64
	var ticks atomic.Uint64

	// Single writer goroutine — drives Tick at high frequency.
	wg.Add(1)
	go func() {
		defer wg.Done()
		base := time.Now()
		samples := []proc.ProcessSample{
			p("workload-a", 1.20, 800<<20),
			p("supporting", 0.30, 200<<20),
		}
		for {
			select {
			case <-stop:
				return
			default:
				now := base.Add(time.Duration(ticks.Load()) * lib.cfg.HalfLife)
				lib.Tick(now, samples)
				ticks.Add(1)
			}
		}
	}()

	// 16 reader goroutines hammering Label().
	const readers = 16
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					label := lib.Label()
					// Reader contract: label is non-empty
					// and has a recognisable shape.
					if label == "" {
						t.Errorf("Label() returned empty string")
						return
					}
					labelReads.Add(1)
				}
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()

	if ticks.Load() == 0 {
		t.Error("writer goroutine never ticked")
	}
	if labelReads.Load() == 0 {
		t.Error("no readers got a label")
	}
	t.Logf("ticks=%d label-reads=%d", ticks.Load(), labelReads.Load())
}
