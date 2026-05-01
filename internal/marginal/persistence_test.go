package marginal

import (
	"encoding/binary"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestPersistence_NamespaceMatchesR15 — RULE-CMB-NAMESPACE-01.
func TestPersistence_NamespaceMatchesR15(t *testing.T) {
	dir := t.TempDir()
	s := convergedShard(t, []float64{-0.05, 0})
	s.cfg.ChannelID = "/sys/class/hwmon/hwmon0/pwm1"
	s.cfg.SignatureLabel = "abc123"
	if err := s.Save(dir, "fp1"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	expected := filepath.Join(dir, "smart", "shard-C")
	entries, err := os.ReadDir(expected)
	if err != nil {
		t.Fatalf("expected smart/shard-C/ dir: %v", err)
	}
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".cbor") &&
			strings.Contains(e.Name(), "abc123") {
			found = true
		}
	}
	if !found {
		t.Errorf("no shard file under smart/shard-C/; entries: %v", entries)
	}
}

// TestShard_HwmonFingerprintInvalidation — RULE-CMB-PERSIST-01.
func TestShard_HwmonFingerprintInvalidation(t *testing.T) {
	dir := t.TempDir()
	s := convergedShard(t, []float64{-0.05, 0})
	if err := s.Save(dir, "fp-original"); err != nil {
		t.Fatal(err)
	}

	s2, _ := New(DefaultConfig(s.cfg.ChannelID, s.cfg.SignatureLabel))
	loaded, err := s2.Load(dir, "fp-different", silentLogger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded {
		t.Errorf("Load: returned loaded=true on fingerprint mismatch; want false")
	}
	if s2.nSamples != 0 {
		t.Errorf("loaded state should be fresh; nSamples = %d", s2.nSamples)
	}
}

// TestShard_SchemaVersionMismatchDiscards — RULE-CMB-PERSIST-02.
func TestShard_SchemaVersionMismatchDiscards(t *testing.T) {
	dir := t.TempDir()
	s := convergedShard(t, []float64{-0.05, 0})
	if err := s.Save(dir, "fp"); err != nil {
		t.Fatal(err)
	}

	path := shardPath(dir, s.cfg.ChannelID, s.cfg.SignatureLabel)
	patchOnDiskBucket(t, path, func(b *Bucket) { b.SchemaVersion = 99 })

	s2, _ := New(DefaultConfig(s.cfg.ChannelID, s.cfg.SignatureLabel))
	loaded, err := s2.Load(dir, "fp", silentLogger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded {
		t.Errorf("Load: returned loaded=true on schema mismatch; want false")
	}
}

// TestShard_RestoredReWarms — RULE-CMB-PERSIST-03.
//
// Inflate on-disk tr(P) past the cap; verify Load clamps it back
// AND that the loaded shard's WarmingUp is true (re-evaluation per
// spec §3.5).
func TestShard_RestoredReWarms(t *testing.T) {
	dir := t.TempDir()
	s := convergedShard(t, []float64{-0.05, 0})
	if err := s.Save(dir, "fp"); err != nil {
		t.Fatal(err)
	}

	// Patch the on-disk Bucket to inflate tr(P): rewrite the
	// upper-triangle [P00, P01, P11] to [1e6, 0, 1e6].
	path := shardPath(dir, s.cfg.ChannelID, s.cfg.SignatureLabel)
	patchOnDiskBucket(t, path, func(b *Bucket) {
		out := make([]byte, 24)
		binary.LittleEndian.PutUint64(out[0:8], math.Float64bits(1e6))
		binary.LittleEndian.PutUint64(out[8:16], math.Float64bits(0))
		binary.LittleEndian.PutUint64(out[16:24], math.Float64bits(1e6))
		b.PSerialised = out
	})

	s2, _ := New(DefaultConfig(s.cfg.ChannelID, s.cfg.SignatureLabel))
	loaded, err := s2.Load(dir, "fp", silentLogger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded {
		t.Fatal("Load: expected loaded=true")
	}
	tr := s2.p.At(0, 0) + s2.p.At(1, 1)
	if tr > TrPCap*1.0001 {
		t.Errorf("tr(P) = %f; expected ≤ %f after restore-clamp", tr, TrPCap)
	}
	if !s2.Read().WarmingUp {
		t.Errorf("loaded shard must re-enter warmup per spec §3.5")
	}
}

// patchOnDiskBucket reads the persisted file, mutates the Bucket
// via fn, re-encodes, and writes it back.
func patchOnDiskBucket(t *testing.T, path string, fn func(*Bucket)) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	var b Bucket
	if err := msgpack.Unmarshal(raw, &b); err != nil {
		t.Fatalf("decode: %v", err)
	}
	fn(&b)
	out, err := msgpack.Marshal(&b)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile(path, out, 0o640); err != nil {
		t.Fatalf("write back: %v", err)
	}
}
