package hal

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// The registry is a process-wide lookup of backend name → FanBackend.
// It exists so main.go can fan out Enumerate across every registered
// backend and so Phase 2 consumers (diagnostic endpoints, UI fan
// inventory) can resolve a tagged channel ID back to the backend that
// owns it.
//
// Backends are registered explicitly from main (not via init() — the
// project convention in .claude/rules/go-conventions.md forbids
// package-level init). The task prompt suggested "Register in init()";
// the deviation is called out in the task PR body.
var (
	regMu    sync.RWMutex
	backends = make(map[string]FanBackend)
)

// Register adds a backend to the registry under its Name(). A second
// registration under the same name overwrites the first — the caller
// is responsible for registering each backend exactly once at
// startup.
func Register(name string, b FanBackend) {
	if name == "" || b == nil {
		return
	}
	regMu.Lock()
	defer regMu.Unlock()
	backends[name] = b
}

// Backend returns the registered backend with the given name.
func Backend(name string) (FanBackend, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	b, ok := backends[name]
	return b, ok
}

// Reset clears the registry. Intended for tests that need a clean
// slate between runs; production code never calls this.
func Reset() {
	regMu.Lock()
	defer regMu.Unlock()
	backends = make(map[string]FanBackend)
}

// Enumerate fans out to every registered backend, collects their
// channels, and tags each ID with the backend name so IDs are
// globally unique (e.g. "hwmon:/sys/class/hwmon/hwmon3/pwm1",
// "nvml:0"). Backend enumeration errors are wrapped with the
// backend's name and returned immediately; partial results are
// discarded.
func Enumerate(ctx context.Context) ([]Channel, error) {
	regMu.RLock()
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	regMu.RUnlock()
	sort.Strings(names)

	var all []Channel
	for _, name := range names {
		b, ok := Backend(name)
		if !ok {
			continue
		}
		chs, err := b.Enumerate(ctx)
		if err != nil {
			return nil, fmt.Errorf("hal: enumerate %s: %w", name, err)
		}
		for _, ch := range chs {
			ch.ID = name + ":" + ch.ID
			all = append(all, ch)
		}
	}
	return all, nil
}

// Resolve finds the backend and Channel for a globally-tagged ID as
// produced by Enumerate (e.g. "hwmon:/sys/.../pwm1"). Returns an
// error when the tag does not match any registered backend or when
// the backend no longer reports a matching channel.
func Resolve(id string) (FanBackend, Channel, error) {
	name, inner, ok := strings.Cut(id, ":")
	if !ok {
		return nil, Channel{}, fmt.Errorf("hal: resolve %q: missing backend tag (expected \"name:inner-id\")", id)
	}
	b, ok := Backend(name)
	if !ok {
		return nil, Channel{}, fmt.Errorf("hal: resolve %q: backend %q not registered", id, name)
	}
	chs, err := b.Enumerate(context.Background())
	if err != nil {
		return nil, Channel{}, fmt.Errorf("hal: resolve %q: enumerate %s: %w", id, name, err)
	}
	for _, ch := range chs {
		if ch.ID == inner {
			return b, ch, nil
		}
	}
	return nil, Channel{}, fmt.Errorf("hal: resolve %q: no channel with id %q in backend %q", id, inner, name)
}
