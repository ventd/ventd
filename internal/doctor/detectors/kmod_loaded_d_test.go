package detectors

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

// stubHwmonFS lets tests stage a synthetic /sys/class/hwmon tree.
type stubHwmonFS struct {
	names   map[string]string // <hwmonN dir> → contents of name file
	readErr error
}

func (s *stubHwmonFS) ReadDir(path string) ([]os.DirEntry, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	if path != hwmonRoot {
		return nil, errors.New("unexpected dir read")
	}
	out := make([]os.DirEntry, 0, len(s.names))
	for dir := range s.names {
		out = append(out, fakeDirEntry{name: dir})
	}
	return out, nil
}

func (s *stubHwmonFS) ReadFile(name string) ([]byte, error) {
	// name is "/sys/class/hwmon/hwmonN/name"
	dir := filepath.Base(filepath.Dir(name))
	content, ok := s.names[dir]
	if !ok {
		return nil, errFileNotExist
	}
	return []byte(content), nil
}

func TestRULE_DOCTOR_DETECTOR_KmodLoaded_AllExpectedPresent(t *testing.T) {
	fs := &stubHwmonFS{
		names: map[string]string{
			"hwmon0": "nct6687",
			"hwmon1": "coretemp",
			"hwmon2": "amdgpu",
		},
	}
	det := NewKmodLoadedDetector([]string{"nct6687", "coretemp"}, fs)

	facts, err := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("all-loaded emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_KmodLoaded_MissingModuleSurfacesAsBlocker(t *testing.T) {
	fs := &stubHwmonFS{
		names: map[string]string{
			"hwmon0": "coretemp",
			// nct6687 NOT loaded
		},
	}
	det := NewKmodLoadedDetector([]string{"nct6687", "coretemp"}, fs)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for missing module, got %d", len(facts))
	}
	f := facts[0]
	if f.Severity != doctor.SeverityBlocker {
		t.Errorf("Severity = %v, want Blocker", f.Severity)
	}
	if !strings.Contains(f.Title, "nct6687") {
		t.Errorf("Title doesn't name the missing module: %q", f.Title)
	}
	if len(f.Journal) == 0 || !strings.Contains(f.Journal[0], "coretemp") {
		t.Errorf("Journal doesn't list loaded names: %+v", f.Journal)
	}
}

func TestRULE_DOCTOR_DETECTOR_KmodLoaded_MultipleMissing(t *testing.T) {
	fs := &stubHwmonFS{names: map[string]string{}} // nothing loaded
	det := NewKmodLoadedDetector([]string{"nct6687", "coretemp", "amdgpu"}, fs)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 3 {
		t.Errorf("expected 3 facts (one per missing), got %d", len(facts))
	}
}

func TestRULE_DOCTOR_DETECTOR_KmodLoaded_HwmonRootAbsentNoFacts(t *testing.T) {
	// Container or sandbox without /sys/class/hwmon. Detector
	// gracefully degrades — emits 1 Blocker per expected module
	// (empty loaded set).
	fs := &stubHwmonFS{readErr: errors.New("hwmon root missing")}
	det := NewKmodLoadedDetector([]string{"nct6687"}, fs)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Errorf("expected 1 fact (module not loaded because we couldn't read hwmon), got %d", len(facts))
	}
}

func TestRULE_DOCTOR_DETECTOR_KmodLoaded_NoExpectedModulesNoOp(t *testing.T) {
	// Empty ExpectedModules → catalog match didn't declare any.
	// Don't fire even with empty hwmon.
	fs := &stubHwmonFS{names: map[string]string{}}
	det := NewKmodLoadedDetector(nil, fs)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("no-expected-modules emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_KmodLoaded_RespectsContextCancel(t *testing.T) {
	det := NewKmodLoadedDetector([]string{"nct6687"}, &stubHwmonFS{names: map[string]string{}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestLoadedHwmonNames_DedupsAcrossChips(t *testing.T) {
	// Two hwmon dirs reporting the same name → set deduplicates.
	fs := &stubHwmonFS{
		names: map[string]string{
			"hwmon0": "drivetemp",
			"hwmon1": "drivetemp", // second drive, same module
			"hwmon2": "coretemp",
		},
	}
	got := loadedHwmonNames(fs)
	if len(got) != 2 {
		t.Errorf("expected 2 unique names, got %d (%v)", len(got), got)
	}
}

func TestSortedKeys_DeterministicOrder(t *testing.T) {
	in := map[string]struct{}{
		"b": {}, "c": {}, "a": {},
	}
	got := sortedKeys(in)
	if got != "a, b, c" {
		t.Errorf("sortedKeys = %q, want %q", got, "a, b, c")
	}
}
