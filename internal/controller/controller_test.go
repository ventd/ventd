package controller

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/watchdog"
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
			m := make(map[string]float64)
			readAllSensors(logger, []config.Sensor{nvSensor, hwmonSensor}, m)

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

// TestTick_WriteRetryAndRestoreOnDoubleFailure asserts the full retry+restore
// path: two consecutive Write failures trigger (a) a write_retry WARN on the
// first failure, (b) a write_failed_restore_triggered ERROR on the second, and
// (c) exactly two Write calls total — one initial plus one retry.
//
// The watchdog is seeded with origEnable=2 for the fan path; after the tick,
// the enable file must read "2" (RestoreOne fired and wrote the origEnable back).
func TestTick_WriteRetryAndRestoreOnDoubleFailure(t *testing.T) {
	ff := newFakeFan(t) // pwm_enable=2

	cfg := &config.Config{
		Sensors: []config.Sensor{{Name: "cpu", Type: "hwmon", Path: ff.tempPath}},
		Fans: []config.Fan{{
			Name: "cpu fan", Type: "hwmon", PWMPath: ff.pwmPath,
			MinPWM: 40, MaxPWM: 200,
		}},
		Curves:   []config.CurveConfig{{Name: "cpu_curve", Type: "fixed", Value: 100}},
		Controls: []config.Control{{Fan: "cpu fan", Curve: "cpu_curve"}},
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	wd := watchdog.New(logger)
	wd.Register(ff.pwmPath, "hwmon") // origEnable=2 captured at registration

	cfgPtr := &atomic.Pointer[config.Config]{}
	cfgPtr.Store(cfg)
	c := New("cpu fan", "cpu_curve", ff.pwmPath, "hwmon", cfgPtr, wd, &stubCal{}, logger)

	// Inject a fake backend whose first two Writes fail.
	writeErr := errors.New("sysfs write failed")
	fb := &fakeErrBackend{errs: []error{writeErr, writeErr}}
	c.backend = fb

	// Simulate daemon taking manual control (enable=1) so RestoreOne's write
	// of origEnable=2 is observable as a change.
	if err := os.WriteFile(ff.enablePath, []byte("1\n"), 0o600); err != nil {
		t.Fatalf("perturb enable: %v", err)
	}

	c.tick()

	// RestoreOne must have fired: enable file restored to the captured origEnable (2).
	if got := readIntFile(t, ff.enablePath); got != 2 {
		t.Errorf("pwm_enable after double-fail tick = %d, want 2 (RestoreOne must have fired)", got)
	}

	// Structured event must appear in logs.
	logs := logBuf.String()
	if !strings.Contains(logs, "write_failed_restore_triggered") {
		t.Errorf("expected write_failed_restore_triggered event in logs, got: %s", logs)
	}

	// Write called exactly twice: initial attempt + one retry.
	if fb.writeCalls != 2 {
		t.Errorf("Write called %d times, want 2 (initial + one retry)", fb.writeCalls)
	}
}

// fakeErrBackend is a hal.FanBackend whose Write returns pre-configured errors
// for the first N calls, then succeeds. Other methods are no-ops.
type fakeErrBackend struct {
	writeCalls int
	errs       []error
}

func (b *fakeErrBackend) Enumerate(_ context.Context) ([]hal.Channel, error) {
	return nil, nil
}
func (b *fakeErrBackend) Read(_ hal.Channel) (hal.Reading, error) { return hal.Reading{}, nil }
func (b *fakeErrBackend) Write(_ hal.Channel, _ uint8) error {
	i := b.writeCalls
	b.writeCalls++
	if i < len(b.errs) {
		return b.errs[i]
	}
	return nil
}
func (b *fakeErrBackend) Restore(_ hal.Channel) error { return nil }
func (b *fakeErrBackend) Close() error                { return nil }
func (b *fakeErrBackend) Name() string                { return "fake" }
