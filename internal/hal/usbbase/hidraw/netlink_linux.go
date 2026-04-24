//go:build linux

package hidraw

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// netlinkSocket is the I/O surface injected into watchWith.
// The production implementation wraps an os.File backed by an AF_NETLINK
// socket; tests inject a socketpair-backed fake (see netlink_test.go).
type netlinkSocket interface {
	Read(p []byte) (int, error)
	Close() error
}

// Watch emits add/remove Events for USB hidraw devices matching matchers.
// Events are filtered to SUBSYSTEM=hidraw. The returned channel is closed
// within 100ms of ctx.Done() (RULE-HIDRAW-05).
//
// If the caller has no matchers, all USB hidraw events are emitted.
// If the netlink socket cannot be opened (e.g. unprivileged container),
// Watch returns a wrapped error; the caller should fall back to polling.
func Watch(ctx context.Context, matchers []Matcher) (<-chan Event, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC,
		unix.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return nil, fmt.Errorf("hidraw: netlink socket: %w", err)
	}
	sa := &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: 1}
	if err := unix.Bind(fd, sa); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("hidraw: netlink bind: %w", err)
	}
	f := os.NewFile(uintptr(fd), "netlink-kobject-uevent")
	return watchWith(ctx, matchers, &netlinkFile{f: f}, "/sys/class/hidraw")
}

// netlinkFile wraps an *os.File as a netlinkSocket.
type netlinkFile struct{ f *os.File }

func (s *netlinkFile) Read(p []byte) (int, error) { return s.f.Read(p) }
func (s *netlinkFile) Close() error               { return s.f.Close() }

// watchWith is the testable core of Watch. It spawns two goroutines:
//   - reader: reads from sock, emits events on out.
//   - closer: waits for ctx.Done() or reader exit, then closes sock.
//
// Both goroutines always terminate; goleak.VerifyNone passes (RULE-HIDRAW-05).
func watchWith(ctx context.Context, matchers []Matcher, sock netlinkSocket, sysRoot string) (<-chan Event, error) {
	out := make(chan Event, 16)

	var closeOnce sync.Once
	closeSock := func() { closeOnce.Do(func() { _ = sock.Close() }) }

	readerDone := make(chan struct{})

	go func() {
		defer close(out)
		defer close(readerDone)
		defer closeSock()

		buf := make([]byte, 16*1024)
		for {
			n, err := sock.Read(buf)
			if err != nil || n == 0 {
				return
			}
			ev, ok := parseUeventMsg(buf[:n], matchers, sysRoot)
			if !ok {
				continue
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			closeSock()
		case <-readerDone:
		}
	}()

	return out, nil
}

// parseUeventMsg decodes one NETLINK_KOBJECT_UEVENT message and returns an
// Event if the message is for the hidraw subsystem and matches matchers.
// The message format is NUL-separated tokens; the first is "<action>@<devpath>".
func parseUeventMsg(msg []byte, matchers []Matcher, sysRoot string) (Event, bool) {
	tokens := bytes.Split(msg, []byte{0})
	if len(tokens) < 2 {
		return Event{}, false
	}

	// First token: "<action>@<devpath>"
	action, devpath, ok2 := strings.Cut(string(tokens[0]), "@")
	if !ok2 {
		return Event{}, false
	}

	// Remaining tokens: KEY=VALUE pairs.
	var subsystem, devName string
	for _, tok := range tokens[1:] {
		s := string(tok)
		switch {
		case strings.HasPrefix(s, "SUBSYSTEM="):
			subsystem = s[len("SUBSYSTEM="):]
		case strings.HasPrefix(s, "DEVNAME="):
			devName = s[len("DEVNAME="):]
		}
	}

	if subsystem != "hidraw" {
		return Event{}, false
	}

	// Derive device name from devpath if DEVNAME is absent.
	if devName == "" {
		// devpath ends with "hidraw/hidrawN"; take the last segment.
		idx := strings.LastIndexByte(devpath, '/')
		if idx >= 0 {
			devName = devpath[idx+1:]
		}
	}
	if devName == "" {
		return Event{}, false
	}

	devNodePath := "/dev/" + devName

	switch action {
	case "add":
		// Re-enumerate to resolve VID/PID; the uevent alone does not carry them.
		infos, err := enumerateFrom(sysRoot, matchers)
		if err != nil {
			slog.Warn("hidraw: watch: enumerate failed on add", "dev", devName, "err", err)
			return Event{}, false
		}
		for _, info := range infos {
			if info.Path == devNodePath {
				return Event{Kind: Add, Info: info}, true
			}
		}
		return Event{}, false

	case "remove":
		return Event{Kind: Remove, Info: DeviceInfo{Path: devNodePath}}, true

	default:
		return Event{}, false
	}
}
