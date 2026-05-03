package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ventd/ventd/internal/ndjson"
	"github.com/ventd/ventd/internal/observation"
	"github.com/ventd/ventd/internal/state"
)

// runDiagExportObservations implements `ventd diag export-observations`.
//
// Reads the binary msgpack observation log written by the v0.5.4+ controller
// and emits one NDJSON line per record so operators can `jq`/`grep` it
// during triage. The schema version is `SchemaVentdJournalV1` (1.0); each
// record is wrapped in the standard `{schema_version, ts, event_type,
// payload}` envelope from internal/ndjson. The `ts` field on the envelope
// is the export-time wall clock; the original record's `Ts` (UnixMicro) is
// preserved inside `payload`.
//
// The state directory is read-only here — no lock is taken. Running this
// concurrently with the daemon is safe as long as the daemon's writer
// owns rotation (we never truncate or rotate from this path).
//
// Bound: RULE-DIAG-EXPORT-01 (round-trip: observation.Record →
// NDJSON envelope → observation.Record reconstructible).
func runDiagExportObservations(args []string, logger *slog.Logger) error {
	var (
		outPath  string
		since    time.Time
		useGzip  bool
		stateDir = state.DefaultDir
	)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--out" && i+1 < len(args):
			i++
			outPath = args[i]
		case strings.HasPrefix(arg, "--out="):
			outPath = strings.TrimPrefix(arg, "--out=")
		case arg == "--since" && i+1 < len(args):
			i++
			t, err := time.Parse(time.RFC3339, args[i])
			if err != nil {
				return fmt.Errorf("diag export-observations: parse --since %q: %w", args[i], err)
			}
			since = t
		case strings.HasPrefix(arg, "--since="):
			s := strings.TrimPrefix(arg, "--since=")
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return fmt.Errorf("diag export-observations: parse --since %q: %w", s, err)
			}
			since = t
		case arg == "--gzip":
			useGzip = true
		case arg == "--state-dir" && i+1 < len(args):
			i++
			stateDir = args[i]
		case strings.HasPrefix(arg, "--state-dir="):
			stateDir = strings.TrimPrefix(arg, "--state-dir=")
		case arg == "--help", arg == "-h":
			printDiagExportObservationsHelp()
			return nil
		}
	}

	st, err := state.Open(stateDir, logger)
	if err != nil {
		return fmt.Errorf("diag export-observations: open state %s: %w", stateDir, err)
	}
	defer func() { _ = st.Close() }()

	w, closer, err := openExportWriter(outPath, useGzip)
	if err != nil {
		return fmt.Errorf("diag export-observations: open output: %w", err)
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}

	rd := observation.NewReader(st.Log)
	count := 0
	streamErr := rd.Stream(since, func(r *observation.Record) bool {
		if err := w.WrapEvent("observation_record", r); err != nil {
			logger.Error("export-observations: wrap event", "err", err)
			return false // stop iteration
		}
		count++
		return true
	})
	if streamErr != nil && !errors.Is(streamErr, io.EOF) {
		return fmt.Errorf("diag export-observations: stream: %w", streamErr)
	}
	logger.Info("export-observations: done", "records", count, "out", outPath, "gzip", useGzip)
	return nil
}

// openExportWriter resolves the output path → ndjson.Writer + an optional
// closer for the underlying file handle. Empty path → stdout (no closer).
func openExportWriter(path string, useGzip bool) (*ndjson.Writer, io.Closer, error) {
	schema := ndjson.SchemaVentdJournalV1
	if path == "" {
		return ndjson.NewWriter(os.Stdout, schema), nil, nil
	}
	if useGzip {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return nil, nil, err
		}
		w, err := ndjson.NewGzipWriter(f, schema)
		if err != nil {
			_ = f.Close()
			return nil, nil, err
		}
		// Caller closes the ndjson writer (which closes gzip + file).
		return w, w, nil
	}
	w, err := ndjson.NewFileWriter(path, schema)
	if err != nil {
		return nil, nil, err
	}
	return w, w, nil
}

func printDiagExportObservationsHelp() {
	fmt.Print(`Usage: ventd diag export-observations [flags]

Exports the binary msgpack observation log to NDJSON for offline analysis
(jq, grep, awk, …). Each record becomes one line wrapped in the standard
ndjson envelope: {schema_version, ts, event_type:"observation_record",
payload: <Record>}.

Flags:
  --out <path>          Output file (default: stdout). Created with mode 0600.
  --since <RFC3339>     Only export records with Ts >= this timestamp.
  --gzip                Gzip-compress the output (writes raw .gz body).
  --state-dir <path>    Override state dir (default: /var/lib/ventd or $XDG_STATE_HOME/ventd).
  --help                Show this help.

Examples:
  ventd diag export-observations | jq 'select(.payload.rpm > 1500)'
  ventd diag export-observations --since 2026-05-01T00:00:00Z --out today.ndjson
  ventd diag export-observations --gzip --out /tmp/obs.ndjson.gz
`)
}
