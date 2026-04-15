package calibrate

import (
	"log/slog"
	"sync"
	"time"
)

// SafePWMFloor is the duty cycle the safety timer escalates to when
// a fan has been parked at PWM=0 for longer than ZeroPWMMaxDuration.
// 30 (~12% of 255) is conservative: above the start_pwm of nearly
// every fan and AIO pump on the market while still being quiet.
const SafePWMFloor uint8 = 30

// ZeroPWMMaxDuration is the upper bound on time a fan can stay at
// PWM=0 during a calibration sweep before the safety timer escalates
// it to SafePWMFloor. Matches the README claim:
//
//	"Calibration never leaves a fan at PWM=0 for more than two seconds."
//
// Set generously enough that legitimate stop_pwm probes (which dwell
// at 0 to confirm the fan has stopped, then move on) complete cleanly,
// but tight enough that a daemon that hangs / panics / loses its
// goroutine mid-sweep cannot leave a fan stopped under load.
const ZeroPWMMaxDuration = 2 * time.Second

// ZeroPWMSentinel watches a fan's most recent commanded PWM value.
// While the value is 0 it arms a 2-second timer; if the timer fires
// before another non-zero write resets it, sentinel calls the
// supplied escalate function to write SafePWMFloor.
//
// Used by calibration sweeps that intentionally drive PWM to 0
// (stop-PWM probe). A concurrent watchdog or controller re-arming
// the fan via WritePWM(non-zero) automatically cancels the timer
// via Set(non-zero).
//
// Thread-safe — multiple goroutines may call Set concurrently. The
// escalate callback is invoked with the sentinel's mutex held; keep
// it short and non-blocking. In production this is a hwmon.WritePWM
// invocation, which is a single sysfs write — fine.
type ZeroPWMSentinel struct {
	mu       sync.Mutex
	logger   *slog.Logger
	escalate func() // called when the 2s timer fires while value is 0
	timer    *time.Timer
	current  uint8
	stopped  bool
}

// NewZeroPWMSentinel returns a sentinel that calls escalate after
// the value stays at 0 for more than ZeroPWMMaxDuration. logger may
// be nil for tests.
func NewZeroPWMSentinel(logger *slog.Logger, escalate func()) *ZeroPWMSentinel {
	return &ZeroPWMSentinel{
		logger:   logger,
		escalate: escalate,
	}
}

// Set updates the tracked PWM value. value=0 arms (or re-arms) the
// 2-second escalation timer; any non-zero value cancels it.
func (s *ZeroPWMSentinel) Set(value uint8) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.current = value
	if value == 0 {
		// Arm or re-arm. AfterFunc creates a one-shot timer that
		// fires once after duration; if a previous timer is still
		// running we Stop it first to avoid double-fire.
		if s.timer != nil {
			s.timer.Stop()
		}
		s.timer = time.AfterFunc(ZeroPWMMaxDuration, s.escalateLocked)
		return
	}
	// Non-zero value: cancel any pending escalation.
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

// Stop cancels any pending timer and disables further Set calls.
// Safe to call from the calibration cleanup defer chain.
func (s *ZeroPWMSentinel) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

// escalateLocked is the AfterFunc body; it re-checks current state
// under the lock so a Set(non-zero) racing with the timer firing
// resolves to "no escalation needed".
func (s *ZeroPWMSentinel) escalateLocked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if s.current != 0 {
		// A Set(non-zero) ran between the timer firing and us
		// taking the lock. Drop the escalation.
		return
	}
	if s.logger != nil {
		s.logger.Warn("calibrate: PWM held at 0 for >2s, escalating to safe floor",
			"floor", SafePWMFloor)
	}
	if s.escalate != nil {
		s.escalate()
	}
}
