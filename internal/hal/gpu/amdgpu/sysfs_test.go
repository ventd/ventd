package amdgpu

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestGPU_NoHwmonNumbersHardcoded verifies RULE-GPU-PR2D-05: no hwmon0/hwmon1/etc.
// literals in non-test production source files under internal/hal/gpu/.
func TestGPU_NoHwmonNumbersHardcoded(t *testing.T) {
	gpuRoot := filepath.Join("..", "..", "..", "..", "internal", "hal", "gpu")
	// Re-anchor from the test binary's working directory.
	// When running as `go test ./internal/hal/gpu/amdgpu/`, cwd is this package.
	// Walk up to repo root and back down.
	absRoot, err := filepath.Abs(filepath.Join(".", "..", "..", "..", "hal", "gpu"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	// Confirm the directory exists; if not, the path math is wrong.
	if _, err := os.Stat(absRoot); err != nil {
		// Try the simpler relative-from-cwd approach.
		absRoot = gpuRoot
	}

	re := regexp.MustCompile(`hwmon[0-9]`)
	err = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if re.Match(data) {
			t.Errorf("file %s contains hwmonN literal — use name-based resolution (RULE-GPU-PR2D-05)", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk gpu dir: %v", err)
	}
}

// TestAMD_RDNA3UsesFanCurve verifies RULE-GPU-PR2D-07: RDNA3+ writes use
// fan_curve, direct pwm1 writes are refused.
func TestAMD_RDNA3UsesFanCurve(t *testing.T) {
	tmp := t.TempDir()
	cardPath := filepath.Join(tmp, "card0")

	// Build a synthetic RDNA3+ fixture: has both pwm1 and fan_curve.
	hwmonPath := filepath.Join(cardPath, "device", "hwmon", "hwmonX")
	fanCurveDir := filepath.Join(cardPath, "device", "gpu_od", "fan_ctrl")
	for _, dir := range []string{hwmonPath, fanCurveDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Write hwmon name file.
	if err := os.WriteFile(filepath.Join(hwmonPath, "name"), []byte("amdgpu\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create pwm1 and pwm1_enable.
	if err := os.WriteFile(filepath.Join(hwmonPath, "pwm1"), []byte("128"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hwmonPath, "pwm1_enable"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create fan_curve sentinel (presence triggers RDNA3 path).
	fanCurvePath := filepath.Join(fanCurveDir, "fan_curve")
	if err := os.WriteFile(fanCurvePath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	card := &CardInfo{
		CardPath:    cardPath,
		HwmonPath:   hwmonPath,
		HasFanCurve: true,
	}

	t.Run("direct_pwm1_write_refused", func(t *testing.T) {
		err := card.WritePWM(200)
		if err == nil {
			t.Fatal("WritePWM on RDNA3+ card: expected ErrRDNA3UseFanCurve, got nil")
		}
		if err != ErrRDNA3UseFanCurve {
			t.Errorf("WritePWM: want ErrRDNA3UseFanCurve, got: %v", err)
		}
		// Verify pwm1 was not modified.
		raw, _ := os.ReadFile(filepath.Join(hwmonPath, "pwm1"))
		if strings.TrimSpace(string(raw)) != "128" {
			t.Errorf("pwm1 was modified on RDNA3+ card: got %q, want 128", string(raw))
		}
	})

	t.Run("fan_curve_write_accepted", func(t *testing.T) {
		points := []FanCurvePoint{
			{0, 50, 30},
			{1, 65, 50},
			{2, 80, 70},
			{3, 90, 85},
			{4, 100, 100},
		}
		err := WriteFanCurve(cardPath, points)
		if err != nil {
			t.Fatalf("WriteFanCurve: %v", err)
		}
		// Verify fan_curve file was written.
		data, err := os.ReadFile(fanCurvePath)
		if err != nil {
			t.Fatalf("read fan_curve: %v", err)
		}
		if !strings.Contains(string(data), "0 50 30") {
			t.Errorf("fan_curve missing expected anchor point: %q", string(data))
		}
		if !strings.Contains(string(data), "c") {
			t.Errorf("fan_curve missing commit byte 'c': %q", string(data))
		}
	})

	t.Run("rdna12_card_accepts_pwm1_write", func(t *testing.T) {
		rdna2Card := &CardInfo{
			CardPath:    cardPath,
			HwmonPath:   hwmonPath,
			HasFanCurve: false, // no fan_curve → RDNA1/2 path
		}
		err := rdna2Card.WritePWM(150)
		if err != nil {
			t.Errorf("RDNA1/2 card WritePWM: unexpected error: %v", err)
		}
	})
}
