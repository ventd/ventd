// Package opportunistic implements v0.5.5's Layer A gap-fill probing.
// The package is consumed by cmd/ventd/main.go and is purposefully
// narrow: a Detector identifies per-channel PWM bins not visited in
// the last 7 days; a Scheduler arbitrates when to fire; a Prober
// fires one 30-second probe at a time; an install_marker tracks the
// 24-hour first-probe delay.
//
// The probe writes go through internal/polarity.WritePWM, the abort
// thresholds come from internal/envelope/thresholds.LookupThresholds,
// and probe events are logged via internal/observation with the new
// EventFlag_OPPORTUNISTIC_PROBE bit.
package opportunistic

import (
	"sort"
	"time"

	"github.com/ventd/ventd/internal/observation"
	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/probe"
)

// CooldownWindow is the per-PWM-bin lockout. A bin with a record in
// the observation log within this window is NOT eligible for re-probe
// (RULE-OPP-PROBE-06). Matches R10 shard TTL and R12 conf_A τ_recency.
const CooldownWindow = 7 * 24 * time.Hour

// LowHalfStep is the probe-grid spacing in raw PWM units below the
// midpoint (PWM 0..96 inclusive). Stall-PWM and min-spin live here.
const LowHalfStep uint8 = 8

// HighHalfStep is the probe-grid spacing in raw PWM units at and above
// PWM 97. Saturation behaviour lives here but per-bin learning value
// is lower than the low end.
const HighHalfStep uint8 = 16

// LowHighBoundary separates the two grid halves. PWM <= 96 uses
// LowHalfStep; PWM >= 97 uses HighHalfStep.
const LowHighBoundary uint8 = 96

// ChannelKnowns is per-channel calibration anchors that should always
// be probed when in a gap, regardless of grid position. Source is
// internal/envelope persisted state from v0.5.3.
type ChannelKnowns struct {
	StallPWM   uint8
	MinSpinPWM uint8
}

// Detector computes per-channel PWM bin coverage gaps over the
// observation log's retention window.
type Detector struct {
	reader *observation.Reader
	// channels are the controllable channels the detector cares about.
	// Phantom and unknown-polarity channels are filtered out at
	// construction time per polarity.IsControllable.
	channels []*probe.ControllableChannel
	// knowns is the optional per-channel-id map of stall/min-spin
	// anchors. Empty map is allowed.
	knowns map[uint16]ChannelKnowns
}

// NewDetector wraps an observation.Reader and a channel list.
// Non-controllable channels are filtered out per RULE-OPP-PROBE-06.
func NewDetector(reader *observation.Reader, channels []*probe.ControllableChannel, knowns map[uint16]ChannelKnowns) *Detector {
	filtered := make([]*probe.ControllableChannel, 0, len(channels))
	for _, ch := range channels {
		if !polarity.IsControllable(ch) {
			continue
		}
		filtered = append(filtered, ch)
	}
	if knowns == nil {
		knowns = map[uint16]ChannelKnowns{}
	}
	return &Detector{
		reader:   reader,
		channels: filtered,
		knowns:   knowns,
	}
}

// MaxAbortsPerBin caps how many aborted opportunistic probes for the
// same (channel, PWM) bin in the CooldownWindow are tolerated before
// the bin is treated as visited. Past the cap, Gaps drops the bin
// from the returned set so the scheduler advances to the next gap
// instead of retrying the same bin forever.
//
// RULE-OPP-PROBE-06 retry-on-abort exists for transient workload
// spikes (a one-off background job briefly trips the slope abort);
// it never anticipated STRUCTURAL aborts (probing PWM=0 reliably
// trips the slope abort on any thermally-loaded host because the
// fan actually stops cooling). Cap = 3 matches
// envelope.DefaultSlopeAbortConsecutive — after three honest
// attempts the bin is empirically not safe to probe under current
// conditions, and pinning the scheduler on it blocks all other
// learning. The bin re-enters the candidate set on the next
// CooldownWindow (7 d) rollover when the record set falls out of
// the window.
const MaxAbortsPerBin = 3

// Gaps returns the set of PWM bins per channel that have not been
// observed in the cool-down window before now. Aborted opportunistic
// probes do NOT count as visits up to MaxAbortsPerBin per bin
// (RULE-OPP-PROBE-06's retry-on-abort allowance for transient
// workload spikes); past the cap, the bin is treated as visited so
// the scheduler doesn't loop on a structurally-unsafe bin.
//
// The returned map is keyed by observation.ChannelID(pwmPath) so it
// matches the ChannelID written into observation records.
func (d *Detector) Gaps(now time.Time) (map[uint16][]uint8, error) {
	visited := make(map[uint16]map[uint8]bool, len(d.channels))
	abortCount := make(map[uint16]map[uint8]int, len(d.channels))
	for _, ch := range d.channels {
		id := observation.ChannelID(ch.PWMPath)
		visited[id] = make(map[uint8]bool)
		abortCount[id] = make(map[uint8]int)
	}

	since := now.Add(-CooldownWindow)
	sinceMicros := since.UnixMicro()
	err := d.reader.Stream(since, func(rec *observation.Record) bool {
		// File-mtime filter cuts most stale data, but a single
		// active file may contain records spanning hours either
		// side of `since`. Apply the per-record timestamp filter
		// here so the cool-down window is exact.
		if rec.Ts < sinceMicros {
			return true
		}
		// Aborted opportunistic probes count toward the per-bin
		// abort cap but do NOT mark the bin visited up to the cap.
		// Past the cap, the bin is dropped from the candidate set
		// below — see MaxAbortsPerBin.
		if rec.EventFlags&observation.EventFlag_OPPORTUNISTIC_PROBE != 0 &&
			rec.EventFlags&observation.EventFlag_ENVELOPE_C_ABORT != 0 {
			if counts, ok := abortCount[rec.ChannelID]; ok {
				counts[rec.PWMWritten]++
			}
			return true
		}
		bins, ok := visited[rec.ChannelID]
		if !ok {
			return true
		}
		bins[rec.PWMWritten] = true
		return true
	})
	if err != nil {
		return nil, err
	}

	out := make(map[uint16][]uint8, len(d.channels))
	for _, ch := range d.channels {
		id := observation.ChannelID(ch.PWMPath)
		grid := buildProbeGrid(d.knowns[id])
		var gaps []uint8
		for _, pwm := range grid {
			if visited[id][pwm] {
				continue
			}
			if abortCount[id][pwm] >= MaxAbortsPerBin {
				continue
			}
			gaps = append(gaps, pwm)
		}
		if len(gaps) > 0 {
			out[id] = gaps
		}
	}
	return out, nil
}

// buildProbeGrid returns the union of:
//   - Low half: PWM 0..96 inclusive every LowHalfStep raw units.
//   - High half: PWM 97..255 inclusive every HighHalfStep raw units.
//   - Stall-PWM and min-spin from the channel's calibration record
//     when non-zero (RULE-OPP-PROBE-12).
//
// Grid is sorted ascending; stall/min-spin are merged in place.
func buildProbeGrid(k ChannelKnowns) []uint8 {
	seen := make(map[uint8]bool)
	var out []uint8
	for pwm := uint16(0); pwm <= uint16(LowHighBoundary); pwm += uint16(LowHalfStep) {
		v := uint8(pwm)
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for pwm := uint16(LowHighBoundary) + 1; pwm <= 255; pwm += uint16(HighHalfStep) {
		v := uint8(pwm)
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if k.StallPWM > 0 && !seen[k.StallPWM] {
		seen[k.StallPWM] = true
		out = append(out, k.StallPWM)
	}
	if k.MinSpinPWM > 0 && !seen[k.MinSpinPWM] {
		seen[k.MinSpinPWM] = true
		out = append(out, k.MinSpinPWM)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ProbeGrid returns the canonical PWM probe grid for inspection by
// scheduler logic and tests.
func ProbeGrid(k ChannelKnowns) []uint8 {
	return buildProbeGrid(k)
}
