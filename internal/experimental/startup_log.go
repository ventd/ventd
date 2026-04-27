package experimental

import (
	"log/slog"
	"os"
	"time"
)

const suppressionWindow = 24 * time.Hour

// LogActiveFlagsOnce emits a single INFO log listing active experimental flags,
// suppressed if the same message was logged within the last 24h. The suppression
// state is persisted in statePath (RFC3339 timestamp). now is called to determine
// the current time; production callers pass time.Now.
//
// No log is emitted and no state file is written when no flags are active.
func LogActiveFlagsOnce(flags Flags, statePath string, logger *slog.Logger, now func() time.Time) {
	active := flags.Active()
	if len(active) == 0 {
		return
	}

	currentTime := now()

	// Check suppression: read state file if it exists.
	if data, err := os.ReadFile(statePath); err == nil {
		if lastLog, err := time.Parse(time.RFC3339, string(data)); err == nil {
			if currentTime.Sub(lastLog) < suppressionWindow {
				return
			}
		}
	}

	logger.Info("experimental features active",
		slog.Any("flags", active),
	)

	_ = os.WriteFile(statePath, []byte(currentTime.Format(time.RFC3339)), 0o600)
}
