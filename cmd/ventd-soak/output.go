// Output formatting for ventd-soak. Two formats:
//
//   - human: a per-channel block with the convergence-gate verdict spelled
//     out, colourless ASCII tables suitable for a terminal or copy-paste
//     into a soak-results .md file.
//   - NDJSON (--json): one ConvergenceVerdict per line, suitable for
//     piping through `jq` or appending to a per-host trace file across
//     a long watch run.
//
// The choice is per-invocation; output is written to the supplied io.Writer
// so tests can capture against bytes.Buffer.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// emitVerdicts writes one verdict per channel to w. Channels are sorted
// by ChannelID so successive watch ticks diff cleanly.
func emitVerdicts(verdicts []ConvergenceVerdict, asJSON bool, w io.Writer) {
	sorted := make([]ConvergenceVerdict, len(verdicts))
	copy(sorted, verdicts)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ChannelID < sorted[j].ChannelID })

	if asJSON {
		enc := json.NewEncoder(w)
		for _, v := range sorted {
			_ = enc.Encode(v)
		}
		return
	}
	for _, v := range sorted {
		writeHuman(w, v)
	}
}

// writeHuman formats a single verdict as a multi-line human-readable
// block. The structure intentionally mirrors what an operator would
// transcribe into a soak-results doc: channel header, dimensions, the
// three RULE-CPL-WARMUP-01 conditions with pass/fail markers, and an
// overall convergence summary.
func writeHuman(w io.Writer, v ConvergenceVerdict) {
	wprintf(w, "channel: %s\n", v.ChannelID)
	wprintf(w, "  hwfp:           %s\n", short(v.HwmonFingerprint, 12))
	wprintf(w, "  d / n_coupled:  %d / %d\n", v.D, v.NCoupled)
	wprintf(w, "  n_samples:      %d / %d %s\n", v.NSamples, v.NSamplesGate, mark(v.NSamplesPasses))
	wprintf(w, "  tr(P):          %.3f / %.3f %s\n", v.TrP, 0.5*v.TrPInitial, mark(v.TrPPasses))
	wprintf(w, "  theta:          %s %s\n", thetaSummary(v.Theta), mark(!v.ThetaIsZero))
	wprintf(w, "  lambda:         %.4f\n", v.Lambda)
	if v.AgeSeconds > 0 {
		wprintf(w, "  last seen:      %s (age %.0fs)\n", v.LastSeen.Format("2006-01-02 15:04:05Z"), v.AgeSeconds)
	} else {
		wprintf(w, "  last seen:      —\n")
	}
	if len(v.GroupedFans) > 0 {
		wprintf(w, "  grouped fans:   %v\n", v.GroupedFans)
	}
	wprintf(w, "  verdict:        %s\n\n", verdictText(v))
}

// wprintf is fmt.Fprintf with the error discarded. Used in human-readable
// formatting where a write failure means the destination (TTY, buffer)
// is in a state we can't usefully report from.
func wprintf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

// thetaSummary formats the theta vector compactly. Empty / nil
// returns "[]"; long vectors are truncated. Non-zero entries surface
// the scale operators care about (Layer-B b_ii in the second slot).
func thetaSummary(theta []float64) string {
	if len(theta) == 0 {
		return "[]"
	}
	parts := make([]string, len(theta))
	for i, v := range theta {
		parts[i] = fmt.Sprintf("%.4f", v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// verdictText collapses the three gate booleans + theta-non-zero into
// a single human-readable sentence. The exact strings here are stable
// across releases — operators grep for them in soak transcripts.
func verdictText(v ConvergenceVerdict) string {
	if v.ThetaIsZero && v.NSamples == 0 {
		return "warming up — no observations yet (excitation gate may still be closed)"
	}
	if v.ThetaIsZero {
		return "warming up — observations admitted, theta still all-zero (no informative samples yet; check RULE-CMB-OAT-01 cross-channel quiet window)"
	}
	if !v.NSamplesPasses {
		return fmt.Sprintf("warming up — n_samples %d below 5·d²=%d gate", v.NSamples, v.NSamplesGate)
	}
	if !v.TrPPasses {
		return "warming up — tr(P) hasn't shrunk below 0.5·tr(P₀); covariance still uninformative"
	}
	return "converged — RULE-CPL-WARMUP-01 conditions all satisfied (κ check still requires live runtime; gate is best-effort from on-disk state)"
}

// mark renders a pass/fail glyph for terminal output without depending
// on Unicode rendering — keeps the log consumable on TTYs that drop
// non-ASCII or in a CI environment.
func mark(ok bool) string {
	if ok {
		return "[ok]"
	}
	return "[--]"
}

// short truncates s to n runes with an ellipsis suffix; used for the
// hwmon fingerprint which is a SHA-of-DMI long enough to overflow the
// alignment column.
func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
