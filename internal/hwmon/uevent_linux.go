//go:build linux

package hwmon

import (
	"context"
	"errors"
	"log/slog"
	"syscall"
)

// subscribeUevents opens a NETLINK_KOBJECT_UEVENT socket and streams filtered
// hwmon add/remove notifications on the returned channel. The channel closes
// when ctx is done or the socket errors unrecoverably. EAGAIN and ENOBUFS on
// recv are non-fatal: we log and keep reading (ENOBUFS means the kernel's
// socket buffer overflowed and we dropped events — the periodic rescan loop
// catches whatever we missed).
//
// The watcher treats the channel purely as a "something changed, re-scan"
// trigger; we coalesce ticks via a dedup timer, so flooding the channel is
// harmless.
func subscribeUevents(ctx context.Context, logger *slog.Logger) <-chan UeventMessage {
	out := make(chan UeventMessage, 16)

	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		logger.Warn("hwmon watcher: uevent socket unavailable, periodic rescan only", "err", err)
		close(out)
		return out
	}

	// Group 1 = kernel-originated uevents (udev uses groups 2+ for userspace
	// rebroadcasts; we want raw kernel messages so that running without a
	// udev daemon still delivers events).
	sa := &syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Groups: 1,
		Pid:    0,
	}
	if err := syscall.Bind(fd, sa); err != nil {
		logger.Warn("hwmon watcher: uevent bind failed, periodic rescan only", "err", err)
		_ = syscall.Close(fd)
		close(out)
		return out
	}

	// Closing the fd on ctx cancellation unblocks the recvfrom goroutine.
	go func() {
		<-ctx.Done()
		_ = syscall.Close(fd)
	}()

	go func() {
		defer close(out)
		buf := make([]byte, 16*1024)
		for {
			n, _, err := syscall.Recvfrom(fd, buf, 0)
			if err != nil {
				if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
					continue
				}
				if errors.Is(err, syscall.ENOBUFS) {
					logger.Warn("hwmon watcher: uevent buffer overflow, events dropped (periodic rescan will catch up)")
					continue
				}
				// EBADF / closed socket → ctx cancelled or shutdown. Exit
				// without logging an error: this is the normal path.
				if ctx.Err() != nil {
					return
				}
				logger.Warn("hwmon watcher: uevent recv failed, stopping netlink listener (periodic rescan continues)", "err", err)
				return
			}
			msg, ok := parseUevent(buf[:n])
			if !ok {
				continue
			}
			select {
			case out <- msg:
			case <-ctx.Done():
				return
			default:
				// Channel full: the watcher is already processing a burst. A
				// dropped trigger is safe because periodic rescan is the
				// safety net.
			}
		}
	}()

	return out
}
