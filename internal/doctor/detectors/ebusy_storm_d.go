package detectors

import (
	"context"
	"fmt"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// ebusyStormWarnEvents is the per-channel EBUSY count, within the backend's
// rolling window, at which a one-off re-acquire becomes a "storm" worth an
// operator card. Mirrors hwmon.EBUSYWarnThreshold; kept as a detector-local
// constant so the detectors package stays free of an import on
// internal/hal/hwmon (the source func carries plain values).
const ebusyStormWarnEvents = 5

// EBUSYStorm is one channel's active EBUSY-rolling-window snapshot, already
// filtered to "currently storming" by the source (stale windows are dropped
// there, where the clock lives).
type EBUSYStorm struct {
	// ChannelPath is the pwm sysfs path the BIOS is contesting.
	ChannelPath string
	// EventCount is the EBUSY events seen in the current window.
	EventCount int
	// WindowSeconds is the rolling-window span the count is measured over.
	WindowSeconds int
}

// EBUSYStormStatusFn returns the channels currently in an EBUSY rolling window.
// Production wires it to a shared collector the controllers' hwmon backends push
// into via SetEBUSYObserver; tests inject a stub. A function seam keeps the
// detectors package decoupled from internal/hal/hwmon and the daemon wiring.
type EBUSYStormStatusFn func() []EBUSYStorm

// EBUSYStormDetector surfaces RULE-HWMON-EBUSY-RATE-OBSERVABILITY to the
// operator: when a BIOS fan-control feature (Gigabyte Q-Fan, ASUS/MSI Smart
// Fan, …) periodically reasserts pwm_enable, every ventd duty write returns
// EBUSY and the backend has to re-acquire manual mode (RULE-HWMON-MODE-
// REACQUIRE). The daemon self-heals each event, so nothing fails outright —
// which is exactly why it needs surfacing: the symptom is fans that drift back
// to the BIOS curve, with the cause buried in the logs. A channel storming past
// ebusyStormWarnEvents in one window gets a Warning card telling the operator
// to disable the BIOS feature.
type EBUSYStormDetector struct {
	status EBUSYStormStatusFn
}

// NewEBUSYStormDetector constructs the detector. A nil status fn is a no-op
// (zero facts) — e.g. monitor-only hosts with no hwmon backend.
func NewEBUSYStormDetector(fn EBUSYStormStatusFn) *EBUSYStormDetector {
	return &EBUSYStormDetector{status: fn}
}

// Name returns the stable detector ID.
func (d *EBUSYStormDetector) Name() string { return "ebusy_storm" }

// Probe reads the active-storm snapshot and emits one Warning Fact per channel
// storming past the threshold. Pure read through the seam; never touches sysfs.
// One-off EBUSY re-acquires (count below the threshold) are benign self-heals
// and stay silent.
func (d *EBUSYStormDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.status == nil {
		return nil, nil
	}
	now := time.Now
	if deps.Now != nil {
		now = deps.Now
	}
	var facts []doctor.Fact
	for _, s := range d.status() {
		if s.EventCount < ebusyStormWarnEvents {
			continue
		}
		facts = append(facts, doctor.Fact{
			Detector:   d.Name(),
			Severity:   doctor.SeverityWarning,
			Class:      recovery.ClassUnknown,
			Title:      "A BIOS fan-control feature is fighting ventd",
			Detail:     ebusyStormBody(s),
			EntityHash: doctor.HashEntity("ebusy_storm", s.ChannelPath),
			Observed:   now(),
		})
	}
	return facts, nil
}

// ebusyStormBody renders the operator-facing explanation + remediation for one
// storming channel.
func ebusyStormBody(s EBUSYStorm) string {
	return fmt.Sprintf(
		"The fan on %s returned EBUSY %d times in the last %ds: the motherboard firmware keeps "+
			"taking the channel back into automatic mode, so every ventd duty write is rejected and "+
			"ventd has to re-acquire manual control. ventd recovers each time, but the fan briefly "+
			"follows the BIOS curve instead of yours. Disable the BIOS fan-control feature "+
			"(Gigabyte \"Smart Fan\"/Q-Fan, ASUS/MSI \"Smart Fan\", etc.) for this header in the UEFI "+
			"setup, then reboot — ventd will then own the fan uncontested.",
		s.ChannelPath, s.EventCount, s.WindowSeconds)
}
