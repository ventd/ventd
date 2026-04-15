package hwmon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// recordCapture is a slog.Handler that captures every Record so tests
// can assert on Level, Message and attributes directly without
// string-matching the formatted output.
type recordCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordCapture) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *recordCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *recordCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordCapture) WithGroup(string) slog.Handler      { return h }

func (h *recordCapture) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]slog.Record, len(h.records))
	copy(out, h.records)
	return out
}

// makeHwmonFixture builds a tempdir hwmon tree. chips maps hwmonN to
// (chipName, []pwmFile). pwmFile entries are basenames like "pwm1",
// "pwm2", "pwm1_enable". Files are written as 0644 unless the basename
// is in groupWritable (then 0664).
func makeHwmonFixture(t *testing.T, chips map[string]struct {
	name           string
	files          []string
	groupWritable  []string
}) string {
	t.Helper()
	root := t.TempDir()
	for hwmon, c := range chips {
		dir := filepath.Join(root, hwmon)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if c.name != "" {
			if err := os.WriteFile(filepath.Join(dir, "name"), []byte(c.name+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		gw := map[string]bool{}
		for _, n := range c.groupWritable {
			gw[n] = true
		}
		for _, f := range c.files {
			full := filepath.Join(dir, f)
			if err := os.WriteFile(full, []byte("0\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if gw[f] {
				// Explicit chmod to bypass process umask — WriteFile's
				// mode arg is masked by umask, so 0o664 typically
				// lands as 0o644 on a stock CI runner.
				if err := os.Chmod(full, 0o664); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return root
}

func findRecord(records []slog.Record, msg string) (slog.Record, bool) {
	for _, r := range records {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}

func attrValue(r slog.Record, key string) (slog.Value, bool) {
	var (
		found bool
		v     slog.Value
	)
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = true
			v = a.Value
			return false
		}
		return true
	})
	return v, found
}

func TestDiagnoseHwmon_NoPWMVisible(t *testing.T) {
	// Empty hwmon root → WARN with remediation pointer to --probe-modules.
	root := t.TempDir()
	h := &recordCapture{}
	DiagnoseHwmonAt(slog.New(h), root)

	r, ok := findRecord(h.snapshot(), "hwmon: no PWM channels visible at startup")
	if !ok {
		t.Fatalf("missing 'no PWM' WARN; got %d records", len(h.snapshot()))
	}
	if r.Level != slog.LevelWarn {
		t.Errorf("level: got %v, want WARN", r.Level)
	}
	action, ok := attrValue(r, "action")
	if !ok || action.String() == "" {
		t.Error("missing 'action' attribute")
	}
}

func TestDiagnoseHwmon_PWMVisibleAndWritable(t *testing.T) {
	// Healthy install: pwm1 + pwm1_enable both group-writable; INFO.
	root := makeHwmonFixture(t, map[string]struct {
		name          string
		files         []string
		groupWritable []string
	}{
		"hwmon3": {
			name:          "nct6687",
			files:         []string{"pwm1", "pwm1_enable", "pwm2", "pwm2_enable"},
			groupWritable: []string{"pwm1", "pwm1_enable", "pwm2", "pwm2_enable"},
		},
	})

	h := &recordCapture{}
	DiagnoseHwmonAt(slog.New(h), root)

	r, ok := findRecord(h.snapshot(), "hwmon: PWM channels visible")
	if !ok {
		t.Fatalf("missing healthy INFO; got %d records", len(h.snapshot()))
	}
	if r.Level != slog.LevelInfo {
		t.Errorf("level: got %v, want INFO", r.Level)
	}
	writable, _ := attrValue(r, "writable")
	total, _ := attrValue(r, "total")
	if writable.Int64() != 2 || total.Int64() != 2 {
		t.Errorf("writable=%d total=%d; want both = 2", writable.Int64(), total.Int64())
	}
	chips, _ := attrValue(r, "chips")
	if got := chips.String(); got == "" {
		t.Error("chips attr is empty")
	} else if !contains(got, "nct6687") {
		t.Errorf("chips attr missing chip name: %q", got)
	}
}

func TestDiagnoseHwmon_PWMVisibleButNotWritable(t *testing.T) {
	// Common failure mode: kernel exposes pwm but udev rule never
	// fired (rule missing or system in container without udev).
	// WARN with remediation pointer to udevadm reload+trigger.
	root := makeHwmonFixture(t, map[string]struct {
		name          string
		files         []string
		groupWritable []string
	}{
		"hwmon3": {
			name:  "nct6687",
			files: []string{"pwm1", "pwm1_enable"},
			// groupWritable empty — pwm files default to 0644.
		},
	})

	h := &recordCapture{}
	DiagnoseHwmonAt(slog.New(h), root)

	r, ok := findRecord(h.snapshot(),
		"hwmon: PWM channels visible but none are group-writable for the ventd group")
	if !ok {
		t.Fatalf("missing 'visible-but-not-writable' WARN; records=%d", len(h.snapshot()))
	}
	if r.Level != slog.LevelWarn {
		t.Errorf("level: got %v, want WARN", r.Level)
	}
	action, _ := attrValue(r, "action")
	if !contains(action.String(), "udevadm") {
		t.Errorf("action attr missing remediation pointer: %q", action.String())
	}
}

func TestDiagnoseHwmon_MultipleChipsAggregated(t *testing.T) {
	// Dual-chip board (mb superIO + amdgpu). Both chips appear in
	// the chips attr; counts aggregate across all.
	root := makeHwmonFixture(t, map[string]struct {
		name          string
		files         []string
		groupWritable []string
	}{
		"hwmon3": {
			name:          "nct6687",
			files:         []string{"pwm1", "pwm1_enable", "pwm2", "pwm2_enable"},
			groupWritable: []string{"pwm1", "pwm1_enable", "pwm2", "pwm2_enable"},
		},
		"hwmon4": {
			name:          "amdgpu",
			files:         []string{"pwm1", "pwm1_enable"},
			groupWritable: []string{"pwm1", "pwm1_enable"},
		},
		// Temperature-only chip with no pwm — must not appear in chips list.
		"hwmon0": {
			name:  "coretemp",
			files: []string{"temp1_input", "temp2_input"},
		},
	})

	h := &recordCapture{}
	DiagnoseHwmonAt(slog.New(h), root)

	r, ok := findRecord(h.snapshot(), "hwmon: PWM channels visible")
	if !ok {
		t.Fatal("missing healthy INFO")
	}
	chips, _ := attrValue(r, "chips")
	got := chips.String()
	if !contains(got, "nct6687") || !contains(got, "amdgpu") {
		t.Errorf("chips attr missing one of the expected chips: %q", got)
	}
	if contains(got, "coretemp") {
		t.Errorf("chips attr included coretemp (no pwm — should be omitted): %q", got)
	}
	writable, _ := attrValue(r, "writable")
	total, _ := attrValue(r, "total")
	// 2 from hwmon3 (pwm1, pwm2) + 1 from hwmon4 (pwm1) = 3 each.
	if writable.Int64() != 3 || total.Int64() != 3 {
		t.Errorf("writable=%d total=%d; want both = 3", writable.Int64(), total.Int64())
	}
}

func TestDiagnoseHwmon_NameFileMissingFallsBackToHwmonN(t *testing.T) {
	// Virtual class entry without a name file — should show "hwmonN"
	// in the chips list, not crash.
	root := makeHwmonFixture(t, map[string]struct {
		name          string
		files         []string
		groupWritable []string
	}{
		"hwmon5": {
			name:          "", // no name file
			files:         []string{"pwm1"},
			groupWritable: []string{"pwm1"},
		},
	})

	h := &recordCapture{}
	DiagnoseHwmonAt(slog.New(h), root)

	r, ok := findRecord(h.snapshot(), "hwmon: PWM channels visible")
	if !ok {
		t.Fatal("missing healthy INFO")
	}
	chips, _ := attrValue(r, "chips")
	if !contains(chips.String(), "hwmon5") {
		t.Errorf("chips attr missing hwmonN fallback: %q", chips.String())
	}
}

func TestDiagnoseHwmon_FindsOnlyNumericPWMSuffixes(t *testing.T) {
	// pwm1_enable and pwm1_freq must NOT be counted as pwm channels —
	// only bare pwm<digits>. The current behaviour comes from the
	// suffix-allDigits filter; tested here so that doesn't regress.
	root := makeHwmonFixture(t, map[string]struct {
		name          string
		files         []string
		groupWritable []string
	}{
		"hwmon3": {
			name: "nct6687",
			// Only pwm1 and pwm2 are real channels; the rest are
			// related sysfs nodes that pattern-match "pwm*".
			files:         []string{"pwm1", "pwm2", "pwm1_enable", "pwm2_enable", "pwm1_mode", "pwm1_freq"},
			groupWritable: []string{"pwm1", "pwm2"},
		},
	})

	h := &recordCapture{}
	DiagnoseHwmonAt(slog.New(h), root)

	r, ok := findRecord(h.snapshot(), "hwmon: PWM channels visible")
	if !ok {
		t.Fatal("missing healthy INFO")
	}
	total, _ := attrValue(r, "total")
	if total.Int64() != 2 {
		t.Errorf("total channel count: got %d, want 2 (pwm1+pwm2 only)", total.Int64())
	}
}

func TestDiagnoseHwmon_RootMissingDoesNotPanic(t *testing.T) {
	// Pointing at /var/empty or another non-hwmon dir must not
	// crash; should fall through to the "no PWM visible" path.
	h := &recordCapture{}
	DiagnoseHwmonAt(slog.New(h), "/nonexistent-test-root-xyz")

	if _, ok := findRecord(h.snapshot(),
		"hwmon: no PWM channels visible at startup"); !ok {
		t.Fatal("expected 'no PWM' WARN for missing root")
	}
}

// (the package's existing distro_test.go already provides a
// `contains` helper; we reuse it rather than redeclaring.)
