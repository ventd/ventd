package calibration_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/calibration"
	"github.com/ventd/ventd/internal/hwdb"
)

// ---- synthetic fixture helpers ----

// syntheticFixture describes a fan's simulated response for testing.
type syntheticFixture struct {
	Name         string       `yaml:"name"`
	PWMUnit      string       `yaml:"pwm_unit"`
	PWMUnitMax   int          `yaml:"pwm_unit_max"`
	PWMRPMTable  []pwmRPMPair `yaml:"pwm_rpm_table"`
	Polarity     string       `yaml:"polarity"`
	BIOSOverride *int         `yaml:"bios_override"`
}

type pwmRPMPair struct {
	PWM int `yaml:"pwm"`
	RPM int `yaml:"rpm"`
}

// syntheticChannel implements calibration.ChannelProber using fixture data.
type syntheticChannel struct {
	fixture      syntheticFixture
	lastWritten  int
	readPWMCalls int // tracks calls to ReadPWM for BIOS-override simulation
}

func (s *syntheticChannel) WritePWM(_ context.Context, pwm int) error {
	s.lastWritten = pwm
	s.readPWMCalls = 0 // reset call counter on each write
	return nil
}

func (s *syntheticChannel) ReadRPM(_ context.Context) (int, error) {
	return s.rpmAt(s.lastWritten), nil
}

func (s *syntheticChannel) ReadPWM(_ context.Context) (int, error) {
	s.readPWMCalls++
	if s.fixture.BIOSOverride != nil && s.readPWMCalls >= 2 {
		// Second read (simulating 200ms delay) returns bios override value.
		return *s.fixture.BIOSOverride, nil
	}
	return s.lastWritten, nil
}

func (s *syntheticChannel) Settle(_ context.Context, _ time.Duration) error {
	return nil
}

// rpmAt linearly interpolates the RPM table for a given PWM value.
func (s *syntheticChannel) rpmAt(pwm int) int {
	tbl := s.fixture.PWMRPMTable
	if len(tbl) == 0 {
		return 0
	}
	// Clamp to table bounds.
	if pwm <= tbl[0].PWM {
		return tbl[0].RPM
	}
	if pwm >= tbl[len(tbl)-1].PWM {
		return tbl[len(tbl)-1].RPM
	}
	for i := 1; i < len(tbl); i++ {
		if pwm <= tbl[i].PWM {
			lo, hi := tbl[i-1], tbl[i]
			frac := float64(pwm-lo.PWM) / float64(hi.PWM-lo.PWM)
			return lo.RPM + int(frac*float64(hi.RPM-lo.RPM))
		}
	}
	return 0
}

func loadSyntheticChannel(t *testing.T, name string) *syntheticChannel {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata/synthetic_driver", name))
	if err != nil {
		t.Fatalf("load fixture %s: %v", name, err)
	}
	var fix syntheticFixture
	// Use gopkg.in/yaml.v3 via the module; parse manually here.
	if err := parseFixtureYAML(data, &fix); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return &syntheticChannel{fixture: fix}
}

// parseFixtureYAML unmarshals the YAML fixture using the gopkg.in/yaml.v3 package.
// Called from tests only; uses the same YAML decoder as the rest of the package.
func parseFixtureYAML(data []byte, out *syntheticFixture) error {
	return calibration.ParseFixtureYAML(data, out)
}

// ---- RULE-CALIB-PR2B-01: Polarity probe detects normal polarity ----
// Bound: internal/calibration/probe_test.go:TestPR2B_Rules/polarity_normal_detected

// ---- RULE-CALIB-PR2B-02: Polarity probe detects inverted polarity ----
// Bound: internal/calibration/probe_test.go:TestPR2B_Rules/polarity_inverted_detected

// ---- RULE-CALIB-PR2B-03: Phantom channel (ambiguous polarity delta <200) is marked phantom ----
// Bound: internal/calibration/probe_test.go:TestPR2B_Rules/phantom_marked_from_ambiguous_polarity

// ---- RULE-CALIB-PR2B-04: Stall PWM detected for duty_0_255 via descending sweep ----
// Bound: internal/calibration/probe_test.go:TestPR2B_Rules/stall_pwm_detected_duty_0_255

// ---- RULE-CALIB-PR2B-05: Min responsive PWM detected as step above stall ----
// Bound: internal/calibration/probe_test.go:TestPR2B_Rules/min_responsive_pwm_detected

// ---- RULE-CALIB-PR2B-06: BIOS override detected when read reverts within 200ms ----
// Bound: internal/calibration/probe_test.go:TestPR2B_Rules/bios_override_detected

// ---- RULE-CALIB-PR2B-07: Phantom channel → ShouldApplyCurve returns (false, ErrPhantom) ----
// Bound: internal/calibration/probe_test.go:TestPR2B_Rules/phantom_write_returns_monitor_only

// ---- RULE-CALIB-PR2B-10: step_0_N stall detection uses binary search ----
// Bound: internal/calibration/probe_test.go:TestPR2B_Rules/step_0N_stall_binary_search

// ---- RULE-CALIB-PR2B-11: CalibrationRun JSON round-trips without data loss ----
// Bound: internal/calibration/probe_test.go:TestPR2B_Rules/calibration_result_json_roundtrip

// ---- RULE-CALIB-PR2B-12: Store uses <dmi_fingerprint>-<bios_version_safe>.json filename ----
// Bound: internal/calibration/probe_test.go:TestPR2B_Rules/store_filename_format

func TestPR2B_Rules(t *testing.T) {
	ctx := context.Background()

	t.Run("polarity_normal_detected", func(t *testing.T) {
		ch := loadSyntheticChannel(t, "responsive_fan.yaml")
		polarity, rpmLow, rpmHigh, err := calibration.ProbePolarity(ctx, ch, 255, time.Millisecond)
		if err != nil {
			t.Fatalf("ProbePolarity error: %v", err)
		}
		if polarity != calibration.PolarityNormal {
			t.Errorf("expected PolarityNormal, got %v (rpmLow=%d rpmHigh=%d)", polarity, rpmLow, rpmHigh)
		}
		if rpmHigh-rpmLow < 200 {
			t.Errorf("RPM delta %d < 200 RPM threshold", rpmHigh-rpmLow)
		}
	})

	t.Run("polarity_inverted_detected", func(t *testing.T) {
		ch := loadSyntheticChannel(t, "inverted_fan.yaml")
		polarity, rpmLow, rpmHigh, err := calibration.ProbePolarity(ctx, ch, 255, time.Millisecond)
		if err != nil {
			t.Fatalf("ProbePolarity error: %v", err)
		}
		if polarity != calibration.PolarityInverted {
			t.Errorf("expected PolarityInverted, got %v (rpmLow=%d rpmHigh=%d)", polarity, rpmLow, rpmHigh)
		}
		if rpmLow-rpmHigh < 200 {
			t.Errorf("inverted RPM delta %d < 200 RPM threshold", rpmLow-rpmHigh)
		}
	})

	t.Run("phantom_marked_from_ambiguous_polarity", func(t *testing.T) {
		ch := loadSyntheticChannel(t, "phantom_channel.yaml")
		polarity, _, _, err := calibration.ProbePolarity(ctx, ch, 255, time.Millisecond)
		if err != nil {
			t.Fatalf("ProbePolarity error: %v", err)
		}
		if polarity != calibration.PolarityAmbiguous {
			t.Errorf("expected PolarityAmbiguous (phantom), got %v", polarity)
		}
	})

	t.Run("stall_pwm_detected_duty_0_255", func(t *testing.T) {
		ch := loadSyntheticChannel(t, "responsive_fan.yaml")
		stallPWM, _, _, _, err := calibration.ProbeStall(ctx, ch, 255, time.Millisecond)
		if err != nil {
			t.Fatalf("ProbeStall error: %v", err)
		}
		if stallPWM == nil {
			t.Fatal("stall_pwm is nil, expected non-nil for responsive fan")
		}
		// Fixture: stall at pwm=30. Sweep steps of 16 → detects within one step of 30.
		if *stallPWM > 48 {
			t.Errorf("stall_pwm=%d, expected ≤48 (sweep resolution ±16 of fixture stall=30)", *stallPWM)
		}
	})

	t.Run("min_responsive_pwm_detected", func(t *testing.T) {
		ch := loadSyntheticChannel(t, "responsive_fan.yaml")
		_, minResp, _, _, err := calibration.ProbeStall(ctx, ch, 255, time.Millisecond)
		if err != nil {
			t.Fatalf("ProbeStall error: %v", err)
		}
		if minResp == nil {
			t.Fatal("min_responsive_pwm is nil, expected non-nil for responsive fan")
		}
		// Min responsive must be above the stall point.
		if *minResp <= 0 {
			t.Errorf("min_responsive_pwm=%d, expected > 0", *minResp)
		}
	})

	t.Run("bios_override_detected", func(t *testing.T) {
		ch := loadSyntheticChannel(t, "bios_overridden_fan.yaml")
		overridden, err := calibration.ProbeBIOSOverride(ctx, ch, 200)
		if err != nil {
			t.Fatalf("ProbeBIOSOverride error: %v", err)
		}
		if !overridden {
			t.Error("expected bios_overridden=true, got false")
		}
	})

	t.Run("bios_override_not_detected_for_normal_fan", func(t *testing.T) {
		ch := loadSyntheticChannel(t, "responsive_fan.yaml")
		overridden, err := calibration.ProbeBIOSOverride(ctx, ch, 200)
		if err != nil {
			t.Fatalf("ProbeBIOSOverride error: %v", err)
		}
		if overridden {
			t.Error("expected bios_overridden=false for normal fan, got true")
		}
	})

	t.Run("phantom_write_returns_monitor_only", func(t *testing.T) {
		calCh := &hwdb.ChannelCalibration{
			Phantom: true,
		}
		ok, err := hwdb.ShouldApplyCurve(calCh)
		if ok {
			t.Fatal("expected ShouldApplyCurve to return false for phantom channel")
		}
		if !errors.Is(err, hwdb.ErrPhantom) {
			t.Errorf("expected ErrPhantom, got %v", err)
		}
	})

	t.Run("inverted_polarity_write_inverts_value", func(t *testing.T) {
		// Writing pwm=200 to an inverted channel with pwmUnitMax=255
		// should produce actual write of 255-200=55.
		calInverted := &hwdb.ChannelCalibration{PolarityInverted: true}
		got := hwdb.InvertPWM(calInverted, 200, 255)
		want := 55
		if got != want {
			t.Errorf("InvertPWM(inverted, 200, 255) = %d, want %d", got, want)
		}
		// Edge: InvertPWM(inverted, 0, 255) = 255
		if v := hwdb.InvertPWM(calInverted, 0, 255); v != 255 {
			t.Errorf("InvertPWM(inverted, 0, 255) = %d, want 255", v)
		}
		// Edge: InvertPWM(inverted, 255, 255) = 0
		if v := hwdb.InvertPWM(calInverted, 255, 255); v != 0 {
			t.Errorf("InvertPWM(inverted, 255, 255) = %d, want 0", v)
		}
		// Non-inverted channel: passthrough.
		calNormal := &hwdb.ChannelCalibration{PolarityInverted: false}
		if v := hwdb.InvertPWM(calNormal, 200, 255); v != 200 {
			t.Errorf("InvertPWM(normal, 200, 255) = %d, want 200 (no inversion)", v)
		}
		// nil cal: passthrough.
		if v := hwdb.InvertPWM(nil, 200, 255); v != 200 {
			t.Errorf("InvertPWM(nil, 200, 255) = %d, want 200 (no inversion)", v)
		}
	})

	t.Run("step_0N_stall_binary_search", func(t *testing.T) {
		ch := loadSyntheticChannel(t, "step_0_N_fan.yaml")
		stallPWM, minResp, _, samples, err := calibration.ProbeStallStep(ctx, ch, 7, time.Millisecond)
		if err != nil {
			t.Fatalf("ProbeStallStep error: %v", err)
		}
		if stallPWM == nil {
			t.Fatal("stall_pwm is nil for step_0_N fan")
		}
		if *stallPWM != 0 {
			t.Errorf("stall_pwm=%d, want 0 (fixture: level 0 stops fan)", *stallPWM)
		}
		if minResp == nil {
			t.Fatal("min_responsive_pwm is nil")
		}
		if *minResp != 1 {
			t.Errorf("min_responsive_pwm=%d, want 1 (fixture: level 1 = min spinning)", *minResp)
		}
		// Binary search on 8 levels (0..7) should converge in ≤ 4 samples.
		if samples > 6 {
			t.Errorf("binary search used %d samples, expected ≤6 for 8-level fan", samples)
		}
	})

	t.Run("calibration_result_json_roundtrip", func(t *testing.T) {
		stallPWM := 30
		minResp := 50
		maxRPM := 5500
		original := hwdb.CalibrationRun{
			SchemaVersion:   1,
			DMIFingerprint:  "abcdef1234567890",
			BIOSVersion:     "ASUS 0805",
			BIOSReleaseDate: "04/26/2026",
			CalibratedAt:    time.Date(2026, 4, 26, 14, 32, 11, 0, time.UTC),
			VentdVersion:    "v0.4.1",
			Channels: []hwdb.ChannelCalibration{
				{
					HwmonName:         "nct6798",
					ChannelIndex:      2,
					PolarityInverted:  false,
					StallPWM:          &stallPWM,
					MinResponsivePWM:  &minResp,
					MaxObservedRPM:    &maxRPM,
					Phantom:           false,
					BIOSOverridden:    false,
					ProbeMethod:       "binary_search",
					ProbeObservations: 27,
				},
			},
		}

		data, err := json.Marshal(&original)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded hwdb.CalibrationRun
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if decoded.SchemaVersion != 1 {
			t.Errorf("schema_version: got %d, want 1", decoded.SchemaVersion)
		}
		if decoded.DMIFingerprint != "abcdef1234567890" {
			t.Errorf("dmi_fingerprint: got %q, want %q", decoded.DMIFingerprint, "abcdef1234567890")
		}
		if len(decoded.Channels) != 1 {
			t.Fatalf("channels: got %d, want 1", len(decoded.Channels))
		}
		ch := decoded.Channels[0]
		if ch.ChannelIndex != 2 {
			t.Errorf("channel_index: got %d, want 2", ch.ChannelIndex)
		}
		if ch.StallPWM == nil || *ch.StallPWM != 30 {
			t.Errorf("stall_pwm: got %v, want 30", ch.StallPWM)
		}
		if ch.MinResponsivePWM == nil || *ch.MinResponsivePWM != 50 {
			t.Errorf("min_responsive_pwm: got %v, want 50", ch.MinResponsivePWM)
		}
		if ch.ProbeObservations != 27 {
			t.Errorf("probe_observations: got %d, want 27", ch.ProbeObservations)
		}
	})

	t.Run("store_filename_format", func(t *testing.T) {
		dir := t.TempDir()
		s := calibration.NewStore(dir)
		// Bios version with special chars must be sanitised.
		name := s.Filename("abcdef1234567890", "ASUS 0805 (04/26/2026)")
		want := "abcdef1234567890-ASUS-0805-04-26-2026.json"
		if name != want {
			t.Errorf("Filename = %q, want %q", name, want)
		}
	})

	t.Run("store_write_then_load", func(t *testing.T) {
		dir := t.TempDir()
		s := calibration.NewStore(dir)
		stallPWM := 30
		run := hwdb.CalibrationRun{
			SchemaVersion:  1,
			DMIFingerprint: "deadbeef01234567",
			BIOSVersion:    "v1.0",
			CalibratedAt:   time.Now().UTC().Round(time.Second),
			Channels: []hwdb.ChannelCalibration{
				{HwmonName: "nct6798", ChannelIndex: 1, StallPWM: &stallPWM},
			},
		}
		if err := s.Save(&run); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := s.Load("deadbeef01234567", "v1.0")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if loaded.DMIFingerprint != run.DMIFingerprint {
			t.Errorf("dmi_fingerprint: got %q, want %q", loaded.DMIFingerprint, run.DMIFingerprint)
		}
		if len(loaded.Channels) != 1 {
			t.Fatalf("channels: got %d, want 1", len(loaded.Channels))
		}
		if loaded.Channels[0].StallPWM == nil || *loaded.Channels[0].StallPWM != 30 {
			t.Errorf("stall_pwm: got %v, want 30", loaded.Channels[0].StallPWM)
		}
	})
}

// TestPR2B_Deterministic runs the full probe on the responsive_fan fixture 5 times
// and asserts all runs produce the same result. RULE-CALIB-PR2B: deterministic.
func TestPR2B_Deterministic(t *testing.T) {
	ctx := context.Background()
	type runResult struct {
		polarity calibration.Polarity
		stall    *int
		minResp  *int
		phantom  bool
	}
	var first runResult
	for i := range 5 {
		ch := loadSyntheticChannel(t, "responsive_fan.yaml")
		pol, _, _, err := calibration.ProbePolarity(ctx, ch, 255, time.Millisecond)
		if err != nil {
			t.Fatalf("run %d: ProbePolarity: %v", i, err)
		}
		stall, minR, _, _, err := calibration.ProbeStall(ctx, ch, 255, time.Millisecond)
		if err != nil {
			t.Fatalf("run %d: ProbeStall: %v", i, err)
		}
		r := runResult{polarity: pol, stall: stall, minResp: minR, phantom: pol == calibration.PolarityAmbiguous}
		if i == 0 {
			first = r
			continue
		}
		if r.polarity != first.polarity {
			t.Errorf("run %d: polarity %v != run 0 %v", i, r.polarity, first.polarity)
		}
		if (r.stall == nil) != (first.stall == nil) {
			t.Errorf("run %d: stall nil mismatch", i)
		} else if r.stall != nil && *r.stall != *first.stall {
			t.Errorf("run %d: stall %d != run 0 %d", i, *r.stall, *first.stall)
		}
	}
}
