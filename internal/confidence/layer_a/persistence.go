package layer_a

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

// PersistedSchemaVersion bumps on any breaking change to Bucket.
// On mismatch, Load discards (RULE-CONFA-PERSIST-03).
const PersistedSchemaVersion uint8 = 1

// Bucket is the on-disk shape from spec-v0_5_9 §3.5. Stored under
// <stateDir>/smart/conf-A/<channelID-flattened>.cbor (R15 §104).
type Bucket struct {
	SchemaVersion       uint8            `msgpack:"schema_version"`
	HwmonFingerprint    string           `msgpack:"hwmon_fingerprint"`
	ChannelID           string           `msgpack:"channel_id"`
	Tier                uint8            `msgpack:"tier"`
	BinCounts           [NumBins]uint32  `msgpack:"bin_counts"`
	BinResidualSumSq    [NumBins]float64 `msgpack:"bin_residual_sum_sq"`
	NoiseFloor          float64          `msgpack:"noise_floor"`
	LastUpdateUnix      int64            `msgpack:"last_update_unix"`
	TierPinnedUntilUnix int64            `msgpack:"tier_pinned_until_unix"`
	SeenFirstContact    bool             `msgpack:"seen_first_contact"`
}

const shardSubdir = "smart/conf-A"

// flattenChannelID maps a sysfs path to a filename-safe token.
// Mirrors the marginal package's pattern so cross-package layouts
// stay consistent.
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

func bucketPath(stateDir, channelID string) string {
	return filepath.Join(stateDir, shardSubdir, flattenChannelID(channelID)+".cbor")
}

// Save writes every channel's state to disk under stateDir. Atomic
// per-channel via tempfile + rename (RULE-STATE-01). Continues on
// per-channel error and returns the joined error at the end.
func (e *Estimator) Save(stateDir, hwmonFingerprint string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	dir := filepath.Join(stateDir, shardSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("layer_a: mkdir %q: %w", dir, err)
	}

	var errs []error
	for channelID, c := range e.channels {
		b := Bucket{
			SchemaVersion:       PersistedSchemaVersion,
			HwmonFingerprint:    hwmonFingerprint,
			ChannelID:           channelID,
			Tier:                c.tier,
			BinCounts:           c.binCounts,
			BinResidualSumSq:    c.binResidualSumSq,
			NoiseFloor:          c.noiseFloor,
			LastUpdateUnix:      c.lastUpdate.Unix(),
			TierPinnedUntilUnix: c.tierPinnedUntil.Unix(),
			SeenFirstContact:    c.seenFirstContact,
		}
		payload, err := msgpack.Marshal(&b)
		if err != nil {
			errs = append(errs, fmt.Errorf("marshal %s: %w", channelID, err))
			continue
		}
		if err := writeAtomic(bucketPath(stateDir, channelID), payload); err != nil {
			errs = append(errs, fmt.Errorf("write %s: %w", channelID, err))
			continue
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func writeAtomic(final string, payload []byte) error {
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("tmp open %q: %w", tmp, err)
	}
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write %q: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fsync %q: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %q→%q: %w", tmp, final, err)
	}
	return nil
}

// LoadChannel attempts to restore a single channel's state. Returns
// (true, nil) on successful load, (false, nil) when on-disk state is
// invalidated (fingerprint or schema mismatch / missing file),
// (false, err) on I/O error other than ENOENT.
//
// Mutates e — caller does not need to hold e.mu.
func (e *Estimator) LoadChannel(stateDir, channelID, currentHwmonFingerprint string, logger *slog.Logger) (bool, error) {
	if logger == nil {
		logger = slog.Default()
	}
	path := bucketPath(stateDir, channelID)
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("layer_a: read %q: %w", path, err)
	}

	var b Bucket
	if err := msgpack.Unmarshal(payload, &b); err != nil {
		logger.Warn("layer_a: bucket decode failed; discarding",
			"channel", channelID, "err", err)
		return false, nil
	}
	if b.SchemaVersion != PersistedSchemaVersion {
		logger.Info("layer_a: schema mismatch; discarding (RULE-CONFA-PERSIST-03)",
			"channel", channelID, "on_disk", b.SchemaVersion, "current", PersistedSchemaVersion)
		return false, nil
	}
	if b.HwmonFingerprint != currentHwmonFingerprint {
		logger.Info("layer_a: hwmon_fingerprint mismatch; discarding (RULE-CONFA-PERSIST-02)",
			"channel", channelID, "on_disk", b.HwmonFingerprint, "current", currentHwmonFingerprint)
		return false, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.channels[channelID]
	if !ok {
		c = &channelState{}
		e.channels[channelID] = c
	}
	c.tier = b.Tier
	c.noiseFloor = b.NoiseFloor
	if c.noiseFloor <= 0 {
		c.noiseFloor = DefaultNoiseFloor
	}
	c.binCounts = b.BinCounts
	c.binResidualSumSq = b.BinResidualSumSq
	if b.LastUpdateUnix > 0 {
		c.lastUpdate = unixToTime(b.LastUpdateUnix)
	}
	if b.TierPinnedUntilUnix > 0 {
		c.tierPinnedUntil = unixToTime(b.TierPinnedUntilUnix)
	}
	c.seenFirstContact = b.SeenFirstContact

	publish(channelID, c, c.lastUpdate)
	return true, nil
}

// unixToTime converts a Unix-seconds timestamp without going through
// time.Unix(0, 0) when the input is zero — returns zero time.Time so
// the caller can detect "never set" cleanly.
func unixToTime(unix int64) time.Time {
	if unix == 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0)
}
