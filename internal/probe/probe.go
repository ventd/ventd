// Package probe implements the catalog-less hardware probe introduced in spec-v0_5_1.
// The probe enumerates thermal sources and controllable fan channels from sysfs,
// detects virtualised and containerised environments, and optionally overlays
// catalog hints when a hardware fingerprint match is found.
//
// All file I/O uses injected fs.FS values so tests run without real /sys access.
// No write syscalls are made during probe (RULE-PROBE-01).
package probe

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// SchemaVersion is the version tag stored in the probe KV namespace.
const SchemaVersion = uint16(1)

// ProbeResult is the full output of a single hardware probe run.
type ProbeResult struct {
	SchemaVersion        uint16
	RuntimeEnvironment   RuntimeEnvironment
	ThermalSources       []ThermalSource
	ControllableChannels []ControllableChannel
	CatalogMatch         *CatalogMatch // nil when no catalog match
	Diagnostics          []Diagnostic
	ProbeTime            time.Time
}

// RuntimeEnvironment describes the execution context detected during probe.
type RuntimeEnvironment struct {
	Virtualised      bool
	VirtType         string // "kvm" | "vmware" | "hyperv" | "qemu" | ""
	Containerised    bool
	ContainerRuntime string   // "docker" | "lxc" | "kubepods" | ""
	DetectedVia      []string // human-readable list of detection signals
}

// ThermalSource is one hwmon device or thermal zone with temperature sensors.
type ThermalSource struct {
	SourceID string // "hwmon0" | "thermal_zone0" | "nvml:0"
	Driver   string
	Sensors  []SensorChannel
}

// SensorChannel is one temp*_input entry within a ThermalSource.
type SensorChannel struct {
	Name        string  // "temp1_input"
	Path        string  // full sysfs path
	Label       string  // from temp*_label; "" when absent
	InitialRead float64 // °C; 0 if ReadOK is false
	ReadOK      bool
}

// ControllableChannel is one fan channel that ventd may write to.
type ControllableChannel struct {
	SourceID       string // "hwmon3"
	PWMPath        string // sysfs pwm file path
	TachPath       string // sysfs fan*_input path; "" for tach-less fans
	Driver         string
	Polarity       string   // always "unknown" in v0.5.1; disambiguated by v0.5.2
	InitialPWM     int      // current PWM value 0-255; 0 if unreadable
	InitialRPM     int      // current RPM; 0 if no tach or unreadable
	CapabilityHint string   // from catalog overlay; "" if no match
	Notes          []string // advisory notes from detection
}

// CatalogMatch summarises catalog overlay application.
type CatalogMatch struct {
	Matched        bool
	Fingerprint    string
	OverlayApplied []string // profile names applied
}

// Diagnostic is a structured event emitted by the probe.
type Diagnostic struct {
	Severity string // "info" | "warning" | "error"
	Code     string // "PROBE-VIRT-DETECTED" etc.
	Message  string
	Context  map[string]string
}

// Outcome is the three-state wizard fork result derived from ProbeResult (§3.2).
type Outcome int

const (
	OutcomeControl     Outcome = iota // ≥1 sensor + ≥1 controllable channel
	OutcomeMonitorOnly                // ≥1 sensor, 0 controllable channels
	OutcomeRefuse                     // virt/container, or 0 sensors
)

// String returns the canonical KV storage string for the outcome.
func (o Outcome) String() string {
	switch o {
	case OutcomeControl:
		return "control_mode"
	case OutcomeMonitorOnly:
		return "monitor_only"
	case OutcomeRefuse:
		return "refused"
	default:
		return "unknown"
	}
}

// Prober enumerates the local hardware environment.
type Prober interface {
	Probe(ctx context.Context) (*ProbeResult, error)
}

// ExecFn is the signature for running external commands. The return value is
// the trimmed stdout output.
type ExecFn func(ctx context.Context, name string, args ...string) (string, error)

// defaultExec runs name with args and returns trimmed stdout.
func defaultExec(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// WriteChecker returns true if the file at the given sysfs path appears writable.
// In production, this opens the file O_WRONLY and immediately closes it — no data
// is written. Tests inject a stub to keep the probe hermetic.
type WriteChecker func(sysPath string) bool

// Config holds injectable dependencies for the probe. All fields are optional;
// zero values fall back to real filesystem and slog.Default().
type Config struct {
	// SysFS is the root of /sys. Defaults to os.DirFS("/sys").
	SysFS fs.FS
	// ProcFS is the root of /proc. Defaults to os.DirFS("/proc").
	ProcFS fs.FS
	// RootFS is the filesystem root "/". Used for /.dockerenv. Defaults to os.DirFS("/").
	RootFS fs.FS
	// ExecFn runs external commands (systemd-detect-virt). Defaults to defaultExec.
	ExecFn ExecFn
	// WriteCheck tests whether a sysfs PWM path is writable. Defaults to defaultWriteCheck.
	WriteCheck WriteChecker
	// Logger. Defaults to slog.Default().
	Logger *slog.Logger
}

// prober is the concrete Prober implementation.
type prober struct {
	cfg Config
}

// New constructs a Prober with injected dependencies.
func New(cfg Config) Prober {
	if cfg.SysFS == nil {
		cfg.SysFS = os.DirFS("/sys")
	}
	if cfg.ProcFS == nil {
		cfg.ProcFS = os.DirFS("/proc")
	}
	if cfg.RootFS == nil {
		cfg.RootFS = os.DirFS("/")
	}
	if cfg.ExecFn == nil {
		cfg.ExecFn = defaultExec
	}
	if cfg.WriteCheck == nil {
		cfg.WriteCheck = defaultWriteCheck
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &prober{cfg: cfg}
}

// Probe runs the full discovery sequence and returns a ProbeResult.
// The probe is entirely read-only: no PWM writes, no IPMI write commands,
// no EC write commands (RULE-PROBE-01).
func (p *prober) Probe(ctx context.Context) (*ProbeResult, error) {
	r := &ProbeResult{
		SchemaVersion: SchemaVersion,
		ProbeTime:     time.Now(),
	}

	// §4.1: Runtime environment detection FIRST.
	// If virtualised or containerised, probe stops and returns immediately.
	renv, envDiags := p.detectEnvironment(ctx)
	r.RuntimeEnvironment = renv
	r.Diagnostics = append(r.Diagnostics, envDiags...)

	if renv.Virtualised || renv.Containerised {
		return r, nil
	}

	// §4.2: Thermal source enumeration.
	thermals, thermalDiags := p.enumerateThermal(ctx)
	r.ThermalSources = thermals
	r.Diagnostics = append(r.Diagnostics, thermalDiags...)

	// §4.3: Controllable channel enumeration.
	channels, chanDiags := p.enumerateChannels(ctx)
	r.ControllableChannels = channels
	r.Diagnostics = append(r.Diagnostics, chanDiags...)

	// §4.4: Catalog overlay (last).
	match, overlayDiags := p.applyOverlay(ctx, r)
	r.CatalogMatch = match
	r.Diagnostics = append(r.Diagnostics, overlayDiags...)

	return r, nil
}
