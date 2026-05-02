package detectors

import (
	"context"
	"fmt"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// SignguardSnapshotter is the read-only surface PolarityFlipDetector
// needs from the v0.5.8 signguard subsystem. The production wiring
// passes the daemon's *signguard.Detector; tests pass a stub.
//
// Confirmed returns true when the channel's b_ii sign vote carries a
// 5/7 majority match for the expected cooling-fan polarity. The
// detector treats false as "polarity has flipped or hasn't gathered
// enough data" — see sign-guard rules in `.claude/rules/signguard.md`.
type SignguardSnapshotter interface {
	Confirmed(channelID string) bool
}

// PolarityFlipDetector emits a Warning Fact for every channel whose
// signguard vote came back unconfirmed. signguard runs continuously
// (RULE-SGD-CONT-01) so a re-cabled fan flipping polarity mid-
// deployment is detected on the next vote window — doctor surfaces
// this to the operator instead of letting the controller silently
// run a fan in the wrong direction.
//
// "Unconfirmed" can also mean "not enough samples yet" — the warning
// resolves itself once signguard has a stable vote, at which point
// the detector emits no fact. There's no Blocker variant: a polarity
// flip isn't an immediate hardware risk because the controller's
// `polarity.WritePWM` already inverts based on the persisted polarity
// classification, and the signguard snapshot is advisory.
type PolarityFlipDetector struct {
	// Channels is the list of controllable channel IDs to query.
	// Production wires this from the daemon's enumerated channel set;
	// tests pass a small fixture slice.
	Channels []string

	// Signguard reads the latest vote per channel.
	Signguard SignguardSnapshotter
}

// NewPolarityFlipDetector constructs a detector for the given
// channels. nil signguard is treated as "no data" and the detector
// emits no facts.
func NewPolarityFlipDetector(channels []string, sg SignguardSnapshotter) *PolarityFlipDetector {
	return &PolarityFlipDetector{Channels: channels, Signguard: sg}
}

// Name returns the stable detector ID.
func (d *PolarityFlipDetector) Name() string { return "polarity_flip" }

// Probe queries signguard for every channel and emits one Fact per
// unconfirmed channel. Confirmed channels emit nothing — RULE-DOCTOR-03
// "no silent passes" is honoured at the Runner level by listing the
// detector in the Report's metadata, not by emitting a no-op Fact per
// healthy channel (which would scale linearly with channel count and
// flood the JSON output).
func (d *PolarityFlipDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Signguard == nil {
		return nil, nil
	}

	now := timeNowFromDeps(deps)

	var facts []doctor.Fact
	for _, ch := range d.Channels {
		if d.Signguard.Confirmed(ch) {
			continue
		}
		facts = append(facts, doctor.Fact{
			Detector:   d.Name(),
			Severity:   doctor.SeverityWarning,
			Class:      recovery.ClassUnknown,
			Title:      fmt.Sprintf("Channel %s polarity unconfirmed by signguard", ch),
			Detail:     "v0.5.8 signguard rolling vote (5-of-7) reports this channel's b_ii sign does not match the expected cooling-fan polarity. Most often: not enough opportunistic-probe samples gathered yet (resolves itself within ~1 hour of normal use). Less often: a fan was re-cabled to an inverted-polarity header. Doctor will keep this Warning until the vote stabilises.",
			EntityHash: doctor.HashEntity("polarity_flip", ch),
			Observed:   now,
		})
	}
	return facts, nil
}
