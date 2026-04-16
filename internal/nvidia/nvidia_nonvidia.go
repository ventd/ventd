//go:build nonvidia

// Package nvidia provides compile-time-disabled GPU support.
//
// Built with `-tags nonvidia` to avoid importing purego, whose fakecgo
// shim pulls glibc SONAMEs (libc/libdl/libpthread) into the binary and
// breaks execution on musl distros (Alpine, Void-musl, etc.).
//
// All functions return ErrNotAvailable or zero values. The daemon
// behaves exactly as if no NVIDIA GPU / driver were present.
package nvidia

import (
	"errors"
	"log/slog"
)

var (
	ErrLibraryUnavailable = errors.New("libnvidia-ml.so.1 not loadable")
	ErrInitFailed         = errors.New("nvmlInit_v2 failed")
	ErrNotAvailable       = errors.New("nvml not available (built with -tags nonvidia)")
)

func Init(logger *slog.Logger) error {
	if logger != nil {
		logger.Info("nvidia: compiled out (nonvidia build tag); GPU features disabled")
	}
	return ErrLibraryUnavailable
}

func Shutdown()                                             {}
func Available() bool                                       { return false }
func CountGPUs() int                                        { return 0 }
func HasFans(index uint) bool                               { return false }
func GPUName(index uint) string                             { return "" }
func SlowdownThreshold(index uint) float64                  { return 0 }
func PowerLimitW(index uint) int                            { return 0 }
func ReadTemp(index uint) (float64, error)                  { return 0, ErrNotAvailable }
func ReadMetric(index uint, metric string) (float64, error) { return 0, ErrNotAvailable }
func ReadFanSpeed(index uint) (uint8, error)                { return 0, ErrNotAvailable }
func WriteFanSpeed(index uint, pwm uint8) error             { return ErrNotAvailable }
func ResetFanSpeed(index uint) error                        { return ErrNotAvailable }
