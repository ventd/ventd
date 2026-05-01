package marginal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"

	"github.com/vmihailenco/msgpack/v5"
	"gonum.org/v1/gonum/mat"
)

// PersistedSchemaVersion is bumped on any breaking Bucket change.
// On mismatch, Load discards (per RULE-CMB-PERSIST-02). v0.5.8 ships
// at v1.
const PersistedSchemaVersion uint8 = 1

// Bucket is the on-disk shape per spec §3.5. Stored msgpack-encoded
// at <stateDir>/smart/shard-C/<ChannelID-safe>-<SignatureLabel>.cbor
// (RULE-CMB-NAMESPACE-01).
type Bucket struct {
	SchemaVersion         uint8     `msgpack:"schema_version"`
	HwmonFingerprint      string    `msgpack:"hwmon_fingerprint"`
	ChannelID             string    `msgpack:"channel_id"`
	SignatureLabel        string    `msgpack:"signature_label"`
	Theta                 []float64 `msgpack:"theta"`
	PSerialised           []byte    `msgpack:"p"`
	InitialP              float64   `msgpack:"initial_p"`
	Lambda                float64   `msgpack:"lambda"`
	NSamples              uint64    `msgpack:"n_samples"`
	LastSeenUnix          int64     `msgpack:"last_seen_unix"`
	HitCount              uint64    `msgpack:"hit_count"`
	EWMAResidual          float64   `msgpack:"ewma_residual"`
	ObservedSaturationPWM uint8     `msgpack:"observed_saturation_pwm"`
}

// shardSubdir is the per-spec namespace under stateDir. R15 §104
// names this layout; v0.5.7 Layer B uses smart/shard-B/ as a sibling.
const shardSubdir = "smart/shard-C"

// shardFilename derives the on-disk filename. Channel paths contain
// '/' so we hyphen-flatten them; signature labels are SipHash hex
// digests so they're already filename-safe. RULE-CMB-NAMESPACE-01.
func shardFilename(channelID, signatureLabel string) string {
	safeChannel := flattenChannelID(channelID)
	return fmt.Sprintf("%s-%s.cbor", safeChannel, signatureLabel)
}

func flattenChannelID(channelID string) string {
	out := make([]byte, 0, len(channelID))
	for i := 0; i < len(channelID); i++ {
		c := channelID[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '-' || c == '_' || c == '.':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}

// shardPath returns the absolute on-disk path for a shard.
func shardPath(stateDir, channelID, signatureLabel string) string {
	return filepath.Join(stateDir, shardSubdir, shardFilename(channelID, signatureLabel))
}

// Save writes the shard's state to disk under stateDir. On any error
// the on-disk state is unchanged (atomic tempfile + rename per
// RULE-STATE-01).
func (s *Shard) Save(stateDir, hwmonFingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	pBytes, err := serialiseSymDense(s.p)
	if err != nil {
		return fmt.Errorf("marginal: serialise P: %w", err)
	}

	b := Bucket{
		SchemaVersion:         PersistedSchemaVersion,
		HwmonFingerprint:      hwmonFingerprint,
		ChannelID:             s.cfg.ChannelID,
		SignatureLabel:        s.cfg.SignatureLabel,
		Theta:                 []float64{s.theta.AtVec(0), s.theta.AtVec(1)},
		PSerialised:           pBytes,
		InitialP:              s.cfg.InitialP,
		Lambda:                s.lambda,
		NSamples:              s.nSamples,
		LastSeenUnix:          0, // runtime sets at periodic-save time
		HitCount:              s.nSamples,
		EWMAResidual:          s.ewmaResidual,
		ObservedSaturationPWM: s.observedSaturationPWM,
	}
	payload, err := msgpack.Marshal(&b)
	if err != nil {
		return fmt.Errorf("marginal: marshal: %w", err)
	}

	dir := filepath.Join(stateDir, shardSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("marginal: mkdir %q: %w", dir, err)
	}

	final := shardPath(stateDir, s.cfg.ChannelID, s.cfg.SignatureLabel)
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("marginal: tmp open %q: %w", tmp, err)
	}
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("marginal: write %q: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("marginal: fsync %q: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("marginal: close %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("marginal: rename %q→%q: %w", tmp, final, err)
	}
	return nil
}

// Load attempts to restore the shard from disk. Returns (true, nil)
// when state was found and loaded; (false, nil) when state was
// invalidated (fingerprint mismatch / schema mismatch / dim mismatch);
// (false, err) on I/O error.
func (s *Shard) Load(stateDir, currentHwmonFingerprint string, logger *slog.Logger) (bool, error) {
	if logger == nil {
		logger = slog.Default()
	}
	path := shardPath(stateDir, s.cfg.ChannelID, s.cfg.SignatureLabel)
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("marginal: read %q: %w", path, err)
	}

	var b Bucket
	if err := msgpack.Unmarshal(payload, &b); err != nil {
		logger.Warn("marginal: bucket decode failed; discarding",
			"channel", s.cfg.ChannelID,
			"signature", s.cfg.SignatureLabel,
			"err", err)
		return false, nil
	}

	if b.SchemaVersion != PersistedSchemaVersion {
		logger.Info("marginal: schema mismatch; discarding (RULE-CMB-PERSIST-02)",
			"channel", s.cfg.ChannelID,
			"on_disk", b.SchemaVersion,
			"current", PersistedSchemaVersion)
		return false, nil
	}
	if b.HwmonFingerprint != currentHwmonFingerprint {
		logger.Info("marginal: hwmon_fingerprint mismatch; discarding (RULE-CMB-PERSIST-01)",
			"channel", s.cfg.ChannelID,
			"on_disk", b.HwmonFingerprint,
			"current", currentHwmonFingerprint)
		return false, nil
	}
	if len(b.Theta) != DimC {
		logger.Warn("marginal: theta dim mismatch; discarding",
			"channel", s.cfg.ChannelID,
			"got", len(b.Theta),
			"want", DimC)
		return false, nil
	}

	pNew, err := deserialiseSymDense(b.PSerialised, DimC)
	if err != nil {
		logger.Warn("marginal: P deserialise failed; discarding",
			"channel", s.cfg.ChannelID, "err", err)
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := 0; i < DimC; i++ {
		s.theta.SetVec(i, b.Theta[i])
	}
	s.p = pNew
	if b.InitialP > 0 {
		s.cfg.InitialP = b.InitialP
		s.pInit = b.InitialP * float64(DimC)
	}
	if b.Lambda >= MinLambda && b.Lambda <= MaxLambda {
		s.lambda = b.Lambda
	}
	s.nSamples = b.NSamples
	s.ewmaResidual = b.EWMAResidual
	s.observedSaturationPWM = b.ObservedSaturationPWM

	// RULE-CMB-PERSIST-03: tr(P) clamped on load.
	tr := mat.Trace(s.p)
	if tr > TrPCap {
		k := TrPCap / tr
		for r := 0; r < DimC; r++ {
			for c := r; c < DimC; c++ {
				s.p.SetSym(r, c, s.p.At(r, c)*k)
			}
		}
	}

	// Three-condition warmup gate re-evaluates against in-memory
	// state on load (per spec §3.5).
	s.observedZeroDeltaTRun = 0
	s.publish()
	return true, nil
}

// serialiseSymDense writes the upper-triangle of an n×n SymDense as
// little-endian float64 bytes. Total length: n·(n+1)/2 · 8 bytes.
func serialiseSymDense(m *mat.SymDense) ([]byte, error) {
	n, _ := m.Dims()
	buf := bytes.NewBuffer(make([]byte, 0, n*(n+1)/2*8))
	for r := 0; r < n; r++ {
		for c := r; c < n; c++ {
			if err := binary.Write(buf, binary.LittleEndian, m.At(r, c)); err != nil {
				return nil, err
			}
		}
	}
	return buf.Bytes(), nil
}

// deserialiseSymDense reads the upper-triangle of an n×n SymDense
// from the byte stream produced by serialiseSymDense.
func deserialiseSymDense(b []byte, n int) (*mat.SymDense, error) {
	want := n * (n + 1) / 2 * 8
	if len(b) != want {
		return nil, fmt.Errorf("len %d != %d", len(b), want)
	}
	out := mat.NewSymDense(n, nil)
	r := bytes.NewReader(b)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			var v float64
			if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
				return nil, err
			}
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("non-finite P[%d,%d]", i, j)
			}
			out.SetSym(i, j, v)
		}
	}
	return out, nil
}
