package fakehwmon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestRULE_FAKEHWMON_QUIRK_HELPERS pins the four canonical chip-quirk
// helpers added in v0.5.32. Each helper models one real-world chip
// misbehaviour the rule catalogue guards against; the bound subtest
// asserts the helper writes the expected value to the expected path
// so consumers of the helper (the controller / hwmon backend / probe
// tests) can rely on the file-system effect.
//
// Bound rule: RULE-FAKEHWMON-QUIRK-HELPERS in
// docs/rules/hwmon-sentinel.md (multi-rule family file).
func TestRULE_FAKEHWMON_QUIRK_HELPERS(t *testing.T) {
	t.Run("inject_sentinel_rpm_writes_65535_to_fan_input", func(t *testing.T) {
		f := New(t, &Options{
			Chips: []ChipOptions{{
				Name: "nct6687",
				Fans: []FanOptions{{Index: 1, RPM: 1200}},
			}},
		})
		if err := f.InjectSentinelRPM(0, 1); err != nil {
			t.Fatalf("InjectSentinelRPM: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(f.Root, "hwmon0", "fan1_input"))
		if err != nil {
			t.Fatalf("read fan1_input: %v", err)
		}
		v, _ := strconv.Atoi(strings.TrimSpace(string(got)))
		if v != SentinelRPMValue {
			t.Errorf("fan1_input = %d; want SentinelRPMValue (%d)", v, SentinelRPMValue)
		}
	})

	t.Run("simulate_bios_revert_writes_original_value_back_to_pwm", func(t *testing.T) {
		f := New(t, &Options{
			Chips: []ChipOptions{{
				Name: "it8689e",
				PWMs: []PWMOptions{{Index: 1, PWM: 128, Enable: 1}},
			}},
		})
		// Daemon writes PWM=200; backend's WritePWM updates the file.
		if err := f.WritePWM(0, 1, 200); err != nil {
			t.Fatalf("WritePWM: %v", err)
		}
		// First readback confirms the daemon's value landed.
		v, err := f.ReadPWM(0, 1)
		if err != nil {
			t.Fatalf("ReadPWM 1st: %v", err)
		}
		if v != 200 {
			t.Errorf("first readback = %d; want 200", v)
		}
		// Test simulates BIOS reverting at >200 ms.
		if err := f.SimulateBIOSRevert(0, 1, 128); err != nil {
			t.Fatalf("SimulateBIOSRevert: %v", err)
		}
		// Second readback returns firmware value.
		v, err = f.ReadPWM(0, 1)
		if err != nil {
			t.Fatalf("ReadPWM 2nd: %v", err)
		}
		if v != 128 {
			t.Errorf("second readback = %d; want 128 (firmware reverted)", v)
		}
	})

	t.Run("simulate_fan_response_normal_polarity_linear_pwm_to_rpm", func(t *testing.T) {
		f := New(t, &Options{
			Chips: []ChipOptions{{
				Name: "nct6798",
				PWMs: []PWMOptions{{Index: 1, PWM: 0, Enable: 1}},
				Fans: []FanOptions{{Index: 1, RPM: 0}},
			}},
		})
		// Set PWM=128 (50%); fan should report ~half of maxRPM.
		_ = f.WritePWM(0, 1, 128)
		if err := f.SimulateFanResponse(0, 1, 1, 2000, false); err != nil {
			t.Fatalf("SimulateFanResponse: %v", err)
		}
		got := readFanRPM(t, f, 0, 1)
		// 128 × 2000 / 255 = 1003 (linear normal polarity).
		if got != 1003 {
			t.Errorf("fan1_input = %d; want 1003 (linear PWM=128/255 × 2000)", got)
		}
	})

	t.Run("simulate_fan_response_inverted_polarity_high_rpm_at_low_pwm", func(t *testing.T) {
		f := New(t, &Options{
			Chips: []ChipOptions{{
				Name: "nct6683",
				PWMs: []PWMOptions{{Index: 1, PWM: 0, Enable: 1}},
				Fans: []FanOptions{{Index: 1, RPM: 0}},
			}},
		})
		// Set PWM=51 (20%); inverted fan should be at ~80% of maxRPM.
		_ = f.WritePWM(0, 1, 51)
		if err := f.SimulateFanResponse(0, 1, 1, 2000, true); err != nil {
			t.Fatalf("SimulateFanResponse inverted: %v", err)
		}
		got := readFanRPM(t, f, 0, 1)
		// (255-51) × 2000 / 255 = 1600 (inverted: high RPM at low PWM).
		if got != 1600 {
			t.Errorf("fan1_input (inverted, PWM=51) = %d; want 1600", got)
		}
	})

	t.Run("reassert_pwm_enable_flips_to_firmware_auto", func(t *testing.T) {
		f := New(t, &Options{
			Chips: []ChipOptions{{
				Name: "it8688",
				PWMs: []PWMOptions{{Index: 1, PWM: 128, Enable: 1}},
			}},
		})
		// Backend acquired manual mode; pwm_enable is 1.
		got := readPWMEnable(t, f, 0, 1)
		if got != 1 {
			t.Fatalf("initial pwm1_enable = %d; want 1", got)
		}
		// BIOS reasserts firmware auto.
		if err := f.ReassertPWMEnable(0, 1, 2); err != nil {
			t.Fatalf("ReassertPWMEnable: %v", err)
		}
		got = readPWMEnable(t, f, 0, 1)
		if got != 2 {
			t.Errorf("after ReassertPWMEnable: pwm1_enable = %d; want 2 (firmware auto)", got)
		}
	})

	t.Run("inject_sentinel_rpm_validates_chip_and_fan_indices", func(t *testing.T) {
		f := New(t, &Options{Chips: []ChipOptions{{Name: "x", Fans: []FanOptions{{Index: 1, RPM: 0}}}}})
		if err := f.InjectSentinelRPM(-1, 1); err == nil {
			t.Error("InjectSentinelRPM(-1, 1): want error on negative chipIndex")
		}
		if err := f.InjectSentinelRPM(0, 0); err == nil {
			t.Error("InjectSentinelRPM(0, 0): want error on fanIndex < 1")
		}
	})

	t.Run("simulate_fan_response_validates_maxRPM", func(t *testing.T) {
		f := New(t, &Options{
			Chips: []ChipOptions{{
				Name: "x",
				PWMs: []PWMOptions{{Index: 1, PWM: 0, Enable: 1}},
				Fans: []FanOptions{{Index: 1, RPM: 0}},
			}},
		})
		if err := f.SimulateFanResponse(0, 1, 1, 0, false); err == nil {
			t.Error("SimulateFanResponse with maxRPM=0: want error")
		}
	})

	t.Run("options_struct_carries_quirk_knobs", func(t *testing.T) {
		// Pin the Options struct fields exist with the expected
		// types. A regression that drops one of these knobs is
		// caught at compile time AND surfaced here for clarity.
		opt := PWMOptions{
			Index:                1,
			EmitSentinelRPMEvery: 5,
			BIOSRevertAfter:      200,
			InvertedPolarity:     true,
			EBUSYReassertEvery:   10,
		}
		if opt.EmitSentinelRPMEvery != 5 ||
			opt.BIOSRevertAfter != 200 ||
			!opt.InvertedPolarity ||
			opt.EBUSYReassertEvery != 10 {
			t.Error("PWMOptions quirk knobs do not round-trip; the four canonical fields must be present")
		}
	})
}

func readFanRPM(t *testing.T, f *Fake, chipIndex, fanIndex int) int {
	t.Helper()
	path := filepath.Join(f.Root, "hwmon"+strconv.Itoa(chipIndex), "fan"+strconv.Itoa(fanIndex)+"_input")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fan input: %v", err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(got)))
	if err != nil {
		t.Fatalf("parse fan input: %v", err)
	}
	return v
}

func readPWMEnable(t *testing.T, f *Fake, chipIndex, pwmIndex int) int {
	t.Helper()
	path := filepath.Join(f.Root, "hwmon"+strconv.Itoa(chipIndex), "pwm"+strconv.Itoa(pwmIndex)+"_enable")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pwm enable: %v", err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(got)))
	if err != nil {
		t.Fatalf("parse pwm enable: %v", err)
	}
	return v
}
