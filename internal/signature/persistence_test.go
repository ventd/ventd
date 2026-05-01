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
