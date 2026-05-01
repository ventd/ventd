package coupling

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	msgpack "github.com/vmihailenco/msgpack/v5"
)

// PersistedSchemaVersion is the on-disk schema. Bump on any
// breaking change to the persisted Bucket layout per RULE-CPL-PERSIST-02.
const PersistedSchemaVersion uint8 = 1

// Bucket is the on-disk shard state. msgpack-encoded under
// $STATE_DIR/smart/shard-B/<channel_id>.cbor per spec-v0_5_7 §2.1.
type Bucket struct {
	SchemaVersion    uint8     `msgpack:"v"`
	HwmonFingerprint string    `msgpack:"hwfp"`
	ChannelID        string    `msgpack:"ch"`
	NCoupled         int       `msgpack:"nc"`
	Theta            []float64 `msgpack:"theta"`
	PSerialised      []byte    `msgpack:"p"` // little-endian float64 raw, length d² × 8
	Lambda           float64   `msgpack:"lambda"`
	NSamples         uint64    `msgpack:"n"`
	LastSeenUnix     int64     `msgpack:"last"`
	GroupedFans      []int     `msgpack:"groups,omitempty"`
}

// PersistDir returns the directory where shards live per
// spec-v0_5_7 §2.1. stateDir is typically $XDG_STATE_HOME/ventd
// (or `/var/lib/ventd`).
func PersistDir(stateDir string) string {
	return filepath.Join(stateDir, "smart", "shard-B")
}

// Save serialises a shard to disk. Atomic via tmpfile + rename.
// Returns an error on I/O failure; callers treat as advisory and
// retry on the next periodic save.
//
// Audit gap #7 (observability): callers should log a warn on
// failure with the channel ID and error text so a silent
// persistence regression is visible in journalctl.
func (s *Shard) Save(stateDir, hwmonFingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := PersistDir(stateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("coupling: mkdir %s: %w", dir, err)
	}

	bucket := Bucket{
		SchemaVersion:    PersistedSchemaVersion,
		HwmonFingerprint: hwmonFingerprint,
		ChannelID:        s.channelID,
		NCoupled:         s.nCoupled,
		Theta:            make([]float64, s.d),
		PSerialised:      serialiseSymDense(s.p, s.d),
		Lambda:           s.lambda,
		NSamples:         s.nSamples,
		LastSeenUnix:     unixZeroOK(s.lastTick),
		GroupedFans:      append([]int(nil), s.groups...),
	}
	for i := 0; i < s.d; i++ {
		bucket.Theta[i] = s.theta.AtVec(i)
	}

	payload, err := msgpack.Marshal(&bucket)
	if err != nil {
		return fmt.Errorf("coupling: marshal: %w", err)
	}

	path := filepath.Join(dir, sanitiseChannelID(s.channelID)+".cbor")
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("coupling: open tmp: %w", err)
	}
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("coupling: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("coupling: fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("coupling: close: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("coupling: rename: %w", err)
	}
	return nil
}

// Load reads a shard's state from disk and applies it. Returns:
//   - (nil, true) when the file is absent (cold start; not an error).
//   - (nil, false) on schema-version mismatch (hwmon fingerprint
//     mismatch or schema bump → discard, re-warm).
//   - (err, false) on read/decode error.
//
// On successful load, applies tr(P) clamp per RULE-CPL-PERSIST-03.
// Audit gap #7 (observability): caller should log a structured
// line with the load outcome so journalctl shows what came back.
func (s *Shard) Load(stateDir, currentHwmonFingerprint string) (loaded bool, err error) {
	dir := PersistDir(stateDir)
	path := filepath.Join(dir, sanitiseChannelID(s.channelID)+".cbor")

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("coupling: read %s: %w", path, err)
	}

	var bucket Bucket
	if err := msgpack.Unmarshal(data, &bucket); err != nil {
		// Corrupt file → discard (don't fail daemon start).
		// RULE-CPL-PERSIST-02 spirit.
		return false, fmt.Errorf("coupling: corrupt bucket %s: %w", path, err)
	}

	// Schema-version invalidation per RULE-CPL-PERSIST-02.
	if bucket.SchemaVersion != PersistedSchemaVersion {
		return false, nil
	}

	// hwmon fingerprint invalidation per RULE-CPL-PERSIST-01.
	if currentHwmonFingerprint != "" && bucket.HwmonFingerprint != "" &&
		bucket.HwmonFingerprint != currentHwmonFingerprint {
		return false, nil
	}

	// Dimension match check.
	if bucket.NCoupled != s.nCoupled {
		return false, nil
	}
	if len(bucket.Theta) != s.d {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < s.d; i++ {
		s.theta.SetVec(i, bucket.Theta[i])
	}
	if err := deserialiseSymDense(bucket.PSerialised, s.p, s.d); err != nil {
		return false, fmt.Errorf("coupling: P deserialise: %w", err)
	}
	// RULE-CPL-PERSIST-03: clamp restored tr(P) to R12 cap.
	tr := traceSym(s.p, s.d)
	if tr > TrPCap {
		ratio := TrPCap / tr
		for i := 0; i < s.d; i++ {
			for j := i; j < s.d; j++ {
				s.p.SetSym(i, j, s.p.At(i, j)*ratio)
			}
		}
	}
	if bucket.Lambda >= MinLambda && bucket.Lambda <= MaxLambda {
		s.lambda = bucket.Lambda
	}
	s.nSamples = bucket.NSamples
	s.groups = append(s.groups[:0], bucket.GroupedFans...)
	s.snapshot.Store(s.buildSnapshot())
	return true, nil
}

// sanitiseChannelID produces a filesystem-safe filename from a
// free-form channel ID. Replaces non-portable characters with `_`
// and caps at 200 chars to avoid PATH_MAX issues.
func sanitiseChannelID(id string) string {
	bad := []string{"/", "\\", " ", ":", "*", "?", "\"", "<", ">", "|", "\x00"}
	out := id
	for _, b := range bad {
		out = strings.ReplaceAll(out, b, "_")
	}
	if len(out) > 200 {
		out = out[:200]
	}
	return out
}

// serialiseSymDense returns the upper-triangle of P as a flat
// little-endian float64 buffer. Length = d × (d+1) / 2 × 8.
func serialiseSymDense(p interface{ At(i, j int) float64 }, d int) []byte {
	n := d * (d + 1) / 2
	out := make([]byte, n*8)
	idx := 0
	for i := 0; i < d; i++ {
		for j := i; j < d; j++ {
			binary.LittleEndian.PutUint64(out[idx*8:], math.Float64bits(p.At(i, j)))
			idx++
		}
	}
	return out
}

// deserialiseSymDense restores the upper-triangle into a
// pre-allocated SymDense.
func deserialiseSymDense(buf []byte, dst interface {
	SetSym(i, j int, v float64)
}, d int) error {
	expected := d * (d + 1) / 2 * 8
	if len(buf) != expected {
		return fmt.Errorf("coupling: P buffer length %d != expected %d", len(buf), expected)
	}
	idx := 0
	for i := 0; i < d; i++ {
		for j := i; j < d; j++ {
			bits := binary.LittleEndian.Uint64(buf[idx*8:])
			dst.SetSym(i, j, math.Float64frombits(bits))
			idx++
		}
	}
	return nil
}

func unixZeroOK(t any) int64 {
	type unixer interface{ Unix() int64 }
	type isZeroer interface{ IsZero() bool }
	if iz, ok := t.(isZeroer); ok && iz.IsZero() {
		return 0
	}
	if u, ok := t.(unixer); ok {
		return u.Unix()
	}
	return 0
}

// traceSym computes tr(M) for a SymDense without importing mat
// in this helper function (used during Load before P is fully
// re-allocated as a SymDense).
func traceSym(m interface{ At(i, j int) float64 }, d int) float64 {
	tr := 0.0
	for i := 0; i < d; i++ {
		tr += m.At(i, i)
	}
	return tr
}
