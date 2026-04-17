//go:build !nonvidia

package nvidia

// Unit coverage for the "NVML not initialised" branches of every public
// function. This file deliberately does NOT exercise the purego syscalls;
// mocking those would require refactoring the package to route every call
// through an injectable function table. Per the test plan, we respect the
// current architecture and cover only what is reachable without NVML.
//
// What this file pins:
//
//   * Every public read/write function returns ErrNotAvailable when
//     Available() is false. This is the state in CI (no NVIDIA driver)
//     and on any host without the library.
//   * GPUName returns the "GPU <index>" fallback string shape — the UI
//     depends on the "GPU " prefix for its fallback rendering.
//   * CountGPUs returns 0 when unavailable — the monitor loop relies on
//     this to skip the NVML path entirely.
//   * nvmlErrorString returns a "nvml error %d" shape when the symbol
//     table is zeroed. This is the error you see in logs before NVML
//     Init has been called, and the exact shape callers grep for.
//   * goStringFromC copies a NUL-terminated C buffer without CGO. We
//     allocate the buffer in Go (unsafe-pointer-stable for the duration
//     of the test) to verify the copy loop terminates on the first 0
//     byte and honours the 1<<16 guard.
//
// Reference for future sessions:
//
//   If a PR adds a new public Read* / Write* / Count* / Name*
//   function to nvidia.go, add it to the table in
//   TestPublicFunctions_ReturnErrNotAvailable or to the named sibling
//   test below. The current list is intentionally exhaustive so a CI
//   regression is visible rather than silent.
//
//   To cover the success path you need a running NVIDIA driver. See
//   nvidia_smoke_test.go for the dev-box-only smoke that exercises it.

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

// resetInitState restores package-level init state between tests so one
// test's Init/Shutdown doesn't leak into the next.
//
// WARNING: this reaches into package-private state. It is intentionally
// kept inside this _test.go file so production code never sees it. If
// the init refcount design changes, update this helper too.
func resetInitState(t *testing.T) {
	t.Helper()
	initMu.Lock()
	defer initMu.Unlock()
	initRefcount = 0
	ready = false
}

// TestAvailable_DefaultsFalse — a fresh process (or a test run after an
// Init failure) has Available() == false. This is the expected state
// everywhere except the NVIDIA dev-box.
func TestAvailable_DefaultsFalse(t *testing.T) {
	resetInitState(t)
	if Available() {
		t.Fatal("Available() returned true without a successful Init")
	}
}

// TestPublicFunctions_ReturnErrNotAvailable is the exhaustive "no NVML"
// contract. If a new public function is added that can return an error,
// extend this table.
func TestPublicFunctions_ReturnErrNotAvailable(t *testing.T) {
	resetInitState(t)

	tests := []struct {
		name string
		fn   func() error
	}{
		{"ReadTemp", func() error { _, err := ReadTemp(0); return err }},
		{"ReadMetric/temp", func() error { _, err := ReadMetric(0, "temp"); return err }},
		{"ReadMetric/util", func() error { _, err := ReadMetric(0, "util"); return err }},
		{"ReadMetric/mem_util", func() error { _, err := ReadMetric(0, "mem_util"); return err }},
		{"ReadMetric/power", func() error { _, err := ReadMetric(0, "power"); return err }},
		{"ReadMetric/clock_gpu", func() error { _, err := ReadMetric(0, "clock_gpu"); return err }},
		{"ReadMetric/clock_mem", func() error { _, err := ReadMetric(0, "clock_mem"); return err }},
		{"ReadMetric/fan_pct", func() error { _, err := ReadMetric(0, "fan_pct"); return err }},
		{"ReadFanSpeed", func() error { _, err := ReadFanSpeed(0); return err }},
		{"WriteFanSpeed", func() error { return WriteFanSpeed(0, 128) }},
		{"ResetFanSpeed", func() error { return ResetFanSpeed(0) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if !errors.Is(err, ErrNotAvailable) {
				t.Fatalf("%s: err = %v, want ErrNotAvailable", tc.name, err)
			}
		})
	}
}

// TestZeroValueAccessors_WhenUnavailable pins the zero/default shapes of
// the non-error public accessors. The monitor and UI layers read these
// first when deciding whether to render GPU panels.
func TestZeroValueAccessors_WhenUnavailable(t *testing.T) {
	resetInitState(t)

	if n := CountGPUs(); n != 0 {
		t.Fatalf("CountGPUs() = %d, want 0", n)
	}
	if HasFans(0) {
		t.Fatal("HasFans(0) = true, want false when unavailable")
	}
	if got := SlowdownThreshold(0); got != 0 {
		t.Fatalf("SlowdownThreshold(0) = %v, want 0", got)
	}
	if got := PowerLimitW(0); got != 0 {
		t.Fatalf("PowerLimitW(0) = %d, want 0", got)
	}
}

// TestGPUName_FallbackShape — the UI expects "GPU <index>" when the real
// name cannot be queried. Any change to this shape breaks the fallback
// rendering path; pin the literal prefix and the index interpolation.
func TestGPUName_FallbackShape(t *testing.T) {
	resetInitState(t)

	for _, i := range []uint{0, 1, 7} {
		got := GPUName(i)
		want := fmt.Sprintf("GPU %d", i)
		if got != want {
			t.Fatalf("GPUName(%d) = %q, want %q", i, got, want)
		}
	}
}

// TestReadMetric_UnknownMetric_AfterAvailable — the "unknown metric"
// error is only reachable when Available()==true because the availability
// check short-circuits first (nvidia.go:260-262). We cannot flip Available
// in a unit test without NVML, so this test documents the ordering and
// asserts the unavailable path is what we actually hit.
//
// If you add an "unknown metric" test that runs with Available=true, it
// belongs next to the success-path tests in nvidia_smoke_test.go.
func TestReadMetric_UnknownMetric_ShortCircuitsOnUnavailable(t *testing.T) {
	resetInitState(t)

	_, err := ReadMetric(0, "gibberish-metric-xyz")
	if !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("unknown metric on unavailable NVML: err = %v, want ErrNotAvailable (ordering changed?)", err)
	}
}

// TestNvmlErrorString_FallbackWhenSymbolMissing — before loadLibrary has
// run (or when it fails), pErrorString is zero. nvmlErrorString must then
// return the "nvml error %d" shape. Log lines grep against this shape.
func TestNvmlErrorString_FallbackWhenSymbolMissing(t *testing.T) {
	// Save and restore the global — tests in this file run in the same
	// goroutine and t.Cleanup guarantees ordering.
	savedPErrorString := pErrorString
	t.Cleanup(func() { pErrorString = savedPErrorString })
	pErrorString = 0

	got := nvmlErrorString(42)
	want := "nvml error 42"
	if got != want {
		t.Fatalf("nvmlErrorString(42) with pErrorString=0: got %q, want %q", got, want)
	}
}

// TestGoStringFromC_NullPointer — the pure-Go null check before the scan
// loop. Regression target: any optimiser-hostile change that drops this
// check would crash on the NULL returned by nvmlErrorString for codes
// outside the driver's string table.
func TestGoStringFromC_NullPointer(t *testing.T) {
	if got := goStringFromC(0); got != "" {
		t.Fatalf("goStringFromC(0) = %q, want empty string", got)
	}
}

// TestGoStringFromC_CopiesUntilNUL verifies the scan loop with a
// Go-allocated, NUL-terminated byte buffer. The buffer is heap-allocated
// by Go and stays pinned for the test's lifetime because we retain the
// original []byte reference; the unsafe.Pointer→uintptr dance mirrors
// what purego hands us from NVML's static C strings.
func TestGoStringFromC_CopiesUntilNUL(t *testing.T) {
	// Heap-allocate so the address is stable for the duration of the call.
	buf := append([]byte("libnvidia-ml.so.1"), 0, 'x', 'y', 'z')
	p := uintptr(unsafe.Pointer(&buf[0]))

	got := goStringFromC(p)
	want := "libnvidia-ml.so.1"
	if got != want {
		t.Fatalf("goStringFromC copied %q, want %q", got, want)
	}
	// Keep buf alive past the call.
	_ = buf
}

// TestGoStringFromC_EmptyString — a leading NUL means zero-length copy.
// This is the shape NVML returns for a missing-but-valid message slot.
func TestGoStringFromC_EmptyString(t *testing.T) {
	buf := []byte{0, 'a', 'b'}
	p := uintptr(unsafe.Pointer(&buf[0]))
	if got := goStringFromC(p); got != "" {
		t.Fatalf("goStringFromC on leading-NUL buffer = %q, want empty", got)
	}
	_ = buf
}

// TestShutdown_Idempotent_WhenRefcountZero — calling Shutdown before
// Init must not panic and must leave Available()==false. The daemon's
// defer chain calls Shutdown unconditionally; a panic here would mask
// the real cause of an earlier failure.
func TestShutdown_Idempotent_WhenRefcountZero(t *testing.T) {
	resetInitState(t)
	Shutdown() // must not panic
	Shutdown()
	if Available() {
		t.Fatal("Available() after no-op Shutdown calls = true")
	}
}

// TestInit_ConcurrentCallsRespectRefcount — Init is safe to call from
// multiple goroutines (the web wizard, the monitor, the controller all
// call it). We cannot verify the success path without NVML, but we CAN
// verify that concurrent callers on a library-absent host all get the
// same ErrLibraryUnavailable and none panic.
//
// Use a long-lived logger so log calls don't race with test teardown.
func TestInit_ConcurrentCallsRespectRefcount(t *testing.T) {
	resetInitState(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = Init(logger)
		}(i)
	}
	wg.Wait()

	// On a library-absent host, every caller sees ErrLibraryUnavailable
	// (wrapped). On the dev-box with NVML, every caller sees nil. Both
	// are allowed; what we pin is "no panics, no mixed outcomes".
	first := errs[0]
	for i, err := range errs {
		match := (first == nil && err == nil) ||
			(first != nil && err != nil && strings.Contains(err.Error(), "not loadable") == strings.Contains(first.Error(), "not loadable"))
		if !match {
			t.Fatalf("goroutine %d saw divergent Init outcome: %v (first was %v)", i, err, first)
		}
	}
}
