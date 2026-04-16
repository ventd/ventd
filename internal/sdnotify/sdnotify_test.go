package sdnotify

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// receiveOne starts a unixgram listener at path and sends every
// received datagram on out. Closes out when the listener is closed.
func receiveOne(t *testing.T, path string) (out chan string, cleanup func()) {
	t.Helper()
	addr, err := net.ResolveUnixAddr("unixgram", path)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatal(err)
	}
	out = make(chan string, 16)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			out <- string(buf[:n])
		}
	}()
	return out, func() {
		_ = conn.Close()
		wg.Wait()
		close(out)
	}
}

func TestNotify_NoEnvIsNoOp(t *testing.T) {
	t.Setenv(envSocket, "")
	if err := Notify(Ready); err != nil {
		t.Fatalf("Notify with empty env: %v", err)
	}
}

func TestNotify_SendsReady(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "notify")
	out, cleanup := receiveOne(t, sock)
	defer cleanup()

	t.Setenv(envSocket, sock)
	if err := Notify(Ready); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	select {
	case got := <-out:
		if got != "READY=1\n" {
			t.Errorf("got %q, want %q", got, "READY=1\n")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no datagram received")
	}
}

func TestNotify_MultipleStatesInOneCall(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "notify")
	out, cleanup := receiveOne(t, sock)
	defer cleanup()

	t.Setenv(envSocket, sock)
	if err := Notify(Ready, "STATUS=warming up"); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	// Each Notify state is sent as its own datagram.
	wantSet := map[string]bool{"READY=1\n": false, "STATUS=warming up\n": false}
	deadline := time.After(500 * time.Millisecond)
	for i := 0; i < 2; i++ {
		select {
		case got := <-out:
			if _, ok := wantSet[got]; !ok {
				t.Errorf("unexpected datagram: %q", got)
			}
			wantSet[got] = true
		case <-deadline:
			t.Fatalf("timed out after %d datagrams", i)
		}
	}
	for line, seen := range wantSet {
		if !seen {
			t.Errorf("missing datagram: %q", line)
		}
	}
}

func TestNotify_BadSocketReturnsError(t *testing.T) {
	t.Setenv(envSocket, "/nonexistent-test-socket-xyz")
	if err := Notify(Ready); err == nil {
		t.Fatal("expected dial error, got nil")
	}
}

func TestWatchdogInterval_UnsetEnvReturnsZero(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "")
	if got := WatchdogInterval(); got != 0 {
		t.Errorf("got %v, want 0", got)
	}
}

func TestWatchdogInterval_HalvesUSecValue(t *testing.T) {
	cases := []struct {
		env  string
		want time.Duration
	}{
		{"2000000", time.Second},           // 2s → 1s
		{"500000", 250 * time.Millisecond}, // 500ms → 250ms
		{"60000000", 30 * time.Second},     // 60s → 30s
		{"1", 0},                           // 1us / 2 = 0us → 0 duration
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("WATCHDOG_USEC", tc.env)
			got := WatchdogInterval()
			if got != tc.want {
				t.Errorf("WATCHDOG_USEC=%s: got %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

func TestWatchdogInterval_NonNumericReturnsZero(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "two-million")
	if got := WatchdogInterval(); got != 0 {
		t.Errorf("non-numeric WATCHDOG_USEC: got %v, want 0", got)
	}
}

func TestStartHeartbeat_NoIntervalReturnsNoOpStop(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "")
	stop := StartHeartbeat()
	stop() // must not panic, must not block
	stop() // must be safe to call twice in real defer chains? — single-call contract documented; one call is sufficient
}

func TestStartHeartbeat_PingsAtHalfInterval(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "notify")
	out, cleanup := receiveOne(t, sock)
	defer cleanup()

	t.Setenv(envSocket, sock)
	// 200ms WATCHDOG_USEC (200000 microseconds) → 100ms heartbeat.
	t.Setenv("WATCHDOG_USEC", "200000")

	stop := StartHeartbeat()
	defer stop()

	// Expect at least 2 WATCHDOG=1 datagrams in 350ms (3+ ticks).
	deadline := time.After(350 * time.Millisecond)
	count := 0
	for count < 2 {
		select {
		case got := <-out:
			if got != "WATCHDOG=1\n" {
				t.Errorf("unexpected datagram: %q", got)
			}
			count++
		case <-deadline:
			t.Fatalf("only saw %d heartbeats in 350ms; want >= 2", count)
		}
	}
}

func TestStartHeartbeat_StopHaltsGoroutine(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "notify")
	out, cleanup := receiveOne(t, sock)
	defer cleanup()

	t.Setenv(envSocket, sock)
	t.Setenv("WATCHDOG_USEC", "100000") // 100ms → 50ms heartbeat

	stop := StartHeartbeat()

	// Drain at least one heartbeat to confirm the goroutine ran.
	select {
	case <-out:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("no heartbeat seen before stop")
	}

	stop()

	// After stop, no further heartbeats should arrive within 200ms.
	select {
	case got := <-out:
		t.Errorf("heartbeat after stop: %q", got)
	case <-time.After(200 * time.Millisecond):
		// expected
	}
}

// TestNotify_StoppingMessage is a smoke for the documented Stopping
// constant — same wire format as Ready, just included so the const is
// exercised end-to-end.
func TestNotify_StoppingMessage(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "notify")
	out, cleanup := receiveOne(t, sock)
	defer cleanup()

	t.Setenv(envSocket, sock)
	_ = Notify(Stopping)

	select {
	case got := <-out:
		if got != "STOPPING=1\n" {
			t.Errorf("got %q, want STOPPING=1", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no datagram received")
	}
}

// Compile-time guard: os import is used (for env vars in StartHeartbeat
// path via WatchdogInterval). Suppress unused lint without polluting prod.
var _ = os.Getpid
