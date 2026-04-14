package controller

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/nvidia"
)

func TestParseNvidiaIndex(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    uint
		wantErr bool
	}{
		{"valid_zero", "0", 0, false},
		{"valid_positive", "3", 3, false},
		{"leading_zero", "007", 7, false},
		{"empty", "", 0, true},
		{"non_numeric", "gpu0", 0, true},
		{"negative", "-1", 0, true},
		{"whitespace", " 0", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseNvidiaIndex(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil (value=%d)", got)
				}
				if !strings.Contains(err.Error(), tc.input) && tc.input != "" {
					t.Errorf("error %q does not echo input %q", err.Error(), tc.input)
				}
				if tc.input == "" && !strings.Contains(err.Error(), `""`) {
					t.Errorf("error %q does not quote empty input", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestReadAllSensors_NvidiaPathInvariant pins the invariant that a non-numeric
// nvidia sensor path at runtime is a config-load bug, not a silent-fallback
// "read GPU 0" situation. Valid paths must forward to the nvidia reader with
// the parsed index; invalid paths must skip the sensor (absent from the map)
// and must never cause the reader to be called with a bogus index.
//
// These subtests mutate the package-level readNvidiaMetric and therefore must
// not call t.Parallel(). t.Cleanup restores the production value after each
// subtest as defence-in-depth.
func TestReadAllSensors_NvidiaPathInvariant(t *testing.T) {
	// One hwmon sibling shared across all subtests: its presence in the
	// returned map proves the loop kept going after the bad nvidia entry.
	dir := t.TempDir()
	hwmonPath := filepath.Join(dir, "temp1_input")
	if err := os.WriteFile(hwmonPath, []byte("42000\n"), 0o600); err != nil {
		t.Fatalf("write hwmon fixture: %v", err)
	}
	hwmonSensor := config.Sensor{Name: "cpu", Type: "hwmon", Path: hwmonPath}

	cases := []struct {
		name    string
		path    string
		wantOK  bool // true: nvidia reader should be called and result landed in map
		wantIdx uint
	}{
		{"valid_zero", "0", true, 0},
		{"valid_positive", "3", true, 3},
		{"empty", "", false, 0},
		{"non_numeric", "gpu0", false, 0},
		{"negative", "-1", false, 0},
		{"overflow", "4294967296", false, 0}, // uint32 max + 1
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Do NOT call t.Parallel — readNvidiaMetric is a mutable global.
			t.Cleanup(func() { readNvidiaMetric = nvidia.ReadMetric })

			var (
				called  bool
				gotIdx  uint
				gotName string
			)
			readNvidiaMetric = func(idx uint, metric string) (float64, error) {
				called = true
				gotIdx = idx
				gotName = metric
				return 77.0, nil
			}

			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			nvSensor := config.Sensor{
				Name:   "gpu",
				Type:   "nvidia",
				Path:   tc.path,
				Metric: "temperature",
			}
			m := readAllSensors(logger, []config.Sensor{nvSensor, hwmonSensor})

			// The hwmon sibling must always land in the map — skip-and-continue
			// means one bad sensor never hides a healthy one.
			if v, ok := m["cpu"]; !ok || v != 42.0 {
				t.Errorf("hwmon sibling: got value=%v present=%v, want 42.0 present", v, ok)
			}

			if tc.wantOK {
				if !called {
					t.Fatalf("readNvidiaMetric was not called for valid path %q", tc.path)
				}
				if gotIdx != tc.wantIdx {
					t.Errorf("readNvidiaMetric idx: got %d want %d", gotIdx, tc.wantIdx)
				}
				if gotName != "temperature" {
					t.Errorf("readNvidiaMetric metric: got %q want %q", gotName, "temperature")
				}
				if v, ok := m["gpu"]; !ok || v != 77.0 {
					t.Errorf("gpu sensor: got value=%v present=%v, want 77.0 present", v, ok)
				}
				if strings.Contains(buf.String(), "invariant violated") {
					t.Errorf("unexpected invariant log for valid path %q: %s", tc.path, buf.String())
				}
				return
			}

			// Invalid-path path: reader must never run with a bogus index,
			// sensor must be absent, and the invariant Error must be logged.
			if called {
				t.Errorf("readNvidiaMetric must not be called for bad path %q (idx=%d)", tc.path, gotIdx)
			}
			if _, ok := m["gpu"]; ok {
				t.Errorf("gpu sensor must be absent for bad path %q, got %v", tc.path, m["gpu"])
			}
			out := buf.String()
			if !strings.Contains(out, "invariant violated") {
				t.Errorf("expected invariant log for bad path %q, got: %s", tc.path, out)
			}
			if !strings.Contains(out, "level=ERROR") {
				t.Errorf("expected ERROR level for bad path %q, got: %s", tc.path, out)
			}
		})
	}
}
