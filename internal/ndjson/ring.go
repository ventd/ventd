package ndjson

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

const (
	ringMaxBytes  = 8 * 1024 * 1024 // 8 MB
	ringMaxEvents = 10_000
	ringMaxAge    = 24 * time.Hour
)

// Ring is a circular NDJSON file that wraps around when it reaches a
// size, event-count, or age cap. On wraparound it renames the current
// file to <path>.prev and starts a new file. Thread-safe.
type Ring struct {
	mu        sync.Mutex
	path      string
	schema    SchemaVersion
	w         *Writer
	f         *os.File
	events    int
	opened    time.Time
	timeNow   func() time.Time // injectable for tests
	maxBytes  int64
	maxEvents int
	maxAge    time.Duration
}

// RingOptions allows overriding ring caps for testing.
type RingOptions struct {
	MaxBytes  int64
	MaxEvents int
	MaxAge    time.Duration
	TimeNow   func() time.Time
}

// OpenRing opens (or creates) a ring buffer file at path with mode 0o600.
func OpenRing(path string, schema SchemaVersion) (*Ring, error) {
	return OpenRingWithOptions(path, schema, RingOptions{})
}

// OpenRingWithOptions is like OpenRing but allows overriding caps (for tests).
func OpenRingWithOptions(path string, schema SchemaVersion, opts RingOptions) (*Ring, error) {
	r := &Ring{
		path:      path,
		schema:    schema,
		maxBytes:  ringMaxBytes,
		maxEvents: ringMaxEvents,
		maxAge:    ringMaxAge,
		timeNow:   time.Now,
	}
	if opts.MaxBytes > 0 {
		r.maxBytes = opts.MaxBytes
	}
	if opts.MaxEvents > 0 {
		r.maxEvents = opts.MaxEvents
	}
	if opts.MaxAge > 0 {
		r.maxAge = opts.MaxAge
	}
	if opts.TimeNow != nil {
		r.timeNow = opts.TimeNow
	}
	if err := r.openFile(); err != nil {
		return nil, err
	}
	return r, nil
}

// Write appends a single event to the ring, triggering wraparound if needed.
func (r *Ring) Write(eventType string, payload any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.maybeRotate(); err != nil {
		return err
	}
	r.events++
	return r.w.WrapEvent(eventType, payload)
}

// WriteRaw appends a pre-encoded JSON object to the ring.
func (r *Ring) WriteRaw(line json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.maybeRotate(); err != nil {
		return err
	}
	r.events++
	return r.w.WriteEvent(line)
}

// Snapshot copies the current ring file (and .prev if present) to dst,
// which must be an open writable file. Used by the bundle to include the
// trace ring without stopping writes.
func (r *Ring) Snapshot(dst io.Writer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Flush current writer so all events are on disk.
	if err := r.w.Close(); err != nil {
		return fmt.Errorf("ring snapshot flush: %w", err)
	}
	// Reopen for continued writing after snapshot.
	defer func() { _ = r.openFileLocked() }()

	// Copy .prev first (older events), then current.
	prev := r.path + ".prev"
	if f, err := os.Open(prev); err == nil {
		_, _ = io.Copy(dst, f)
		_ = f.Close()
	}
	cur, err := os.Open(r.path)
	if err != nil {
		return nil // nothing written yet is fine
	}
	defer func() { _ = cur.Close() }()
	_, err = io.Copy(dst, cur)
	return err
}

// SnapshotGzip is like Snapshot but writes gzip-compressed output.
func (r *Ring) SnapshotGzip(dst io.Writer) error {
	gz, err := gzip.NewWriterLevel(dst, gzip.DefaultCompression)
	if err != nil {
		return fmt.Errorf("ring snapshot gzip: %w", err)
	}
	if snapshotErr := r.Snapshot(gz); snapshotErr != nil {
		if err := gz.Close(); err != nil {
			return fmt.Errorf("ring snapshot gzip: %w", err)
		}
		return snapshotErr
	}
	return gz.Close()
}

// Close flushes and closes the ring file.
func (r *Ring) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.w != nil {
		return r.w.Close()
	}
	return nil
}

func (r *Ring) maybeRotate() error {
	now := r.timeNow()
	fi, err := r.f.Stat()
	if err != nil {
		return fmt.Errorf("ring stat: %w", err)
	}
	rotate := fi.Size() >= r.maxBytes ||
		r.events >= r.maxEvents ||
		now.Sub(r.opened) >= r.maxAge
	if !rotate {
		return nil
	}
	// Close current writer and rename to .prev.
	if err := r.w.Close(); err != nil {
		return fmt.Errorf("ring rotate close: %w", err)
	}
	prev := r.path + ".prev"
	_ = os.Remove(prev)
	if err := os.Rename(r.path, prev); err != nil {
		return fmt.Errorf("ring rotate rename: %w", err)
	}
	return r.openFileLocked()
}

func (r *Ring) openFile() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.openFileLocked()
}

func (r *Ring) openFileLocked() error {
	f, err := os.OpenFile(r.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("ring open %s: %w", r.path, err)
	}
	r.f = f
	r.w = NewWriter(f, r.schema)
	r.events = 0
	r.opened = r.timeNow()
	return nil
}
