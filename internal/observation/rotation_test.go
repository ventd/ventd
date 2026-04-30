package observation

import (
	"log/slog"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
)

// TestWriter_Rotate_MidnightCrossing verifies that the first Append after
// midnight triggers a log rotation and a new header (RULE-OBS-ROTATE-01).
func TestWriter_Rotate_MidnightCrossing(t *testing.T) {
	day1 := time.Date(2026, 4, 30, 23, 59, 59, 0, time.UTC)
	day2 := time.Date(2026, 5, 1, 0, 0, 1, 0, time.UTC)

	tick := day1
	clock := func() time.Time { return tick }

	w, env := newTestWriterWithClock(t, nil, clock)

	// First append on day1 — creates the file and writes the header.
	if err := w.Append(&Record{Ts: 1}); err != nil {
		t.Fatalf("Append day1: %v", err)
	}

	// Advance clock to day2 — next Append must rotate.
	tick = day2
	if err := w.Append(&Record{Ts: 2}); err != nil {
		t.Fatalf("Append day2: %v", err)
	}

	// Expect 2 log files: day1 file + day2 file.
	if env.log.fileCount() != 2 {
		t.Fatalf("file count: got %d, want 2 (rotation on midnight crossing)", env.log.fileCount())
	}

	// Each file must start with a Header.
	for fi, file := range env.log.files {
		if len(file) == 0 {
			t.Fatalf("file[%d] is empty", fi)
		}
		hdr, _, err := UnmarshalPayload(file[0])
		if err != nil {
			t.Fatalf("file[%d] payload[0] unmarshal: %v", fi, err)
		}
		if hdr == nil {
			t.Fatalf("file[%d] first payload is not a Header", fi)
		}
	}
}

// TestWriter_Rotate_DaemonRestart_SameDay verifies that a fresh Writer
// constructed on the same calendar day as the existing log does NOT rotate
// and does NOT emit a duplicate header (RULE-OBS-ROTATE-01 restart path).
func TestWriter_Rotate_DaemonRestart_SameDay(t *testing.T) {
	day := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return day }

	// Session 1: write one record.
	ml := &mockLogStore{}
	mkv := &mockKVStore{}
	w1, err := newWithClock(ml, mkv, nil, "", "v", slog.Default(), clock)
	if err != nil {
		t.Fatalf("newWithClock session1: %v", err)
	}
	if err := w1.Append(&Record{Ts: 1}); err != nil {
		t.Fatalf("Append session1: %v", err)
	}

	// Session 2: new Writer, same stores, same day.
	w2, err := newWithClock(ml, mkv, nil, "", "v", slog.Default(), clock)
	if err != nil {
		t.Fatalf("newWithClock session2: %v", err)
	}
	if err := w2.Append(&Record{Ts: 2}); err != nil {
		t.Fatalf("Append session2: %v", err)
	}

	// Exactly one file, exactly one header at the start.
	if ml.fileCount() != 1 {
		t.Fatalf("file count: got %d, want 1 (no rotation on same-day restart)", ml.fileCount())
	}
	payloads := collectPayloads(t, ml)
	var headerCount int
	for _, p := range payloads {
		hdr, _, err := UnmarshalPayload(p)
		if err == nil && hdr != nil {
			headerCount++
		}
	}
	if headerCount != 1 {
		t.Errorf("header count: got %d, want 1 (no duplicate header on restart)", headerCount)
	}
}

// TestWriter_Rotate_SizeLimit verifies that exceeding the 50 MiB size cap
// triggers rotation on the next Append (RULE-OBS-ROTATE-02).
func TestWriter_Rotate_SizeLimit(t *testing.T) {
	clock := func() time.Time { return testBaseTime }
	w, env := newTestWriterWithClock(t, nil, clock)

	// Write one record to initialise the file and headerWritten state.
	if err := w.Append(&Record{Ts: 1}); err != nil {
		t.Fatalf("first Append: %v", err)
	}

	// Simulate having reached the size cap.
	w.bytesWritten = maxActiveSize

	// Next Append must rotate.
	if err := w.Append(&Record{Ts: 2}); err != nil {
		t.Fatalf("second Append after cap: %v", err)
	}

	if env.log.fileCount() != 2 {
		t.Fatalf("file count: got %d, want 2 (rotation on size cap)", env.log.fileCount())
	}
}

// TestWriter_Rotate_IntegrationWithState verifies that rotation, header
// persistence, and Reader.Stream all work end-to-end against the real
// state store (RULE-OBS-ROTATE-03).
func TestWriter_Rotate_IntegrationWithState(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Open(dir, slog.Default())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}

	day1 := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	tick := day1

	ch := &probe.ControllableChannel{PWMPath: "/sys/test/pwm1", Driver: "nct6775"}

	// Construct with clock injection using real state backends.
	w2, err := newWithClock(
		st.Log, st.KV,
		[]*probe.ControllableChannel{ch},
		"testfp01", "v0.5.4", slog.Default(),
		func() time.Time { return tick },
	)
	if err != nil {
		t.Fatalf("newWithClock: %v", err)
	}

	id := ChannelID(ch.PWMPath)

	// Write two records on day1.
	for i := range 2 {
		if err := w2.Append(&Record{Ts: int64(i + 1), ChannelID: id}); err != nil {
			t.Fatalf("Append day1[%d]: %v", i, err)
		}
	}

	// Advance to day2 — next Append triggers rotation.
	tick = day2
	if err := w2.Append(&Record{Ts: 3, ChannelID: id}); err != nil {
		t.Fatalf("Append day2: %v", err)
	}

	// Reader must stream all 3 records in order.
	rd := NewReader(st.Log)
	var tss []int64
	if err := rd.Stream(time.Time{}, func(r *Record) bool {
		tss = append(tss, r.Ts)
		return true
	}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(tss) != 3 {
		t.Fatalf("stream count: got %d, want 3", len(tss))
	}
	for i, ts := range tss {
		if ts != int64(i+1) {
			t.Errorf("tss[%d]: got %d, want %d", i, ts, int64(i+1))
		}
	}
}

// TestWriter_Rotate_RetentionPolicyApplied verifies that the Writer calls
// SetRotationPolicy at construction with writerPolicy — disabling LogDB
// auto-rotation while preserving pruning parameters (RULE-OBS-ROTATE-04).
func TestWriter_Rotate_RetentionPolicyApplied(t *testing.T) {
	clock := func() time.Time { return testBaseTime }
	_, env := newTestWriterWithClock(t, nil, clock)

	got := env.log.policy
	// Writer handles rotation itself; MaxSizeMB=0 and MaxAgeDays=0 disable
	// LogDB's own rotation triggers so headers are never transparently skipped.
	if got.MaxSizeMB != 0 {
		t.Errorf("MaxSizeMB: got %d, want 0 (LogDB auto-rotation disabled)", got.MaxSizeMB)
	}
	if got.MaxAgeDays != 0 {
		t.Errorf("MaxAgeDays: got %d, want 0 (LogDB auto-rotation disabled)", got.MaxAgeDays)
	}
	// Pruning parameters must match DefaultRotationPolicy so LogDB still
	// manages file retention after the Writer calls Rotate().
	if got.KeepCount != DefaultRotationPolicy.KeepCount {
		t.Errorf("KeepCount: got %d, want %d", got.KeepCount, DefaultRotationPolicy.KeepCount)
	}
	if got.CompressOld != DefaultRotationPolicy.CompressOld {
		t.Errorf("CompressOld: got %v, want %v", got.CompressOld, DefaultRotationPolicy.CompressOld)
	}
}
