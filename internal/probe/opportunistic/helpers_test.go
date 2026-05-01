package opportunistic

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/observation"
	"github.com/ventd/ventd/internal/state"
)

// fakeLogStore is an in-memory observation.LogStore for opportunistic
// tests — same shape as the observation package's mockLogStore but
// publicly constructable from this package via newFakeLogStore.
type fakeLogStore struct {
	files [][][]byte
}

func (f *fakeLogStore) Append(_ string, payload []byte) error {
	if len(f.files) == 0 {
		f.files = append(f.files, nil)
	}
	idx := len(f.files) - 1
	f.files[idx] = append(f.files[idx], payload)
	return nil
}

func (f *fakeLogStore) Rotate(_ string) error {
	f.files = append(f.files, nil)
	return nil
}

func (f *fakeLogStore) SetRotationPolicy(_ string, _ state.RotationPolicy) error {
	return nil
}

func (f *fakeLogStore) Iterate(_ string, _ time.Time, fn func([]byte) error) error {
	for _, file := range f.files {
		for _, payload := range file {
			if err := fn(payload); err != nil {
				return err
			}
		}
	}
	return nil
}

// newFakeLogStore returns an observation.LogStore pre-populated with
// the supplied records as a single file, preceded by a v2 header.
// The `now` parameter is unused except for documentation; tests pass
// a stable timestamp.
func newFakeLogStore(_ time.Time, records ...*observation.Record) *fakeLogStore {
	store := &fakeLogStore{}
	hdr := &observation.Header{
		// Use the v2 schema version so the v0.5.5 reader accepts
		// the synthetic file. Fields beyond SchemaVersion are
		// optional for opportunistic tests; the reader does not
		// require a populated DMI fingerprint or class map.
		SchemaVersion: 2,
	}
	hdrPayload, err := observation.MarshalHeader(hdr)
	if err == nil {
		_ = store.Append("", hdrPayload)
	}
	for _, r := range records {
		payload, err := observation.MarshalRecord(r)
		if err != nil {
			continue
		}
		_ = store.Append("", payload)
	}
	return store
}

// makeIdleProcRoot builds a minimal /proc + /sys fixture under
// t.TempDir() that satisfies the StartupGate predicate. Mirrors the
// helper of the same name in internal/idle/idle_test.go (which is
// unexported and only visible inside that package). Used by
// scheduler tests that need OpportunisticGate to actually pass the
// predicate check.
func makeIdleProcRoot(t *testing.T) (procRoot, sysRoot string) {
	t.Helper()
	dir := t.TempDir()
	procRoot = dir + "/proc"
	sysRoot = dir + "/sys"

	for _, p := range []string{
		"pressure/cpu",
		"pressure/io",
		"pressure/memory",
	} {
		writeFile(t, procRoot, p, "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	}
	writeFile(t, procRoot, "uptime", "7200.00 14400.00\n")
	return procRoot, sysRoot
}

// writeFile writes contents to dir/rel, creating intermediate
// directories. Test helper.
func writeFile(t *testing.T, dir, rel, contents string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// fakeRec is a one-line constructor for an observation.Record with the
// fields opportunistic tests care about.
func fakeRec(ts time.Time, channelID uint16, pwm uint8, eventFlags uint32) *observation.Record {
	return &observation.Record{
		Ts:         ts.UnixMicro(),
		ChannelID:  channelID,
		PWMWritten: pwm,
		EventFlags: eventFlags,
	}
}

// memLastProbe is an in-memory LastProbeStore for scheduler tests.
type memLastProbe struct {
	mu sync.Mutex
	m  map[uint16]time.Time
}

func newMemLastProbe() *memLastProbe {
	return &memLastProbe{m: make(map[uint16]time.Time)}
}

func (s *memLastProbe) GetLastProbe(channelID uint16) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, ok := s.m[channelID]
	return ts, ok
}

func (s *memLastProbe) SetLastProbe(channelID uint16, ts time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[channelID] = ts
	return nil
}

// stubContext returns a Background context — many tests don't need a
// real cancelable context, this just keeps the signatures uniform.
func stubContext() context.Context { return context.Background() }
