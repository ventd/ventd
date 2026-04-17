package testutil

import "sync"

// Call records a function call with its arguments.
type Call struct {
	Name string
	Args []any
}

// CallRecorder is a goroutine-safe record of function calls.
type CallRecorder struct {
	mu    sync.Mutex
	calls []Call
}

// NewCallRecorder returns an empty call recorder.
func NewCallRecorder() *CallRecorder {
	return &CallRecorder{}
}

// Record adds a call with the given name and arguments.
func (cr *CallRecorder) Record(name string, args ...any) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	cr.calls = append(cr.calls, Call{Name: name, Args: args})
}

// Calls returns a copy of all recorded calls.
func (cr *CallRecorder) Calls() []Call {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	calls := make([]Call, len(cr.calls))
	copy(calls, cr.calls)
	return calls
}
