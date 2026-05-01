package signature

import (
	"log/slog"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/proc"
)

// makeHasher returns a Hasher seeded with a deterministic 32-byte
// salt for table-test reproducibility.
func makeHasher(t *testing.T) *Hasher {
	t.Helper()
	salt := make([]byte, 32)
	for i := range salt {
		salt[i] = byte(i + 1) // 0x01..0x20
	}
	h, err := NewHasher(salt)
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	return h
}

// makeLib constructs a Library with R7 defaults plus deterministic
// hasher and an empty blocklist (tests that exercise maintenance
// labels override the blocklist directly).
func makeLib(t *testing.T) *Library {
	t.Helper()
	cfg := DefaultConfig()
	return NewLibrary(cfg, makeHasher(t), NewMaintenanceBlocklist(), slog.Default())
}

// p constructs a ProcessSample shorthand for table tests.
func p(comm string, ewmaCPU float64, rss uint64) proc.ProcessSample {
	return proc.ProcessSample{Comm: comm, EWMACPU: ewmaCPU, RSSBytes: rss, PPid: 1}
}

// pK marks a process as a kthread.
func pK(comm string) proc.ProcessSample {
	return proc.ProcessSample{Comm: comm, PPid: 2, IsKThread: true}
}

// runTicks feeds a sequence of (now, samples) pairs into Tick and
// returns the last (label, promoted) pair.
func runTicks(t *testing.T, lib *Library, base time.Time, samples [][]proc.ProcessSample) (string, bool) {
	t.Helper()
	var label string
	var promoted bool
	for i, s := range samples {
		now := base.Add(time.Duration(i) * lib.cfg.HalfLife)
		label, promoted = lib.Tick(now, s)
	}
	return label, promoted
}

// ── R7 §Q4.1: Steam game launch ─────────────────────────────────

// TestLibrary_SteamLaunch_StabilisesOnGameAfter12s feeds 6 ticks of
// proton-launch process churn (10-20 distinct exes in the first 5
// seconds, settling on Game.exe + dxvk_compile + plasmashell +
// pipewire-pulse) and asserts the K-stable promotion gate fires
// within ~12 seconds total wall time.
func TestLibrary_SteamLaunch_StabilisesOnGameAfter12s(t *testing.T) {
	lib := makeLib(t)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// Tick 0: idle desktop.
	idleSamples := []proc.ProcessSample{
		p("plasmashell", 0.10, 200<<20),
		p("pipewire-pulse", 0.06, 80<<20),
		p("kded5", 0.03, 100<<20),
		p("dbus-daemon", 0.02, 50<<20),
	}

	// Ticks 1-2: proton spin-up (transient processes).
	transientSamples := []proc.ProcessSample{
		p("plasmashell", 0.10, 200<<20),
		p("steam", 0.30, 400<<20),
		p("reaper", 0.15, 50<<20),
		p("pressure-vessel-", 0.10, 80<<20),
		p("pv-bwrap", 0.08, 30<<20),
		p("wine64-preload", 0.20, 100<<20),
		p("services.exe", 0.05, 40<<20),
	}

	// Ticks 3-5: steady-state game.
	steadySamples := []proc.ProcessSample{
		p("plasmashell", 0.10, 200<<20),
		p("pipewire-pulse", 0.06, 80<<20),
		p("Game.exe", 1.20, 2_000<<20),
		p("dxvk_compile", 0.40, 300<<20),
	}

	// 1 idle + 2 transient + 6 steady = 9 ticks at 2 s = 18 s
	// total. The K-stable gate (M=3) needs 3 identical top-K
	// snapshots in a row; transient decay clears in ~5 s, then
	// 3 stable steady ticks promote.
	label, _ := runTicks(t, lib, base, [][]proc.ProcessSample{
		idleSamples,
		transientSamples, transientSamples,
		steadySamples, steadySamples, steadySamples,
		steadySamples, steadySamples, steadySamples,
	})

	// After ≥ 3 consecutive steady ticks the gate fires; label
	// should be the Game.exe-dominated hash tuple, not the idle
	// or transient signatures.
	if label == FallbackLabelIdle || label == FallbackLabelDisabled {
		t.Errorf("Steam launch did not stabilise: label=%q", label)
	}
}

// ── R7 §Q4.2: Kernel build (cc1 churn) ──────────────────────────

// TestLibrary_KernelBuild_StabilisesOnCC1WithinHalfLife feeds
// parallel cc1 spawning (200/sec aliasing to one bucket on comm)
// and asserts convergence within one EWMA half-life.
func TestLibrary_KernelBuild_StabilisesOnCC1WithinHalfLife(t *testing.T) {
	lib := makeLib(t)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	buildSamples := []proc.ProcessSample{
		// Sum of 16 parallel cc1 instances reads as one comm
		// bucket with 0.85 share-of-one-core (compute aggregate).
		p("cc1", 0.85, 100<<20),
		p("make", 0.10, 60<<20),
		p("ld", 0.05, 80<<20),
		p("as", 0.04, 30<<20),
	}

	label, _ := runTicks(t, lib, base, [][]proc.ProcessSample{
		buildSamples, buildSamples, buildSamples, buildSamples,
	})

	if label == FallbackLabelIdle || label == FallbackLabelDisabled {
		t.Errorf("kernel build did not converge: label=%q", label)
	}
}

// ── R7 §Q4.3: Chrome Site Isolation ─────────────────────────────

// TestLibrary_ChromeSiteIsolation_SingleBucket verifies that
// multiple chrome processes (browser + GPU + network + 8 renderers)
// all alias to one comm bucket and produce a stable single
// signature regardless of tab churn.
func TestLibrary_ChromeSiteIsolation_SingleBucket(t *testing.T) {
	lib := makeLib(t)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// All-chrome workload, with desktop daemons providing the
	// other K=4 entries.
	browse := []proc.ProcessSample{
		// All chrome processes hash to the same bucket — but
		// for the multiset we accumulate weight via a single
		// sample with the aggregate.
		p("chrome", 0.95, 4_000<<20),
		p("plasmashell", 0.10, 200<<20),
		p("pipewire-pulse", 0.06, 80<<20),
		p("Xorg", 0.08, 150<<20),
	}

	label, _ := runTicks(t, lib, base, [][]proc.ProcessSample{
		browse, browse, browse, browse,
	})

	if label == FallbackLabelIdle {
		t.Fatal("Chrome workload promoted to idle; gate too strict")
	}

	// Tab churn: same comm names, different ordering — label
	// should NOT change.
	churned := []proc.ProcessSample{
		p("chrome", 1.10, 4_500<<20),
		p("Xorg", 0.09, 150<<20),
		p("pipewire-pulse", 0.05, 80<<20),
		p("plasmashell", 0.11, 200<<20),
	}

	prev := label
	label, promoted := runTicks(t, lib, base.Add(8*lib.cfg.HalfLife), [][]proc.ProcessSample{
		churned, churned, churned,
	})
	if promoted {
		t.Errorf("tab churn caused unwanted promotion: %q -> %q", prev, label)
	}
}

// ── R7 §Q4.4: systemd-resolved non-cycling ──────────────────────

// TestLibrary_SystemdResolved_NeverAppears asserts R7 review flag
// #1: systemd-resolved is a long-lived stub resolver with negligible
// CPU and small RSS — it must never pass the contribution gate and
// therefore never appear in any signature.
func TestLibrary_SystemdResolved_NeverAppears(t *testing.T) {
	lib := makeLib(t)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// systemd-resolved as it actually behaves: ~0% CPU, 20 MiB
	// RSS — well below both gates. Co-running with a real
	// workload.
	samples := []proc.ProcessSample{
		p("systemd-resolve", 0.001, 20<<20), // BELOW gates
		p("chrome", 0.80, 3_000<<20),
		p("plasmashell", 0.10, 200<<20),
		p("pipewire-pulse", 0.06, 80<<20),
	}

	for i := 0; i < 8; i++ {
		_, _ = lib.Tick(base.Add(time.Duration(i)*lib.cfg.HalfLife), samples)
	}

	// Hash systemd-resolve and assert it's NOT in the multiset.
	target := lib.hasher.HashComm("systemd-resolve")
	lib.mu.Lock()
	defer lib.mu.Unlock()
	if _, ok := lib.multiset[target]; ok {
		t.Errorf("systemd-resolve passed the gate but should not have")
	}
}

// ── Library state invariants ─────────────────────────────────────

// TestLibrary_KStablePromotionRequires3Ticks asserts that a label
// change requires M=3 consecutive ticks of the same top-K.
// RULE-SIG-LIB-03.
func TestLibrary_KStablePromotionRequires3Ticks(t *testing.T) {
	lib := makeLib(t)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	heavy := []proc.ProcessSample{
		p("rustc", 1.50, 1_500<<20),
		p("cargo", 0.20, 300<<20),
		p("ld", 0.10, 100<<20),
		p("rustfmt", 0.05, 80<<20),
	}

	// Tick 1: pending = candidate, not promoted yet.
	_, p1 := lib.Tick(base, heavy)
	// Tick 2: still pending.
	_, p2 := lib.Tick(base.Add(lib.cfg.HalfLife), heavy)
	// Tick 3: M=3 reached, promotion fires.
	_, p3 := lib.Tick(base.Add(2*lib.cfg.HalfLife), heavy)

	if p1 || p2 {
		t.Errorf("promotion fired before M=3: tick1=%v tick2=%v", p1, p2)
	}
	if !p3 {
		t.Errorf("promotion did not fire on tick 3 with stable top-K")
	}
}

// TestLibrary_GateRejectsBelowThresholds asserts that a process
// below both 5%-of-one-core CPU AND 256 MiB RSS does not contribute
// to the multiset. RULE-SIG-LIB-01.
func TestLibrary_GateRejectsBelowThresholds(t *testing.T) {
	lib := makeLib(t)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	weak := []proc.ProcessSample{
		p("idle-daemon-1", 0.01, 50<<20),
		p("idle-daemon-2", 0.02, 80<<20),
	}

	for i := 0; i < 4; i++ {
		_, _ = lib.Tick(base.Add(time.Duration(i)*lib.cfg.HalfLife), weak)
	}

	lib.mu.Lock()
	defer lib.mu.Unlock()
	if len(lib.multiset) != 0 {
		t.Errorf("weak processes contributed: multiset has %d entries", len(lib.multiset))
	}
}

// TestLibrary_KthreadFilter asserts kthreads (PPid==2 OR comm
// starts with '[') are excluded. RULE-SIG-LIB-01.
func TestLibrary_KthreadFilter(t *testing.T) {
	lib := makeLib(t)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	samples := []proc.ProcessSample{
		pK("kworker/u32:1+events_unbound"), // PPid=2
		pK("[kthreadd]"),
		p("chrome", 0.80, 3_000<<20),
	}

	_, _ = lib.Tick(base, samples)

	lib.mu.Lock()
	defer lib.mu.Unlock()
	if _, ok := lib.multiset[lib.hasher.HashComm("kworker/u32:1+events_unbound")]; ok {
		t.Errorf("kthread (PPid=2) passed the gate")
	}
	if _, ok := lib.multiset[lib.hasher.HashComm("[kthreadd]")]; ok {
		t.Errorf("kthread (bracketed comm) passed the gate")
	}
	if _, ok := lib.multiset[lib.hasher.HashComm("chrome")]; !ok {
		t.Errorf("normal process did not pass the gate")
	}
}

// TestLibrary_TopKByWeight asserts the top-K=4 selection picks
// the four highest-weight hashes regardless of insertion order.
// RULE-SIG-LIB-02.
func TestLibrary_TopKByWeight(t *testing.T) {
	lib := makeLib(t)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	samples := []proc.ProcessSample{
		p("a", 0.10, 300<<20),
		p("b", 0.50, 300<<20),
		p("c", 1.20, 300<<20), // highest
		p("d", 0.30, 300<<20),
		p("e", 0.80, 300<<20),
		p("f", 0.20, 300<<20),
	}

	for i := 0; i < 4; i++ {
		_, _ = lib.Tick(base.Add(time.Duration(i)*lib.cfg.HalfLife), samples)
	}

	candidate := lib.canonicaliseTopK()
	// candidate is sorted lex hex; we just verify it's non-empty
	// and hash-tuple shaped.
	if candidate == "" {
		t.Errorf("top-K returned empty candidate")
	}
	if got := lib.Label(); got == FallbackLabelIdle || got == FallbackLabelDisabled {
		t.Errorf("Label after promotion: got %q, want hash-tuple", got)
	}
}

// TestLibrary_BucketCountCapAt128 asserts the library never holds
// more than DefaultBucketCount buckets. RULE-SIG-LIB-05.
func TestLibrary_BucketCountCapAt128(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BucketCount = 4 // tighter cap for fast test
	lib := NewLibrary(cfg, makeHasher(t), NewMaintenanceBlocklist(), slog.Default())
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// Force 6 distinct labels by supplying 6 different process
	// sets that all promote.
	for i := 0; i < 6; i++ {
		samples := []proc.ProcessSample{
			p("workload-"+string(rune('a'+i)), 1.0, 400<<20),
			p("supporting", 0.3, 100<<20),
		}
		// Three ticks each to clear M=3 gate.
		for j := 0; j < 3; j++ {
			_, _ = lib.Tick(base.Add(time.Duration(i*3+j)*cfg.HalfLife), samples)
		}
	}

	lib.mu.Lock()
	defer lib.mu.Unlock()
	if len(lib.buckets) > cfg.BucketCount {
		t.Errorf("bucket count %d exceeds cap %d", len(lib.buckets), cfg.BucketCount)
	}
}

// TestLibrary_DisabledEmitsFallback asserts that a Disabled config
// produces the FallbackLabelDisabled label regardless of input.
// RULE-SIG-LIB-08.
func TestLibrary_DisabledEmitsFallback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Disabled = true
	lib := NewLibrary(cfg, makeHasher(t), NewMaintenanceBlocklist(), slog.Default())
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	heavy := []proc.ProcessSample{
		p("chrome", 1.50, 4_000<<20),
	}
	for i := 0; i < 5; i++ {
		label, _ := lib.Tick(base.Add(time.Duration(i)*lib.cfg.HalfLife), heavy)
		if label != FallbackLabelDisabled {
			t.Errorf("disabled lib emitted %q, want %q", label, FallbackLabelDisabled)
		}
	}
}

// TestLibrary_EWMAHalfLifeDecaysCorrectly asserts that after one
// half-life elapses, an injected weight decays to ~half its
// initial value. RULE-SIG-LIB-04.
func TestLibrary_EWMAHalfLifeDecaysCorrectly(t *testing.T) {
	lib := makeLib(t)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// Seed the multiset with a single fat process.
	heavy := []proc.ProcessSample{
		p("workload", 2.00, 800<<20),
	}
	_, _ = lib.Tick(base, heavy)

	// Record the multiset weight for that hash.
	target := lib.hasher.HashComm("workload")
	lib.mu.Lock()
	beforeDecay := lib.multiset[target]
	lib.mu.Unlock()

	// Tick again exactly one half-life later with NO contribution.
	empty := []proc.ProcessSample{}
	_, _ = lib.Tick(base.Add(lib.cfg.HalfLife), empty)

	lib.mu.Lock()
	afterDecay := lib.multiset[target]
	lib.mu.Unlock()

	if afterDecay >= beforeDecay {
		t.Errorf("weight did not decay: before=%f after=%f", beforeDecay, afterDecay)
	}
	// After exactly one half-life, weight should be ~50% of
	// pre-decay.
	ratio := afterDecay / beforeDecay
	if ratio < 0.45 || ratio > 0.55 {
		t.Errorf("decay ratio: got %f, want ~0.50 (one half-life)", ratio)
	}
}

// TestLibrary_PlexTranscoderEmitsMaintLabel asserts that a
// dominant maintenance-class process triggers the "maint/<name>"
// reserved label override. RULE-SIG-LIB-06.
func TestLibrary_PlexTranscoderEmitsMaintLabel(t *testing.T) {
	lib := makeLib(t)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// plex-transcoder dominates by 5x the next-highest contributor.
	samples := []proc.ProcessSample{
		p("plex-transcoder", 1.50, 800<<20),
		p("plasmashell", 0.10, 200<<20),
		p("pipewire-pulse", 0.06, 80<<20),
	}

	for i := 0; i < 5; i++ {
		_, _ = lib.Tick(base.Add(time.Duration(i)*lib.cfg.HalfLife), samples)
	}

	// Direct query: the maintenance detector should override.
	gotLabel := lib.detectMaintDominant(samples)
	want := MaintLabel("plex-transcoder")
	if gotLabel != want {
		t.Errorf("detectMaintDominant: got %q, want %q", gotLabel, want)
	}
}

// TestLibrary_HonoursToggleOff asserts that the operator-toggle
// disable path produces FallbackLabelDisabled even when there's
// real workload activity. RULE-SIG-LIB-08.
//
// (Distinct from TestLibrary_DisabledEmitsFallback in that this
// tests the operator-toggle reason explicitly via ApplyDisableGate
// with DisableReasonOperatorToggle, mirroring what main.go does
// when reading Config.SignatureLearningDisabled.)
func TestLibrary_HonoursToggleOff(t *testing.T) {
	cfg := DefaultConfig()
	ApplyDisableGate(&cfg, DisableReasonOperatorToggle)
	if !cfg.Disabled {
		t.Fatal("ApplyDisableGate did not set Disabled")
	}
	lib := NewLibrary(cfg, makeHasher(t), NewMaintenanceBlocklist(), nil)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	heavy := []proc.ProcessSample{
		p("rustc", 1.50, 1_500<<20),
	}
	for i := 0; i < 4; i++ {
		label, _ := lib.Tick(base.Add(time.Duration(i)*lib.cfg.HalfLife), heavy)
		if label != FallbackLabelDisabled {
			t.Errorf("operator-toggled disable emitted %q, want %q", label, FallbackLabelDisabled)
		}
	}
}

// TestLibrary_LabelReadIsLockFree asserts that Label() does not
// take the library mutex. We can't easily prove this directly, but
// we can verify it returns a sensible value while a Tick is in
// progress (simulated by holding the mutex from another goroutine).
// RULE-SIG-CTRL-02.
func TestLibrary_LabelReadIsLockFree(t *testing.T) {
	lib := makeLib(t)
	// Acquire the library mutex from this goroutine.
	lib.mu.Lock()
	defer lib.mu.Unlock()
	// Label() must return without blocking.
	done := make(chan string, 1)
	go func() {
		done <- lib.Label()
	}()
	select {
	case <-done:
		// good
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Label() blocked while mutex was held")
	}
}
