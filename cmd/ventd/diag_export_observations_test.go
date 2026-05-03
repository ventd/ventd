package main

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/observation"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
)

// silentExportLogger returns a slog.Logger that drops all output —
// keeps test logs clean.
func silentExportLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestDiagExportObservations_RoundTrip writes three observation
// records via the writer, runs the export subcommand, and asserts
// each NDJSON line round-trips back to a Record with matching
// fields.
//
// Bound: RULE-DIAG-EXPORT-01 (round-trip integrity).
func TestDiagExportObservations_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	logger := silentExportLogger()

	// Open state so the observation writer can claim st.Log.
	st, err := state.Open(dir, logger)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}

	// Seed three records covering different field shapes. SensorReadings
	// values are clamped to int16 (max 32 767) — represents temp °C × 100,
	// e.g. 4500 = 45.00°C.
	channels := []*probe.ControllableChannel{
		{PWMPath: "/sys/class/hwmon/hwmon3/pwm1", Polarity: "normal"},
		{PWMPath: "/sys/class/hwmon/hwmon4/pwm1", Polarity: "normal"},
	}
	w, err := observation.New(st.Log, st.KV, channels, "test-fp", "v0.5.11-test", logger)
	if err != nil {
		t.Fatalf("observation.New: %v", err)
	}

	want := []*observation.Record{
		{
			Ts:              time.Now().UnixMicro(),
			ChannelID:       1,
			PWMWritten:      128,
			PWMEnable:       1,
			ControllerState: observation.ControllerState_CONVERGED,
			RPM:             1100,
			TachTier:        0,
			SensorReadings:  map[uint16]int16{0: 4500, 1: 3800},
			Polarity:        0,
			SignatureLabel:  "abcd1234abcd5678",
			EventFlags:      0,
		},
		{
			Ts:              time.Now().UnixMicro() + 1000,
			ChannelID:       2,
			PWMWritten:      255,
			PWMEnable:       1,
			ControllerState: observation.ControllerState_DRIFTING,
			RPM:             7200,
			TachTier:        1,
			SensorReadings:  map[uint16]int16{2: 4200},
			EventFlags:      observation.EventFlag_OPPORTUNISTIC_PROBE,
		},
		{
			Ts:              time.Now().UnixMicro() + 2000,
			ChannelID:       1,
			PWMWritten:      96,
			PWMEnable:       1,
			ControllerState: observation.ControllerState_CONVERGED,
			RPM:             900,
			SensorReadings:  map[uint16]int16{0: 4700},
		},
	}
	for _, r := range want {
		if err := w.Append(r); err != nil {
			t.Fatalf("observation.Append: %v", err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("state.Close: %v", err)
	}

	// Run the export subcommand to a tempfile.
	outPath := filepath.Join(t.TempDir(), "obs.ndjson")
	args := []string{"--state-dir", dir, "--out", outPath}
	if err := runDiagExportObservations(args, logger); err != nil {
		t.Fatalf("runDiagExportObservations: %v", err)
	}

	// Read the file and decode each line.
	f, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("open out: %v", err)
	}
	defer func() { _ = f.Close() }()

	var got []*observation.Record
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var env struct {
			SchemaVersion string              `json:"schema_version"`
			Timestamp     string              `json:"ts"`
			EventType     string              `json:"event_type"`
			Payload       *observation.Record `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal line %q: %v", scanner.Text(), err)
		}
		if env.SchemaVersion != "1.0" {
			t.Errorf("schema_version = %q, want 1.0", env.SchemaVersion)
		}
		if env.EventType != "observation_record" {
			t.Errorf("event_type = %q, want observation_record", env.EventType)
		}
		if env.Payload == nil {
			t.Fatalf("payload nil")
		}
		got = append(got, env.Payload)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d records, want %d", len(got), len(want))
	}
	for i, r := range got {
		if r.Ts != want[i].Ts {
			t.Errorf("record[%d].Ts = %d, want %d", i, r.Ts, want[i].Ts)
		}
		if r.ChannelID != want[i].ChannelID {
			t.Errorf("record[%d].ChannelID = %d, want %d", i, r.ChannelID, want[i].ChannelID)
		}
		if r.PWMWritten != want[i].PWMWritten {
			t.Errorf("record[%d].PWMWritten = %d, want %d", i, r.PWMWritten, want[i].PWMWritten)
		}
		if r.RPM != want[i].RPM {
			t.Errorf("record[%d].RPM = %d, want %d", i, r.RPM, want[i].RPM)
		}
		if r.SignatureLabel != want[i].SignatureLabel {
			t.Errorf("record[%d].SignatureLabel = %q, want %q", i, r.SignatureLabel, want[i].SignatureLabel)
		}
		if r.EventFlags != want[i].EventFlags {
			t.Errorf("record[%d].EventFlags = %d, want %d", i, r.EventFlags, want[i].EventFlags)
		}
	}
}

// TestDiagExportObservations_Help ensures the --help flag is silent
// (no error, no panic) on the parser side.
func TestDiagExportObservations_Help(t *testing.T) {
	if err := runDiagExportObservations([]string{"--help"}, silentExportLogger()); err != nil {
		t.Errorf("--help returned error: %v", err)
	}
}

// TestDiagExportObservations_BadSince rejects an unparseable timestamp
// before opening state, so a typo doesn't waste an open / close cycle.
func TestDiagExportObservations_BadSince(t *testing.T) {
	err := runDiagExportObservations([]string{"--since", "yesterday"}, silentExportLogger())
	if err == nil {
		t.Fatalf("expected error on bad --since")
	}
}
