//go:build !nonvidia

// Package nvidia provides GPU temperature and fan control via NVML.
//
// NVML is loaded at runtime from libnvidia-ml.so.1 via purego (no CGO).
// Build the binary with CGO_ENABLED=0; it runs everywhere and silently
// disables GPU features when the driver/library is absent.
//
// Lifecycle: call Init once at startup. Init is refcount-safe: callers
// like the web setup wizard and the hardware monitor may also call
// Init/Shutdown in matched pairs; NVML is only released on the final
// Shutdown. Library handle resolution happens at most once per process.
//
// Init has three outcomes, each logged once:
//   - library absent (info): driver/library not installed; GPU features off
//   - loaded and initialised (info): ready
//   - loaded but init failed (warn): usually a driver/persistence issue;
//     hint surfaced in the log message
//
// All read/write functions are safe to call concurrently after Init and
// return a typed error when NVML is not usable.
package nvidia

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/user"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Sentinel errors so callers can distinguish "driver not installed" from
// "driver present but init failed".
var (
	ErrLibraryUnavailable = errors.New("libnvidia-ml.so.1 not loadable")
	ErrInitFailed         = errors.New("nvmlInit_v2 failed")
	ErrNotAvailable       = errors.New("nvml not available")
)

// NVML constants (see /usr/include/nvml.h).
const (
	nvmlSuccess                      = 0
	nvmlTemperatureGPU               = 0
	nvmlTemperatureThresholdSlowdown = 1
	nvmlClockGraphics                = 0
	nvmlClockMem                     = 2
	nvmlDeviceNameBufSize            = 96
)

var (
	// Library + symbol resolution happens at most once.
	loadOnce sync.Once
	loadErr  error

	// Function pointers resolved from libnvidia-ml.so.1.
	pInit_v2                       uintptr
	pShutdown                      uintptr
	pDeviceGetCount_v2             uintptr
	pDeviceGetHandleByIndex_v2     uintptr
	pDeviceGetName                 uintptr
	pDeviceGetTemperature          uintptr
	pDeviceGetTemperatureThreshold uintptr
	pDeviceGetUtilizationRates     uintptr
	pDeviceGetPowerUsage           uintptr
	pDeviceGetPowerManagementLimit uintptr
	pDeviceGetClockInfo            uintptr
	pDeviceGetNumFans              uintptr
	pDeviceGetFanSpeed_v2          uintptr
	pDeviceSetFanSpeed_v2          uintptr
	pDeviceSetDefaultFanSpeed_v2   uintptr
	pErrorString                   uintptr

	// Init/Shutdown refcount.
	initMu       sync.Mutex
	initRefcount int
	ready        bool // true only when nvmlInit_v2 succeeded and refcount > 0

	// Log each of the three Init outcomes at most once per process.
	logAbsentOnce      sync.Once
	logSymbolMissOnce  sync.Once
	logInitFailedOnce  sync.Once
	logInitialisedOnce sync.Once
)

// Init loads libnvidia-ml.so.1 and initialises NVML. Returns nil when NVML
// is ready to use. Returns ErrLibraryUnavailable when the driver/library
// is not installed, or a wrapped ErrInitFailed when the library is present
// but initialisation failed. In both error cases the daemon should keep
// running; GPU features are simply disabled.
//
// Safe to call from multiple call sites; Init/Shutdown are refcounted.
// logger may be nil.
func Init(logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	loadOnce.Do(func() { loadErr = loadLibrary(logger) })
	if loadErr != nil {
		return loadErr
	}

	initMu.Lock()
	defer initMu.Unlock()

	if initRefcount == 0 {
		r, _, _ := purego.SyscallN(pInit_v2)
		if rc := int32(r); rc != nvmlSuccess {
			msg := nvmlErrorString(rc)
			initErr := fmt.Errorf("%w: %s", ErrInitFailed, msg)
			logInitFailedOnce.Do(func() {
				logger.Warn("NVML init failed; GPU features disabled",
					"err", msg,
					"diagnostic", diagnoseNvmlFailure(initErr))
			})
			return initErr
		}
		ready = true
		logInitialisedOnce.Do(func() {
			logger.Info("NVML initialised")
		})
	}
	initRefcount++
	return nil
}

// Shutdown decrements the NVML refcount. The library is released on the
// final matching call. Idempotent when the refcount is already zero.
func Shutdown() {
	initMu.Lock()
	defer initMu.Unlock()

	if initRefcount == 0 {
		return
	}
	initRefcount--
	if initRefcount == 0 {
		if pShutdown != 0 {
			purego.SyscallN(pShutdown) //nolint:errcheck
		}
		ready = false
	}
}

// Available reports whether NVML is currently initialised and usable.
func Available() bool {
	initMu.Lock()
	defer initMu.Unlock()
	return ready
}

// CountGPUs returns the number of NVML-visible GPUs. Returns 0 when NVML
// is unavailable.
func CountGPUs() int {
	if !Available() {
		return 0
	}
	var count uint32
	r, _, _ := purego.SyscallN(pDeviceGetCount_v2, uintptr(unsafe.Pointer(&count)))
	if int32(r) != nvmlSuccess {
		return 0
	}
	return int(count)
}

// HasFans returns true if the GPU at the given index has at least one
// controllable fan.
func HasFans(index uint) bool {
	if !Available() {
		return false
	}
	dev, err := deviceHandle(index)
	if err != nil {
		return false
	}
	n, err := numFans(dev)
	return err == nil && n > 0
}

// GPUName returns the model name of the GPU at the given index, falling
// back to "GPU <index>" when the query fails or NVML is unavailable.
func GPUName(index uint) string {
	if !Available() {
		return fmt.Sprintf("GPU %d", index)
	}
	dev, err := deviceHandle(index)
	if err != nil {
		return fmt.Sprintf("GPU %d", index)
	}
	var buf [nvmlDeviceNameBufSize]byte
	r, _, _ := purego.SyscallN(pDeviceGetName,
		dev,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if int32(r) != nvmlSuccess {
		return fmt.Sprintf("GPU %d", index)
	}
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return string(buf[:n])
}

// SlowdownThreshold returns the temperature (°C) at which the GPU starts
// throttling. Returns 0 when unavailable.
func SlowdownThreshold(index uint) float64 {
	if !Available() {
		return 0
	}
	dev, err := deviceHandle(index)
	if err != nil {
		return 0
	}
	var temp uint32
	r, _, _ := purego.SyscallN(pDeviceGetTemperatureThreshold,
		dev,
		uintptr(nvmlTemperatureThresholdSlowdown),
		uintptr(unsafe.Pointer(&temp)),
	)
	if int32(r) != nvmlSuccess {
		return 0
	}
	return float64(temp)
}

// PowerLimitW returns the active power-management limit in watts.
// Returns 0 when unavailable.
func PowerLimitW(index uint) int {
	if !Available() {
		return 0
	}
	dev, err := deviceHandle(index)
	if err != nil {
		return 0
	}
	var mw uint32
	r, _, _ := purego.SyscallN(pDeviceGetPowerManagementLimit,
		dev,
		uintptr(unsafe.Pointer(&mw)),
	)
	if int32(r) != nvmlSuccess {
		return 0
	}
	return int(mw / 1000)
}

// ReadTemp returns the GPU temperature in °C.
func ReadTemp(index uint) (float64, error) {
	return ReadMetric(index, "temp")
}

// ReadMetric reads a named metric from an NVML GPU.
// Supported metrics: "temp" (°C), "util" (GPU util %), "mem_util" (mem util %),
// "power" (W), "clock_gpu" (MHz), "clock_mem" (MHz), "fan_pct" (fan speed %).
func ReadMetric(index uint, metric string) (float64, error) {
	if !Available() {
		return 0, ErrNotAvailable
	}
	dev, err := deviceHandle(index)
	if err != nil {
		return 0, err
	}
	switch metric {
	case "", "temp":
		var v uint32
		r, _, _ := purego.SyscallN(pDeviceGetTemperature,
			dev,
			uintptr(nvmlTemperatureGPU),
			uintptr(unsafe.Pointer(&v)),
		)
		if rc := int32(r); rc != nvmlSuccess {
			return 0, fmt.Errorf("nvml: read temp device %d: %s", index, nvmlErrorString(rc))
		}
		return float64(v), nil
	case "util":
		u, err := utilizationRates(dev, index)
		if err != nil {
			return 0, err
		}
		return float64(u.gpu), nil
	case "mem_util":
		u, err := utilizationRates(dev, index)
		if err != nil {
			return 0, err
		}
		return float64(u.memory), nil
	case "power":
		var mw uint32
		r, _, _ := purego.SyscallN(pDeviceGetPowerUsage,
			dev,
			uintptr(unsafe.Pointer(&mw)),
		)
		if rc := int32(r); rc != nvmlSuccess {
			return 0, fmt.Errorf("nvml: read power device %d: %s", index, nvmlErrorString(rc))
		}
		return float64(mw) / 1000.0, nil
	case "clock_gpu":
		return readClock(dev, index, nvmlClockGraphics, "clock_gpu")
	case "clock_mem":
		return readClock(dev, index, nvmlClockMem, "clock_mem")
	case "fan_pct":
		pct, err := fanSpeedPct(dev, index)
		if err != nil {
			return 0, err
		}
		return float64(pct), nil
	default:
		return 0, fmt.Errorf("nvml: unknown metric %q for device %d", metric, index)
	}
}

// ReadFanSpeed returns the current fan speed as a PWM value (0-255).
// NVML reports percentage (0-100); we convert to match the hwmon PWM scale.
// Reads fan 0 on the device.
func ReadFanSpeed(index uint) (uint8, error) {
	if !Available() {
		return 0, ErrNotAvailable
	}
	dev, err := deviceHandle(index)
	if err != nil {
		return 0, err
	}
	pct, err := fanSpeedPct(dev, index)
	if err != nil {
		return 0, err
	}
	return uint8(math.Round(float64(pct) / 100.0 * 255.0)), nil
}

// WriteFanSpeed sets all fans on the GPU to the given PWM value (0-255).
// Converts to NVML percentage (0-100) internally.
func WriteFanSpeed(index uint, pwm uint8) error {
	if !Available() {
		return ErrNotAvailable
	}
	dev, err := deviceHandle(index)
	if err != nil {
		return err
	}
	n, err := numFans(dev)
	if err != nil {
		return fmt.Errorf("nvml: get num fans device %d: %w", index, err)
	}
	pct := int(math.Round(float64(pwm) / 255.0 * 100.0))
	for i := 0; i < n; i++ {
		r, _, _ := purego.SyscallN(pDeviceSetFanSpeed_v2,
			dev,
			uintptr(uint32(i)),
			uintptr(uint32(pct)),
		)
		if rc := int32(r); rc != nvmlSuccess {
			return fmt.Errorf("nvml: set fan %d speed device %d: %s", i, index, nvmlErrorString(rc))
		}
	}
	return nil
}

// ResetFanSpeed restores all fans on the GPU to automatic control.
func ResetFanSpeed(index uint) error {
	if !Available() {
		return ErrNotAvailable
	}
	dev, err := deviceHandle(index)
	if err != nil {
		return err
	}
	n, err := numFans(dev)
	if err != nil {
		return fmt.Errorf("nvml: get num fans device %d: %w", index, err)
	}
	for i := 0; i < n; i++ {
		r, _, _ := purego.SyscallN(pDeviceSetDefaultFanSpeed_v2,
			dev,
			uintptr(uint32(i)),
		)
		if rc := int32(r); rc != nvmlSuccess {
			return fmt.Errorf("nvml: reset fan %d device %d: %s", i, index, nvmlErrorString(rc))
		}
	}
	return nil
}

// ─── internal helpers ────────────────────────────────────────────────────

// loadLibrary dlopens libnvidia-ml.so.1 and resolves every NVML symbol we
// use. Called at most once via loadOnce.
func loadLibrary(logger *slog.Logger) error {
	h, err := purego.Dlopen("libnvidia-ml.so.1", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		logAbsentOnce.Do(func() {
			logger.Info("NVIDIA driver not detected; GPU features disabled",
				"err", err)
		})
		return fmt.Errorf("%w: %v", ErrLibraryUnavailable, err)
	}

	type sym struct {
		name string
		out  *uintptr
	}
	syms := []sym{
		{"nvmlInit_v2", &pInit_v2},
		{"nvmlShutdown", &pShutdown},
		{"nvmlDeviceGetCount_v2", &pDeviceGetCount_v2},
		{"nvmlDeviceGetHandleByIndex_v2", &pDeviceGetHandleByIndex_v2},
		{"nvmlDeviceGetName", &pDeviceGetName},
		{"nvmlDeviceGetTemperature", &pDeviceGetTemperature},
		{"nvmlDeviceGetTemperatureThreshold", &pDeviceGetTemperatureThreshold},
		{"nvmlDeviceGetUtilizationRates", &pDeviceGetUtilizationRates},
		{"nvmlDeviceGetPowerUsage", &pDeviceGetPowerUsage},
		{"nvmlDeviceGetPowerManagementLimit", &pDeviceGetPowerManagementLimit},
		{"nvmlDeviceGetClockInfo", &pDeviceGetClockInfo},
		{"nvmlDeviceGetNumFans", &pDeviceGetNumFans},
		{"nvmlDeviceGetFanSpeed_v2", &pDeviceGetFanSpeed_v2},
		{"nvmlDeviceSetFanSpeed_v2", &pDeviceSetFanSpeed_v2},
		{"nvmlDeviceSetDefaultFanSpeed_v2", &pDeviceSetDefaultFanSpeed_v2},
		{"nvmlErrorString", &pErrorString},
	}
	for _, s := range syms {
		p, err := purego.Dlsym(h, s.name)
		if err != nil {
			logSymbolMissOnce.Do(func() {
				logger.Warn("NVML library loaded but symbol missing; GPU features disabled",
					"symbol", s.name, "err", err)
			})
			return fmt.Errorf("%w: symbol %q: %v", ErrInitFailed, s.name, err)
		}
		*s.out = p
	}
	return nil
}

// deviceHandle fetches an NVML device handle for the given GPU index.
// Callers must have verified Available() first.
func deviceHandle(index uint) (uintptr, error) {
	var dev uintptr
	r, _, _ := purego.SyscallN(pDeviceGetHandleByIndex_v2,
		uintptr(uint32(index)),
		uintptr(unsafe.Pointer(&dev)),
	)
	if rc := int32(r); rc != nvmlSuccess {
		return 0, fmt.Errorf("nvml: get device %d: %s", index, nvmlErrorString(rc))
	}
	return dev, nil
}

// numFans returns the fan count for an NVML device.
func numFans(dev uintptr) (int, error) {
	var n uint32
	r, _, _ := purego.SyscallN(pDeviceGetNumFans,
		dev,
		uintptr(unsafe.Pointer(&n)),
	)
	if rc := int32(r); rc != nvmlSuccess {
		return 0, errors.New(nvmlErrorString(rc))
	}
	return int(n), nil
}

// fanSpeedPct returns the current fan speed percentage (0-100) for fan 0.
func fanSpeedPct(dev uintptr, index uint) (uint32, error) {
	var pct uint32
	r, _, _ := purego.SyscallN(pDeviceGetFanSpeed_v2,
		dev,
		uintptr(uint32(0)),
		uintptr(unsafe.Pointer(&pct)),
	)
	if rc := int32(r); rc != nvmlSuccess {
		return 0, fmt.Errorf("nvml: read fan speed device %d: %s", index, nvmlErrorString(rc))
	}
	return pct, nil
}

// nvmlUtilization mirrors the C struct nvmlUtilization_t { uint32 gpu, uint32 memory }.
type nvmlUtilization struct {
	gpu    uint32
	memory uint32
}

func utilizationRates(dev uintptr, index uint) (nvmlUtilization, error) {
	var u nvmlUtilization
	r, _, _ := purego.SyscallN(pDeviceGetUtilizationRates,
		dev,
		uintptr(unsafe.Pointer(&u)),
	)
	if rc := int32(r); rc != nvmlSuccess {
		return nvmlUtilization{}, fmt.Errorf("nvml: read utilization device %d: %s", index, nvmlErrorString(rc))
	}
	return u, nil
}

func readClock(dev uintptr, index uint, clockType int, label string) (float64, error) {
	var v uint32
	r, _, _ := purego.SyscallN(pDeviceGetClockInfo,
		dev,
		uintptr(clockType),
		uintptr(unsafe.Pointer(&v)),
	)
	if rc := int32(r); rc != nvmlSuccess {
		return 0, fmt.Errorf("nvml: read %s device %d: %s", label, index, nvmlErrorString(rc))
	}
	return float64(v), nil
}

// nvmlErrorString wraps NVML's nvmlErrorString, falling back to a generic
// message if the symbol or the returned pointer is missing.
func nvmlErrorString(code int32) string {
	if pErrorString == 0 {
		return fmt.Sprintf("nvml error %d", code)
	}
	r, _, _ := purego.SyscallN(pErrorString, uintptr(code))
	if r == 0 {
		return fmt.Sprintf("nvml error %d", code)
	}
	return goStringFromC(r)
}

// diagnoseNvmlFailure returns an actionable diagnostic string when nvmlInit_v2
// fails. The error argument is not examined; filesystem state of the control
// device is the authoritative signal.
func diagnoseNvmlFailure(_ error) string {
	return diagnoseNvmlDevice("/dev/nvidiactl")
}

// diagnoseNvmlDevice inspects ctlPath (normally /dev/nvidiactl) and returns a
// human-readable explanation of why NVML cannot be initialised. Accepts an
// explicit path so tests can exercise every branch without real hardware.
func diagnoseNvmlDevice(ctlPath string) string {
	stat, err := os.Stat(ctlPath)
	if err != nil {
		return ctlPath + " not found; NVIDIA driver may not be installed"
	}
	f, err := os.Open(ctlPath)
	if err != nil {
		if os.IsPermission(err) {
			gid := stat.Sys().(*syscall.Stat_t).Gid
			group, _ := user.LookupGroupId(strconv.Itoa(int(gid)))
			gname := "unknown"
			if group != nil {
				gname = group.Name
			}
			return fmt.Sprintf(
				"Permission denied on %s (owner group: %s). Fix: sudo usermod -aG %s ventd && sudo systemctl restart ventd",
				ctlPath, gname, gname,
			)
		}
		return fmt.Sprintf("Cannot open %s: %v", ctlPath, err)
	}
	_ = f.Close() // readonly probe; error is ignorable
	return "Device accessible but NVML still failed — driver in bad state; try `sudo nvidia-smi -pm 1`"
}

// goStringFromC copies a NUL-terminated C string into a Go string without
// relying on CGO. Safe for strings returned by NVML (statically allocated
// in libnvidia-ml.so.1, never GC-moved).
//
// The bit-pattern reinterpretation via *(*unsafe.Pointer)(unsafe.Pointer(&p))
// avoids the go vet unsafeptr warning; a direct unsafe.Pointer(p) conversion
// would trip it. Pointer arithmetic is done with unsafe.Add, not uintptr +.
func goStringFromC(p uintptr) string {
	if p == 0 {
		return ""
	}
	base := *(*unsafe.Pointer)(unsafe.Pointer(&p))
	var n uintptr
	for *(*byte)(unsafe.Add(base, n)) != 0 {
		n++
		if n > 1<<16 { // guard against runaway scans
			break
		}
	}
	return string(unsafe.Slice((*byte)(base), int(n)))
}
