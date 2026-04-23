//go:build linux

package hidraw

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/sys/unix"
)

// socketpairSock wraps one end of a socketpair as a netlinkSocket.
// Used only in tests — no real AF_NETLINK socket is opened.
type socketpairSock struct {
	f *os.File
}

func (s *socketpairSock) Read(p []byte) (int, error) { return s.f.Read(p) }
func (s *socketpairSock) Close() error               { return s.f.Close() }

// newSocketpairSock creates a socketpair; returns the read-end socket and
// the write-end file. The caller owns the write end and is responsible
// for closing it.
func newSocketpairSock(t *testing.T) (*socketpairSock, *os.File) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	readEnd := os.NewFile(uintptr(fds[0]), "netlink-test-read")
	writeEnd := os.NewFile(uintptr(fds[1]), "netlink-test-write")
	t.Cleanup(func() {
		_ = readEnd.Close()
		_ = writeEnd.Close()
	})
	return &socketpairSock{f: readEnd}, writeEnd
}

// sendUevent writes a NUL-terminated uevent message to the write end of the
// socketpair in the NETLINK_KOBJECT_UEVENT wire format:
//
//	<action>@<devpath>\0KEY=VALUE\0KEY=VALUE\0...
func sendUevent(t *testing.T, w *os.File, action, devName string, extra map[string]string) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString(action + "@/devices/virtual/hidraw/" + devName + "\x00")
	sb.WriteString("SUBSYSTEM=hidraw\x00")
	sb.WriteString("DEVNAME=" + devName + "\x00")
	for k, v := range extra {
		sb.WriteString(k + "=" + v + "\x00")
	}
	if _, err := io.WriteString(w, sb.String()); err != nil {
		t.Fatalf("write uevent: %v", err)
	}
}

// TestWatch_ContextCancelTerminates verifies RULE-HIDRAW-05:
// Watch must return, close its socket, and leave no goroutines within 100ms
// of ctx.Done(). goleak.VerifyNone catches any leaked goroutines.
func TestWatch_ContextCancelTerminates(t *testing.T) {
	defer goleak.VerifyNone(t)

	sock, w := newSocketpairSock(t)
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := watchWith(ctx, nil, sock, t.TempDir())
	if err != nil {
		t.Fatalf("watchWith: %v", err)
	}

	// Write side must be closed for graceful shutdown (avoids stray goroutine
	// waiting on the write-end cleanup).
	_ = w.Close()

	cancel()

	// Channel must close within 100ms.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Watch channel did not close within 100ms of ctx cancel")
	}
}

// TestWatch_EmitsAddAndRemoveEvents verifies that Watch emits the correct
// Event kinds when synthetic uevents are received.
func TestWatch_EmitsAddAndRemoveEvents(t *testing.T) {
	defer goleak.VerifyNone(t)

	// Build a fake sysfs root with one USB hidraw device so that the
	// "add" uevent can be resolved via enumerateFrom.
	sysRoot := buildFakeSysfs(t, []fakeHidrawDev{
		{name: "hidraw0", busType: 0x0003, vid: 0x1b1c, pid: 0x0c32},
	})

	sock, w := newSocketpairSock(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := watchWith(ctx, nil, sock, sysRoot)
	if err != nil {
		t.Fatalf("watchWith: %v", err)
	}

	// Emit an "add" uevent for hidraw0.
	sendUevent(t, w, "add", "hidraw0", nil)

	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before first event")
		}
		if ev.Kind != Add {
			t.Errorf("first event Kind = %v, want Add", ev.Kind)
		}
		if ev.Info.Path != "/dev/hidraw0" {
			t.Errorf("first event Path = %q, want /dev/hidraw0", ev.Info.Path)
		}
	case <-time.After(time.Second):
		t.Fatal("no Add event within 1s")
	}

	// Emit a "remove" uevent for hidraw0.
	sendUevent(t, w, "remove", "hidraw0", nil)

	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before remove event")
		}
		if ev.Kind != Remove {
			t.Errorf("second event Kind = %v, want Remove", ev.Kind)
		}
		if ev.Info.Path != "/dev/hidraw0" {
			t.Errorf("second event Path = %q, want /dev/hidraw0", ev.Info.Path)
		}
	case <-time.After(time.Second):
		t.Fatal("no Remove event within 1s")
	}

	_ = w.Close()
}
