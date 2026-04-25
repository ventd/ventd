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

// Writer writes schema-versioned NDJSON events to an underlying writer.
// It is safe for concurrent use; each WriteEvent call is atomic.
type Writer struct {
	mu      sync.Mutex
	schema  SchemaVersion
	enc     *json.Encoder
	closers []io.Closer
}

// NewWriter creates a Writer that writes plain NDJSON to w.
func NewWriter(w io.Writer, schema SchemaVersion) *Writer {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Writer{schema: schema, enc: enc}
}

// NewGzipWriter creates a Writer that writes gzip-compressed NDJSON to w.
// The caller must call Close to flush the gzip trailer.
func NewGzipWriter(w io.Writer, schema SchemaVersion) (*Writer, error) {
	gz, err := gzip.NewWriterLevel(w, gzip.DefaultCompression)
	if err != nil {
		return nil, fmt.Errorf("ndjson: gzip writer: %w", err)
	}
	enc := json.NewEncoder(gz)
	enc.SetEscapeHTML(false)
	return &Writer{
		schema:  schema,
		enc:     enc,
		closers: []io.Closer{gz},
	}, nil
}

// NewFileWriter creates a Writer that appends plain NDJSON to a file at path,
// creating it with mode 0o600 if it does not exist.
func NewFileWriter(path string, schema SchemaVersion) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("ndjson: open %s: %w", path, err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return &Writer{
		schema:  schema,
		enc:     enc,
		closers: []io.Closer{f},
	}, nil
}

// WriteEvent marshals event to a single NDJSON line. The event must be
// JSON-marshallable. The schema_version and ts fields are injected from
// the Writer's schema if the event embeds or has those fields — but the
// caller is responsible for setting them on the struct before calling
// WriteEvent. Use WrapEvent for automatic header injection.
func (w *Writer) WriteEvent(event any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.enc.Encode(event); err != nil {
		return fmt.Errorf("ndjson: encode: %w", err)
	}
	return nil
}

// WrapEvent wraps any value with the standard NDJSON envelope (schema_version + ts)
// and writes it as a single line. eventType labels the payload type.
func (w *Writer) WrapEvent(eventType string, payload any) error {
	env := &envelope{
		SchemaVersion: w.schema,
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		EventType:     eventType,
		Payload:       payload,
	}
	return w.WriteEvent(env)
}

// Close flushes and closes any underlying closers (gzip writer, file handle).
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var first error
	for _, c := range w.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// envelope is the generic wrapper written by WrapEvent.
type envelope struct {
	SchemaVersion SchemaVersion `json:"schema_version"`
	Timestamp     string        `json:"ts"`
	EventType     string        `json:"event_type"`
	Payload       any           `json:"payload"`
}
