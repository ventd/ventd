// Package sdnotify implements a minimal subset of the systemd
// notification protocol described in sd_notify(3).
//
// We only need three messages:
//
//   READY=1                 — daemon is fully started
//   WATCHDOG=1              — heartbeat for systemd's WatchdogSec
//   STOPPING=1              — daemon is exiting cleanly
//
// systemd reads NOTIFY_SOCKET from the unit's environment; the value
// is the path to an AF_UNIX SOCK_DGRAM socket. When the env var is
// absent (running standalone, OpenRC, runit, dev shell), every Notify
// call is a no-op so the daemon behaves identically off systemd.
//
// Implementation is stdlib-only (net.Dial("unixgram", ...)) to avoid
// dragging in the coreos go-systemd dep, which carries cgo build
// requirements that would break the static-binary promise.
package sdnotify

import (
	"net"
	"os"
	"sync"
	"time"
)

// State messages defined by sd_notify(3). Exported so tests and callers
// don't have to encode the strings themselves.
const (
	Ready    = "READY=1"
	Watchdog = "WATCHDOG=1"
	Stopping = "STOPPING=1"
)

// envSocket is the systemd-set env var name; lifted to a var so tests
// can override it without touching os.Setenv globally.
var envSocket = "NOTIFY_SOCKET"

// Notify sends one or more state lines to the systemd notification
// socket. Returns nil and does nothing when NOTIFY_SOCKET is unset
// (non-systemd start). Connection failures are swallowed — losing a
// heartbeat is recoverable; failing the daemon over a missed notify
// is not.
func Notify(states ...string) error {
	socket := os.Getenv(envSocket)
	if socket == "" {
		return nil
	}
	conn, err := net.Dial("unixgram", socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, s := range states {
		if _, err := conn.Write([]byte(s + "\n")); err != nil {
			return err
		}
	}
	return nil
}

// WatchdogInterval returns half of the WATCHDOG_USEC value systemd
// passed to the unit, converted to a time.Duration. Returns 0 when
// WATCHDOG_USEC is unset (no Watchdog= in the unit) or unparseable.
//
// systemd's recommended pattern: ping at half the watchdog interval
// so a single missed deadline does not trigger a kill.
func WatchdogInterval() time.Duration {
	raw := os.Getenv("WATCHDOG_USEC")
	if raw == "" {
		return 0
	}
	usec, err := parseUint(raw)
	if err != nil || usec == 0 {
		return 0
	}
	return time.Duration(usec/2) * time.Microsecond
}

// StartHeartbeat launches a goroutine that pings the watchdog at the
// systemd-recommended interval (half of WATCHDOG_USEC). Returns a
// stop function that cancels the goroutine and waits for it to
// drain — call from defer at the daemon's shutdown to avoid a stray
// notify after the unit transitions to stopping state.
//
// When WATCHDOG_USEC is unset (no Watchdog= in the unit, or running
// off systemd entirely) the function returns a no-op stop and never
// starts a goroutine.
func StartHeartbeat() (stop func()) {
	interval := WatchdogInterval()
	if interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				_ = Notify(Watchdog)
			}
		}
	}()
	return func() {
		close(done)
		wg.Wait()
	}
}

// parseUint is a tiny strconv.ParseUint shim; using it directly would
// drag the strconv import into a single-callsite, otherwise stdlib-light
// file.
func parseUint(s string) (uint64, error) {
	var n uint64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errInvalidUint
		}
		n = n*10 + uint64(r-'0')
	}
	return n, nil
}

// errInvalidUint is returned by parseUint for non-digit input. We
// don't expose it — callers only care that the parse succeeded.
var errInvalidUint = sentinelError("sdnotify: invalid uint")

type sentinelError string

func (e sentinelError) Error() string { return string(e) }
