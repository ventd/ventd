//go:build !linux

package hwmon

import (
	"context"
	"log/slog"
)

// subscribeUevents is a no-op on non-Linux platforms. Netlink is Linux-only;
// the watcher falls back to periodic rescan alone.
func subscribeUevents(_ context.Context, logger *slog.Logger) <-chan UeventMessage {
	logger.Info("hwmon watcher: uevent subscription unavailable on non-Linux, periodic rescan only")
	out := make(chan UeventMessage)
	close(out)
	return out
}
