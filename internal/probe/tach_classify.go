// Copyright the ventd authors.
// SPDX-License-Identifier: GPL-3.0-or-later

package probe

import (
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MonitorChannelVisibility is the read-side phantom classification of a
// `fan*_input` channel (#796). Mirrors the write-side `Polarity` enum on
// ControllableChannel: "real" surfaces in the default dashboard view;
// "mirror" + "phantom" are hidden by default behind an
// `?include_phantoms=1` toggle so the operator doesn't see four
// dashboard rows when their case has one physical fan.
type MonitorChannelVisibility string

// Closed set of visibility values.
const (
	// VisibilityReal — the channel has non-zero RPM during the baseline
	// AND (a) responds to writes if it has a paired PWM (existing
	// polarity machinery confirms), or (b) reports a unique RPM not
	// mirrored by another channel on the same chip. Real channels are
	// the default-visible dashboard rows.
	VisibilityReal MonitorChannelVisibility = "real"

	// VisibilityMirror — RPM matches another channel on the same chip
	// within ±MirrorEpsilonRPM over the 5-sample baseline. Common on
	// laptop ECs where the firmware mirrors one physical fan into 2-4
	// virtual zones (CPU/GPU/chassis/spare). The mirrors are hidden by
	// default; the representative real channel of each cluster stays
	// visible so the operator sees one row per physical fan.
	VisibilityMirror MonitorChannelVisibility = "mirror"

	// VisibilityPhantom — RPM is consistently 0 across the baseline AND
	// the channel has no paired PWM (cannot be written, cannot spin).
	// The EC reports the zone but no fan is wired. Hidden by default;
	// the operator can reveal phantoms in Settings if they want to see
	// every register-level zone the chip exposes.
	VisibilityPhantom MonitorChannelVisibility = "phantom"
)

// MonitorChannel is the read-side analog of ControllableChannel — one
// `fan*_input` tach with a classification verdict from the probe-time
// baseline (#796). Distinct from ControllableChannel because a fan
// channel can exist without a PWM surface (the minipc EC case: 4 tach
// zones, 1 PWM) and the dashboard needs the standalone tach rows to
// classify properly even when no daemon-driven control is possible.
type MonitorChannel struct {
	SourceID   string                   `json:"source_id"` // "hwmon3"
	TachPath   string                   `json:"tach_path"` // sysfs fan*_input path
	Driver     string                   `json:"driver"`    // hwmon name (e.g. "nct6687")
	PairedPWM  string                   `json:"paired_pwm,omitempty"`
	Visibility MonitorChannelVisibility `json:"visibility"`
	// Baseline carries the per-sample RPM readings the classifier saw.
	// Surfaced for diagnostic completeness — the operator can verify
	// the classification against the raw observations.
	Baseline []int `json:"baseline,omitempty"`
	// MirrorOf is non-empty when Visibility=="mirror"; the value is
	// the TachPath of the representative real channel this one mirrors.
	// Operators inspecting the artifact see which physical fan their
	// hidden mirror collapses into.
	MirrorOf string `json:"mirror_of,omitempty"`
}

// MirrorEpsilonRPM is the per-sample RPM tolerance for the mirror
// classifier: two channels are mirrors when every sample of one falls
// within ±MirrorEpsilonRPM of the corresponding sample of the other.
// 5 RPM is generous enough to absorb tach-edge counting noise on a
// 1500-RPM fan (≈ 4 ticks/100ms at 2 edges/rev) but tight enough to
// reject two distinct fans that happen to spin at similar speeds.
const MirrorEpsilonRPM = 5

// PhantomBaselineSamples is the default number of samples the
// classifier takes per channel. 5 samples × 100ms spacing = 500ms
// baseline window — well within the wizard's probe-phase budget and
// long enough that a fan idling at the noise floor produces a stable
// reading.
const PhantomBaselineSamples = 5

// PhantomBaselineInterval is the per-sample sleep between baseline reads.
const PhantomBaselineInterval = 100 * time.Millisecond

// TachReader reads an `fan*_input` file and returns its current RPM.
// Injected so tests can provide a deterministic sample stream without
// depending on real sysfs.
type TachReader func(path string) (int, error)

// EnumerateMonitorChannels walks every `fan*_input` file under the
// hwmon root (paired with a PWM or not) and classifies each as real /
// mirror / phantom per the 5-sample baseline algorithm (#796).
//
// `sysFS` is the filesystem the classifier walks; `hwmonRoot` is the
// fs.FS-relative directory containing the hwmonN subtree. The
// production wiring is sysFS=os.DirFS("/sys"), hwmonRoot="class/hwmon";
// the orchestrator integration uses sysFS=os.DirFS("/"),
// hwmonRoot=strings.TrimPrefix(absHwmon, "/") so tests staged in a
// t.TempDir() see TachPath values that point at the staged fixture
// rather than the live host's /sys.
//
// `sysAbsRoot` is prepended to relative chipDir+name to construct
// each MonitorChannel.TachPath. Pass "/sys" in production (matches
// sysFS rooted at /sys), "/" when sysFS is rooted at the filesystem
// root, or any absolute prefix the caller wants the TachReader to
// open. Empty falls back to "/sys" for backwards compatibility with
// the v1.0.0 single call signature.
//
// Synchronous and bounded: the function returns after
// PhantomBaselineSamples × PhantomBaselineInterval (default 500ms)
// plus the cost of N file reads. Safe to call from the wizard's
// probe phase.
//
// The pairedPWMs map carries known PWM↔tach pairings from the
// existing ControllableChannel enumeration so the classifier can
// surface PairedPWM on the MonitorChannel. The classifier itself
// does not perform PWM writes — that's the polarity probe's job.
func EnumerateMonitorChannels(sysFS fs.FS, hwmonRoot, sysAbsRoot string, read TachReader, pairedPWMs map[string]string) []MonitorChannel {
	if read == nil {
		return nil
	}
	if sysAbsRoot == "" {
		sysAbsRoot = "/sys"
	}
	tachs := discoverTachFiles(sysFS, hwmonRoot, sysAbsRoot)
	if len(tachs) == 0 {
		return nil
	}

	// Take PhantomBaselineSamples samples of every tach. Sorting
	// tachs first keeps the iteration order deterministic across
	// runs so test fixtures see stable output.
	sort.Slice(tachs, func(i, j int) bool { return tachs[i].TachPath < tachs[j].TachPath })
	samples := make(map[string][]int, len(tachs))
	for s := 0; s < PhantomBaselineSamples; s++ {
		if s > 0 {
			time.Sleep(PhantomBaselineInterval)
		}
		for _, t := range tachs {
			v, err := read(t.TachPath)
			if err != nil {
				samples[t.TachPath] = append(samples[t.TachPath], 0)
				continue
			}
			samples[t.TachPath] = append(samples[t.TachPath], v)
		}
	}

	out := make([]MonitorChannel, 0, len(tachs))
	classifications := make([]MonitorChannel, len(tachs))
	for i, t := range tachs {
		ch := MonitorChannel{
			SourceID:  t.SourceID,
			TachPath:  t.TachPath,
			Driver:    t.Driver,
			PairedPWM: pairedPWMs[t.TachPath],
			Baseline:  samples[t.TachPath],
		}
		ch.Visibility = classifyBaseline(samples[t.TachPath], ch.PairedPWM)
		classifications[i] = ch
	}

	// Second pass: detect mirrors within the same chip group. A mirror
	// is two channels whose baseline samples track each other within
	// MirrorEpsilonRPM at every sample. The lower-indexed channel wins
	// the "real" verdict; later matches collapse to "mirror" pointing
	// at the winner.
	for i := range classifications {
		if classifications[i].Visibility != VisibilityReal {
			continue
		}
		for j := i + 1; j < len(classifications); j++ {
			if classifications[j].Visibility != VisibilityReal {
				continue
			}
			if classifications[i].SourceID != classifications[j].SourceID {
				continue
			}
			if samplesAreMirror(classifications[i].Baseline, classifications[j].Baseline) {
				classifications[j].Visibility = VisibilityMirror
				classifications[j].MirrorOf = classifications[i].TachPath
			}
		}
	}

	out = append(out, classifications...)
	return out
}

// classifyBaseline produces the per-channel visibility verdict from a
// baseline-sample slice and the paired-PWM status. A channel with all-
// zero samples AND no paired PWM is phantom (EC reports a zone with no
// fan wired). Any non-zero sample → real. PWM-paired channels with all-
// zero baseline stay real on the first-pass classification (the
// polarity probe will downgrade them to phantom later if writes don't
// produce RPM).
func classifyBaseline(samples []int, pairedPWM string) MonitorChannelVisibility {
	if len(samples) == 0 {
		return VisibilityPhantom
	}
	anyNonZero := false
	for _, v := range samples {
		if v != 0 {
			anyNonZero = true
			break
		}
	}
	if anyNonZero {
		return VisibilityReal
	}
	if pairedPWM == "" {
		// All-zero AND no PWM to drive → no signal in either direction.
		return VisibilityPhantom
	}
	// All-zero but the channel has a PWM. Stay real for now; the
	// polarity probe + calibrate sweep will reclassify if writes
	// don't move RPM.
	return VisibilityReal
}

// samplesAreMirror reports whether two baseline slices track each
// other within MirrorEpsilonRPM at every sample. Length mismatch
// returns false (defensive — shouldn't happen since both slices come
// from the same loop).
func samplesAreMirror(a, b []int) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	for i := range a {
		diff := a[i] - b[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > MirrorEpsilonRPM {
			return false
		}
	}
	return true
}

// tachCandidate is the minimal information the classifier needs about
// a tach file before sampling it.
type tachCandidate struct {
	SourceID string
	TachPath string
	Driver   string
}

// discoverTachFiles walks every hwmonN directory under hwmonRoot
// (relative to sysFS) and collects each `fanN_input` file as a
// candidate. Errors during walk are swallowed silently — the
// classifier degrades gracefully on a host with partial sysfs (e.g.
// unprivileged read on /sys with a container's restricted view).
// Honours the same `fs.FS` injection as the rest of the probe
// package so tests can use an fstest.MapFS fixture.
//
// sysAbsRoot is prepended to the fs.FS-relative chip dir + filename
// to produce TachPath. Pass "/sys" when sysFS is rooted at /sys
// (production) or "/" when sysFS is rooted at the filesystem root
// (orchestrator integration). Either way callers receive TachPath
// values they can hand straight to a sysfs reader.
func discoverTachFiles(sysFS fs.FS, hwmonRoot, sysAbsRoot string) []tachCandidate {
	if sysFS == nil {
		return nil
	}
	entries, err := fs.ReadDir(sysFS, hwmonRoot)
	if err != nil {
		return nil
	}
	prefix := sysAbsRoot
	if prefix == "" {
		prefix = "/sys"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	out := make([]tachCandidate, 0)
	for _, e := range entries {
		if !e.IsDir() && (e.Type()&fs.ModeSymlink) == 0 {
			continue
		}
		hwmonName := e.Name()
		chipDir := path.Join(hwmonRoot, hwmonName)
		driver, _ := readTrimmed(sysFS, path.Join(chipDir, "name"))
		files, err := fs.ReadDir(sysFS, chipDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f.Name()
			if !isFanInputFile(name) {
				continue
			}
			out = append(out, tachCandidate{
				SourceID: hwmonName,
				TachPath: prefix + chipDir + "/" + name,
				Driver:   driver,
			})
		}
	}
	return out
}

// isFanInputFile reports whether name matches the `fanN_input` shape.
// Mirrors the `pwmN` validator in channels.go: digits-only suffix, no
// other underscores.
func isFanInputFile(name string) bool {
	if !strings.HasPrefix(name, "fan") {
		return false
	}
	rest := strings.TrimPrefix(name, "fan")
	if !strings.HasSuffix(rest, "_input") {
		return false
	}
	idx := strings.TrimSuffix(rest, "_input")
	if idx == "" {
		return false
	}
	if _, err := strconv.Atoi(idx); err != nil {
		return false
	}
	return true
}
