package setup

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

// TestRestoreExcludedChannels_HandsBackToBIOS pins
// RULE-SETUP-NO-ORPHANED-CHANNELS: every fan that the wizard probed but
// did not include in doneFans MUST have pwm_enable=2 written before the
// wizard returns. Channels that ARE in doneFans MUST be left alone (the
// controller will own their pwm_enable=1 from here).
//
// Without this, the calibration sweep's last-written PWM byte stays
// frozen on every excluded channel forever — issue #753, observed live
// on Phoenix's Proxmox box where pwm2/pwm3/pwm4 sat at PWM=70/70/0 with
// 0 RPM after wizard completion.
func TestRestoreExcludedChannels_HandsBackToBIOS(t *testing.T) {
	dir := t.TempDir()
	hwmon := filepath.Join(dir, "sys", "class", "hwmon", "hwmon0")
	layout := map[string]string{
		"sys/class/hwmon/hwmon0/name":        "it8688\n",
		"sys/class/hwmon/hwmon0/pwm1":        "27\n",
		"sys/class/hwmon/hwmon0/pwm1_enable": "1\n",
		"sys/class/hwmon/hwmon0/pwm2":        "70\n",
		"sys/class/hwmon/hwmon0/pwm2_enable": "1\n",
		"sys/class/hwmon/hwmon0/pwm3":        "70\n",
		"sys/class/hwmon/hwmon0/pwm3_enable": "1\n",
		"sys/class/hwmon/hwmon0/pwm4":        "0\n",
		"sys/class/hwmon/hwmon0/pwm4_enable": "1\n",
		"sys/class/hwmon/hwmon0/pwm5":        "27\n",
		"sys/class/hwmon/hwmon0/pwm5_enable": "1\n",
	}
	fakeHwmon(t, dir, layout)

	fans := []FanState{
		{Name: "Fan 1", Type: "hwmon", PWMPath: filepath.Join(hwmon, "pwm1"), CalPhase: "done"},
		{Name: "Fan 2", Type: "hwmon", PWMPath: filepath.Join(hwmon, "pwm2"), DetectPhase: "none"},
		{Name: "Fan 3", Type: "hwmon", PWMPath: filepath.Join(hwmon, "pwm3"), DetectPhase: "none"},
		{Name: "Fan 4", Type: "hwmon", PWMPath: filepath.Join(hwmon, "pwm4"), CalPhase: "error"},
		{Name: "Fan 5", Type: "hwmon", PWMPath: filepath.Join(hwmon, "pwm5"), CalPhase: "done"},
	}
	doneFans := []fanDiscovery{
		{name: "Fan 1", pwmPath: filepath.Join(hwmon, "pwm1")},
		{name: "Fan 5", pwmPath: filepath.Join(hwmon, "pwm5")},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	restoreExcludedChannels(fans, doneFans, logger)

	// Fan 1 + Fan 5 (in doneFans) must be unchanged.
	for _, ch := range []string{"pwm1_enable", "pwm5_enable"} {
		got := readEnable(t, filepath.Join(hwmon, ch))
		if got != "1" {
			t.Errorf("%s: got %q, want %q (controlled fans must stay manual)", ch, got, "1")
		}
	}
	// Fan 2 / Fan 3 / Fan 4 (excluded) must have been handed back to BIOS.
	for _, ch := range []string{"pwm2_enable", "pwm3_enable", "pwm4_enable"} {
		got := readEnable(t, filepath.Join(hwmon, ch))
		if got != "2" {
			t.Errorf("%s: got %q, want %q (excluded channels must be handed back to BIOS)", ch, got, "2")
		}
	}
}

// TestRestoreExcludedChannels_NoOpWhenAllControlled covers the happy
// path: every probed fan made it into doneFans. No pwm_enable writes
// should occur.
func TestRestoreExcludedChannels_NoOpWhenAllControlled(t *testing.T) {
	dir := t.TempDir()
	hwmon := filepath.Join(dir, "sys", "class", "hwmon", "hwmon0")
	layout := map[string]string{
		"sys/class/hwmon/hwmon0/pwm1":        "100\n",
		"sys/class/hwmon/hwmon0/pwm1_enable": "1\n",
	}
	fakeHwmon(t, dir, layout)

	fans := []FanState{
		{Name: "Fan 1", Type: "hwmon", PWMPath: filepath.Join(hwmon, "pwm1"), CalPhase: "done"},
	}
	doneFans := []fanDiscovery{{name: "Fan 1", pwmPath: filepath.Join(hwmon, "pwm1")}}

	restoreExcludedChannels(fans, doneFans, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if got := readEnable(t, filepath.Join(hwmon, "pwm1_enable")); got != "1" {
		t.Errorf("pwm1_enable: got %q, want %q (must remain manual)", got, "1")
	}
}

// TestRestoreExcludedChannels_SkipsNonHwmonTypes covers the contract
// boundary: NVML / IPMI channels are restored by their own backends,
// not by the hwmon helper. The helper must not stat or write hwmon
// paths for non-hwmon fan types.
func TestRestoreExcludedChannels_SkipsNonHwmonTypes(t *testing.T) {
	fans := []FanState{
		{Name: "GPU Fan", Type: "nvidia", PWMPath: "/dev/null", DetectPhase: "none"},
		{Name: "BMC Fan", Type: "ipmi", PWMPath: "/dev/null", CalPhase: "error"},
	}
	// No hwmon paths involved — must not panic, must not write to /dev/null.
	restoreExcludedChannels(fans, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestRestoreExcludedChannels_TolerantOfMissingPwmEnable covers the
// nct6683 case (NCT6687D chip — no pwm_enable file). WritePWMEnable
// returns wrapped fs.ErrNotExist; restoreExcludedChannels must swallow
// it (the channel never had manual mode). Other channels must still be
// restored.
func TestRestoreExcludedChannels_TolerantOfMissingPwmEnable(t *testing.T) {
	dir := t.TempDir()
	hwmon := filepath.Join(dir, "sys", "class", "hwmon", "hwmon0")
	// pwm1 has both pwm + pwm_enable.
	// pwm2 has pwm only (simulates nct6683).
	layout := map[string]string{
		"sys/class/hwmon/hwmon0/pwm1":        "100\n",
		"sys/class/hwmon/hwmon0/pwm1_enable": "1\n",
		"sys/class/hwmon/hwmon0/pwm2":        "100\n",
	}
	fakeHwmon(t, dir, layout)

	fans := []FanState{
		{Name: "Fan 1", Type: "hwmon", PWMPath: filepath.Join(hwmon, "pwm1"), DetectPhase: "none"},
		{Name: "Fan 2", Type: "hwmon", PWMPath: filepath.Join(hwmon, "pwm2"), DetectPhase: "none"},
	}
	restoreExcludedChannels(fans, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// pwm1_enable must have been restored to 2.
	if got := readEnable(t, filepath.Join(hwmon, "pwm1_enable")); got != "2" {
		t.Errorf("pwm1_enable: got %q, want %q", got, "2")
	}
	// pwm2_enable must not have been created (the helper failed gracefully).
	if _, err := os.Stat(filepath.Join(hwmon, "pwm2_enable")); !os.IsNotExist(err) {
		t.Errorf("pwm2_enable was unexpectedly created: stat err=%v", err)
	}
}

func readEnable(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(data))
}

// TestRestoreExcludedChannels_EINVALFallsBackToMode5 pins the
// nct6687 SmartFan recovery half of RULE-HWMON-ENABLE-EINVAL-FALLBACK
// (issue #909). The mainline kernel nct6687 driver rejects
// pwm_enable=2 with EINVAL because its enum is {0,1,5} — 2 is not a
// valid value, 5 is its SmartFan/auto. The handback chain MUST
// catch the EINVAL, retry with mode=5, and not surface a WARN on
// the happy path. Test injects writePWMEnableFn directly so the
// EINVAL behaviour can be reproduced without a real nct6687.
func TestRestoreExcludedChannels_EINVALFallsBackToMode5(t *testing.T) {
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm2"
	var (
		mode2Calls atomic.Int32
		mode5Calls atomic.Int32
		pwmCalls   atomic.Int32
	)
	prevEnable, prevPWM := writePWMEnableFn, writePWMFn
	writePWMEnableFn = func(path string, value int) error {
		switch value {
		case 2:
			mode2Calls.Add(1)
			return fmt.Errorf("hwmon: write pwm_enable %s_enable=2: %w", path, syscall.EINVAL)
		case 5:
			mode5Calls.Add(1)
			return nil
		}
		return fmt.Errorf("unexpected enable value: %d", value)
	}
	writePWMFn = func(path string, value uint8) error {
		pwmCalls.Add(1)
		return nil
	}
	t.Cleanup(func() { writePWMEnableFn, writePWMFn = prevEnable, prevPWM })

	fans := []FanState{
		{Name: "Pump Fan", Type: "hwmon", PWMPath: pwmPath, DetectPhase: "none"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	restoreExcludedChannels(fans, nil, logger)

	if mode2Calls.Load() != 1 {
		t.Errorf("mode=2 calls = %d, want 1 (initial attempt)", mode2Calls.Load())
	}
	if mode5Calls.Load() != 1 {
		t.Errorf("mode=5 calls = %d, want 1 (EINVAL fallback)", mode5Calls.Load())
	}
	if pwmCalls.Load() != 0 {
		t.Errorf("pwm calls = %d, want 0 (mode=5 succeeded; safe-PWM should NOT fire)", pwmCalls.Load())
	}
}

// TestRestoreExcludedChannels_BothModesFailFallsBackToSafePWM pins
// the third leg of the chain: when both mode=2 and mode=5 fail
// (chip is genuinely unwilling to accept any auto value), the
// daemon writes a safe PWM floor (60%) under the manual mode the
// calibration sweep already set, instead of stranding the channel
// at sweep-end PWM. The "daemon shouldn't ignore failures" rule
// says we resolve to a safe state, even when the obviously-correct
// resolution path is blocked.
func TestRestoreExcludedChannels_BothModesFailFallsBackToSafePWM(t *testing.T) {
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm3"
	var (
		mode2Calls atomic.Int32
		mode5Calls atomic.Int32
		pwmCalls   atomic.Int32
		pwmValue   atomic.Uint32
	)
	prevEnable, prevPWM := writePWMEnableFn, writePWMFn
	writePWMEnableFn = func(path string, value int) error {
		switch value {
		case 2:
			mode2Calls.Add(1)
			return fmt.Errorf("hwmon: write pwm_enable %s_enable=2: %w", path, syscall.EINVAL)
		case 5:
			mode5Calls.Add(1)
			return fmt.Errorf("hwmon: write pwm_enable %s_enable=5: %w", path, syscall.EINVAL)
		}
		return fmt.Errorf("unexpected enable value: %d", value)
	}
	writePWMFn = func(path string, value uint8) error {
		pwmCalls.Add(1)
		pwmValue.Store(uint32(value))
		return nil
	}
	t.Cleanup(func() { writePWMEnableFn, writePWMFn = prevEnable, prevPWM })

	fans := []FanState{
		{Name: "Stubborn Fan", Type: "hwmon", PWMPath: pwmPath, DetectPhase: "none"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	restoreExcludedChannels(fans, nil, logger)

	if mode2Calls.Load() != 1 {
		t.Errorf("mode=2 calls = %d, want 1", mode2Calls.Load())
	}
	if mode5Calls.Load() != 1 {
		t.Errorf("mode=5 calls = %d, want 1 (EINVAL fallback path)", mode5Calls.Load())
	}
	if pwmCalls.Load() != 1 {
		t.Errorf("pwm-write calls = %d, want 1 (safe-PWM final fallback)", pwmCalls.Load())
	}
	if got := pwmValue.Load(); got != uint32(safeExcludedPWM) {
		t.Errorf("safe PWM value = %d, want %d (60%% floor const)", got, safeExcludedPWM)
	}
}

// TestRestoreExcludedChannels_OnlyEINVALTriggersMode5Retry pins
// the asymmetry in the chain: a non-EINVAL error on mode=2 (e.g.
// permission denied, IO error) MUST NOT trigger the mode=5 retry —
// that's a known-quirk-only path, not a generic "try anything"
// fallback. Such errors fall straight through to safe-PWM.
func TestRestoreExcludedChannels_OnlyEINVALTriggersMode5Retry(t *testing.T) {
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm4"
	var (
		mode2Calls atomic.Int32
		mode5Calls atomic.Int32
		pwmCalls   atomic.Int32
	)
	prevEnable, prevPWM := writePWMEnableFn, writePWMFn
	writePWMEnableFn = func(path string, value int) error {
		if value == 2 {
			mode2Calls.Add(1)
			// non-EINVAL: pretend the file got a generic IO error
			return fmt.Errorf("hwmon: write pwm_enable %s_enable=2: %w", path, errors.New("input/output error"))
		}
		if value == 5 {
			mode5Calls.Add(1)
			return nil
		}
		return nil
	}
	writePWMFn = func(path string, value uint8) error {
		pwmCalls.Add(1)
		return nil
	}
	t.Cleanup(func() { writePWMEnableFn, writePWMFn = prevEnable, prevPWM })

	fans := []FanState{
		{Name: "IO-error Fan", Type: "hwmon", PWMPath: pwmPath, DetectPhase: "none"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	restoreExcludedChannels(fans, nil, logger)

	if mode2Calls.Load() != 1 {
		t.Errorf("mode=2 calls = %d, want 1", mode2Calls.Load())
	}
	if mode5Calls.Load() != 0 {
		t.Errorf("mode=5 calls = %d, want 0 (only EINVAL triggers the SmartFan retry — not generic errors)", mode5Calls.Load())
	}
	if pwmCalls.Load() != 1 {
		t.Errorf("pwm-write calls = %d, want 1 (non-EINVAL falls straight through to safe-PWM)", pwmCalls.Load())
	}
}
