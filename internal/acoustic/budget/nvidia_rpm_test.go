package budget

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ventd/ventd/internal/acoustic/proxy"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/nvidia"
)

// TestNvidiaShroudDiameterMM_AftermarketGets120 verifies the diameter
// heuristic picks 120mm shrouds for triple-fan aftermarket AIBs and
// 80mm for FE-class single-fan reference designs. (#1282)
func TestNvidiaShroudDiameterMM_AftermarketGets120(t *testing.T) {
	tests := []struct {
		name string
		want float64
	}{
		{"RTX 4090 Aorus Master", 120},
		{"ROG Strix RTX 4090 OC", 120},
		{"TUF RTX 4080", 120},
		{"MSI Gaming X RTX 4070", 120},
		{"Trinity OC GeForce RTX", 120},
		{"RTX 4090 Founders Edition", 80}, // FE shroud
		{"Tesla A100", 80},                // datacentre passive
		{"", 80},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nvidiaShroudDiameterMM(tc.name); got != tc.want {
				t.Errorf("diameter(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestBuildAcousticBudget_NvidiaFanIncludedWhenRPMSupported wires NVML
// fan RPM into the host loudness composition. Without #1282 the
// budget builder ignored every NVIDIA fan; with #1282 a fan with a
// readable RPM contributes proportionally to current_dba. (#1282)
func TestBuildAcousticBudget_NvidiaFanIncludedWhenRPMSupported(t *testing.T) {
	origFn := readNvidiaFanRPMFn
	t.Cleanup(func() { readNvidiaFanRPMFn = origFn })

	// Stage a hwmon fan as a baseline so the budget builder doesn't
	// short-circuit on "no fans visible".
	rpmDir := t.TempDir()
	rpmPath := filepath.Join(rpmDir, "fan1_input")
	if err := os.WriteFile(rpmPath, []byte("1500\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	live := &config.Config{
		Smart: config.SmartConfig{Preset: "balanced"},
		Fans: []config.Fan{
			{Name: "case_fan", Type: "hwmon", RPMPath: rpmPath, MinPWM: 80, MaxPWM: 255},
		},
	}
	uncalibBudget := Build(live, "case_fan", controller.PresetBalanced)
	if uncalibBudget.CurrentDBA <= 0 {
		t.Fatalf("hwmon-only budget should be positive; got %v", uncalibBudget.CurrentDBA)
	}

	// Now add an NVML-controlled fan reporting 3000 RPM via the
	// injected ReadFanRPM seam. The host total must rise above the
	// hwmon-only baseline because Compose is an energetic sum.
	live.Fans = append(live.Fans, config.Fan{
		Name:    "RTX 4090 Aorus",
		Type:    "nvidia",
		PWMPath: "0",
		MinPWM:  30,
		MaxPWM:  100,
	})
	readNvidiaFanRPMFn = func(idx uint) (uint32, error) {
		if idx != 0 {
			t.Errorf("unexpected NVML idx %d", idx)
		}
		return 3000, nil
	}
	withGPU := Build(live, "case_fan", controller.PresetBalanced)
	if withGPU.CurrentDBA <= uncalibBudget.CurrentDBA {
		t.Errorf("adding an NVIDIA fan must raise CurrentDBA; got %v then %v",
			uncalibBudget.CurrentDBA, withGPU.CurrentDBA)
	}
}

// TestBuildAcousticBudget_NvidiaFanSkippedWhenUnsupported covers the
// graceful degrade contract: an older driver / pre-Maxwell GPU
// returns ErrFanRPMUnsupported, and the fan must be skipped without
// killing the host budget. (#1282 acceptance: "NVML version older
// than the API: graceful fallthrough (no RPM, fan skipped)")
func TestBuildAcousticBudget_NvidiaFanSkippedWhenUnsupported(t *testing.T) {
	origFn := readNvidiaFanRPMFn
	t.Cleanup(func() { readNvidiaFanRPMFn = origFn })

	rpmDir := t.TempDir()
	rpmPath := filepath.Join(rpmDir, "fan1_input")
	if err := os.WriteFile(rpmPath, []byte("1500\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	live := &config.Config{
		Smart: config.SmartConfig{Preset: "balanced"},
		Fans: []config.Fan{
			{Name: "case_fan", Type: "hwmon", RPMPath: rpmPath, MinPWM: 80, MaxPWM: 255},
			{Name: "RTX 2080", Type: "nvidia", PWMPath: "0", MinPWM: 30, MaxPWM: 100},
		},
	}
	readNvidiaFanRPMFn = func(idx uint) (uint32, error) {
		return 0, nvidia.ErrFanRPMUnsupported
	}
	out := Build(live, "case_fan", controller.PresetBalanced)
	if out.CurrentDBA <= 0 {
		t.Fatalf("hwmon fan must still produce positive CurrentDBA after NVIDIA skip; got %v",
			out.CurrentDBA)
	}
}

// TestReadNvidiaFanRPM_ParsesPWMPathAsIndex pins the PWMPath
// convention used by NVMLPhase: GPU index encoded as a decimal
// string. (#1282)
func TestReadNvidiaFanRPM_ParsesPWMPathAsIndex(t *testing.T) {
	origFn := readNvidiaFanRPMFn
	t.Cleanup(func() { readNvidiaFanRPMFn = origFn })

	var seen uint
	readNvidiaFanRPMFn = func(idx uint) (uint32, error) {
		seen = idx
		return 2500, nil
	}
	got, err := readNvidiaFanRPM("3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen != 3 {
		t.Errorf("idx = %d, want 3", seen)
	}
	if got != 2500 {
		t.Errorf("rpm = %d, want 2500", got)
	}
}

// TestReadNvidiaFanRPM_GarbagePathFails ensures non-integer PWMPath
// surfaces an error so the caller doesn't treat it as a real fan.
func TestReadNvidiaFanRPM_GarbagePathFails(t *testing.T) {
	origFn := readNvidiaFanRPMFn
	t.Cleanup(func() { readNvidiaFanRPMFn = origFn })

	readNvidiaFanRPMFn = func(idx uint) (uint32, error) {
		t.Errorf("ReadFanRPM should not be reached on garbage input")
		return 0, nil
	}
	if _, err := readNvidiaFanRPM("/sys/hwmon/pwm1"); err == nil {
		t.Error("expected error parsing non-integer PWMPath")
	}
	// And the sentinel: nvidia.ErrFanRPMUnsupported is defined.
	if !errors.Is(nvidia.ErrFanRPMUnsupported, nvidia.ErrFanRPMUnsupported) {
		t.Error("ErrFanRPMUnsupported must be a valid sentinel")
	}
	_ = proxy.ClassGPUShroud
}
