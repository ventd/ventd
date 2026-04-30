package observation

import (
	"log/slog"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
)

// noSince is the zero time — iterate all files, no mtime filter.
var noSince time.Time

// testBaseTime is the stable "now" used by tests that do not care about
// rotation timing.
var testBaseTime = time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)

// testEnv holds the mock stores for a test Writer.
type testEnv struct {
	log *mockLogStore
	kv  *mockKVStore
}

// newTestWriter creates a Writer backed by in-memory mocks with a fixed clock.
func newTestWriter(t *testing.T, channels []*probe.ControllableChannel) (*Writer, *testEnv) {
	t.Helper()
	return newTestWriterWithClock(t, channels, func() time.Time { return testBaseTime })
}

// newTestWriterWithClock creates a Writer backed by in-memory mocks with an
// injectable clock — used by rotation tests.
func newTestWriterWithClock(
	t *testing.T,
	channels []*probe.ControllableChannel,
	clock func() time.Time,
) (*Writer, *testEnv) {
	t.Helper()
	ml := &mockLogStore{}
	mkv := &mockKVStore{}
	if channels == nil {
		channels = []*probe.ControllableChannel{}
	}
	w, err := newWithClock(ml, mkv, channels, "", "v0.5.4-test", slog.Default(), clock)
	if err != nil {
		t.Fatalf("newWithClock: %v", err)
	}
	return w, &testEnv{log: ml, kv: mkv}
}

// appendNRecords appends n trivial Records to w, fataling on any error.
func appendNRecords(t *testing.T, w *Writer, n int) {
	t.Helper()
	for i := range n {
		r := &Record{Ts: int64(i + 1), ChannelID: 1}
		if err := w.Append(r); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}
}

// collectPayloads returns all raw payloads stored in ml, across all files,
// in append order (oldest file first).
func collectPayloads(t *testing.T, ml *mockLogStore) [][]byte {
	t.Helper()
	var result [][]byte
	for _, file := range ml.files {
		result = append(result, file...)
	}
	return result
}

// --- mock implementations ---

// mockLogStore is an in-memory logStore for tests.
// files[i] holds the ordered payloads for log file i.
// Rotate() starts a new empty file (appends a nil slice).
type mockLogStore struct {
	files  [][][]byte
	policy state.RotationPolicy
}

func (m *mockLogStore) ensureFile() {
	if len(m.files) == 0 {
		m.files = append(m.files, nil)
	}
}

func (m *mockLogStore) Append(_ string, payload []byte) error {
	m.ensureFile()
	idx := len(m.files) - 1
	m.files[idx] = append(m.files[idx], payload)
	return nil
}

func (m *mockLogStore) Rotate(_ string) error {
	m.files = append(m.files, nil)
	return nil
}

func (m *mockLogStore) SetRotationPolicy(_ string, p state.RotationPolicy) error {
	m.policy = p
	return nil
}

func (m *mockLogStore) Iterate(_ string, _ time.Time, fn func([]byte) error) error {
	for _, file := range m.files {
		for _, payload := range file {
			if err := fn(payload); err != nil {
				return err
			}
		}
	}
	return nil
}

// fileCount returns the number of distinct files (rotated + active).
func (m *mockLogStore) fileCount() int { return len(m.files) }

// mockKVStore is an in-memory kvStore for tests.
type mockKVStore struct {
	data map[string]map[string]any
}

func (m *mockKVStore) Get(ns, key string) (any, bool, error) {
	if m.data == nil {
		return nil, false, nil
	}
	nsMap, ok := m.data[ns]
	if !ok {
		return nil, false, nil
	}
	v, ok := nsMap[key]
	return v, ok, nil
}

func (m *mockKVStore) Set(ns, key string, value any) error {
	if m.data == nil {
		m.data = make(map[string]map[string]any)
	}
	if m.data[ns] == nil {
		m.data[ns] = make(map[string]any)
	}
	m.data[ns][key] = value
	return nil
}
