// Package nvml provides NVIDIA GPU fan control via NVML.
// It wraps internal/nvidia (purego, no CGO) and adds capability probing
// and laptop-dGPU detection on top of the core NVML access layer.
package nvml

import (
	"log/slog"

	"github.com/ventd/ventd/internal/nvidia"
)

// Open initialises the NVML layer, returning an error if libnvidia-ml.so.1
// is absent or fails to initialise. Callers must call Close when done.
// The underlying init is refcounted so multiple Open/Close pairs are safe.
func Open(logger *slog.Logger) error {
	return nvidia.Init(logger)
}

// Close decrements the NVML refcount.
func Close() { nvidia.Shutdown() }

// Available reports whether NVML is currently initialised and usable.
func Available() bool { return nvidia.Available() }
