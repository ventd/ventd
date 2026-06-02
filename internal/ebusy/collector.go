// Package ebusy aggregates the per-channel EBUSY rolling-window telemetry that
// each controller's hwmon backend records (RULE-HWMON-EBUSY-RATE-OBSERVABILITY).
// Every controller constructs its own hal/hwmon.Backend, so the per-backend
// stats are unreachable from the aggregate doctor surface; this collector is the
// single daemon-lifetime sink the backends push into (via SetEBUSYObserver) and
// the doctor's ebusy_storm detector reads from.
package ebusy

import (
	"sort"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/hal/hwmon"
)

// Collector holds the latest EBUSY snapshot per channel. Safe for concurrent
// use: many controller-backend goroutines push via Observe while the doctor
// reads via ActiveStorms.
type Collector struct {
	mu     sync.Mutex
	latest map[string]hwmon.EBUSYRate // keyed by pwm path
}

// New constructs an empty collector.
func New() *Collector {
	return &Collector{latest: map[string]hwmon.EBUSYRate{}}
}

// Observe records the latest rolling-window snapshot for a channel. Wired as
// each hwmon backend's SetEBUSYObserver callback. Nil-safe so a wiring path that
// never built a collector (monitor-only) can pass (*Collector)(nil).Observe.
func (c *Collector) Observe(r hwmon.EBUSYRate) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.latest[r.PWMPath] = r
}

// ActiveStorms returns the channels whose EBUSY rolling window is still open at
// now, sorted by path for deterministic output. A channel that stormed and then
// went quiet ages out of its window and is dropped here — staleness is decided
// where the clock lives, leaving the detector to apply only its count
// threshold. Nil-safe (returns nil).
func (c *Collector) ActiveStorms(now time.Time) []hwmon.EBUSYRate {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []hwmon.EBUSYRate
	for _, r := range c.latest {
		if r.WindowStart == 0 {
			continue
		}
		if now.Unix()-r.WindowStart < int64(r.WindowSeconds) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PWMPath < out[j].PWMPath })
	return out
}
