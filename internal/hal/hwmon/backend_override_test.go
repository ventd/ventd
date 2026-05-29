package hwmon_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ventd/ventd/internal/hal"
	hwhal "github.com/ventd/ventd/internal/hal/hwmon"
	"github.com/ventd/ventd/internal/hwmon"
)

// seedSyntheticDevice writes a faithful, controllable (ClassPrimary) hwmonN
// directory: name + pwmN + pwmN_enable + fanN_input + tempN_input.
func seedSyntheticDevice(t *testing.T, root, dir, chip string, pwm, rpm, milliC int) string {
	t.Helper()
	d := filepath.Join(root, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, val string) {
		if err := os.WriteFile(filepath.Join(d, name), []byte(val+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("name", chip)
	write("pwm1", itoa(pwm))
	write("pwm1_enable", "1")
	write("fan1_input", itoa(rpm))
	write("temp1_input", itoa(milliC))
	return filepath.Join(d, "pwm1")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestEnumerate_HonorsHwmonRootOverride(t *testing.T) {
	root := t.TempDir()
	wantPWMPath := seedSyntheticDevice(t, root, "hwmon0", "nct6687", 128, 1500, 42000)
	t.Setenv(hwmon.RootOverrideEnv, root)

	if !hwmon.RootIsOverridden() {
		t.Fatal("RootIsOverridden() = false with VENTD_HWMON_ROOT set")
	}
	if got := hwmon.EffectiveRoot(); got != root {
		t.Fatalf("EffectiveRoot() = %q, want %q", got, root)
	}

	be := hwhal.NewBackend(nil)
	chans, err := be.Enumerate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(chans) != 1 {
		t.Fatalf("Enumerate returned %d channels, want 1: %+v", len(chans), chans)
	}
	ch := chans[0]
	if ch.ID != wantPWMPath {
		t.Errorf("channel ID = %q, want the synthetic pwm path %q", ch.ID, wantPWMPath)
	}
	if ch.Caps&hal.CapWritePWM == 0 {
		t.Errorf("channel caps %v missing CapWritePWM — synthetic device not controllable", ch.Caps)
	}

	// Reads must follow the discovered path into the synthetic tree.
	reading, err := be.Read(ch)
	if err != nil {
		t.Fatal(err)
	}
	if !reading.OK {
		t.Fatal("Read OK=false on synthetic channel")
	}
	if reading.PWM != 128 {
		t.Errorf("Read PWM = %d, want 128 (from synthetic pwm1)", reading.PWM)
	}
	if reading.RPM != 1500 {
		t.Errorf("Read RPM = %d, want 1500 (from synthetic fan1_input)", reading.RPM)
	}
}

func TestEffectiveRoot_DefaultsWhenUnset(t *testing.T) {
	t.Setenv(hwmon.RootOverrideEnv, "")
	if hwmon.RootIsOverridden() {
		t.Error("RootIsOverridden() = true with override empty")
	}
	if got := hwmon.EffectiveRoot(); got != hwmon.DefaultHwmonRoot {
		t.Errorf("EffectiveRoot() = %q, want DefaultHwmonRoot %q", got, hwmon.DefaultHwmonRoot)
	}
}
