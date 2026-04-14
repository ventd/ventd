package nvidia

import (
	"log/slog"
	"os"
	"testing"
)

// TestSmoke runs a best-effort smoke test against the local NVML library.
// It is a no-op when libnvidia-ml.so.1 is not present (typical CI). When
// present, it exercises Init/Shutdown refcounting and the basic read path.
func TestSmoke(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	err := Init(logger)
	if err != nil {
		t.Logf("NVML not initialisable (expected when no driver): %v", err)
		return
	}
	defer Shutdown()

	if !Available() {
		t.Fatal("Init returned nil but Available() is false")
	}

	n := CountGPUs()
	t.Logf("NVML reports %d GPU(s)", n)
	for i := 0; i < n; i++ {
		name := GPUName(uint(i))
		temp, tempErr := ReadTemp(uint(i))
		t.Logf("GPU %d: %q temp=%v (err=%v)", i, name, temp, tempErr)
	}

	// Refcount check: a second Init/Shutdown should leave NVML usable
	// between them.
	if err := Init(logger); err != nil {
		t.Fatalf("refcount Init failed: %v", err)
	}
	if !Available() {
		t.Fatal("Available() false after refcount bump")
	}
	Shutdown()
	if !Available() {
		t.Fatal("Available() false after inner Shutdown — refcount not honoured")
	}
}
