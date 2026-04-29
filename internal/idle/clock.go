package idle

import "time"

// Clock abstracts time.Sleep so tests can advance time without real sleeps.
type Clock interface {
	Sleep(d time.Duration)
	Now() time.Time
}

// realClock is the production Clock backed by the stdlib.
type realClock struct{}

func (realClock) Sleep(d time.Duration) { time.Sleep(d) }
func (realClock) Now() time.Time        { return time.Now() }

// newClock returns the production Clock.
func newClock() Clock { return realClock{} }
