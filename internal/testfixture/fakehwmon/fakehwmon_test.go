package fakehwmon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestNew_EmptyOptions(t *testing.T) {
	fake := New(t, nil)
	if fake.Root == "" {
		t.Fatal("Root is empty")
	}
	entries, err := os.ReadDir(fake.Root)
	if err != nil {
		t.Fatalf("ReadDir(Root): %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("Root not empty: %v", entries)
	}
}

func TestNew_SingleChip(t *testing.T) {
	mode := 0
	max := 255
	fake := New(t, &Options{
		Chips: []ChipOptions{{
			Name: "nct6798",
			PWMs: []PWMOptions{{
				Index:  1,
				PWM:    128,
				Enable: 2,
				Mode:   &mode,
				Max:    &max,
			}},
			Fans:  []FanOptions{{Index: 1, RPM: 1200}},
			Temps: []TempOptions{{Index: 1, MilliC: 40000, Label: "SYSTIN"}},
		}},
	})

	chipDir := filepath.Join(fake.Root, "hwmon0")

	check := func(file, want string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(chipDir, file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.TrimSpace(string(got)) != want {
			t.Errorf("%s = %q, want %q", file, strings.TrimSpace(string(got)), want)
		}
	}

	check("name", "nct6798")
	check("pwm1", "128")
	check("pwm1_enable", "2")
	check("pwm1_mode", "0")
	check("pwm1_max", "255")
	check("fan1_input", "1200")
	check("temp1_input", "40000")
	check("temp1_label", "SYSTIN")
}

func TestWritePWM_RoundTrip(t *testing.T) {
	fake := New(t, &Options{
		Chips: []ChipOptions{{
			Name: "testchip",
			PWMs: []PWMOptions{{Index: 1, PWM: 0, Enable: 1}},
		}},
	})

	if err := fake.WritePWM(0, 1, 200); err != nil {
		t.Fatalf("WritePWM: %v", err)
	}
	got, err := fake.ReadPWM(0, 1)
	if err != nil {
		t.Fatalf("ReadPWM: %v", err)
	}
	if got != 200 {
		t.Errorf("ReadPWM = %d, want 200", got)
	}

	// Verify file on disk.
	data, err := os.ReadFile(filepath.Join(fake.Root, "hwmon0", "pwm1"))
	if err != nil {
		t.Fatalf("read pwm1: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Errorf("pwm1 missing trailing newline: %q", data)
	}
	if strings.TrimSpace(string(data)) != "200" {
		t.Errorf("pwm1 file = %q, want %q", strings.TrimSpace(string(data)), "200")
	}
}

func TestWritePWM_Clamps(t *testing.T) {
	fake := New(t, &Options{
		Chips: []ChipOptions{{
			Name: "testchip",
			PWMs: []PWMOptions{{Index: 1, PWM: 100, Enable: 1}},
		}},
	})

	if err := fake.WritePWM(0, 1, 300); err != nil {
		t.Fatalf("WritePWM 300: %v", err)
	}
	got, err := fake.ReadPWM(0, 1)
	if err != nil {
		t.Fatalf("ReadPWM: %v", err)
	}
	if got != 255 {
		t.Errorf("WritePWM(300) clamped to %d, want 255", got)
	}

	if err := fake.WritePWM(0, 1, -5); err != nil {
		t.Fatalf("WritePWM -5: %v", err)
	}
	got, err = fake.ReadPWM(0, 1)
	if err != nil {
		t.Fatalf("ReadPWM: %v", err)
	}
	if got != 0 {
		t.Errorf("WritePWM(-5) clamped to %d, want 0", got)
	}
}

func TestMultipleChips(t *testing.T) {
	fake := New(t, &Options{
		Chips: []ChipOptions{
			{Name: "chip_a", PWMs: []PWMOptions{{Index: 1, PWM: 100, Enable: 1}}},
			{Name: "chip_b", PWMs: []PWMOptions{{Index: 2, PWM: 200, Enable: 2}}},
		},
	})

	readFile := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return strings.TrimSpace(string(data))
	}

	if got := readFile(filepath.Join(fake.Root, "hwmon0", "name")); got != "chip_a" {
		t.Errorf("hwmon0/name = %q, want chip_a", got)
	}
	if got := readFile(filepath.Join(fake.Root, "hwmon0", "pwm1")); got != "100" {
		t.Errorf("hwmon0/pwm1 = %q, want 100", got)
	}
	if got := readFile(filepath.Join(fake.Root, "hwmon1", "name")); got != "chip_b" {
		t.Errorf("hwmon1/name = %q, want chip_b", got)
	}
	if got := readFile(filepath.Join(fake.Root, "hwmon1", "pwm2")); got != "200" {
		t.Errorf("hwmon1/pwm2 = %q, want 200", got)
	}
}

func TestCallRecorder(t *testing.T) {
	fake := New(t, &Options{
		Chips: []ChipOptions{{
			Name: "testchip",
			PWMs: []PWMOptions{{Index: 1, PWM: 0, Enable: 1}},
		}},
	})

	if err := fake.WritePWM(0, 1, 128); err != nil {
		t.Fatalf("WritePWM: %v", err)
	}
	if _, err := fake.ReadPWM(0, 1); err != nil {
		t.Fatalf("ReadPWM: %v", err)
	}

	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("Calls() = %d, want 2", len(calls))
	}
	if calls[0].Name != "WritePWM" {
		t.Errorf("calls[0].Name = %q, want WritePWM", calls[0].Name)
	}
	if calls[1].Name != "ReadPWM" {
		t.Errorf("calls[1].Name = %q, want ReadPWM", calls[1].Name)
	}
}

func TestConcurrentWrites(t *testing.T) {
	fake := New(t, &Options{
		Chips: []ChipOptions{{
			Name: "testchip",
			PWMs: []PWMOptions{{Index: 1, PWM: 128, Enable: 1}},
		}},
	})

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			v := i % 256
			_ = fake.WritePWM(0, 1, v)
		}()
	}
	wg.Wait()

	// File must contain a valid integer.
	data, err := os.ReadFile(filepath.Join(fake.Root, "hwmon0", "pwm1"))
	if err != nil {
		t.Fatalf("read pwm1 after concurrent writes: %v", err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse pwm1 after concurrent writes: %v (got %q)", err, data)
	}
	if v < 0 || v > 255 {
		t.Errorf("pwm1 after concurrent writes = %d, want 0–255", v)
	}
}
