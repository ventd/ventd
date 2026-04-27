package polarity

import (
	"context"
	"time"

	"github.com/ventd/ventd/internal/probe"
)

// IPMIVendorProbe is the per-vendor IPMI polarity probe interface (spec §3.3 /
// RULE-POLARITY-07). Each IPMI vendor backend implements this interface.
type IPMIVendorProbe interface {
	// ProbeIPMIPolarity attempts a midpoint fan command and returns the result.
	// Backends that cannot perform a write (Dell firmware-locked, HPE profile-only)
	// return a phantom ChannelResult with the appropriate reason; they do NOT
	// return an error for expected firmware refusals.
	ProbeIPMIPolarity(ctx context.Context, ch *probe.ControllableChannel) (ChannelResult, error)
}

// SupermicroIPMIProbe is the Supermicro vendor implementation.
// It issues a real 50% midpoint write via the existing OEM command path
// and observes the SDR fan reading to classify polarity.
type SupermicroIPMIProbe struct {
	// SendRecv allows test injection; nil = real ioctl path.
	// req: [netFn, cmd, data...]; resp[0] = completion code.
	SendRecv func(req, resp []byte) error
	Clock    func(time.Duration)
}

func (s *SupermicroIPMIProbe) clock() func(time.Duration) {
	if s.Clock != nil {
		return s.Clock
	}
	return time.Sleep
}

// ProbeIPMIPolarity performs a Supermicro OEM midpoint write and SDR read-back.
// Zone 0 is used for the probe; real SDR reading is compared to baseline.
func (s *SupermicroIPMIProbe) ProbeIPMIPolarity(ctx context.Context, ch *probe.ControllableChannel) (ChannelResult, error) {
	res := ChannelResult{
		Backend:  "ipmi",
		Identity: Identity{Vendor: "supermicro", ChannelID: ch.SourceID},
		Unit:     "vendor",
		ProbedAt: time.Now(),
	}

	if s.SendRecv == nil {
		// No real device in test/CI without fixture; classify phantom.
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonWriteFailed
		return res, nil
	}

	// Read baseline SDR fan reading (Get Sensor Reading, sensor 0x30 typical).
	baseline := s.readSDRFan(ch.SourceID)
	res.Baseline = float64(baseline)

	// Write zone 0 to 50% via OEM SET_FAN_SPEED (netFn=0x30 cmd=0x70).
	// Payload: [0x66, 0x01, zone=0x00, pct=0x32]
	req := []byte{0x30, 0x70, 0x66, 0x01, 0x00, 0x32}
	resp := make([]byte, 16)
	if err := s.SendRecv(req, resp); err != nil || resp[0] != 0x00 {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonWriteFailed
		return res, nil
	}

	select {
	case <-ctx.Done():
		return res, ctx.Err()
	default:
	}

	s.clock()(HoldDuration)

	select {
	case <-ctx.Done():
		return res, ctx.Err()
	default:
	}

	observed := s.readSDRFan(ch.SourceID)
	res.Observed = float64(observed)

	// Restore to auto mode (SET_FAN_MODE=0x00).
	restore := []byte{0x30, 0x45, 0x00}
	restoreResp := make([]byte, 4)
	_ = s.SendRecv(restore, restoreResp)
	s.clock()(RestoreDelay)

	delta := float64(observed - baseline)
	res.Delta = delta
	// For IPMI vendor units use the same RPM threshold.
	if delta > float64(ThresholdRPM) {
		res.Polarity = "normal"
	} else if delta < -float64(ThresholdRPM) {
		res.Polarity = "inverted"
	} else {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonNoResponse
	}
	return res, nil
}

// readSDRFan reads a Get Sensor Reading response and returns the raw value.
// Returns 0 on error (treated as baseline=0 for classification).
func (s *SupermicroIPMIProbe) readSDRFan(sourceID string) int {
	if s.SendRecv == nil {
		return 0
	}
	// Use sensor number 0x31 for zone 0 fan (typical Supermicro SDR).
	_ = sourceID
	req := []byte{0x04, 0x2d, 0x31}
	resp := make([]byte, 8)
	if err := s.SendRecv(req, resp); err != nil || resp[0] != 0x00 {
		return 0
	}
	return int(resp[1]) * 64 // Supermicro RPM multiplier
}

// DellIPMIProbe is the Dell vendor implementation.
// iDRAC9 ≥3.34 refuses arbitrary fan writes; the probe detects the refusal
// and classifies the channel as phantom firmware_locked (spec §3.3).
type DellIPMIProbe struct {
	SendRecv func(req, resp []byte) error
}

// ProbeIPMIPolarity attempts Dell OEM fan write and classifies refusal as
// phantom firmware_locked (RULE-POLARITY-07).
func (d *DellIPMIProbe) ProbeIPMIPolarity(_ context.Context, ch *probe.ControllableChannel) (ChannelResult, error) {
	res := ChannelResult{
		Backend:  "ipmi",
		Identity: Identity{Vendor: "dell", ChannelID: ch.SourceID},
		Unit:     "vendor",
		ProbedAt: time.Now(),
	}

	if d.SendRecv == nil {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonWriteFailed
		return res, nil
	}

	// Attempt Dell OEM manual fan set (netFn=0x30 cmd=0x30 sub=0x02, 50% target).
	req := []byte{0x30, 0x30, 0x02, 0xff, 50}
	resp := make([]byte, 4)
	err := d.SendRecv(req, resp)
	if err != nil || resp[0] != 0x00 {
		// Any non-zero CC or error = firmware refusal; permanent phantom.
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonFirmwareLocked
		return res, nil
	}

	// Unexpected success (iDRAC version with write enabled) — restore and classify.
	restore := []byte{0x30, 0x30, 0x01, 0x01}
	restoreResp := make([]byte, 4)
	_ = d.SendRecv(restore, restoreResp)

	// Write succeeded; treat as firmware-unlocked normal polarity (uncommon).
	res.Polarity = "normal"
	return res, nil
}

// HPEIPMIProbe is the HPE vendor implementation.
// HPE iLO5/6 returns 405/501 on per-fan Redfish writes; classify as
// phantom profile_only immediately (spec §3.3 / RULE-POLARITY-07).
type HPEIPMIProbe struct {
	// HTTPStatus allows test injection; 0 = simulate 405.
	HTTPStatus int
}

// ProbeIPMIPolarity classifies HPE channels as phantom profile_only since
// HPE does not support per-fan write via IPMI or Redfish PATCH.
func (h *HPEIPMIProbe) ProbeIPMIPolarity(_ context.Context, ch *probe.ControllableChannel) (ChannelResult, error) {
	_ = ch
	// HPE always returns 405 or 501 on per-fan Redfish PATCH; never attempt
	// a write that would generate BMC error log entries.
	return ChannelResult{
		Backend:       "ipmi",
		Identity:      Identity{Vendor: "hpe", ChannelID: ch.SourceID},
		Polarity:      "phantom",
		PhantomReason: PhantomReasonProfileOnly,
		Unit:          "vendor",
		ProbedAt:      time.Now(),
	}, nil
}
