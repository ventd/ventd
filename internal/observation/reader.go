package observation

import (
	"errors"
	"fmt"
	"time"
)

// errStop is a sentinel returned from the Iterate callback to stop traversal cleanly.
var errStop = errors.New("observation: stop iteration")

// Reader streams records from the observation log.
type Reader struct {
	log logStore
}

// NewReader creates a Reader backed by the given log store.
func NewReader(log logStore) *Reader {
	return &Reader{log: log}
}

// Stream iterates records in append order across active and rotated files
// within retention, beginning at records in files whose mtime is >= since
// (zero means all files). For each decoded Record, fn is called; returning
// false stops iteration cleanly. Headers are consumed transparently.
// A Header with schema_version != 1 returns a diagnostic error
// (RULE-OBS-SCHEMA-03). Torn or CRC-mismatched records are skipped silently
// (RULE-OBS-CRASH-01).
func (rd *Reader) Stream(since time.Time, fn func(*Record) bool) error {
	err := rd.log.Iterate(obsLogName, since, func(payload []byte) error {
		hdr, rec, unmarshalErr := UnmarshalPayload(payload)
		if unmarshalErr != nil {
			return nil // skip corrupted payloads (RULE-OBS-CRASH-01)
		}
		if hdr != nil {
			if hdr.SchemaVersion != schemaVersion {
				return fmt.Errorf("observation: schema version %d not supported (reader supports %d)",
					hdr.SchemaVersion, schemaVersion)
			}
			return nil
		}
		if rec != nil && !fn(rec) {
			return errStop
		}
		return nil
	})
	if errors.Is(err, errStop) {
		return nil
	}
	return err
}

// Latest returns at most n Records matching pred (nil pred accepts all),
// from files with mtime >= since. Records are returned in append order.
// Uses a bounded ring of capacity n — full retention is never loaded into
// memory (RULE-OBS-READ-02). pred may be nil to accept all records.
func (rd *Reader) Latest(since time.Time, pred func(*Record) bool, n int) ([]*Record, error) {
	if n <= 0 {
		return nil, nil
	}

	ring := make([]*Record, n)
	head := 0 // index of the oldest slot when full
	count := 0

	err := rd.log.Iterate(obsLogName, since, func(payload []byte) error {
		_, rec, _ := UnmarshalPayload(payload)
		if rec == nil {
			return nil
		}
		if pred != nil && !pred(rec) {
			return nil
		}
		ring[head%n] = rec
		head++
		if count < n {
			count++
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("observation: latest: %w", err)
	}

	if count == 0 {
		return nil, nil
	}

	// head points past the most-recently written slot.
	// When count < n, entries are at ring[0..count-1] in order.
	// When count == n (ring full), oldest is at ring[head%n].
	result := make([]*Record, count)
	if count < n {
		copy(result, ring[:count])
	} else {
		oldest := head % n
		for i := 0; i < n; i++ {
			result[i] = ring[(oldest+i)%n]
		}
	}
	return result, nil
}
