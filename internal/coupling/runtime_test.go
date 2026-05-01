package coupling

import (
	"context"
	"log/slog"
	"runtime"
	"testing"
	"time"
)

// TestRuntime_OneGoroutinePerChannel — RULE-CPL-RUNTIME-01.
//
// Verify that Run starts exactly len(shards) goroutines, NOT one
// per goroutine-per-shard or one per ticker.
func TestRuntime_OneGoroutinePerChannel(t *testing.T) {
	dir := t.TempDir()
	rt := NewRuntime(dir, "fp", slog.Default())

	for i := 0; i < 3; i++ {
		s, err := New(DefaultConfig("ch"+itoa(i), 1))
		if err != nil {
			t.Fatal(err)
		}
		if err := rt.AddShard(s); err != nil {
			t.Fatal(err)
		}
	}

	pre := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = rt.Run(ctx)
		close(done)
	}()

	// Give Run time to start its goroutines.
	time.Sleep(50 * time.Millisecond)
	mid := runtime.NumGoroutine()

	delta := mid - pre
	if delta < 3 {
		t.Errorf("expected ≥3 new goroutines (one per shard), got delta=%d", delta)
	}
	// Tolerance: pre + 3 + ours-helper. Should not be 6+ per shard.
	if delta > 10 {
		t.Errorf("goroutine count grew by %d for 3 shards; suggests goroutine-per-shard explosion", delta)
	}

	cancel()
	<-done
}

// TestRuntime_AddShard_RegistersAndLoads — happy path: AddShard
// returns success, the shard is retrievable, no persisted state
// yet so cold-start.
func TestRuntime_AddShard_RegistersAndLoads(t *testing.T) {
	dir := t.TempDir()
	rt := NewRuntime(dir, "fp", slog.Default())

	s, err := New(DefaultConfig("test/ch", 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.AddShard(s); err != nil {
		t.Fatal(err)
	}
	if got := rt.Shard("test/ch"); got != s {
		t.Errorf("Shard returned different pointer than AddShard'd")
	}
	if got := rt.Shard("missing"); got != nil {
		t.Errorf("Shard(missing) should return nil")
	}
}

// TestRuntime_SnapshotAll_IncludesEveryShard — doctor surface
// integration: SnapshotAll returns one snapshot per shard.
func TestRuntime_SnapshotAll_IncludesEveryShard(t *testing.T) {
	dir := t.TempDir()
	rt := NewRuntime(dir, "fp", slog.Default())

	for i := 0; i < 4; i++ {
		s, err := New(DefaultConfig("ch"+itoa(i), 1))
		if err != nil {
			t.Fatal(err)
		}
		if err := rt.AddShard(s); err != nil {
			t.Fatal(err)
		}
	}

	snaps := rt.SnapshotAll()
	if len(snaps) != 4 {
		t.Errorf("SnapshotAll: got %d, want 4", len(snaps))
	}
}

// itoa avoids strconv import in tests; tiny helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	const digits = "0123456789"
	var b [20]byte
	pos := len(b)
	for n > 0 {
		pos--
		b[pos] = digits[n%10]
		n /= 10
	}
	return string(b[pos:])
}
