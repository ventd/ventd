package controller

import (
	"sync"
	"sync/atomic"
)

// Decision is the operator-meaningful slice of one controller tick:
// the BlendedResult (what the controller decided to do) plus the
// ReactivePWM input (what the upstream curve would have done on
// its own, before the predictive arm). Together they let downstream
// consumers compute the next-tick ramp size (OutputPWM −
// ReactivePWM) and the predicted thermal change for that ramp
// (MarginalSlope × ramp). Both values are needed for the dashboard
// forecast surface (#790); BlendedResult alone carries the output
// but not the reactive baseline.
type Decision struct {
	Result      BlendedResult
	ReactivePWM uint8
}

// DecisionCache records the most-recent Decision per channel so
// readers (notably the web /api/v1/smart/channels handler) can show
// the controller's actual next-tick PWM target alongside Layer-C's
// MarginalSlope. Without this, the dashboard could only display the
// per-PWM marginal rate (a property of the model) — not the predicted
// thermal change for the candidate ramp the controller actually plans
// to issue (a property of the controller's blended decision).
//
// Hot-loop safe: writers take a per-channel pointer-swap (no global
// lock); readers Load() lock-free. The Store() critical section is
// the cheap one — bounded by the constant cost of inserting into
// the channel map on the very first observation.
//
// The cache owns no goroutines; lifecycle matches the smart bundle
// in cmd/ventd/main.go.
type DecisionCache struct {
	mu       sync.Mutex
	channels map[string]*atomic.Pointer[Decision]
}

// NewDecisionCache constructs an empty cache. Channels are admitted
// lazily on first Store call.
func NewDecisionCache() *DecisionCache {
	return &DecisionCache{channels: make(map[string]*atomic.Pointer[Decision])}
}

// Store records the most-recent Decision for the given channel.
// Pointer-swap; safe to call from the controller hot loop on every
// tick. A nil receiver is a no-op so callers don't have to guard.
func (c *DecisionCache) Store(channelID string, d Decision) {
	if c == nil {
		return
	}
	ptr := c.slot(channelID)
	cp := d
	ptr.Store(&cp)
}

// Load returns the most-recent Decision for the given channel,
// or (zero, false) if none has been stored. Lock-free atomic read.
func (c *DecisionCache) Load(channelID string) (Decision, bool) {
	if c == nil {
		return Decision{}, false
	}
	c.mu.Lock()
	ptr, ok := c.channels[channelID]
	c.mu.Unlock()
	if !ok {
		return Decision{}, false
	}
	v := ptr.Load()
	if v == nil {
		return Decision{}, false
	}
	return *v, true
}

// LoadAll snapshots every channel's most-recent decision in one pass.
// Returned map is owned by the caller; safe to mutate.
func (c *DecisionCache) LoadAll() map[string]Decision {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	out := make(map[string]Decision, len(c.channels))
	for chID, ptr := range c.channels {
		v := ptr.Load()
		if v != nil {
			out[chID] = *v
		}
	}
	c.mu.Unlock()
	return out
}

// slot returns the per-channel atomic.Pointer slot, creating it
// under c.mu the first time we see a new channel ID.
func (c *DecisionCache) slot(channelID string) *atomic.Pointer[Decision] {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ptr, ok := c.channels[channelID]; ok {
		return ptr
	}
	ptr := new(atomic.Pointer[Decision])
	c.channels[channelID] = ptr
	return ptr
}
