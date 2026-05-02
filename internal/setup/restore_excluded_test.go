package setup

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
