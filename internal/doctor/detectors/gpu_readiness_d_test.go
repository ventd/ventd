package detectors

import (
	"context"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

type stubGPUFS struct {
	files  map[string]string
	exists map[string]bool
	globs  map[string][]string
}

func (s *stubGPUFS) FileExists(p string) bool { return s.exists[p] }
func (s *stubGPUFS) ReadFile(p string) ([]byte, error) {
	if v, ok := s.files[p]; ok {
		return []byte(v), nil
	}
	return nil, errFileNotExist
}
func (s *stubGPUFS) Glob(pattern string) ([]string, error) {
	return s.globs[pattern], nil
}

func TestRULE_DOCTOR_DETECTOR_GPUReadiness_NoGPUNoFacts(t *testing.T) {
	det := NewGPUReadinessDetector(&stubGPUFS{})

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("no-GPU emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_GPUReadiness_OldNVIDIASurfacesAsBlocker(t *testing.T) {
	stub := &stubGPUFS{
		files: map[string]string{
			"/proc/driver/nvidia/version": "NVRM version: NVIDIA UNIX x86_64 Kernel Module  470.86  Fri Jul  5 14:40:48 UTC 2021\n",
		},
		exists: map[string]bool{
			"/usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1": true,
		},
	}
	det := NewGPUReadinessDetector(stub)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for old NVIDIA driver, got %d", len(facts))
	}
	f := facts[0]
	if f.Severity != doctor.SeverityBlocker {
		t.Errorf("Severity = %v, want Blocker", f.Severity)
	}
	if !strings.Contains(f.Title, "R470") {
		t.Errorf("Title doesn't say R470: %q", f.Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_GPUReadiness_NewNVIDIANoFacts(t *testing.T) {
	stub := &stubGPUFS{
		files: map[string]string{
			"/proc/driver/nvidia/version": "NVRM version: NVIDIA UNIX x86_64 Kernel Module  555.85  Fri Jul  5 14:40:48 UTC 2024\n",
		},
		exists: map[string]bool{
			"/usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1": true,
		},
	}
	det := NewGPUReadinessDetector(stub)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("R555 emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_GPUReadiness_NVMLLibMissingWarning(t *testing.T) {
	stub := &stubGPUFS{
		files: map[string]string{
			"/proc/driver/nvidia/version": "NVRM version: NVIDIA UNIX x86_64 Kernel Module  555.85\n",
		},
		// no libnvidia-ml.so.1 present
	}
	det := NewGPUReadinessDetector(stub)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for missing NVML lib, got %d", len(facts))
	}
	if !strings.Contains(facts[0].Title, "libnvidia-ml.so.1") {
		t.Errorf("Title doesn't mention libnvidia-ml.so.1: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_GPUReadiness_AMDGPUWithoutFanInterfaceWarns(t *testing.T) {
	stub := &stubGPUFS{
		files: map[string]string{
			"/sys/class/drm/card0/device/hwmon/hwmon0/name": "amdgpu\n",
		},
		globs: map[string][]string{
			"/sys/class/drm/card*/device/hwmon/hwmon*": {"/sys/class/drm/card0/device/hwmon/hwmon0"},
		},
		// Neither pwm1 nor fan_curve present
	}
	det := NewGPUReadinessDetector(stub)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for amdgpu without fan iface, got %d", len(facts))
	}
	if !strings.Contains(facts[0].Title, "amdgpu") {
		t.Errorf("Title doesn't mention amdgpu: %q", facts[0].Title)
	}
}

func TestRULE_DOCTOR_DETECTOR_GPUReadiness_AMDGPUWithPWMNoFacts(t *testing.T) {
	stub := &stubGPUFS{
		files: map[string]string{
			"/sys/class/drm/card0/device/hwmon/hwmon0/name": "amdgpu\n",
		},
		exists: map[string]bool{
			"/sys/class/drm/card0/device/hwmon/hwmon0/pwm1": true,
		},
		globs: map[string][]string{
			"/sys/class/drm/card*/device/hwmon/hwmon*": {"/sys/class/drm/card0/device/hwmon/hwmon0"},
		},
	}
	det := NewGPUReadinessDetector(stub)

	facts, _ := det.Probe(context.Background(), doctor.Deps{Now: fixedNow})
	if len(facts) != 0 {
		t.Errorf("amdgpu+pwm1 emitted facts: %+v", facts)
	}
}

func TestRULE_DOCTOR_DETECTOR_GPUReadiness_RespectsContextCancel(t *testing.T) {
	det := NewGPUReadinessDetector(&stubGPUFS{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := det.Probe(ctx, doctor.Deps{Now: fixedNow})
	if err == nil {
		t.Errorf("Probe on cancelled ctx returned nil err")
	}
}

func TestParseNvidiaDriverMajor_Variants(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"NVRM version: NVIDIA UNIX x86_64 Kernel Module  555.85  Fri Jul  5 14:40:48 UTC 2024", 555},
		{"NVRM version: NVIDIA UNIX x86_64 Kernel Module  470.86  Fri Jul  5 14:40:48 UTC 2021", 470},
		{"NVRM version: NVIDIA UNIX x86_64 Kernel Module  R470.99", 0}, // non-numeric leading
		{"GCC version: gcc 13.2.0", 0}, // wrong line
		{"", 0},
	}
	for _, c := range cases {
		if got := parseNvidiaDriverMajor(c.in); got != c.want {
			t.Errorf("parseNvidiaDriverMajor(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
