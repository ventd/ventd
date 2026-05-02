package detectors

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// GPUReadinessFS is the read-only filesystem surface
// GPUReadinessDetector needs. Production wires the real /sys + ldconfig
// search; tests inject a stub.
type GPUReadinessFS interface {
	// FileExists reports presence (no metadata).
	FileExists(path string) bool

	// ReadFile returns contents; os.ErrNotExist on absence.
	ReadFile(path string) ([]byte, error)

	// Glob expands a shell-glob pattern. Used to find
	// /sys/class/drm/card*/device/hwmon and similar.
	Glob(pattern string) ([]string, error)
}

// liveGPUFS reads the real filesystem.
type liveGPUFS struct{}

func (liveGPUFS) FileExists(path string) bool   { _, err := os.Stat(path); return err == nil }
func (liveGPUFS) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }
func (liveGPUFS) Glob(p string) ([]string, error)   { return filepath.Glob(p) }

// nvmlMinDriverMajor is the minimum NVIDIA driver release that
// supports the NVML fan-control APIs ventd depends on
// (nvmlDeviceSetFanSpeed_v2 / nvmlDeviceSetDefaultFanSpeed_v2 /
// nvmlDeviceGetFanControlPolicy_v2). Per RULE-POLARITY-06, drivers
// below this return ErrNotSupported on the fan write path.
const nvmlMinDriverMajor = 515

// GPUReadinessDetector surfaces three classes of GPU concern:
//   - NVIDIA driver too old (major <515) → Blocker for GPU writes
//   - libnvidia-ml.so.1 missing → Warning ("NVML disabled")
//   - amdgpu present but no controllable hwmon → Warning
//
// Designed to be informational on systems without the relevant GPU
// — no facts emitted for an Intel-only desktop.
type GPUReadinessDetector struct {
	// FS is the env reader. Defaults to liveGPUFS{}.
	FS GPUReadinessFS
}

// NewGPUReadinessDetector constructs a detector. fs nil → live FS.
func NewGPUReadinessDetector(fs GPUReadinessFS) *GPUReadinessDetector {
	if fs == nil {
		fs = liveGPUFS{}
	}
	return &GPUReadinessDetector{FS: fs}
}

// Name returns the stable detector ID.
func (d *GPUReadinessDetector) Name() string { return "gpu_readiness" }

// Probe runs the audit.
func (d *GPUReadinessDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := timeNowFromDeps(deps)
	var facts []doctor.Fact

	// 1. NVIDIA driver path — proc/driver/nvidia/version is the
	//    canonical source. Format: "NVRM version: NVIDIA UNIX x86_64 Kernel Module 555.85"
	if raw, err := d.FS.ReadFile("/proc/driver/nvidia/version"); err == nil {
		major := parseNvidiaDriverMajor(string(raw))
		if major > 0 && major < nvmlMinDriverMajor {
			facts = append(facts, doctor.Fact{
				Detector: d.Name(),
				Severity: doctor.SeverityBlocker,
				Class:    recovery.ClassUnknown,
				Title:    fmt.Sprintf("NVIDIA driver R%d is below the R%d minimum for fan control", major, nvmlMinDriverMajor),
				Detail: fmt.Sprintf(
					"NVML's nvmlDeviceSetFanSpeed_v2 + GetFanControlPolicy_v2 are R%d+. R%d cannot drive GPU fans through ventd. Upgrade the NVIDIA driver via your distro's package manager or accept GPU fans staying on the firmware curve.",
					nvmlMinDriverMajor, major,
				),
				EntityHash: doctor.HashEntity("gpu_nvidia_driver_too_old"),
				Observed:   now,
			})
		}
		// Driver present + new-enough → check that libnvidia-ml.so.1
		// is reachable. The purego dlopen path needs the .so.1
		// symlink; some headless installs ship only the unversioned
		// libnvidia-ml.so.
		if !d.FS.FileExists("/usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1") &&
			!d.FS.FileExists("/usr/lib64/libnvidia-ml.so.1") &&
			!d.FS.FileExists("/usr/lib/libnvidia-ml.so.1") {
			facts = append(facts, doctor.Fact{
				Detector: d.Name(),
				Severity: doctor.SeverityWarning,
				Class:    recovery.ClassUnknown,
				Title:    "NVIDIA driver loaded but libnvidia-ml.so.1 not found",
				Detail:   "ventd's NVML access is dlopen-based and needs the .so.1 symlink. Install nvidia-utils-* (or equivalent) so the userspace library lands alongside the kernel module.",
				EntityHash: doctor.HashEntity("gpu_nvml_lib_missing"),
				Observed:   now,
			})
		}
	}

	// 2. AMD GPU path — every /sys/class/drm/card*/device/hwmon entry
	//    means an amdgpu-managed card. Confirm at least one controllable
	//    fan (pwm1 file) exists; absence is informational on RDNA3+
	//    where the gpu_od/fan_ctrl/fan_curve interface replaces pwm1
	//    (RULE-GPU-PR2D-07) — surface as OK so the operator sees the
	//    correct path was detected.
	cards, _ := d.FS.Glob("/sys/class/drm/card*/device/hwmon/hwmon*")
	for _, hw := range cards {
		// amdgpu-managed?
		nameRaw, err := d.FS.ReadFile(filepath.Join(hw, "name"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(nameRaw))
		if name != "amdgpu" {
			continue
		}
		hasPWM := d.FS.FileExists(filepath.Join(hw, "pwm1"))
		hasFanCurve := d.FS.FileExists(filepath.Dir(filepath.Dir(hw)) + "/gpu_od/fan_ctrl/fan_curve")
		if !hasPWM && !hasFanCurve {
			card := filepath.Base(filepath.Dir(filepath.Dir(hw)))
			facts = append(facts, doctor.Fact{
				Detector: d.Name(),
				Severity: doctor.SeverityWarning,
				Class:    recovery.ClassUnknown,
				Title:    fmt.Sprintf("amdgpu %s exposes no controllable fan interface", card),
				Detail:   "Neither legacy pwm1 nor RDNA3+ gpu_od/fan_ctrl/fan_curve was found. The card may be in firmware-only mode or require kernel cmdline `amdgpu.ppfeaturemask=0xfff7ffff` for fan control. See RULE-EXPERIMENTAL-AMD-OVERDRIVE-* for the gating criteria.",
				EntityHash: doctor.HashEntity("gpu_amd_no_fan_iface", card),
				Observed:   now,
			})
		}
	}

	return facts, nil
}

// parseNvidiaDriverMajor extracts the major version from the
// /proc/driver/nvidia/version content. Returns 0 if not parseable.
//
// Sample input:
//
//	NVRM version: NVIDIA UNIX x86_64 Kernel Module  555.85  Fri Jul  5 14:40:48 UTC 2024
//	GCC version:  gcc version 13.2.0 ...
func parseNvidiaDriverMajor(content string) int {
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, "Kernel Module") {
			continue
		}
		// Look for the first dotted-numeric token after "Kernel Module".
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "Module" && i+1 < len(fields) {
				ver := fields[i+1]
				dot := strings.Index(ver, ".")
				if dot <= 0 {
					return 0
				}
				if n, err := strconv.Atoi(ver[:dot]); err == nil {
					return n
				}
			}
		}
	}
	return 0
}
