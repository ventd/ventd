package fakepwmsys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestNew_BasicConstruction(t *testing.T) {
	opts := &Options{
		Chips: []ChipOptions{
			{NumChannels: 2, Period: 20_000_000, DutyCycles: []uint32{100, 200}, Enabled: []bool{true, false}},
		},
	}
	f := New(t, opts)
	if f.Root() == "" {
		t.Fatal("Root() must not be empty")
	}
}

func TestNew_FilesPresent(t *testing.T) {
	opts := &Options{
		Chips: []ChipOptions{
			{NumChannels: 2, Period: 20_000_000, DutyCycles: []uint32{100, 200}, Enabled: []bool{true, false}},
		},
	}
	f := New(t, opts)
	root := f.Root()

	for _, rel := range []string{
		"pwmchip0/npwm",
		"pwmchip0/export",
		"pwmchip0/pwm0/period",
		"pwmchip0/pwm0/duty_cycle",
		"pwmchip0/pwm0/enable",
		"pwmchip0/pwm0/polarity",
		"pwmchip0/pwm1/period",
		"pwmchip0/pwm1/duty_cycle",
		"pwmchip0/pwm1/enable",
		"pwmchip0/pwm1/polarity",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("expected file %s: %v", rel, err)
		}
	}
}

func TestNew_InitialValues(t *testing.T) {
	opts := &Options{
		Chips: []ChipOptions{
			{NumChannels: 2, Period: 20_000_000, DutyCycles: []uint32{100, 200}, Enabled: []bool{true, false}},
		},
	}
	f := New(t, opts)
	root := f.Root()

	cases := []struct {
		rel  string
		want string
	}{
		{"pwmchip0/npwm", "2"},
		{"pwmchip0/pwm0/period", "20000000"},
		{"pwmchip0/pwm0/duty_cycle", "100"},
		{"pwmchip0/pwm0/enable", "1"},
		{"pwmchip0/pwm1/period", "20000000"},
		{"pwmchip0/pwm1/duty_cycle", "200"},
		{"pwmchip0/pwm1/enable", "0"},
	}
	for _, tc := range cases {
		t.Run(tc.rel, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, tc.rel))
			if err != nil {
				t.Fatalf("read %s: %v", tc.rel, err)
			}
			got := strings.TrimSpace(string(data))
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChipPath(t *testing.T) {
	f := New(t, &Options{Chips: []ChipOptions{{NumChannels: 1}}})
	cp := f.ChipPath(0)
	if !strings.HasSuffix(cp, "pwmchip0") {
		t.Errorf("ChipPath(0) = %q, want suffix pwmchip0", cp)
	}
	if _, err := os.Stat(cp); err != nil {
		t.Errorf("ChipPath(0) dir does not exist: %v", err)
	}
}

func TestReadDutyCycle_RoundTrip(t *testing.T) {
	opts := &Options{
		Chips: []ChipOptions{
			{NumChannels: 2, DutyCycles: []uint32{42, 99}},
		},
	}
	f := New(t, opts)

	for ch, want := range []uint32{42, 99} {
		got, err := f.ReadDutyCycle(0, ch)
		if err != nil {
			t.Fatalf("ReadDutyCycle(0,%d): %v", ch, err)
		}
		if got != want {
			t.Errorf("ReadDutyCycle(0,%d) = %d, want %d", ch, got, want)
		}
	}
}

func TestReadDutyCycle_WriteThenRead(t *testing.T) {
	f := New(t, &Options{Chips: []ChipOptions{{NumChannels: 1}}})
	path := filepath.Join(f.ChipPath(0), "pwm0", "duty_cycle")
	if err := os.WriteFile(path, []byte("12345\n"), 0644); err != nil {
		t.Fatalf("write duty_cycle: %v", err)
	}
	got, err := f.ReadDutyCycle(0, 0)
	if err != nil {
		t.Fatalf("ReadDutyCycle: %v", err)
	}
	if got != 12345 {
		t.Errorf("got %d, want 12345", got)
	}
}

func TestRPi5Preset(t *testing.T) {
	f := New(t, &ChipRPi5)
	root := f.Root()

	for _, rel := range []string{
		"pwmchip0/npwm",
		"pwmchip0/pwm0/period",
		"pwmchip0/pwm1/period",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("RPi5 preset: %s missing: %v", rel, err)
		}
	}

	data, err := os.ReadFile(filepath.Join(root, "pwmchip0/pwm0/period"))
	if err != nil {
		t.Fatalf("read period: %v", err)
	}
	if strings.TrimSpace(string(data)) != "20000000" {
		t.Errorf("RPi5 period = %q, want 20000000", strings.TrimSpace(string(data)))
	}

	for ch := 0; ch < 2; ch++ {
		got, err := f.ReadDutyCycle(0, ch)
		if err != nil {
			t.Fatalf("ReadDutyCycle(0,%d): %v", ch, err)
		}
		if got != 0 {
			t.Errorf("RPi5 initial duty_cycle ch%d = %d, want 0", ch, got)
		}
	}

	if _, err := os.Stat(filepath.Join(root, "pwmchip1")); !os.IsNotExist(err) {
		t.Error("RPi5 preset must create exactly one chip")
	}
}

func TestNew_DefaultPeriod(t *testing.T) {
	f := New(t, &Options{Chips: []ChipOptions{{NumChannels: 1}}})
	data, err := os.ReadFile(filepath.Join(f.Root(), "pwmchip0/pwm0/period"))
	if err != nil {
		t.Fatalf("read period: %v", err)
	}
	if strings.TrimSpace(string(data)) != "40000" {
		t.Errorf("default period = %q, want 40000", strings.TrimSpace(string(data)))
	}
}

func TestNew_MissingDutyCyclesDefaultToZero(t *testing.T) {
	f := New(t, &Options{Chips: []ChipOptions{{NumChannels: 3}}})
	for ch := 0; ch < 3; ch++ {
		got, err := f.ReadDutyCycle(0, ch)
		if err != nil {
			t.Fatalf("ReadDutyCycle(0,%d): %v", ch, err)
		}
		if got != 0 {
			t.Errorf("ch%d duty_cycle = %d, want 0", ch, got)
		}
	}
}
