// Package faultbackend is a programmable fault-injecting hal.FanBackend for
// tests that must exercise syscall-level error paths a file-backed sim
// (tools/hwmonsim) cannot express: EBUSY storms (a BIOS contesting manual
// mode), EINVAL on a mode write, EIO transients, EPERM/ErrNotPermitted, and
// transient-then-recover sequences. hwmonsim can model any RPM/temp the fan
// reports, but it cannot make a file *return an errno on write* — that lives
// below the filesystem, at the syscall boundary, which is exactly where this
// fake substitutes for the real backend.
//
// When no fault is injected it behaves like a working fan: Enumerate returns
// the configured channels, Write records the duty, Read returns it (plus any
// simulated RPM/temp). Every Write and Restore is logged for assertions.
//
// It is safe for concurrent use (the controller tick and the watchdog restore
// run in separate goroutines), matching the FanBackend contract.
package faultbackend

import (
	"context"
	"sync"

	"github.com/ventd/ventd/internal/hal"
)

// WriteRecord is one observed Write call and the error the fake returned for it.
type WriteRecord struct {
	ChannelID string
	PWM       uint8
	Err       error
}

// Backend is the fault-injecting fake. Construct with New, then set one of the
// per-method error policies (the *Errs FIFO queues for the common "fail the
// first N calls" scripts, or the *Policy funcs for full control). The zero
// policies mean "no injected fault — behave like a working fan".
type Backend struct {
	mu       sync.Mutex
	name     string
	channels []hal.Channel

	pwm  map[string]uint8  // last committed duty per channel ID
	rpm  map[string]uint16 // simulated tach per channel ID (0 unless SetRPM)
	temp map[string]float64

	// WriteErrs / RestoreErrs / ReadErrs are consumed one-per-call (FIFO); a
	// nil entry passes, and once exhausted the call succeeds. The simplest way
	// to script "EBUSY ×3 then recover" ([EBUSY,EBUSY,EBUSY]) or "transient"
	// ([EIO]). Ignored on a method whose *Policy is set.
	WriteErrs   []error
	RestoreErrs []error
	ReadErrs    []error

	// WritePolicy / RestorePolicy, when non-nil, take precedence over the FIFO
	// queues and get the 1-based per-channel call count so a test can express
	// "always EBUSY" or "fail until call N". Returning nil = success.
	WritePolicy   func(chID string, pwm uint8, call int) error
	RestorePolicy func(chID string, call int) error

	writeCalls   map[string]int
	restoreCalls map[string]int
	readCalls    map[string]int

	// Writes records every Write that reached the backend with the error it
	// returned; Restores records the channel IDs Restore was called on, in
	// order. For assertions about retry counts, hand-back firing, etc.
	Writes   []WriteRecord
	Restores []string
}

// New builds a fault-injecting backend exposing the given channels. Use Channel
// to construct a standard read+write+restore hwmon-shaped channel.
func New(name string, channels ...hal.Channel) *Backend {
	return &Backend{
		name:         name,
		channels:     channels,
		pwm:          map[string]uint8{},
		rpm:          map[string]uint16{},
		temp:         map[string]float64{},
		writeCalls:   map[string]int{},
		restoreCalls: map[string]int{},
		readCalls:    map[string]int{},
	}
}

// Channel builds a standard hwmon-shaped channel (read + write-PWM + restore).
func Channel(id string) hal.Channel {
	return hal.Channel{
		ID:   id,
		Role: hal.RoleUnknown,
		Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore,
	}
}

// SetRPM sets the tach value Read reports for a channel (e.g. to simulate a
// spinning fan whose write path nonetheless errors).
func (b *Backend) SetRPM(chID string, rpm uint16) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rpm[chID] = rpm
}

// LastPWM returns the most recent duty committed to a channel (the value of the
// last Write that did NOT return an error), and whether any landed.
func (b *Backend) LastPWM(chID string) (uint8, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.pwm[chID]
	return v, ok
}

// WriteCount / RestoreCount return how many times each method was invoked for a
// channel (including calls that returned an error).
func (b *Backend) WriteCount(chID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.writeCalls[chID]
}

func (b *Backend) RestoreCount(chID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.restoreCalls[chID]
}

// --- hal.FanBackend ---

func (b *Backend) Name() string { return b.name }

func (b *Backend) Close() error { return nil }

func (b *Backend) Enumerate(_ context.Context) ([]hal.Channel, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]hal.Channel, len(b.channels))
	copy(out, b.channels)
	return out, nil
}

func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writeCalls[ch.ID]++
	err := b.nextWriteErr(ch.ID, pwm)
	b.Writes = append(b.Writes, WriteRecord{ChannelID: ch.ID, PWM: pwm, Err: err})
	if err == nil {
		b.pwm[ch.ID] = pwm
	}
	return err
}

func (b *Backend) Read(ch hal.Channel) (hal.Reading, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.readCalls[ch.ID]++
	if err := nextFIFO(&b.ReadErrs); err != nil {
		// A non-recoverable read error (rare). The empty-by-construction
		// invariant holds: OK=false carries no sub-state.
		return hal.Reading{OK: false}, err
	}
	return hal.Reading{
		OK:   true,
		PWM:  b.pwm[ch.ID],
		RPM:  b.rpm[ch.ID],
		Temp: b.temp[ch.ID],
	}, nil
}

func (b *Backend) Restore(ch hal.Channel) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.restoreCalls[ch.ID]++
	b.Restores = append(b.Restores, ch.ID)
	if b.RestorePolicy != nil {
		return b.RestorePolicy(ch.ID, b.restoreCalls[ch.ID])
	}
	return nextFIFO(&b.RestoreErrs)
}

// nextWriteErr resolves the error for this Write: the WritePolicy when set,
// else the WriteErrs FIFO. Caller holds b.mu.
func (b *Backend) nextWriteErr(chID string, pwm uint8) error {
	if b.WritePolicy != nil {
		return b.WritePolicy(chID, pwm, b.writeCalls[chID])
	}
	return nextFIFO(&b.WriteErrs)
}

// nextFIFO pops the head of an error queue (consuming it); returns nil when the
// queue is empty. Caller holds b.mu.
func nextFIFO(q *[]error) error {
	if len(*q) == 0 {
		return nil
	}
	err := (*q)[0]
	*q = (*q)[1:]
	return err
}

// AlwaysFail is a WritePolicy/RestorePolicy-compatible helper that returns err
// on every call — a sustained storm.
func AlwaysFail(err error) func(string, uint8, int) error {
	return func(string, uint8, int) error { return err }
}

// Compile-time proof the fake satisfies the backend contract.
var _ hal.FanBackend = (*Backend)(nil)
