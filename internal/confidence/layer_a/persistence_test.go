package layer_a

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

func newSilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestPersistence_Namespace binds RULE-CONFA-PERSIST-01: KV namespace
// is smart/conf-A/<channel>; the on-disk path lives under that
// subdir; persisted Bucket carries inputs not the computed output
// (the four-term product is recomputed on load).
func TestPersistence_Namespace(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(1_000_000, 0)
	e, _ := New(Config{})
	_ = e.Admit("/sys/class/hwmon/hwmon4/pwm1", TierRPMTach, 0, t0)
	e.Observe("/sys/class/hwmon/hwmon4/pwm1", 100, 1000, 1000, t0)

	if err := e.Save(dir, "fp-A"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := filepath.Join(dir, "smart", "conf-A", "-sys-class-hwmon-hwmon4-pwm1.cbor")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected file at %s, got: %v", want, err)
	}

	// The Bucket on disk must not carry the computed ConfA — only the
	// inputs. Decode and verify the structural shape.
	payload, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	var b Bucket
	if err := msgpack.Unmarshal(payload, &b); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b.SchemaVersion != PersistedSchemaVersion {
		t.Errorf("SchemaVersion: got %d, want %d", b.SchemaVersion, PersistedSchemaVersion)
	}
	if b.HwmonFingerprint != "fp-A" {
		t.Errorf("HwmonFingerprint: got %q, want %q", b.HwmonFingerprint, "fp-A")
	}
	if b.Tier != TierRPMTach {
		t.Errorf("Tier: got %d, want %d", b.Tier, TierRPMTach)
	}
	if b.NoiseFloor != DefaultNoiseFloor {
		t.Errorf("NoiseFloor: got %v, want %v", b.NoiseFloor, DefaultNoiseFloor)
	}

	// Round-trip via LoadChannel into a fresh Estimator.
	e2, _ := New(Config{})
	loaded, err := e2.LoadChannel(dir, "/sys/class/hwmon/hwmon4/pwm1", "fp-A", newSilentLogger())
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if !loaded {
		t.Fatal("LoadChannel returned !loaded for valid bucket")
	}
	s := e2.Read("/sys/class/hwmon/hwmon4/pwm1")
	if s == nil {
		t.Fatal("Read returned nil after Load")
	}
	if s.Tier != TierRPMTach {
		t.Errorf("Tier after load: got %d, want %d", s.Tier, TierRPMTach)
	}
}

// TestPersistence_FingerprintInvalidation binds
// RULE-CONFA-PERSIST-02: hwmon_fingerprint mismatch on Load discards.
// Saved with fp-A; loaded with fp-B; LoadChannel returns (false, nil).
func TestPersistence_FingerprintInvalidation(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(1_000_000, 0)
	e, _ := New(Config{})
	_ = e.Admit("ch1", TierRPMTach, 0, t0)
	e.Observe("ch1", 50, 1000, 1000, t0)
	if err := e.Save(dir, "fp-A"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	e2, _ := New(Config{})
	loaded, err := e2.LoadChannel(dir, "ch1", "fp-B", newSilentLogger())
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if loaded {
		t.Error("LoadChannel returned loaded=true on fingerprint mismatch — discard expected")
	}
	if got := e2.Read("ch1"); got != nil {
		t.Errorf("channel state present after fingerprint discard: %+v", got)
	}
}

// TestPersistence_SchemaMismatch binds RULE-CONFA-PERSIST-03: schema
// version mismatch on Load discards (no migration). Hand-craft a
// bucket with bumped SchemaVersion, write to disk, attempt Load.
func TestPersistence_SchemaMismatch(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, shardSubdir)
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	b := Bucket{
		SchemaVersion:    255, // future version
		HwmonFingerprint: "fp",
		ChannelID:        "ch1",
		Tier:             TierRPMTach,
		NoiseFloor:       DefaultNoiseFloor,
	}
	payload, err := msgpack.Marshal(&b)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(subdir, flattenChannelID("ch1")+".cbor")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	e, _ := New(Config{})
	loaded, err := e.LoadChannel(dir, "ch1", "fp", newSilentLogger())
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if loaded {
		t.Error("LoadChannel returned loaded=true on schema mismatch — discard expected")
	}
}

// TestPersistence_MissingFile verifies Load returns (false, nil) — not
// an error — when the on-disk bucket simply does not exist (fresh
// install / first run for this channel).
func TestPersistence_MissingFile(t *testing.T) {
	dir := t.TempDir()
	e, _ := New(Config{})
	loaded, err := e.LoadChannel(dir, "neverseen", "fp", newSilentLogger())
	if err != nil {
		t.Errorf("LoadChannel on missing file returned err: %v", err)
	}
	if loaded {
		t.Error("LoadChannel returned loaded=true for missing file")
	}
}
