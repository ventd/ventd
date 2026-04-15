package hwmon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// recCapture is a tiny slog.Handler that captures Records for
// assertion. Local to recover_test.go to avoid coupling with
// diagnose_test.go's recordCapture.
type recCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recCapture) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *recCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *recCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recCapture) WithGroup(string) slog.Handler      { return h }
func (h *recCapture) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]slog.Record, len(h.records))
	copy(out, h.records)
	return out
}

// makeRecoverFixture builds a hwmon tree with the requested
// pwm<N>_enable files. Each file is created with the given initial
// content so tests can verify it gets overwritten with "1\n".
func makeRecoverFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for relPath, initial := range files {
		full := filepath.Join(root, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(initial), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readBack(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestRecoverAllPWM_WritesOneToEveryEnableFile(t *testing.T) {
	root := t.TempDir()
	makeRecoverFixture(t, root, map[string]string{
		"hwmon3/pwm1_enable": "5", // manual mode
		"hwmon3/pwm2_enable": "5",
		"hwmon4/pwm1_enable": "5",
	})

	h := &recCapture{}
	succeeded, failed := RecoverAllPWMAt(slog.New(h), root)

	if succeeded != 3 {
		t.Errorf("succeeded: got %d, want 3", succeeded)
	}
	if failed != 0 {
		t.Errorf("failed: got %d, want 0", failed)
	}
	for _, p := range []string{
		"hwmon3/pwm1_enable", "hwmon3/pwm2_enable", "hwmon4/pwm1_enable",
	} {
		if got := readBack(t, filepath.Join(root, p)); got != "1" {
			t.Errorf("%s after recover: got %q, want %q", p, got, "1")
		}
	}
}

func TestRecoverAllPWM_NoEnableFilesIsHarmlessNoOp(t *testing.T) {
	root := t.TempDir() // empty
	h := &recCapture{}
	s, f := RecoverAllPWMAt(slog.New(h), root)
	if s != 0 || f != 0 {
		t.Errorf("got s=%d f=%d, want 0/0", s, f)
	}
}

func TestRecoverAllPWM_SkipsNonNumericPwmFiles(t *testing.T) {
	// Files like pwm_extra_freq_enable should be ignored — only
	// pwm<digits>_enable counts as a real channel.
	root := t.TempDir()
	makeRecoverFixture(t, root, map[string]string{
		"hwmon3/pwm1_enable":            "5",
		"hwmon3/pwm_extra_freq_enable":  "5", // not pwm<digits>_enable
		"hwmon3/pwmfan_enable":          "5", // not pwm<digits>_enable
	})

	h := &recCapture{}
	s, f := RecoverAllPWMAt(slog.New(h), root)
	if s != 1 {
		t.Errorf("succeeded: got %d, want 1 (only pwm1_enable counts)", s)
	}
	if f != 0 {
		t.Errorf("failed: got %d, want 0", f)
	}
	// pwm_extra_freq_enable must be untouched.
	if got := readBack(t, filepath.Join(root, "hwmon3/pwm_extra_freq_enable")); got != "5" {
		t.Errorf("non-channel file was modified: got %q, want %q", got, "5")
	}
}

func TestRecoverAllPWM_OpenFailureDoesNotAbortLoop(t *testing.T) {
	// One "file" is actually a directory — open(O_WRONLY) on a
	// directory returns EISDIR. Tests the recovery loop's
	// continue-on-error contract without depending on permission
	// bits (the test runner may be root; root bypasses 0o444).
	// On real hwmon, a comparable failure mode is the device being
	// removed (rmmod) between glob and open.
	root := t.TempDir()
	makeRecoverFixture(t, root, map[string]string{
		"hwmon3/pwm1_enable": "5",
		"hwmon5/pwm1_enable": "5",
	})
	// Replace hwmon4/pwm1_enable with a directory so open(O_WRONLY)
	// fails. mkdir -p the path to satisfy the glob.
	badPath := filepath.Join(root, "hwmon4", "pwm1_enable")
	if err := os.MkdirAll(badPath, 0o755); err != nil {
		t.Fatal(err)
	}

	h := &recCapture{}
	s, f := RecoverAllPWMAt(slog.New(h), root)
	if s != 2 {
		t.Errorf("succeeded: got %d, want 2", s)
	}
	if f != 1 {
		t.Errorf("failed: got %d, want 1", f)
	}
	// Verify the writable ones DID get reset despite the directory failure.
	for _, p := range []string{"hwmon3/pwm1_enable", "hwmon5/pwm1_enable"} {
		if got := readBack(t, filepath.Join(root, p)); got != "1" {
			t.Errorf("%s: got %q, want %q (loop aborted on dir-as-file failure?)", p, got, "1")
		}
	}
}

func TestRecoverAllPWM_LogsCompletionWithCounts(t *testing.T) {
	root := t.TempDir()
	makeRecoverFixture(t, root, map[string]string{
		"hwmon3/pwm1_enable": "5",
		"hwmon3/pwm2_enable": "5",
	})

	h := &recCapture{}
	RecoverAllPWMAt(slog.New(h), root)

	var found bool
	for _, r := range h.snapshot() {
		if r.Message == "recover: complete" {
			found = true
			r.Attrs(func(a slog.Attr) bool {
				if a.Key == "succeeded" && a.Value.Int64() != 2 {
					t.Errorf("succeeded attr: got %d, want 2", a.Value.Int64())
				}
				if a.Key == "failed" && a.Value.Int64() != 0 {
					t.Errorf("failed attr: got %d, want 0", a.Value.Int64())
				}
				if a.Key == "total" && a.Value.Int64() != 2 {
					t.Errorf("total attr: got %d, want 2", a.Value.Int64())
				}
				return true
			})
		}
	}
	if !found {
		t.Fatal("missing 'recover: complete' summary log")
	}
}
