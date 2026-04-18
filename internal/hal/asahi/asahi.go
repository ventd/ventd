// Package asahi is the Apple Silicon (Asahi Linux) fan backend.
//
// It wraps the macsmc_hwmon kernel driver that exposes Apple Silicon fans as
// standard hwmon chips under /sys/class/hwmon.  Detection is done at
// Enumerate time by reading /proc/device-tree/compatible; if no Apple Silicon
// SoC prefix is found the backend returns an empty slice and is effectively
// invisible to the registry.
//
// Role classification uses the per-channel fan label exposed by macsmc_hwmon
// (fan1_label, fan2_label, …).  Unknown or absent labels fall back to
// RoleCase rather than panicking.
//
// Read / Write / Restore operations are delegated to the hwmon backend —
// macsmc_hwmon implements the standard hwmon sysfs interface so no special
// dispatch is needed.
package asahi

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ventd/ventd/internal/hal"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
	"github.com/ventd/ventd/internal/hwmon"
)

// BackendName is the registry tag applied to channels produced by this backend.
const BackendName = "asahi"

// macsmcDriverName is the hwmon chip name reported by the macsmc_hwmon driver.
const macsmcDriverName = "macsmc_hwmon"

// dtPrefixes are the Apple Silicon SoC compatible-string prefixes we recognise
// in /proc/device-tree/compatible.  The file is NUL-separated so we match on
// each NUL-delimited entry, not the whole blob.
//
// Broad T-class prefixes are intentional: apple,t8xxx covers M1 (t8103) and
// M2 (t8112) monolithic SoCs; apple,t6xxx covers M1/M2 Pro/Max/Ultra die
// combinations (t6000–t6002, t6020–t6022).  The apple,mN entries catch any
// board-level compatible strings that may be added by future Asahi ports.
var dtPrefixes = []string{
	"apple,t8", // M1 (t8103), M2 (t8112), and future T8xxx SoCs
	"apple,t6", // M1 Pro/Max/Ultra (t6000-t6002, t6020), M2 Pro/Max/Ultra
	"apple,m1", // forward-compat board compat strings
	"apple,m2",
	"apple,m3",
}

// Backend is the Asahi Linux implementation of hal.FanBackend.
type Backend struct {
	logger  *slog.Logger
	hwmon   *halhwmon.Backend
	dtPath  string // path to DT compatible file; overrideable for tests
	sysRoot string // hwmon sysfs root; overrideable for tests
}

// NewBackend constructs a Backend that logs through logger.  A nil logger
// falls back to slog.Default().
func NewBackend(logger *slog.Logger) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{
		logger:  logger,
		hwmon:   halhwmon.NewBackend(logger),
		dtPath:  "/proc/device-tree/compatible",
		sysRoot: hwmon.DefaultHwmonRoot,
	}
}

// withOverrides returns a copy of b with the DT path and hwmon root replaced.
// Exposed to tests via export_test.go as NewBackendForTest.
func (b *Backend) withOverrides(dtPath, sysRoot string) *Backend {
	return &Backend{
		logger:  b.logger,
		hwmon:   b.hwmon,
		dtPath:  dtPath,
		sysRoot: sysRoot,
	}
}

// Name returns the registry tag.
func (b *Backend) Name() string { return BackendName }

// Close is a no-op — hwmon holds no process-level resources.
func (b *Backend) Close() error { return b.hwmon.Close() }

// Enumerate walks /sys/class/hwmon, selects chips named "macsmc_hwmon", and
// returns one Channel per pwm* channel found.  Returns an empty slice (no
// error) when:
//   - the machine is not Apple Silicon (DT compatible absent or non-Apple),
//   - the macsmc_hwmon driver is not loaded.
func (b *Backend) Enumerate(ctx context.Context) ([]hal.Channel, error) {
	if !b.isAppleSilicon() {
		return nil, nil
	}

	devices := hwmon.EnumerateDevices(b.sysRoot)
	var out []hal.Channel
	for _, dev := range devices {
		if dev.ChipName != macsmcDriverName {
			continue
		}
		for _, ch := range dev.PWM {
			label := readLabel(ch.Path, ch.Index)
			role := classifyRole(label)

			caps := hal.CapRead | hal.CapWritePWM | hal.CapRestore
			if ch.EnablePath == "" {
				// Driver does not expose pwm_enable — write unsupported on
				// this machine / driver version.
				caps = hal.CapRead
			}

			out = append(out, hal.Channel{
				ID:   ch.Path,
				Role: role,
				Caps: caps,
				Opaque: halhwmon.State{
					PWMPath:    ch.Path,
					OrigEnable: -1,
				},
			})
		}
	}
	return out, nil
}

// Read delegates to the hwmon backend — macsmc_hwmon uses standard sysfs.
func (b *Backend) Read(ch hal.Channel) (hal.Reading, error) {
	return b.hwmon.Read(ch)
}

// Write delegates to the hwmon backend.
func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	return b.hwmon.Write(ch, pwm)
}

// Restore delegates to the hwmon backend.
func (b *Backend) Restore(ch hal.Channel) error {
	return b.hwmon.Restore(ch)
}

// isAppleSilicon reads /proc/device-tree/compatible (or the test override)
// and returns true if any NUL-delimited entry matches a known Apple Silicon
// SoC prefix.  Returns false on any read error or absent file.
func (b *Backend) isAppleSilicon() bool {
	data, err := os.ReadFile(b.dtPath)
	if err != nil {
		return false
	}
	// DT compatible is NUL-separated, NUL-terminated.
	for _, entry := range bytes.Split(data, []byte{0}) {
		s := string(entry)
		for _, prefix := range dtPrefixes {
			if strings.HasPrefix(s, prefix) {
				return true
			}
		}
	}
	return false
}

// readLabel reads the fan label for a given PWM channel.  It tries
// fan${N}_label first (the standard macsmc_hwmon placement), then
// pwm${N}_label as a fallback.  Returns an empty string when neither exists.
func readLabel(pwmPath, index string) string {
	dir := filepath.Dir(pwmPath)
	for _, name := range []string{
		"fan" + index + "_label",
		"pwm" + index + "_label",
	} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return strings.TrimSpace(string(raw))
		}
	}
	return ""
}

// classifyRole maps a fan label to a ChannelRole.  Matching is
// case-insensitive on substrings so partial labels ("CPU Fan", "left pump")
// are handled correctly.  Non-empty labels that don't match a specific role
// fall back to RoleCase (generic case fan).  Empty/absent labels yield
// RoleUnknown.
func classifyRole(label string) hal.ChannelRole {
	lower := strings.ToLower(label)
	switch {
	case strings.Contains(lower, "pump"):
		return hal.RolePump
	case strings.Contains(lower, "gpu"):
		return hal.RoleGPU
	case strings.Contains(lower, "cpu"):
		return hal.RoleCPU
	case label != "":
		return hal.RoleCase
	default:
		return hal.RoleUnknown
	}
}
