package main

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/signature"
	"github.com/ventd/ventd/internal/state"
)

// TestSignatureLoadLabels_RestoresPersistedBuckets pins
// RULE-SIG-WIRING-01: the daemon-startup path (runDaemonInternal)
// dispatches the persisted-manifest restore through the named
// loadSignatureState helper. Audit pass-3 (#1075) extracted the
// helper from main.go so the rule binding tests the same code
// path production runs — not a replayed LoadManifest + LoadLabels
// sequence in isolation.
//
// The flow:
//
//  1. Phase 1 — seed: build a fresh library, run one Tick + Save +
//     SaveManifest so the spec-16 KV has at least one persisted
//     bucket and a manifest pointer.
//  2. Phase 2 — restore: build a SECOND empty library, call
//     loadSignatureState (the production helper) against the same
//     KV, assert the bucket map is non-empty.
//
// Without the wiring every daemon restart wipes the operator-visible
// workload history (issue #1035 row 11).
func TestSignatureLoadLabels_RestoresPersistedBuckets(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	st, err := state.Open(dir, logger)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Phase 1: build a library and persist one bucket + manifest.
	{
		hasher, err := signature.NewHasher(make([]byte, 16))
		if err != nil {
			t.Fatalf("NewHasher: %v", err)
		}
		lib := signature.NewLibrary(signature.DefaultConfig(), hasher,
			signature.NewMaintenanceBlocklist(), logger)
		// One Tick with no samples → commits the FallbackLabelIdle
		// bucket; that's a real, persistable bucket.
		lib.Tick(time.Now(), nil)
		if err := lib.Save(st.KV); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if err := lib.SaveManifest(st.KV); err != nil {
			t.Fatalf("SaveManifest: %v", err)
		}
	}

	// Phase 2: a fresh library + the production helper. Without the
	// helper the library starts empty; with it the bucket persisted
	// in Phase 1 is rehydrated.
	hasher2, err := signature.NewHasher(make([]byte, 16))
	if err != nil {
		t.Fatalf("NewHasher 2: %v", err)
	}
	lib2 := signature.NewLibrary(signature.DefaultConfig(), hasher2,
		signature.NewMaintenanceBlocklist(), logger)

	loadSignatureState(lib2, st.KV, logger)

	restored := lib2.Buckets()
	if len(restored) == 0 {
		t.Errorf("Buckets empty after loadSignatureState; wiring broken (issue #1035 row 11)")
	}
}

// TestSignatureLoadLabels_NoManifestIsColdStart pins the
// no-persisted-labels branch: a fresh state directory has no
// manifest, so loadSignatureState logs "no persisted labels; cold
// start" and leaves the empty library untouched. Re-running on a
// host that has never persisted a signature is the canonical
// fresh-install path and MUST NOT error or mutate the library.
func TestSignatureLoadLabels_NoManifestIsColdStart(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	st, err := state.Open(dir, logger)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	hasher, err := signature.NewHasher(make([]byte, 16))
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	lib := signature.NewLibrary(signature.DefaultConfig(), hasher,
		signature.NewMaintenanceBlocklist(), logger)

	loadSignatureState(lib, st.KV, logger)

	if got := len(lib.Buckets()); got != 0 {
		t.Errorf("Buckets()=%d after cold-start; want 0", got)
	}
}

// TestSignatureLoadLabels_NilArgsAreNoOp pins the defence-in-depth
// nil guards: a nil library OR nil KV makes the helper a clean
// no-op. The production call site gates on a non-nil library AND
// non-nil state; the helper's own guards exist so test scaffolding
// and pre-smart-mode hosts don't crash.
func TestSignatureLoadLabels_NilArgsAreNoOp(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// nil sigLib — no panic, no error path observable.
	loadSignatureState(nil, nil, logger)

	// nil KV with real library — no panic, library stays empty.
	hasher, err := signature.NewHasher(make([]byte, 16))
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	lib := signature.NewLibrary(signature.DefaultConfig(), hasher,
		signature.NewMaintenanceBlocklist(), logger)

	loadSignatureState(lib, nil, logger)

	if got := len(lib.Buckets()); got != 0 {
		t.Errorf("Buckets()=%d after nil-KV call; want 0", got)
	}
}
