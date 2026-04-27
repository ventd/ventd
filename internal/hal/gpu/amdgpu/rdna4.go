package amdgpu

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrRDNA4NeedsKernel615 is returned when an RDNA4 write is attempted on a
// kernel older than 6.15, where the gpu_od fan_curve interface is incomplete.
var ErrRDNA4NeedsKernel615 = errors.New("amdgpu: RDNA4 fan curve writes require kernel 6.15+")

// rdna4DeviceIDs lists Navi 48 PCI device IDs (RDNA4).
// Navi 48 covers both RX 9070 and RX 9070 XT — the two SKUs share the same
// device ID; the split is via subsystem ID, which ventd does not need to
// distinguish for fan-control purposes.
// Verified against kernel master amdgpu_drv.c pciidlist[] (April 2026).
// Future Navi 48 derivatives (e.g. hypothetical RX 9060) will add new entries
// to this map when confirmed in upstream amdgpu_drv.c.
var rdna4DeviceIDs = map[uint32]bool{
	0x7550: true, // Navi 48 — RX 9070 / RX 9070 XT
}

// IsRDNA4 reads cardPath/device/device (the PCI device ID sysfs file) and
// returns true when the device matches a known RDNA4 (Navi 48) device ID.
func IsRDNA4(cardPath string) (bool, error) {
	devFile := filepath.Join(cardPath, "device", "device")
	raw, err := os.ReadFile(devFile)
	if err != nil {
		return false, fmt.Errorf("amdgpu: read device id %s: %w", devFile, err)
	}
	s := strings.TrimSpace(string(raw))
	// Device file is "0x7550\n" format.
	val, err := strconv.ParseUint(s, 0, 32)
	if err != nil {
		return false, fmt.Errorf("amdgpu: parse device id %q: %w", s, err)
	}
	return rdna4DeviceIDs[uint32(val)], nil
}

// osReleasePath is the kernel version file. Overridable in tests.
var osReleasePath = "/proc/sys/kernel/osrelease"

// kernelAtLeast returns true when the running kernel is ≥ major.minor.
// osrPath should be /proc/sys/kernel/osrelease (injectable for tests).
func kernelAtLeast(major, minor int, osrPath string) (bool, error) {
	raw, err := os.ReadFile(osrPath)
	if err != nil {
		return false, fmt.Errorf("amdgpu: read osrelease: %w", err)
	}
	// Format: "6.12.18-gentoo" — take first two numeric components.
	parts := strings.SplitN(strings.TrimSpace(string(raw)), ".", 3)
	if len(parts) < 2 {
		return false, fmt.Errorf("amdgpu: parse osrelease %q: too few components", strings.TrimSpace(string(raw)))
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return false, fmt.Errorf("amdgpu: parse osrelease major: %w", err)
	}
	// Minor may have a suffix like "15-arch1"; strip it.
	minParts := strings.SplitN(parts[1], "-", 2)
	min, err := strconv.Atoi(minParts[0])
	if err != nil {
		return false, fmt.Errorf("amdgpu: parse osrelease minor: %w", err)
	}
	if maj != major {
		return maj > major, nil
	}
	return min >= minor, nil
}

// checkRDNA4KernelGate returns ErrRDNA4NeedsKernel615 when cardPath identifies
// an RDNA4 GPU and the running kernel is older than 6.15. Returns nil when the
// card is not RDNA4 or the kernel satisfies the minimum.
func checkRDNA4KernelGate(cardPath, osrPath string) error {
	isRDNA4, err := IsRDNA4(cardPath)
	if err != nil || !isRDNA4 {
		return err
	}
	ok, err := kernelAtLeast(6, 15, osrPath)
	if err != nil {
		// Cannot determine kernel version; fail safe — allow the write.
		return nil
	}
	if !ok {
		return ErrRDNA4NeedsKernel615
	}
	return nil
}
