package signature

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeKV is an in-memory kvStore for tests.
type fakeKV struct {
	mu   sync.Mutex
	data map[string]map[string][]byte
}

func newFakeKV() *fakeKV { return &fakeKV{data: make(map[string]map[string][]byte)} }

func (f *fakeKV) Get(ns, key string) (any, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.data[ns] == nil {
		return nil, false, nil
	}
	v, ok := f.data[ns][key]
	if !ok {
		return nil, false, nil
	}
	return v, true, nil
}

func (f *fakeKV) Set(ns, key string, value any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.data[ns] == nil {
		f.data[ns] = map[string][]byte{}
	}
	switch v := value.(type) {
	case []byte:
		f.data[ns][key] = v
	case string:
		f.data[ns][key] = []byte(v)
	default:
		return errors.New("fakeKV: unsupported value type")
	}
	return nil
}

func (f *fakeKV) Delete(ns, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.data[ns] != nil {
		delete(f.data[ns], key)
	}
	return nil
}

// TestPersistence_KVRoundTrip asserts that Save → Load reproduces
// the bucket map. RULE-SIG-PERSIST-01.
func TestPersistence_KVRoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	hasher := makeHasher(t)
	kv := newFakeKV()

	// Build a library with two buckets.
	lib1 := NewLibrary(cfg, hasher, NewMaintenanceBlocklist(), nil)
	lib1.buckets["abc|def|ghi|jkl"] = &Bucket{
		Version: 1, HashAlg: HashAlgSipHash24, LabelKind: LabelKindHashTuple,
		FirstSeenUnix: now.Unix() - 7200,
		LastSeenUnix:  now.Unix() - 60,
		HitCount:      42,
		CurrentEWMA:   0.85,
	}
	lib1.buckets["maint/rsync"] = &Bucket{
		Version: 1, HashAlg: HashAlgSipHash24, LabelKind: LabelKindMaint,
		FirstSeenUnix: now.Unix() - 86400,
		LastSeenUnix:  now.Unix() - 30,
		HitCount:      7,
	}

	if err := lib1.Save(kv); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := lib1.SaveManifest(kv); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	// Build a fresh library and Load.
	lib2 := NewLibrary(cfg, hasher, NewMaintenanceBlocklist(), nil)
	labels, err := LoadManifest(kv)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if err := lib2.LoadLabels(kv, labels); err != nil {
		t.Fatalf("LoadLabels: %v", err)
	}

	// Verify bucket fields round-trip.
	if got := len(lib2.buckets); got != 2 {
		t.Errorf("loaded bucket count: got %d, want 2", got)
	}
	if b := lib2.buckets["abc|def|ghi|jkl"]; b == nil || b.HitCount != 42 {
		t.Errorf("hash-tuple bucket lost or corrupted: %+v", b)
	}
	if b := lib2.buckets["maint/rsync"]; b == nil || b.LabelKind != LabelKindMaint {
		t.Errorf("maint bucket lost or corrupted: %+v", b)
	}
}

// TestLibrary_WarmRestartFromKV asserts the library resumes counting
// hits against persisted buckets without losing prior state.
// RULE-SIG-PERSIST-02.
func TestLibrary_WarmRestartFromKV(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	hasher := makeHasher(t)
	kv := newFakeKV()

	lib1 := NewLibrary(cfg, hasher, NewMaintenanceBlocklist(), nil)
	lib1.buckets["fixed-label"] = &Bucket{
		Version: 1, HashAlg: HashAlgSipHash24,
		FirstSeenUnix: now.Unix() - 100,
		LastSeenUnix:  now.Unix() - 50,
		HitCount:      10,
	}
	if err := lib1.Save(kv); err != nil {
		t.Fatal(err)
	}
	if err := lib1.SaveManifest(kv); err != nil {
		t.Fatal(err)
	}

	lib2 := NewLibrary(cfg, hasher, NewMaintenanceBlocklist(), nil)
	labels, _ := LoadManifest(kv)
	if err := lib2.LoadLabels(kv, labels); err != nil {
		t.Fatal(err)
	}

	if b := lib2.buckets["fixed-label"]; b == nil {
		t.Fatal("warm-restart lost the bucket")
	} else if b.HitCount != 10 {
		t.Errorf("HitCount: got %d, want 10", b.HitCount)
	}
}

// seedBucket plants a single bucket + manifest entry under (lib,
// kv) so eviction-sweep tests can drive the wire format directly.
// Returns the wall-clock Unix the bucket was stamped with.
func seedBucket(t *testing.T, kv *fakeKV, label string, lastSeen time.Time, hitCount uint64) int64 {
	t.Helper()
	cfg := DefaultConfig()
	hasher, _ := NewHasher(make([]byte, 16))
	lib := NewLibrary(cfg, hasher, NewMaintenanceBlocklist(), nil)
	lib.buckets[label] = &Bucket{
		Version:       1,
		HashAlg:       HashAlgSipHash24,
		FirstSeenUnix: lastSeen.Unix() - 1,
		LastSeenUnix:  lastSeen.Unix(),
		HitCount:      hitCount,
		CurrentEWMA:   1.0,
	}
	if err := lib.Save(kv); err != nil {
		t.Fatalf("Save %q: %v", label, err)
	}
	if err := lib.SaveManifest(kv); err != nil {
		t.Fatalf("SaveManifest %q: %v", label, err)
	}
	return lastSeen.Unix()
}

// addToManifest appends labels to the existing manifest so a test
// can seed multiple buckets without each Save overwriting the
// previous manifest. Equivalent to running multiple lib.Save() +
// one merged SaveManifest at the end — but easier to read.
func addToManifest(t *testing.T, kv *fakeKV, labels ...string) {
	t.Helper()
	existing, err := LoadManifest(kv)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	merged := append(existing, labels...)
	// dedupe while preserving order
	seen := make(map[string]struct{}, len(merged))
	out := merged[:0]
	for _, l := range merged {
		if _, ok := seen[l]; ok {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	if err := kv.Set(manifestNamespace, manifestKey, joinLabels(out)); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}
}

func joinLabels(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += "\n"
		}
		out += x
	}
	return out
}

// TestEvictPersistedBefore_DropsStaleBucketsAndRewritesManifest
// pins the happy path: bucket older than cutoff is deleted, fresh
// bucket survives, manifest names only the survivor afterwards.
func TestEvictPersistedBefore_DropsStaleBucketsAndRewritesManifest(t *testing.T) {
	kv := newFakeKV()
	now := time.Now()

	// Stale: 45 days old.
	seedBucket(t, kv, "stale-label", now.Add(-45*24*time.Hour), 5)
	// Fresh: 1 day old. Use addToManifest so the second seed's
	// SaveManifest doesn't clobber the first.
	seedBucket(t, kv, "fresh-label", now.Add(-24*time.Hour), 50)
	addToManifest(t, kv, "stale-label", "fresh-label")

	lib := NewLibrary(DefaultConfig(), mustHasher(t), NewMaintenanceBlocklist(), nil)
	cutoff := now.Add(-PersistedEvictionAge)
	deleted, err := lib.EvictPersistedBefore(kv, cutoff)
	if err != nil {
		t.Fatalf("EvictPersistedBefore: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted=%d, want 1", deleted)
	}

	// Stale bucket gone from KV.
	if _, ok, _ := kv.Get(KVNamespace, "stale-label"); ok {
		t.Errorf("stale-label still present after eviction")
	}
	// Fresh bucket still there.
	if _, ok, _ := kv.Get(KVNamespace, "fresh-label"); !ok {
		t.Errorf("fresh-label evicted as collateral damage")
	}
	// Manifest names fresh only.
	survivors, _ := LoadManifest(kv)
	if len(survivors) != 1 || survivors[0] != "fresh-label" {
		t.Errorf("survivors=%v, want [fresh-label]", survivors)
	}
}

// TestEvictPersistedBefore_NoManifestIsNoOp pins the fresh-install
// path: no manifest, no rows, sweep returns (0, nil) without
// mutation.
func TestEvictPersistedBefore_NoManifestIsNoOp(t *testing.T) {
	kv := newFakeKV()
	lib := NewLibrary(DefaultConfig(), mustHasher(t), NewMaintenanceBlocklist(), nil)
	deleted, err := lib.EvictPersistedBefore(kv, time.Now())
	if err != nil {
		t.Fatalf("EvictPersistedBefore on empty KV: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted=%d on empty KV; want 0", deleted)
	}
}

// TestEvictPersistedBefore_CorruptBucketIsDeleted pins the
// corruption-recovery path: a row whose msgpack payload fails to
// decode is unrecoverable; the sweep deletes it and drops it from
// the manifest. Mirrors the audit-noted gap that corrupt rows
// otherwise count against the on-disk budget forever.
func TestEvictPersistedBefore_CorruptBucketIsDeleted(t *testing.T) {
	kv := newFakeKV()
	// Stamp a healthy bucket so the manifest exists.
	seedBucket(t, kv, "healthy-label", time.Now(), 1)
	// Then plant a corrupt row under a second label and add it
	// to the manifest.
	if err := kv.Set(KVNamespace, "corrupt-label", []byte{0xff, 0x00, 0x42}); err != nil {
		t.Fatal(err)
	}
	addToManifest(t, kv, "corrupt-label")

	lib := NewLibrary(DefaultConfig(), mustHasher(t), NewMaintenanceBlocklist(), nil)
	deleted, err := lib.EvictPersistedBefore(kv, time.Now().Add(-PersistedEvictionAge))
	if err != nil {
		t.Fatalf("EvictPersistedBefore: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted=%d, want 1 (the corrupt row)", deleted)
	}
	if _, ok, _ := kv.Get(KVNamespace, "corrupt-label"); ok {
		t.Errorf("corrupt-label survived eviction")
	}
	survivors, _ := LoadManifest(kv)
	for _, s := range survivors {
		if s == "corrupt-label" {
			t.Errorf("manifest still lists corrupt-label after eviction")
		}
	}
}

// TestEvictPersistedBefore_DanglingManifestEntryIsPruned pins the
// natural cleanup case: a manifest entry whose KV row was deleted
// out-of-band (e.g. operator ran 'ventd state reset' on a single
// row) is silently dropped from the rewritten manifest. No error
// surfaced, no spurious delete-of-nothing.
func TestEvictPersistedBefore_DanglingManifestEntryIsPruned(t *testing.T) {
	kv := newFakeKV()
	seedBucket(t, kv, "healthy-label", time.Now(), 1)
	addToManifest(t, kv, "dangling-label")
	// No KV row for "dangling-label" — but it's in the manifest.

	lib := NewLibrary(DefaultConfig(), mustHasher(t), NewMaintenanceBlocklist(), nil)
	deleted, err := lib.EvictPersistedBefore(kv, time.Now().Add(-PersistedEvictionAge))
	if err != nil {
		t.Fatalf("EvictPersistedBefore: %v", err)
	}
	// Dangling-label dropped silently from manifest; not counted
	// as a deletion (nothing was deleted from KV).
	if deleted != 0 {
		t.Errorf("deleted=%d, want 0 (dangling entries don't count)", deleted)
	}
	survivors, _ := LoadManifest(kv)
	for _, s := range survivors {
		if s == "dangling-label" {
			t.Errorf("manifest still lists dangling-label after eviction")
		}
	}
}

// TestEvictPersistedBefore_FreshBucketsSurvive pins the no-op
// branch: every bucket is younger than cutoff; nothing is deleted;
// the manifest is unchanged byte-for-byte (no spurious rewrite).
func TestEvictPersistedBefore_FreshBucketsSurvive(t *testing.T) {
	kv := newFakeKV()
	now := time.Now()
	seedBucket(t, kv, "a", now, 1)
	seedBucket(t, kv, "b", now.Add(-time.Hour), 2)
	addToManifest(t, kv, "a", "b")

	manifestBefore, _ := LoadManifest(kv)

	lib := NewLibrary(DefaultConfig(), mustHasher(t), NewMaintenanceBlocklist(), nil)
	deleted, err := lib.EvictPersistedBefore(kv, now.Add(-PersistedEvictionAge))
	if err != nil {
		t.Fatalf("EvictPersistedBefore: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted=%d, want 0", deleted)
	}
	manifestAfter, _ := LoadManifest(kv)
	if len(manifestAfter) != len(manifestBefore) {
		t.Errorf("manifest changed: before=%v after=%v", manifestBefore, manifestAfter)
	}
	for _, label := range []string{"a", "b"} {
		if _, ok, _ := kv.Get(KVNamespace, label); !ok {
			t.Errorf("%q evicted; want survive", label)
		}
	}
}

// TestPersistedEvictionAge_Is30Days pins the locked constant so a
// future refactor can't silently shrink the window. The 30-day
// choice comes from R7 §Q5's τ=14d × 2 — a workload two
// τ-halvings stale has weighted-LRU score < 0.07, i.e. it's
// functionally gone.
func TestPersistedEvictionAge_Is30Days(t *testing.T) {
	want := 30 * 24 * time.Hour
	if PersistedEvictionAge != want {
		t.Errorf("PersistedEvictionAge=%v, want %v", PersistedEvictionAge, want)
	}
}

func mustHasher(t *testing.T) *Hasher {
	t.Helper()
	h, err := NewHasher(make([]byte, 16))
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	return h
}
