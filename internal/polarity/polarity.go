// Package polarity implements the v0.5.2 polarity disambiguation probe.
// It resolves each ControllableChannel.Polarity from "unknown" to one of
// "normal", "inverted", or "phantom" across all backend types (hwmon, NVML,
// IPMI, EC). Results are persisted to the spec-16 KV store under the
// "calibration" namespace.
package polarity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ventd/ventd/internal/probe"
)

// Phantom reason constants surfaced in diagnostics and doctor output.
const (
	PhantomReasonNoTach         = "no_tach"
	PhantomReasonNoResponse     = "no_response"
	PhantomReasonFirmwareLocked = "firmware_locked"
	PhantomReasonProfileOnly    = "profile_only"
	PhantomReasonDriverTooOld   = "driver_too_old"
	PhantomReasonWriteFailed    = "write_failed"
)

// Threshold constants (spec §3.1, §3.2).
const (
	ThresholdRPM   = 150 // hwmon/EC: minimum RPM delta for non-phantom classification
	ThresholdPct   = 10  // NVML: minimum percentage-point delta
	HoldDuration   = 3 * time.Second
	RestoreDelay   = 500 * time.Millisecond
	BaselineWindow = time.Second
)

// Sentinel errors.
var (
	ErrChannelNotControllable = errors.New("polarity: channel is phantom; writes not permitted")
	ErrPolarityNotResolved    = errors.New("polarity: polarity not yet resolved (still unknown)")
)

// ChannelResult is the resolved polarity for one channel after probing.
type ChannelResult struct {
	Backend       string   // "hwmon" | "nvml" | "ipmi" | "ec"
	Identity      Identity // backend-specific channel identity
	Polarity      string   // "normal" | "inverted" | "phantom"
	PhantomReason string   // non-empty when Polarity == "phantom"
	Baseline      float64  // baseline reading (RPM for hwmon, pct for NVML, vendor for IPMI)
	Observed      float64  // reading after midpoint write
	Delta         float64  // Observed - Baseline
	Unit          string   // "rpm" | "pct" | "vendor"
	ProbedAt      time.Time
}

// Identity encodes the backend-specific information needed to match a
// persisted ChannelResult to a live ControllableChannel on daemon restart.
type Identity struct {
	// hwmon / EC
	PWMPath  string `json:"pwm_path,omitempty"`
	TachPath string `json:"tach_path,omitempty"`
	// NVML
	PCIAddress string `json:"pci_address,omitempty"`
	FanIndex   int    `json:"fan_index,omitempty"`
	// IPMI
	BMCAddress string `json:"bmc_address,omitempty"`
	Vendor     string `json:"vendor,omitempty"`
	ChannelID  string `json:"channel_id,omitempty"`
}

// ApplyToChannel updates ch.Polarity and ch.PhantomReason from a resolved
// ChannelResult. Used by the daemon-start match path (RULE-POLARITY-08).
func ApplyToChannel(ch *probe.ControllableChannel, r ChannelResult) {
	ch.Polarity = r.Polarity
	ch.PhantomReason = r.PhantomReason
}

// WritePWM is the polarity-aware write helper (spec §3.4 / RULE-POLARITY-05).
// It inverts value for inverted channels and refuses writes to phantom/unknown.
// The actual write is dispatched via fn, which must forward the adjusted byte
// to the backend.
func WritePWM(ch *probe.ControllableChannel, value uint8, fn func(uint8) error) error {
	var actual uint8
	switch ch.Polarity {
	case "normal":
		actual = value
	case "inverted":
		actual = 255 - value
	case "phantom":
		return ErrChannelNotControllable
	case "unknown":
		return ErrPolarityNotResolved
	default:
		return fmt.Errorf("polarity: invalid polarity %q for channel %s", ch.Polarity, ch.PWMPath)
	}
	return fn(actual)
}

// IsControllable reports whether ch has a resolved non-phantom polarity.
func IsControllable(ch *probe.ControllableChannel) bool {
	return ch.Polarity == "normal" || ch.Polarity == "inverted"
}

// Prober probes a single ControllableChannel and returns its resolved polarity.
// Each backend type implements its own Prober.
type Prober interface {
	ProbeChannel(ctx context.Context, ch *probe.ControllableChannel) (ChannelResult, error)
}
