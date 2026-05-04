package setup

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// resetProbedAutoModesForTest clears the per-process pwm_enable
// probe cache. Necessary between tests that share a pwm path —
// otherwise a probe result from one test leaks into the next and
// the call counts go off.
func resetProbedAutoModesForTest(t *testing.T) {
	t.Helper()
	probedPWMEnableModesMu.Lock()
	probedPWMEnableModes = map[string][]int{}
	probedPWMEnableModesMu.Unlock()
}

// TestRestoreExcludedChannels_EINVALProbeFindsAcceptedMode pins the
// probe leg of RULE-HWMON-ENABLE-EINVAL-FALLBACK (issue #909, refined
// by the runtime-probe redesign per Phoenix's "ventd should probe"
// directive). When mode=2 returns EINVAL, ventd probes the chip's
// accepted pwm_enable enum (writes each candidate in {2..7} once,
// observing which return success), picks the HIGHEST-numbered
// accepted value (richer-mode-wins on conventional drivers — e.g.
// nct6687 SmartFan=5), and writes that. The probe restores
// pwm_enable=1 (manual) before returning so subsequent control
// writes still work.
func TestRestoreExcludedChannels_EINVALProbeFindsAcceptedMode(t *testing.T) {
	resetProbedAutoModesForTest(t)
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm2"
	var (
		writes   []int // chronological log of every write attempt
		writesMu sync.Mutex
	)
	prevEnable, prevPWM := writePWMEnableFn, writePWMFn
	writePWMEnableFn = func(path string, value int) error {
		writesMu.Lock()
		writes = append(writes, value)
		writesMu.Unlock()
		// Chip rejects 2 with EINVAL; accepts 5 (nct6687 SmartFan-style)
		// and 1 (manual, used by probe restore). Everything else EINVAL.
		switch value {
		case 1, 5:
			return nil
		default:
			return fmt.Errorf("hwmon: write pwm_enable %s_enable=%d: %w", path, value, syscall.EINVAL)
		}
	}
	writePWMFn = func(path string, value uint8) error {
		t.Errorf("safe-PWM write should NOT fire when probe finds an accepted mode (got pwm=%d)", value)
		return nil
	}
	t.Cleanup(func() { writePWMEnableFn, writePWMFn = prevEnable, prevPWM })

	fans := []FanState{
		{Name: "Pump Fan", Type: "hwmon", PWMPath: pwmPath, DetectPhase: "none"},
	}
	restoreExcludedChannels(fans, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Expected write sequence:
	//   1. mode=2 (initial standard attempt)         → EINVAL
	//   2-7. probe writes 2,3,4,5,6,7 in order       → 5 accepted, rest EINVAL
	//   8. probe restore writes 1                    → success
	//   9. handback writes 5 (highest accepted)      → success
	want := []int{2, 2, 3, 4, 5, 6, 7, 1, 5}
	writesMu.Lock()
	got := append([]int(nil), writes...)
	writesMu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("write sequence length = %d, want %d\ngot: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("write[%d] = %d, want %d (full sequence: got %v want %v)", i, got[i], v, got, want)
		}
	}
}

// TestRestoreExcludedChannels_ProbeFindsNothingFallsBackToSafePWM
// pins the in-tree-nct6687 case: the chip exposes pwm_enable but
// rejects every value in {2..7} with EINVAL (driver supports only
// {0,1}). Probe returns []; chain falls through to safe-PWM.
// Caught live on Phoenix's MSI PRO Z690-A DDR4 — empirical probe
// of the chip showed only pwm_enable=1 accepted; everything else
// EINVAL.
func TestRestoreExcludedChannels_ProbeFindsNothingFallsBackToSafePWM(t *testing.T) {
	resetProbedAutoModesForTest(t)
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm3"
	var (
		probeWrites atomic.Int32
		pwmCalls    atomic.Int32
		pwmValue    atomic.Uint32
	)
	prevEnable, prevPWM := writePWMEnableFn, writePWMFn
	writePWMEnableFn = func(path string, value int) error {
		probeWrites.Add(1)
		// Manual-only chip: 1 succeeds, everything else EINVAL.
		if value == 1 {
			return nil
		}
		return fmt.Errorf("hwmon: write pwm_enable %s_enable=%d: %w", path, value, syscall.EINVAL)
	}
	writePWMFn = func(path string, value uint8) error {
		pwmCalls.Add(1)
		pwmValue.Store(uint32(value))
		return nil
	}
	t.Cleanup(func() { writePWMEnableFn, writePWMFn = prevEnable, prevPWM })

	fans := []FanState{
		{Name: "System Fan #5", Type: "hwmon", PWMPath: pwmPath, DetectPhase: "none"},
	}
	restoreExcludedChannels(fans, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Expected: 1 initial mode=2 + 6 probe (2,3,4,5,6,7) + 1 restore (1) = 8
	if got := probeWrites.Load(); got != 8 {
		t.Errorf("pwm_enable writes = %d, want 8 (1 initial + 6 probe + 1 restore-to-1)", got)
	}
	if got := pwmCalls.Load(); got != 1 {
		t.Errorf("safe-PWM writes = %d, want 1 (probe found nothing → safe-PWM fallback)", got)
	}
	if got := pwmValue.Load(); got != uint32(safeExcludedPWM) {
		t.Errorf("safe PWM value = %d, want %d", got, safeExcludedPWM)
	}
}

// TestRestoreExcludedChannels_NonEINVALSkipsProbe pins the
// asymmetry: only EINVAL on the initial mode=2 write triggers the
// probe. Other errors (IO error, permission denied, etc.) skip the
// probe and fall straight through to safe-PWM. The probe is a
// known-quirk-discovery path, not a generic "try anything if it
// fails" loop.
func TestRestoreExcludedChannels_NonEINVALSkipsProbe(t *testing.T) {
	resetProbedAutoModesForTest(t)
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm4"
	var (
		enableWrites atomic.Int32
		pwmCalls     atomic.Int32
	)
	prevEnable, prevPWM := writePWMEnableFn, writePWMFn
	writePWMEnableFn = func(path string, value int) error {
		enableWrites.Add(1)
		// Initial mode=2 returns IO error (NOT EINVAL).
		return fmt.Errorf("hwmon: write pwm_enable %s_enable=%d: %w", path, value, errors.New("input/output error"))
	}
	writePWMFn = func(path string, value uint8) error {
		pwmCalls.Add(1)
		return nil
	}
	t.Cleanup(func() { writePWMEnableFn, writePWMFn = prevEnable, prevPWM })

	fans := []FanState{
		{Name: "IO-error Fan", Type: "hwmon", PWMPath: pwmPath, DetectPhase: "none"},
	}
	restoreExcludedChannels(fans, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if got := enableWrites.Load(); got != 1 {
		t.Errorf("pwm_enable writes = %d, want 1 (non-EINVAL must NOT trigger probe)", got)
	}
	if got := pwmCalls.Load(); got != 1 {
		t.Errorf("safe-PWM writes = %d, want 1 (non-EINVAL falls straight through to safe-PWM)", got)
	}
}

// TestRestoreExcludedChannels_ProbeResultIsCached pins that the
// chip-capability probe runs ONCE per pwm path per daemon lifetime.
// Two excluded channels on the same chip → only one probe pass.
// (Cached per-path because some drivers have per-channel quirks;
// per-channel cache costs O(channels) memory and is correct in
// edge cases where pwm1 and pwm5 of the same chip behave
// differently.)
func TestRestoreExcludedChannels_ProbeResultIsCached(t *testing.T) {
	resetProbedAutoModesForTest(t)
	const pwmPath = "/sys/class/hwmon/hwmon0/pwm5"
	var (
		writes   []int
		writesMu sync.Mutex
	)
	prevEnable, prevPWM := writePWMEnableFn, writePWMFn
	writePWMEnableFn = func(path string, value int) error {
		writesMu.Lock()
		writes = append(writes, value)
		writesMu.Unlock()
		// Manual + 5 succeed; 2/3/4/6/7 EINVAL.
		if value == 1 || value == 5 {
			return nil
		}
		return fmt.Errorf("hwmon: write pwm_enable %s_enable=%d: %w", path, value, syscall.EINVAL)
	}
	writePWMFn = func(path string, value uint8) error { return nil }
	t.Cleanup(func() { writePWMEnableFn, writePWMFn = prevEnable, prevPWM })

	// Same fan re-handed-back twice (simulates two excluded channels
	// sharing a pwm path — degenerate but deterministic for the
	// caching contract).
	fans := []FanState{
		{Name: "Fan A", Type: "hwmon", PWMPath: pwmPath, DetectPhase: "none"},
		{Name: "Fan B", Type: "hwmon", PWMPath: pwmPath, DetectPhase: "none"},
	}
	restoreExcludedChannels(fans, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// First pass: 1 (mode=2) + 6 (probe 2..7) + 1 (restore 1) + 1 (handback mode=5) = 9 writes.
	// Second pass for the same path: probe is cached → 1 (mode=2) + 1 (handback mode=5) = 2 writes.
	// Total = 11.
	writesMu.Lock()
	got := len(writes)
	writesMu.Unlock()
	if got != 11 {
		t.Errorf("total pwm_enable writes = %d, want 11 (first pass 9 incl. probe + second pass 2 cached) — got sequence: %v", got, writes)
	}
}
