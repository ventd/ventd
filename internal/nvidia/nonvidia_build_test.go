//go:build nonvidia

package nvidia

// Build-tag coverage for nvidia_nonvidia.go.
//
// Shipped binaries on musl distros (Alpine, Void-musl) are built with
// `-tags nonvidia` so purego's fakecgo shim doesn't pull glibc SONAMEs
// into the ELF. This file verifies that the -tags nonvidia build
// returns ErrLibraryUnavailable from Init and zero/ErrNotAvailable
// from every other entry point, regardless of whether NVML is
// actually present on the host.
//
// Run locally with:
//
//	go test -count=1 -tags nonvidia ./internal/nvidia/...
//
// Reference note for future sessions:
//
//	The stub's public function set must stay in sync with the real
//	nvidia.go. If you add a public function to nvidia.go, add a
//	matching stub to nvidia_nonvidia.go AND a matching line to this
//	test — otherwise a -tags nonvidia build silently loses the
//	capability and no test catches it.

import (
	"errors"
	"log/slog"
	"testing"
)

func TestNonVidiaBuild_InitReturnsLibraryUnavailable(t *testing.T) {
	err := Init(slog.Default())
	if !errors.Is(err, ErrLibraryUnavailable) {
		t.Fatalf("nonvidia Init: err = %v, want ErrLibraryUnavailable", err)
	}
	// Shutdown must be callable and not panic, matching the real
	// package's idempotent contract.
	Shutdown()
}

func TestNonVidiaBuild_AllReadersReturnErrNotAvailable(t *testing.T) {
	if Available() {
		t.Fatal("Available() = true in nonvidia build — stub is lying about state")
	}

	cases := []struct {
		name string
		fn   func() error
	}{
		{"ReadTemp", func() error { _, err := ReadTemp(0); return err }},
		{"ReadMetric", func() error { _, err := ReadMetric(0, "temp"); return err }},
		{"ReadFanSpeed", func() error { _, err := ReadFanSpeed(0); return err }},
		{"WriteFanSpeed", func() error { return WriteFanSpeed(0, 128) }},
		{"ResetFanSpeed", func() error { return ResetFanSpeed(0) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(); !errors.Is(err, ErrNotAvailable) {
				t.Fatalf("%s: err = %v, want ErrNotAvailable", tc.name, err)
			}
		})
	}
}

func TestNonVidiaBuild_ZeroValueAccessors(t *testing.T) {
	if n := CountGPUs(); n != 0 {
		t.Fatalf("CountGPUs = %d, want 0", n)
	}
	if HasFans(0) {
		t.Fatal("HasFans = true, want false")
	}
	if got := GPUName(0); got != "" {
		t.Fatalf("GPUName = %q, want empty (stub shape differs from real pkg — the real shape is \"GPU 0\", see internal/nvidia/nvidia.go:186-206)", got)
	}
	if got := SlowdownThreshold(0); got != 0 {
		t.Fatalf("SlowdownThreshold = %v, want 0", got)
	}
	if got := PowerLimitW(0); got != 0 {
		t.Fatalf("PowerLimitW = %d, want 0", got)
	}
}
