package ndjson

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Reader reads schema-versioned NDJSON events from an underlying reader.
// It is strict-major: the first line's schema_version major must match
// the expected schema's major or Read returns ErrSchemaMismatch.
type Reader struct {
	schema  SchemaVersion
	scanner *bufio.Scanner
	closers []io.Closer
	checked bool
}

// ErrSchemaMismatch is returned when the major schema version in an
// event line does not match the Reader's expected schema major.
var ErrSchemaMismatch = errors.New("ndjson: schema major mismatch")

// NewReader creates a Reader that reads plain NDJSON from r.
func NewReader(r io.Reader, schema SchemaVersion) *Reader {
	return &Reader{schema: schema, scanner: bufio.NewScanner(r)}
}

// NewGzipReader creates a Reader that decompresses gzip-NDJSON from r.
// Returns an error if the gzip header is invalid.
func NewGzipReader(r io.Reader, schema SchemaVersion) (*Reader, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("ndjson: gzip reader: %w", err)
	}
	return &Reader{
		schema:  schema,
		scanner: bufio.NewScanner(gz),
		closers: []io.Closer{gz},
	}, nil
}

// NewFileReader opens path for reading and returns a Reader. The file
// may be plain NDJSON or gzip-compressed (detected by magic bytes).
func NewFileReader(path string, schema SchemaVersion) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("ndjson: open %s: %w", path, err)
	}
	// Peek at magic bytes to detect gzip.
	var magic [2]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		f.Close()
		return nil, fmt.Errorf("ndjson: read magic %s: %w", path, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, fmt.Errorf("ndjson: seek %s: %w", path, err)
	}
	if magic[0] == 0x1f && magic[1] == 0x8b {
		r, err := NewGzipReader(f, schema)
		if err != nil {
			f.Close()
			return nil, err
		}
		r.closers = append(r.closers, f)
		return r, nil
	}
	return &Reader{
		schema:  schema,
		scanner: bufio.NewScanner(f),
		closers: []io.Closer{f},
	}, nil
}

// Read decodes the next NDJSON line into dst. dst must be a pointer to
// a struct with JSON tags. Returns io.EOF when there are no more lines.
// Returns ErrSchemaMismatch if the event's major version differs.
func (r *Reader) Read(dst any) error {
	if !r.scanner.Scan() {
		if err := r.scanner.Err(); err != nil {
			return fmt.Errorf("ndjson: scan: %w", err)
		}
		return io.EOF
	}
	line := r.scanner.Bytes()
	if len(line) == 0 {
		return r.Read(dst) // skip blank lines
	}
	// Schema check: peek at schema_version without full decode.
	if !r.checked {
		var hdr Header
		if err := json.Unmarshal(line, &hdr); err != nil {
			return fmt.Errorf("ndjson: header decode: %w", err)
		}
		if hdr.SchemaVersion != "" {
			if err := MustMajorMatch(r.schema, hdr.SchemaVersion); err != nil {
				return fmt.Errorf("%w: %s", ErrSchemaMismatch, err)
			}
		}
		r.checked = true
	}
	if err := json.Unmarshal(line, dst); err != nil {
		return fmt.Errorf("ndjson: decode: %w", err)
	}
	return nil
}

// Close releases any underlying resources.
func (r *Reader) Close() error {
	var first error
	for _, c := range r.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
