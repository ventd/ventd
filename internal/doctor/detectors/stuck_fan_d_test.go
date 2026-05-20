package detectors

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/doctor"
)

// fakeStuckFanLoader is a stub StuckFanArtifactLoader used by tests
// to inject canned exclusion reasons without staging a state.json.
type fakeStuckFanLoader struct {
	reasons map[string]string
	err     error
}

func (f fakeStuckFanLoader) LoadStuckFanReasons() (map[string]string, error) {
	return f.reasons, f.err
}

// stageStuckHwmonFixture writes a minimal hwmon tree under root with
// one chip and the given pwm fixtures. Returns root.
type stuckPwmFixture struct {
	idx      int
	enable   int
	pwm      int
	rpm      int
	label    string
	noTach   bool // when true, omit fan<N>_input entirely
	noEnable bool // when true, omit pwm<N>_enable entirely
}

func stageStuckHwmonFixture(t *testing.T, chipName string, pwms []stuckPwmFixture) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "sys", "class", "hwmon")
	chipDir := filepath.Join(root, "hwmon0")
	if err := os.MkdirAll(chipDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chipDir, "name"), []byte(chipName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range pwms {
		idx := strconv.Itoa(p.idx)
		if err := os.WriteFile(filepath.Join(chipDir, "pwm"+idx), []byte(strconv.Itoa(p.pwm)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !p.noEnable {
			if err := os.WriteFile(filepath.Join(chipDir, "pwm"+idx+"_enable"), []byte(strconv.Itoa(p.enable)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if !p.noTach {
			if err := os.WriteFile(filepath.Join(chipDir, "fan"+idx+"_input"), []byte(strconv.Itoa(p.rpm)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if p.label != "" {
			if err := os.WriteFile(filepath.Join(chipDir, "pwm"+idx+"_label"), []byte(p.label+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func depsAt(now time.Time) doctor.Deps {
	return doctor.Deps{Now: func() time.Time { return now }}
}

// TestStuckFanDetector_EmitsPerStuckChannel covers the canonical
// case from the issue body: one healthy fan + multiple stuck channels
// in different exclusion states. Each stuck channel produces one
// Warning Fact; the healthy channel produces nothing.
func TestStuckFanDetector_EmitsPerStuckChannel(t *testing.T) {
	root := stageStuckHwmonFixture(t, "nct6687", []stuckPwmFixture{
		{idx: 1, enable: 1, pwm: 80, rpm: 1500, label: "CPU Fan"},  // healthy
		{idx: 2, enable: 1, pwm: 70, rpm: 0, label: "Front Fan 1"}, // stuck, mode_mismatch
		{idx: 3, enable: 2, pwm: 70, rpm: 0, label: "Front Fan 2"}, // stuck, disconnected_suspected
		{idx: 4, enable: 1, pwm: 70, rpm: 0, label: "Rear Fan"},    // stuck, will join to detect_failed exclusion
	})

	loader := fakeStuckFanLoader{
		reasons: map[string]string{
			filepath.Join(root, "hwmon0", "pwm4"): "no_sensor_correlated",
		},
	}

	det := &StuckFanDetector{HwmonRoot: root, Loader: loader, DMIFS: os.DirFS("/")}
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	facts, err := det.Probe(context.Background(), depsAt(now))
	if err != nil {
		t.Fatalf("Probe error: %v", err)
	}
	if len(facts) != 3 {
		t.Fatalf("expected 3 facts (3 stuck channels); got %d:\n%+v", len(facts), facts)
	}

	byEntity := map[string]doctor.Fact{}
	for _, f := range facts {
		byEntity[f.EntityHash] = f
		if f.Severity != doctor.SeverityWarning {
			t.Errorf("fact %q severity = %v, want Warning", f.Title, f.Severity)
		}
		if f.Detector != "stuck_fan_diagnosis" {
			t.Errorf("detector name = %q, want stuck_fan_diagnosis", f.Detector)
		}
		if f.Observed != now {
			t.Errorf("observed = %v, want %v", f.Observed, now)
		}
	}

	// Each stuck channel gets a unique EntityHash so the suppression
	// store can dismiss one without affecting the others.
	if len(byEntity) != 3 {
		t.Errorf("EntityHash collision among 3 stuck channels: %+v", byEntity)
	}

	// Classifications: pwm2 mode_mismatch, pwm3 disconnected_suspected,
	// pwm4 excluded:detect_failed. Cross-check via Title content.
	titles := []string{}
	for _, f := range facts {
		titles = append(titles, f.Title)
	}
	joined := strings.Join(titles, "\n")
	for _, want := range []string{"mode_mismatch", "disconnected_suspected", "excluded:detect_failed"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected classification %q in titles; got:\n%s", want, joined)
		}
	}
}

// TestStuckFanDetector_HealthyHostEmitsNothing — an all-healthy host
// produces zero facts. Distinct from the "no candidates" case below.
func TestStuckFanDetector_HealthyHostEmitsNothing(t *testing.T) {
	root := stageStuckHwmonFixture(t, "it8688e", []stuckPwmFixture{
		{idx: 1, enable: 1, pwm: 80, rpm: 1200},
		{idx: 2, enable: 1, pwm: 100, rpm: 1500},
		{idx: 3, enable: 2, pwm: 0, rpm: 800}, // BIOS auto + low PWM, RPM > 0 — fine
	})
	det := &StuckFanDetector{HwmonRoot: root, DMIFS: os.DirFS("/")}
	facts, err := det.Probe(context.Background(), depsAt(time.Now()))
	if err != nil {
		t.Fatalf("Probe error: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("healthy host should emit zero facts; got %d: %+v", len(facts), facts)
	}
}

// TestStuckFanDetector_NoWalkableHwmon — a missing hwmon root
// returns no facts and no error (the detector degrades cleanly on
// container / restricted-sysfs hosts).
func TestStuckFanDetector_NoWalkableHwmon(t *testing.T) {
	det := &StuckFanDetector{HwmonRoot: filepath.Join(t.TempDir(), "does-not-exist")}
	facts, err := det.Probe(context.Background(), depsAt(time.Now()))
	if err != nil {
		t.Errorf("missing hwmon root should not error; got %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("missing hwmon root should produce zero facts; got %d", len(facts))
	}
}

// TestStuckFanDetector_LowPWMIsNotStuck — a fan at PWM=20 with RPM=0
// is below the stiction floor and not classified as stuck; that's
// working as intended (low duty cycle, fan parked).
func TestStuckFanDetector_LowPWMIsNotStuck(t *testing.T) {
	root := stageStuckHwmonFixture(t, "nct6687", []stuckPwmFixture{
		{idx: 1, enable: 1, pwm: 20, rpm: 0}, // below threshold
	})
	det := &StuckFanDetector{HwmonRoot: root, DMIFS: os.DirFS("/")}
	facts, err := det.Probe(context.Background(), depsAt(time.Now()))
	if err != nil {
		t.Fatalf("Probe error: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("PWM=20 + RPM=0 should NOT be stuck (below stiction floor); got %d facts: %+v",
			len(facts), facts)
	}
}

// TestStuckFanDetector_LoaderErrorEmitsSingleSelfFact — when the
// state-file loader errors out, the detector emits one Warning Fact
// describing its own degraded state rather than silently no-op'ing.
func TestStuckFanDetector_LoaderErrorEmitsSingleSelfFact(t *testing.T) {
	det := &StuckFanDetector{
		HwmonRoot: "/sys/class/hwmon",
		Loader:    fakeStuckFanLoader{err: os.ErrPermission},
		DMIFS:     os.DirFS("/"),
	}
	facts, err := det.Probe(context.Background(), depsAt(time.Now()))
	if err != nil {
		t.Fatalf("Probe error: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("loader error should produce 1 self-diagnostic fact; got %d", len(facts))
	}
	if !strings.Contains(facts[0].Title, "wizard state file") {
		t.Errorf("self-diagnostic title should mention the state file; got %q", facts[0].Title)
	}
}

// TestStuckFanDetector_PerVendorGuidance — the guidance picker
// returns vendor-specific text based on DMI BoardVendor. We use a
// fake DMI fs to drive each branch deterministically.
func TestStuckFanDetector_PerVendorGuidance(t *testing.T) {
	tests := []struct {
		vendor     string
		wantSubstr string
	}{
		{"GIGABYTE", "Smart Fan 6"},
		{"ASUSTeK COMPUTER INC.", "Q-Fan"},
		{"Micro-Star International Co., Ltd.", "MSI Center"},
		{"ASRock", "Polychrome"},
		{"Dell Inc.", "SMM"},
		{"LENOVO", "thinkpad_acpi"},
		{"HP", "omen-fan"},
		{"some random vendor", "Smart Fan"}, // generic
	}
	for _, tc := range tests {
		t.Run(tc.vendor, func(t *testing.T) {
			dmiRoot := stageDMIFixture(t, tc.vendor)
			det := &StuckFanDetector{HwmonRoot: "", DMIFS: os.DirFS(dmiRoot)}
			got := det.boardGuidance()
			if !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("vendor %q guidance missing %q; got:\n%s",
					tc.vendor, tc.wantSubstr, got)
			}
		})
	}
}

// stageDMIFixture writes a minimal /sys/class/dmi/id tree at the
// returned root so hwdb.ReadDMI(os.DirFS(root)) resolves to the
// supplied vendor. Also writes a tiny /proc/cpuinfo so ReadDMI's
// CPUInfo branch doesn't error.
func stageDMIFixture(t *testing.T, boardVendor string) string {
	t.Helper()
	root := t.TempDir()
	dmiDir := filepath.Join(root, "sys", "class", "dmi", "id")
	if err := os.MkdirAll(dmiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dmiDir, "board_vendor"), []byte(boardVendor+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	procDir := filepath.Join(root, "proc")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "cpuinfo"), []byte("model name\t: Test CPU\nprocessor\t: 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestStuckFanDetector_CalibrateExclusionFromState — when the
// state file loader returns a calibrate_phantom reason for a path,
// the detector surfaces "excluded:cal_aborted" classification.
func TestStuckFanDetector_CalibrateExclusionFromState(t *testing.T) {
	root := stageStuckHwmonFixture(t, "nct6687", []stuckPwmFixture{
		{idx: 1, enable: 1, pwm: 100, rpm: 0},
	})
	pwm1 := filepath.Join(root, "hwmon0", "pwm1")
	loader := fakeStuckFanLoader{reasons: map[string]string{pwm1: "calibrate_phantom:no_sustained_spin"}}
	det := &StuckFanDetector{HwmonRoot: root, Loader: loader, DMIFS: os.DirFS("/")}
	facts, err := det.Probe(context.Background(), depsAt(time.Now()))
	if err != nil {
		t.Fatalf("Probe error: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact; got %d", len(facts))
	}
	if !strings.Contains(facts[0].Title, "excluded:cal_aborted") {
		t.Errorf("title missing calibrate-aborted classification; got %q", facts[0].Title)
	}
	if !strings.Contains(facts[0].Detail, "calibrate_phantom:no_sustained_spin") {
		t.Errorf("detail should surface the exclusion reason: %q", facts[0].Detail)
	}
}
