package asahi

import "log/slog"

// NewBackendForTest creates an Asahi backend with test-overridden DT and
// hwmon paths.  Only reachable from _test.go files.
func NewBackendForTest(logger *slog.Logger, dtPath, sysRoot string) *Backend {
	return NewBackend(logger).withOverrides(dtPath, sysRoot)
}
