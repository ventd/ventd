package crosec

import "log/slog"

// NewBackendForTest constructs a Backend with an injected SendFunc.
// The send function replaces the ioctl path so tests never need /dev/cros_ec.
func NewBackendForTest(logger *slog.Logger, send SendFunc) *Backend {
	b := NewBackend(logger)
	b.send = send
	return b
}
