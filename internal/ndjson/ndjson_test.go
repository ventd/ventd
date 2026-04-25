package ndjson_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/ndjson"
)

type testEvent struct {
	SchemaVersion ndjson.SchemaVersion `json:"schema_version"`
	Timestamp     string               `json:"ts"`
	EventType     string               `json:"event_type"`
	Value         int                  `json:"value"`
}

func TestWriter_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := ndjson.NewWriter(&buf, "1.0")

	events := []testEvent{
		{SchemaVersion: "1.0", Timestamp: "2026-04-26T00:00:00Z", EventType: "test", Value: 1},
		{SchemaVersion: "1.0", Timestamp: "2026-04-26T00:00:01Z", EventType: "test", Value: 2},
	}
	for _, e := range events {
		if err := w.WriteEvent(e); err != nil {
			t.Fatalf("WriteEvent: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r := ndjson.NewReader(&buf, "1.0")
	defer func() { _ = r.Close() }()
	for i, want := range events {
		var got testEvent
		if err := r.Read(&got); err != nil {
			t.Fatalf("Read[%d]: %v", i, err)
		}
		if got.Value != want.Value {
			t.Errorf("Read[%d]: got value %d, want %d", i, got.Value, want.Value)
		}
	}
	var dummy testEvent
	if err := r.Read(&dummy); err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestWriter_GzipRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w, err := ndjson.NewGzipWriter(&buf, "1.0")
	if err != nil {
		t.Fatalf("NewGzipWriter: %v", err)
	}
	if err := w.WrapEvent("ping", map[string]string{"hello": "world"}); err != nil {
		t.Fatalf("WrapEvent: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := ndjson.NewGzipReader(&buf, "1.0")
	if err != nil {
		t.Fatalf("NewGzipReader: %v", err)
	}
	defer func() { _ = r.Close() }()
	var got json.RawMessage
	if err := r.Read(&got); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Contains(got, []byte(`"ping"`)) {
		t.Errorf("decoded event missing event_type: %s", got)
	}
}

func TestReader_SchemaMajorMismatch(t *testing.T) {
	// Write a v2 event, try to read with v1 reader.
	line := `{"schema_version":"2.0","ts":"2026-04-26T00:00:00Z","event_type":"x"}` + "\n"
	r := ndjson.NewReader(strings.NewReader(line), "1.0")
	defer func() { _ = r.Close() }()
	var got json.RawMessage
	err := r.Read(&got)
	if err == nil {
		t.Fatal("expected schema mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "schema") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReader_SameMinorOK(t *testing.T) {
	// v1.5 is compatible with v1.0 (same major).
	line := `{"schema_version":"1.5","ts":"2026-04-26T00:00:00Z","event_type":"x","value":42}` + "\n"
	r := ndjson.NewReader(strings.NewReader(line), "1.0")
	defer func() { _ = r.Close() }()
	var got testEvent
	if err := r.Read(&got); err != nil {
		t.Fatalf("Read: %v", err)
	}
}

func TestSchemaVersion_Major(t *testing.T) {
	cases := []struct {
		v    ndjson.SchemaVersion
		want int
	}{
		{"1.0", 1},
		{"2.3", 2},
		{"10.0", 10},
		{"0.1", 0},
	}
	for _, c := range cases {
		if got := c.v.Major(); got != c.want {
			t.Errorf("Major(%q) = %d, want %d", c.v, got, c.want)
		}
	}
}
