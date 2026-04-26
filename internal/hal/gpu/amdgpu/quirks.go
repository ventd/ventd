package amdgpu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CheckPPFeatureMask reads /sys/module/amdgpu/parameters/ppfeaturemask and
// checks whether the OverDrive bit (0x4000) is set. Returns the raw value
// and a bool indicating OverDrive is enabled.
func CheckPPFeatureMask(sysRoot string) (uint64, bool, error) {
	path := filepath.Join(sysRoot, "module", "amdgpu", "parameters", "ppfeaturemask")
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false, fmt.Errorf("amdgpu: read ppfeaturemask: %w", err)
	}
	var val uint64
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		if _, err := fmt.Sscanf(s, "%v", &val); err != nil {
			return 0, false, fmt.Errorf("amdgpu: parse ppfeaturemask %q: %w", s, err)
		}
	} else {
		if _, err := fmt.Sscan(s, &val); err != nil {
			return 0, false, fmt.Errorf("amdgpu: parse ppfeaturemask %q: %w", s, err)
		}
	}
	return val, (val & 0x4000) != 0, nil
}

// StuckAutoModeDance performs the RDNA1/2 stuck-auto-mode workaround:
// write pwm1_enable=1, then pwm1=128, then pwm1_enable=2 to re-arm the
// firmware curve. Only called when pwm1==0 AND temp > 50°C at startup.
func StuckAutoModeDance(hwmonPath string) error {
	enablePath := filepath.Join(hwmonPath, "pwm1_enable")
	pwmPath := filepath.Join(hwmonPath, "pwm1")

	for _, step := range []struct{ path, val string }{
		{enablePath, "1"},
		{pwmPath, "128"},
		{enablePath, "2"},
	} {
		if err := os.WriteFile(step.path, []byte(step.val), 0o644); err != nil {
			return fmt.Errorf("amdgpu: stuck-auto dance %s=%s: %w", step.path, step.val, err)
		}
	}
	return nil
}
