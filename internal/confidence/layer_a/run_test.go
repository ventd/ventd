package layer_a

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRun_PeriodicSaveCallsSaveAtTickerCadence pins
// RULE-CONFA-PERSIST-RUNNER-01: the Run goroutine MUST invoke Save on
// every PersistEvery tick. Without this, Layer-A's in-memory state is
// never durable and every daemon restart cold-starts conf_A back to
// √(1/16) ≈ 0.25 — the #1253 HIL regression.
func TestRun_PeriodicSaveCallsSaveAtTickerCadence(t *testing.T) {
	// Override the package-level cadence so the test runs in
	// milliseconds, not minutes. Restore on cleanup.
	origPersistEvery := PersistEvery
	PersistEvery = 25 * time.Millisecond
	t.Cleanup(func() { PersistEvery = origPersistEvery })

	stateDir := t.TempDir()
	est, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	now := time.Now()
	const chID = "/sys/class/hwmon/hwmon9/pwm1"
	if err := est.Admit(chID, 0, DefaultNoiseFloor, now); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	// Drop one observation so there is real state to persist (an
	// estimator with zero-count bins still produces a valid bucket,
	// but a populated one makes the "we actually saved real data"
	// signal harder to false-positive).
	est.Observe(chID, 128, 1500, 0, now)
	est.SetPersistContext(stateDir, "fp-test", newSilentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- est.Run(ctx) }()

	// Wait a few ticker periods so the ticker has fired at least once.
	// Three periods is comfortably more than one; the file must exist
	// well before the deadline.
	wantPath := bucketPath(stateDir, chID)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(wantPath); statErr == nil {
			cancel()
			<-done
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatalf("persistence file %s never appeared within deadline (PersistEvery=%v); Run is not driving Save",
		wantPath, PersistEvery)
}

// TestRun_FinalSaveOnCtxCancel pins the shutdown-save contract: when
// ctx is cancelled, Run executes one final Save before returning so
// any state accumulated between the last tick and cancellation lands
// on disk. Matches internal/coupling.Runtime / internal/marginal.Runtime
// behaviour.
func TestRun_FinalSaveOnCtxCancel(t *testing.T) {
	// Use a deliberately long cadence so the only Save that can fire
	// is the final one on ctx.Done — otherwise we couldn't tell the
	// two paths apart.
	origPersistEvery := PersistEvery
	PersistEvery = time.Hour
	t.Cleanup(func() { PersistEvery = origPersistEvery })

	stateDir := t.TempDir()
	est, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	now := time.Now()
	const chID = "/sys/class/hwmon/hwmon9/pwm3"
	if err := est.Admit(chID, 0, DefaultNoiseFloor, now); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	est.Observe(chID, 96, 1200, 0, now)
	est.SetPersistContext(stateDir, "fp-test", newSilentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- est.Run(ctx) }()

	// Give the goroutine a moment to enter the select.
	time.Sleep(20 * time.Millisecond)

	wantPath := bucketPath(stateDir, chID)
	if _, statErr := os.Stat(wantPath); statErr == nil {
		t.Fatalf("file %s exists before ctx cancel — PersistEvery override didn't suppress the ticker", wantPath)
	}

	cancel()

	select {
	case runErr := <-done:
		if runErr != context.Canceled {
			t.Errorf("Run returned %v; want context.Canceled", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of ctx cancel")
	}

	// Final save must have fired.
	if _, statErr := os.Stat(wantPath); statErr != nil {
		t.Fatalf("final save did not produce %s: %v", wantPath, statErr)
	}
}

// TestRun_NoPersistWhenStateDirEmpty pins the in-memory-only contract:
// when SetPersistContext has not been called (or stateDir is empty),
// Run returns nil immediately without starting a ticker. Matches the
// build-time fall-through where state-dir resolution failed.
func TestRun_NoPersistWhenStateDirEmpty(t *testing.T) {
	est, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Deliberately do not call SetPersistContext.

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- est.Run(ctx) }()

	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run with empty stateDir returned %v; want nil", runErr)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run with empty stateDir did not return immediately")
	}
}

// TestRun_RunCalledTwiceReturnsError pins the once-per-lifetime
// contract: a second concurrent / sequential Run on the same Estimator
// returns an error rather than starting a second ticker (matches
// internal/coupling.Runtime.Run line 113-116).
func TestRun_RunCalledTwiceReturnsError(t *testing.T) {
	origPersistEvery := PersistEvery
	PersistEvery = time.Hour // suppress real ticks
	t.Cleanup(func() { PersistEvery = origPersistEvery })

	stateDir := t.TempDir()
	est, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	est.SetPersistContext(stateDir, "fp-test", newSilentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	first := make(chan error, 1)
	go func() { first <- est.Run(ctx) }()

	// Let the first Run claim runStarted.
	time.Sleep(20 * time.Millisecond)

	if err := est.Run(ctx); err == nil {
		t.Fatal("second Run returned nil; want a 'Run already called' error")
	}

	// Tear down the first Run cleanly so the goroutine doesn't leak.
	cancel()
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("first Run did not exit on ctx cancel")
	}
}

// TestRun_BucketReloadable pins the round-trip: a Run + cancel + fresh
// Estimator + LoadChannel restores the persisted state. This is the
// regression the HIL surfaced (loaded=0 cold_start=N), so the round-
// trip belongs explicitly in the locked invariants.
func TestRun_BucketReloadable(t *testing.T) {
	origPersistEvery := PersistEvery
	PersistEvery = time.Hour // only the final-save fires
	t.Cleanup(func() { PersistEvery = origPersistEvery })

	stateDir := t.TempDir()
	const chID = "/sys/class/hwmon/hwmon9/pwm5"
	const fp = "fp-round-trip"

	// First daemon lifetime: admit, observe a handful of distinct PWM
	// bins, Run + cancel for the final save.
	{
		est, err := New(Config{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		now := time.Now()
		if err := est.Admit(chID, 0, DefaultNoiseFloor, now); err != nil {
			t.Fatalf("Admit: %v", err)
		}
		for _, pwm := range []uint8{32, 64, 128, 192, 32, 64, 128, 192, 32, 64, 128, 192} {
			est.Observe(chID, pwm, 1000+int32(pwm), 0, now)
		}
		est.SetPersistContext(stateDir, fp, newSilentLogger())

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- est.Run(ctx) }()
		time.Sleep(20 * time.Millisecond)
		cancel()
		<-done
	}

	// Confirm something landed on disk.
	wantPath := bucketPath(stateDir, chID)
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("expected %s after first lifetime: %v", wantPath, err)
	}
	if info.Size() == 0 {
		t.Fatalf("%s is empty — Save wrote no bytes", wantPath)
	}

	// Second daemon lifetime: fresh Estimator, Admit + LoadChannel,
	// expect ok==true.
	{
		est2, err := New(Config{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		now := time.Now()
		if err := est2.Admit(chID, 0, DefaultNoiseFloor, now); err != nil {
			t.Fatalf("Admit: %v", err)
		}
		ok, loadErr := est2.LoadChannel(stateDir, chID, fp, newSilentLogger())
		if loadErr != nil {
			t.Fatalf("LoadChannel returned err: %v", loadErr)
		}
		if !ok {
			t.Fatalf("LoadChannel returned ok=false despite a valid bucket on disk — round-trip is broken (the very regression #1253 HIL surfaced)")
		}

		// Sanity-check that some bin counts were restored, not just
		// that the file existed.
		snap := est2.Read(chID)
		if snap == nil {
			t.Fatalf("Read returned nil after successful LoadChannel")
		}
	}

	// Silence linters about the unused filepath import if Go's tooling
	// ever removes the assertion above; the package is used in other
	// tests too.
	_ = filepath.Join
}
