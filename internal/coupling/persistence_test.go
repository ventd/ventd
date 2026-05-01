package coupling

import (
	"path/filepath"
	"testing"
	"time"

	"gonum.org/v1/gonum/mat"
)

// TestShard_RoundTripCBOR — Save then Load returns identical state.
func TestShard_RoundTripCBOR(t *testing.T) {
	dir := t.TempDir()
	fp := "abcd1234"

	s1 := makeShard(t, 2)
	t0 := time.Now()
	for i := 0; i < 50; i++ {
		_ = s1.Update(t0.Add(time.Duration(i)*time.Second),
			[]float64{50.0, 100.0, 120.0, 0.5}, 50.0)
	}
	if err := s1.Save(dir, fp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2 := makeShard(t, 2)
	loaded, err := s2.Load(dir, fp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded {
		t.Fatal("Load returned !loaded after fresh Save")
	}

	t1 := s1.Read().Theta
	t2 := s2.Read().Theta
	for i := range t1 {
		if t1[i] != t2[i] {
			t.Errorf("theta[%d]: pre=%f post=%f", i, t1[i], t2[i])
		}
	}
	if s1.Read().NSamples != s2.Read().NSamples {
		t.Errorf("NSamples: pre=%d post=%d",
			s1.Read().NSamples, s2.Read().NSamples)
	}
}

// TestShard_HwmonFingerprintInvalidation — RULE-CPL-PERSIST-01.
func TestShard_HwmonFingerprintInvalidation(t *testing.T) {
	dir := t.TempDir()
	fpOld := "OLD-fingerprint"
	fpNew := "NEW-fingerprint"

	s1 := makeShard(t, 1)
	t0 := time.Now()
	for i := 0; i < 10; i++ {
		_ = s1.Update(t0.Add(time.Duration(i)*time.Second),
			[]float64{50.0, 100.0, 0.5}, 50.0)
	}
	if err := s1.Save(dir, fpOld); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load with a different hwmon fingerprint → must discard.
	s2 := makeShard(t, 1)
	loaded, err := s2.Load(dir, fpNew)
	if err != nil {
		t.Fatalf("Load (mismatch): %v", err)
	}
	if loaded {
		t.Errorf("Load: expected discard on hwmon fingerprint mismatch, got loaded=true")
	}
	if s2.Read().NSamples != 0 {
		t.Errorf("s2 should be at fresh state, has %d samples", s2.Read().NSamples)
	}
}

// TestShard_SchemaVersionMismatchDiscards — RULE-CPL-PERSIST-02.
func TestShard_SchemaVersionMismatchDiscards(t *testing.T) {
	dir := t.TempDir()
	fp := "fp"

	// Manually write a bucket with a future schema version.
	s := makeShard(t, 1)
	if err := s.Save(dir, fp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Patch the on-disk file to claim schema version 99.
	path := filepath.Join(PersistDir(dir), sanitiseChannelID(s.channelID)+".cbor")
	patchSchemaVersion(t, path, 99)

	s2 := makeShard(t, 1)
	loaded, err := s2.Load(dir, fp)
	if err != nil {
		t.Fatalf("Load (future version): %v", err)
	}
	if loaded {
		t.Errorf("Load: expected discard on schema version mismatch, got loaded=true")
	}
}

// TestShard_RestoredTrPClamped — RULE-CPL-PERSIST-03.
func TestShard_RestoredTrPClamped(t *testing.T) {
	dir := t.TempDir()
	fp := "fp"

	s1 := makeShard(t, 2)
	// Inflate s1's tr(P) by skipping the in-memory clamp —
	// directly patch the SymDense.
	d := s1.d
	for i := 0; i < d; i++ {
		s1.p.SetSym(i, i, TrPCap*100.0/float64(d)) // tr(P) = 100×TrPCap
	}
	if err := s1.Save(dir, fp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2 := makeShard(t, 2)
	loaded, err := s2.Load(dir, fp)
	if err != nil || !loaded {
		t.Fatalf("Load: loaded=%v err=%v", loaded, err)
	}
	tr := mat.Trace(s2.p)
	if tr > TrPCap*1.001 {
		t.Errorf("restored tr(P) = %f exceeds clamp %f", tr, TrPCap)
	}
}

// patchSchemaVersion rewrites the schema_version field in a
// persisted bucket file. Implementation detail: msgpack-encoded
// `v: <byte>` is at a known offset in the small-fixmap encoding,
// but we don't want a fragile encoding assumption — instead
// re-decode + re-encode with the patched value.
func patchSchemaVersion(t *testing.T, path string, newVer uint8) {
	t.Helper()
	patchOnDiskBucket(t, path, func(b *Bucket) {
		b.SchemaVersion = newVer
	})
}
