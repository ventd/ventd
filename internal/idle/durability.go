package idle

import "time"

// durabilityState tracks the continuous idle predicate for the StartupGate
// 300-second requirement (RULE-IDLE-01).
type durabilityState struct {
	idleSince time.Time
	everTrue  bool
	required  time.Duration
	clock     Clock
}

func newDurabilityState(required time.Duration, clk Clock) *durabilityState {
	return &durabilityState{required: required, clock: clk}
}

// Record updates the state with the current predicate result.
// Returns true when the predicate has been continuously TRUE for >= required.
func (s *durabilityState) Record(isIdle bool) bool {
	now := s.clock.Now()
	if !isIdle {
		s.everTrue = false
		s.idleSince = time.Time{}
		return false
	}
	if !s.everTrue {
		s.everTrue = true
		s.idleSince = now
	}
	return now.Sub(s.idleSince) >= s.required
}

// Reset clears the durability timer (called when a non-idle event fires).
func (s *durabilityState) Reset() {
	s.everTrue = false
	s.idleSince = time.Time{}
}
