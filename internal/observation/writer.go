package observation

import (
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
)

const (
	// kvActiveDayKey tracks the UTC date of the active log file across restarts.
	kvActiveDayKey = "active_file_day"
	// maxActiveSize is the 50 MB hard cap (RULE-OBS-ROTATE-02).
	maxActiveSize int64 = 50 * 1024 * 1024
)

// exclusionKeywords are substrings that MUST NOT appear in any msgpack field
// tag of the Record struct. Checked at writer construction (RULE-OBS-PRIVACY-01).
var exclusionKeywords = []string{
	"hostname", "username", "pid", "comm", "exe",
	"cmdline", "mac_addr", "ip_addr", "home", "nickname",
}

// validateFieldExclusion reflects over the msgpack field tags of v and returns
// an error if any tag matches a privacy exclusion keyword. Called by New() on
// the concrete Record type, and by tests with synthetic structs.
func validateFieldExclusion(v any) error {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("msgpack")
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "" || name == "-" {
			continue
		}
		for _, kw := range exclusionKeywords {
			if strings.Contains(name, kw) {
				return fmt.Errorf("observation: field %q matches privacy exclusion keyword %q", name, kw)
			}
		}
	}
	return nil
}

// LogStore covers the spec-16 LogDB methods used by Writer and Reader.
// Satisfied by *state.LogDB.
//
// Exported in v0.5.5 so consumers (notably internal/probe/opportunistic)
// can supply their own log store in tests via observation.NewReader.
// Production callers continue to pass *state.LogDB unchanged.
type LogStore interface {
	Append(name string, payload []byte) error
	Rotate(name string) error
	SetRotationPolicy(name string, policy state.RotationPolicy) error
	Iterate(name string, since time.Time, fn func(payload []byte) error) error
}

// logStore is the legacy unexported alias kept for in-package fields
// that originally took the unexported type. New code uses LogStore.
type logStore = LogStore

// kvStore is the KV side of spec-16 KVDB. Satisfied by *state.KVDB.
type kvStore interface {
	Get(namespace, key string) (any, bool, error)
	Set(namespace, key string, value any) error
}

// writerPolicy disables LogDB auto-rotation so the Writer manages all
// rotation itself. KeepCount and CompressOld apply on explicit Rotate calls.
var writerPolicy = state.RotationPolicy{
	MaxSizeMB:   0,
	MaxAgeDays:  0,
	KeepCount:   DefaultRotationPolicy.KeepCount,
	CompressOld: DefaultRotationPolicy.CompressOld,
}

// Writer appends per-tick controller records to the observation log.
// Append is goroutine-safe via the per-Writer mu; concurrent Append
// calls from the controller tick goroutine AND the opportunistic
// prober goroutine (cmd/ventd/main.go::buildObsAppend +
// internal/probe/opportunistic/prober.go) serialise through it.
//
// Bug-hunt iteration 2 (Agent 3 #2) caught this: the prior
// "NOT safe for concurrent use" contract was being violated in
// production. Concurrent Append against unsynchronised
// bytesWritten / headerWritten / activeDay would race the
// rotation-trigger check at line 169 — two goroutines could both
// observe `bytesWritten >= maxActiveSize`, both call Rotate(),
// and the second rotate would write a Header against the FIRST
// one's brand-new file (the underlying log.Append is goroutine-
// safe but the per-Writer counters aren't).
type Writer struct {
	mu            sync.Mutex
	log           logStore
	kv            kvStore
	classMap      map[uint16]uint8
	header        Header    // template; RotationTs updated per file
	activeDay     time.Time // UTC midnight of the active file; zero if no file yet
	headerWritten bool
	bytesWritten  int64
	clock         func() time.Time
	logger        *slog.Logger
}

// New creates a Writer for the given channels.
// dmiFingerprint is the hex string returned by hwdb.Fingerprint (e.g. "a1b2c3d4e5f60708").
// version is the ventd build version string (e.g. "v0.5.4").
func New(
	logDB *state.LogDB,
	kvDB *state.KVDB,
	channels []*probe.ControllableChannel,
	dmiFingerprint string,
	version string,
	logger *slog.Logger,
) (*Writer, error) {
	return newWithClock(logDB, kvDB, channels, dmiFingerprint, version, logger, time.Now)
}

// newWithClock is the testable constructor that accepts an injectable clock.
func newWithClock(
	log logStore,
	kv kvStore,
	channels []*probe.ControllableChannel,
	dmiFingerprint string,
	version string,
	logger *slog.Logger,
	clock func() time.Time,
) (*Writer, error) {
	if err := validateFieldExclusion(Record{}); err != nil {
		return nil, err
	}

	if err := log.SetRotationPolicy(obsLogName, writerPolicy); err != nil {
		return nil, fmt.Errorf("observation: set rotation policy: %w", err)
	}

	classMap, err := loadOrInferClassMap(kv, channels)
	if err != nil {
		return nil, err
	}

	today := truncateToDay(clock())
	activeDay, headerWritten := loadActiveDay(kv, today)

	hdr := Header{
		SchemaVersion:   schemaVersion,
		DMIFingerprint:  dmiFingerprint,
		VentdVersion:    version,
		ChannelClassMap: classMap,
	}

	return &Writer{
		log:           log,
		kv:            kv,
		classMap:      classMap,
		header:        hdr,
		activeDay:     activeDay,
		headerWritten: headerWritten,
		clock:         clock,
		logger:        logger,
	}, nil
}

// Append writes one Record to the active log file.
// On the first call (or after midnight UTC, or after reaching 50 MB), the
// active file is rotated and a new header written first.
// EventFlags bits 13–31 are cleared before writing (RULE-OBS-SCHEMA-05).
//
// Goroutine-safe: serialises through w.mu. Both the controller tick
// path AND the opportunistic prober path call Append concurrently
// under production load.
func (w *Writer) Append(r *Record) error {
	r.EventFlags &^= eventFlagReservedMask

	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.clock()
	today := truncateToDay(now)

	if !w.headerWritten || !today.Equal(w.activeDay) || w.bytesWritten >= maxActiveSize {
		if err := w.rotateLocked(); err != nil {
			return err
		}
	}

	payload, err := MarshalRecord(r)
	if err != nil {
		return fmt.Errorf("observation: append: %w", err)
	}
	if err := w.log.Append(obsLogName, payload); err != nil {
		return fmt.Errorf("observation: append: %w", err)
	}
	w.bytesWritten += int64(len(payload))
	return nil
}

// Rotate rotates the active log file and writes a header to the new file.
// Append calls this automatically on midnight and 50 MB triggers; callers may
// also invoke it explicitly. Goroutine-safe via w.mu.
func (w *Writer) Rotate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rotateLocked()
}

// rotateLocked is the lock-held inner of Rotate so Append's auto-
// rotate path can call it without the recursive-lock dance.
func (w *Writer) rotateLocked() error {
	now := w.clock()
	today := truncateToDay(now)

	if err := w.log.Rotate(obsLogName); err != nil {
		return fmt.Errorf("observation: rotate: %w", err)
	}
	w.activeDay = today
	w.bytesWritten = 0

	if err := w.writeHeader(now); err != nil {
		return fmt.Errorf("observation: write header: %w", err)
	}
	w.headerWritten = true

	// Persist the new active day so a restarted Writer knows whether to rotate.
	if err := w.kv.Set(kvNamespace, kvActiveDayKey, today.Format("2006-01-02")); err != nil {
		w.logger.Warn("observation: persist active day failed", "err", err)
	}
	return nil
}

// writeHeader appends a Header payload to the active log file.
func (w *Writer) writeHeader(now time.Time) error {
	hdr := w.header
	hdr.RotationTs = now.Unix()
	payload, err := MarshalHeader(&hdr)
	if err != nil {
		return fmt.Errorf("observation: marshal header: %w", err)
	}
	if err := w.log.Append(obsLogName, payload); err != nil {
		return fmt.Errorf("observation: write header append: %w", err)
	}
	w.bytesWritten += int64(len(payload))
	return nil
}

// loadActiveDay reads the persisted active file day from KV.
// Returns the stored day and whether it equals today (same-session continuity).
func loadActiveDay(kv kvStore, today time.Time) (activeDay time.Time, headerWritten bool) {
	v, ok, err := kv.Get(kvNamespace, kvActiveDayKey)
	if err != nil || !ok {
		return time.Time{}, false
	}
	s, ok2 := v.(string)
	if !ok2 {
		return time.Time{}, false
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, false
	}
	d = d.UTC()
	return d, d.Equal(today)
}

// loadOrInferClassMap loads channel classes from KV (RULE-OBS-RATE-03).
// Channels absent from KV are inferred from driver name (RULE-OBS-RATE-04)
// and persisted to KV so future sessions do not need to re-infer.
func loadOrInferClassMap(kv kvStore, channels []*probe.ControllableChannel) (map[uint16]uint8, error) {
	m := make(map[uint16]uint8, len(channels))
	for _, ch := range channels {
		id := ChannelID(ch.PWMPath)
		key := fmt.Sprintf("%s/%d", kvClassPrefix, id)

		v, ok, err := kv.Get(kvNamespace, key)
		if err != nil {
			return nil, fmt.Errorf("observation: get channel class %d: %w", id, err)
		}
		if ok {
			m[id] = parseClassVal(v)
			continue
		}

		// Not in KV: infer from driver name per slowClassDrivers table.
		var class uint8
		if slowClassDrivers[ch.Driver] {
			class = 1
		}
		m[id] = class

		strVal := "0"
		if class == 1 {
			strVal = "1"
		}
		if err := kv.Set(kvNamespace, key, strVal); err != nil {
			return nil, fmt.Errorf("observation: persist channel class %d: %w", id, err)
		}
	}
	return m, nil
}

// parseClassVal converts a KV-stored class value to uint8.
func parseClassVal(v any) uint8 {
	switch s := v.(type) {
	case string:
		if s == "1" {
			return 1
		}
	case uint8:
		return s
	}
	return 0
}

// truncateToDay returns midnight UTC on the day containing t.
func truncateToDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}
