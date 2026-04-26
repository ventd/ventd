package calibration

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ventd/ventd/internal/hwdb"
)

// CaptureHookFn is the callback type fired after a successful calibration save.
type CaptureHookFn func(*hwdb.CalibrationRun)

// captureHook is the package-level post-save hook. Set at daemon startup.
var captureHook atomic.Pointer[CaptureHookFn]

// captureWG tracks in-flight capture goroutines for goleak compatibility.
var captureWG sync.WaitGroup

// SetCaptureHook registers fn to be called asynchronously after each
// successful Store.Save. Pass nil to disable. Safe for concurrent use.
func SetCaptureHook(fn CaptureHookFn) {
	if fn == nil {
		captureHook.Store(nil)
		return
	}
	captureHook.Store(&fn)
}

// WaitCapture blocks until all in-flight capture goroutines spawned by
// Store.Save have returned. Used in tests alongside goleak.VerifyNone.
func WaitCapture() { captureWG.Wait() }

// reSafe matches characters allowed in a filename component.
var reSafe = regexp.MustCompile(`[^a-zA-Z0-9]`)

// safeName sanitises a string for use in a filename by replacing any character
// outside [a-zA-Z0-9] with a hyphen, then collapsing runs of hyphens.
func safeName(s string) string {
	safe := reSafe.ReplaceAllString(s, "-")
	// Collapse runs of dashes.
	for strings.Contains(safe, "--") {
		safe = strings.ReplaceAll(safe, "--", "-")
	}
	return strings.Trim(safe, "-")
}

// Store manages reading and writing CalibrationRun JSON files under a
// directory tree. The default production path is /var/lib/ventd/calibration/.
// RULE-CALIB-PR2B-12.
type Store struct {
	dir string
}

// NewStore returns a Store rooted at dir.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Filename returns the sanitised filename for the given fingerprint and BIOS version.
// Format: <dmi_fingerprint>-<bios_version_safe>.json
func (s *Store) Filename(dmiFingerprint, biosVersion string) string {
	return fmt.Sprintf("%s-%s.json", dmiFingerprint, safeName(biosVersion))
}

// path returns the full file path for the given fingerprint + BIOS version.
func (s *Store) path(dmiFingerprint, biosVersion string) string {
	return filepath.Join(s.dir, s.Filename(dmiFingerprint, biosVersion))
}

// Save writes run to disk as JSON. On success, fires the capture hook (if
// registered) in a background goroutine. Capture is best-effort — a hook
// failure never blocks or fails the calibration write.
func (s *Store) Save(run *hwdb.CalibrationRun) error {
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return fmt.Errorf("calibration store mkdir: %w", err)
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("calibration store marshal: %w", err)
	}
	p := s.path(run.DMIFingerprint, run.BIOSVersion)
	if err := os.WriteFile(p, data, 0o640); err != nil {
		return fmt.Errorf("calibration store write %s: %w", p, err)
	}
	if fp := captureHook.Load(); fp != nil {
		fn := *fp
		captureWG.Add(1)
		go func() {
			defer captureWG.Done()
			fn(run)
		}()
	}
	return nil
}

// Load reads the calibration run for the given fingerprint and BIOS version.
// Returns (nil, nil) if no file exists (not-yet-calibrated is not an error).
func (s *Store) Load(dmiFingerprint, biosVersion string) (*hwdb.CalibrationRun, error) {
	p := s.path(dmiFingerprint, biosVersion)
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("calibration store read %s: %w", p, err)
	}
	var run hwdb.CalibrationRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("calibration store parse %s: %w", p, err)
	}
	return &run, nil
}
