package hwmon_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ventd/ventd/internal/hal"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
)

// modeRecorder is a concurrency-safe stand-in for the pwm*_mode sysfs
// seams. cur models the chip's current mode; writes mutate it and append
// to writes so a test can assert exactly what the backend wrote.
type modeRecorder struct {
	mu     sync.Mutex
	cur    int
	writes []int
}

func (m *modeRecorder) read(string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur, nil
}

func (m *modeRecorder) write(_ string, mode int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cur = mode
	m.writes = append(m.writes, mode)
	return nil
}

// TestAssertResolvedMode_FlipsWhenDifferent: a channel resolved to DC
// whose chip currently reports PWM must get exactly one mode write to DC
// on acquire.
func TestAssertResolvedMode_FlipsWhenDifferent(t *testing.T) {
	rec := &modeRecorder{cur: hal.ModePWM}
	b := halhwmon.NewBackendForModeTest(nil, rec.read, rec.write)
	ch := halhwmon.MakeTestChannelWithMode("/sys/class/hwmon/hwmon0/pwm1", halhwmon.ModePtr(hal.ModeDC))

	if err := b.Write(ch, 128); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(rec.writes) != 1 || rec.writes[0] != hal.ModeDC {
		t.Fatalf("mode writes = %v, want exactly [%d] (DC)", rec.writes, hal.ModeDC)
	}
}

// TestAssertResolvedMode_NoWriteWhenAlreadyCorrect: a channel resolved to
// DC whose chip already reports DC must NOT be written (assertResolvedMode
// reads first and only writes on mismatch).
func TestAssertResolvedMode_NoWriteWhenAlreadyCorrect(t *testing.T) {
	rec := &modeRecorder{cur: hal.ModeDC}
	b := halhwmon.NewBackendForModeTest(nil, rec.read, rec.write)
	ch := halhwmon.MakeTestChannelWithMode("/sys/class/hwmon/hwmon0/pwm1", halhwmon.ModePtr(hal.ModeDC))

	if err := b.Write(ch, 128); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(rec.writes) != 0 {
		t.Fatalf("mode writes = %v, want none (already in DC mode)", rec.writes)
	}
}

// TestAssertResolvedMode_NilNeverTouchesMode: the common case — a channel
// the wizard never healed (ResolvedMode nil) must never read or write the
// mode attribute.
func TestAssertResolvedMode_NilNeverTouchesMode(t *testing.T) {
	var reads, writes int
	read := func(string) (int, error) { reads++; return hal.ModePWM, nil }
	write := func(string, int) error { writes++; return nil }
	b := halhwmon.NewBackendForModeTest(nil, read, write)
	ch := halhwmon.MakeTestChannel("/sys/class/hwmon/hwmon0/pwm1", false) // ResolvedMode nil

	if err := b.Write(ch, 128); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if reads != 0 || writes != 0 {
		t.Fatalf("mode reads=%d writes=%d, want 0/0 for an unhealed channel", reads, writes)
	}
}

// TestModeHealer_RealSysfs exercises ModeWritable / Mode / SetMode against
// a real tmpdir sysfs tree (default seams → hwmon.ReadPWMMode etc.).
func TestModeHealer_RealSysfs(t *testing.T) {
	dir := t.TempDir()
	pwm := filepath.Join(dir, "pwm1")
	mode := filepath.Join(dir, "pwm1_mode")
	if err := os.WriteFile(pwm, []byte("128\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mode, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := halhwmon.NewBackend(nil)
	healer, ok := any(b).(hal.ModeHealer)
	if !ok {
		t.Fatal("hwmon.Backend must implement hal.ModeHealer")
	}
	ch := halhwmon.MakeTestChannel(pwm, false)

	if !healer.ModeWritable(ch) {
		t.Fatal("ModeWritable = false for a present 0644 pwm1_mode")
	}
	got, err := healer.Mode(ch)
	if err != nil {
		t.Fatalf("Mode: %v", err)
	}
	if got != hal.ModePWM {
		t.Fatalf("Mode = %d, want %d (PWM)", got, hal.ModePWM)
	}
	if err := healer.SetMode(ch, hal.ModeDC); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if got, _ := healer.Mode(ch); got != hal.ModeDC {
		t.Fatalf("after SetMode(DC), Mode = %d, want %d", got, hal.ModeDC)
	}
	if err := healer.SetMode(ch, 7); err == nil {
		t.Fatal("SetMode(7) must reject an out-of-range mode")
	}
}

// TestModeHealer_NoModeFile: a channel whose pwm*_mode file is absent
// (it87-style) must report ModeWritable=false.
func TestModeHealer_NoModeFile(t *testing.T) {
	dir := t.TempDir()
	pwm := filepath.Join(dir, "pwm1")
	if err := os.WriteFile(pwm, []byte("128\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := halhwmon.NewBackend(nil)
	healer := any(b).(hal.ModeHealer)
	if healer.ModeWritable(halhwmon.MakeTestChannel(pwm, false)) {
		t.Fatal("ModeWritable must be false when pwm1_mode is absent (it87)")
	}
}
