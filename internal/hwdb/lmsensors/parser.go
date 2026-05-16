// SPDX-License-Identifier: GPL-3.0-or-later
//
// Package lmsensors parses the upstream lm-sensors `sensors.conf` /
// `sensors.conf.d/*.conf` format and translates labeled / ignored
// sensors into ventd's hwdb sensor-overlay shape. The mainline
// lm-sensors corpus carries community-maintained per-board labels
// (e.g. "Vcore", "CPU Fan", "GPU Temp") that ventd's catalog does
// not duplicate; importing them as overlays makes the operator-facing
// UI labels match the standard lm-sensors output without manual
// hand-rolling.
//
// Scope:
//   - `chip <pattern>` — defines a match block for one or more chips
//     by hwmon name. Multiple chip patterns on one line are space-
//     separated; quotation marks may wrap patterns containing spaces.
//   - `label <input> "<friendly>"` — friendly name for the input
//     within the active chip block.
//   - `ignore <input>` — operator-asserted "this input is bogus on
//     this board, hide it from the UI".
//   - `# comment` and blank lines.
//
// Deferred (not parsed in v1):
//   - `compute <input> @ * 2, @ / 2` — value-scaling expressions.
//     Ventd's hwmon backend reads the raw kernel value; supporting
//     compute would require a small expression evaluator in the
//     sensor-read hot path, which is moderate additional risk.
//     Operators with `compute`-bearing boards see the raw values
//     until a follow-up adds the expression layer.
//   - `set <input> <value>` — write-on-load directives (rare in the
//     community corpus and outside ventd's read-only sensor surface).
//   - `bus` — bus name aliasing (used for I²C topology; ventd doesn't
//     care).
//
// Licence: upstream `sensors.conf.d/*.conf` files are GPL-2-or-later
// per the lm-sensors package; redistribution under GPL-3 is
// permitted by the "or-later" disjunction. Per-file SPDX headers
// are preserved when files are vendored into
// `internal/hwdb/lmsensors/sensorsconf/`.
package lmsensors

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// ChipBlock is a single `chip <pattern>` section parsed from a
// sensors.conf. The Patterns field carries every chip name pattern
// from the chip line (one block can match multiple chips).
type ChipBlock struct {
	// Patterns is the list of chip-name patterns this block applies
	// to. Patterns can include shell-style wildcards (`*`, `?`) per
	// upstream lm-sensors semantics. Lowercase normalisation is the
	// caller's responsibility.
	Patterns []string

	// Labels maps an input name (e.g. "in0", "fan1", "temp2") to its
	// operator-friendly label string. Quotation marks are stripped.
	Labels map[string]string

	// Ignored is the set of input names the conf file asks ventd to
	// hide from the UI. Used by overlay generation to emit
	// `sensor_ignore: [...]` in the hwdb overlay.
	Ignored map[string]struct{}
}

// Parse reads sensors.conf-format text and returns one ChipBlock per
// `chip` directive seen. Lines outside any chip block are ignored
// (the upstream files occasionally have file-level comments before
// the first chip statement).
//
// Errors are returned only on I/O failure or fundamentally malformed
// syntax (e.g. unterminated quoted string). Unknown directives are
// silently skipped per upstream's tolerance model — operators
// frequently extend their conf with experimental directives that
// older lm-sensors releases don't recognise.
func Parse(r io.Reader) ([]ChipBlock, error) {
	scanner := bufio.NewScanner(r)
	var blocks []ChipBlock
	var current *ChipBlock
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := stripComment(scanner.Text())
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields, err := tokenise(line)
		if err != nil {
			return nil, fmt.Errorf("lmsensors: line %d: %w", lineNo, err)
		}
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "chip":
			// Flush previous block + start a new one.
			if current != nil {
				blocks = append(blocks, *current)
			}
			patterns := fields[1:]
			// Each pattern is its own argument — `chip "it87-*" "nct6775-*"`
			// parses as ["it87-*", "nct6775-*"]. Quoted patterns are
			// already stripped by tokenise.
			current = &ChipBlock{
				Patterns: patterns,
				Labels:   make(map[string]string),
				Ignored:  make(map[string]struct{}),
			}
		case "label":
			if current == nil {
				continue
			}
			if len(fields) < 3 {
				continue
			}
			current.Labels[fields[1]] = fields[2]
		case "ignore":
			if current == nil {
				continue
			}
			if len(fields) < 2 {
				continue
			}
			current.Ignored[fields[1]] = struct{}{}
		default:
			// compute / set / bus / unknown — silently skipped.
		}
	}
	if current != nil {
		blocks = append(blocks, *current)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("lmsensors: scan: %w", err)
	}
	return blocks, nil
}

// stripComment removes anything after an un-quoted `#`. lm-sensors
// allows `#` inside quoted strings; the parser tracks quote state
// to preserve those.
func stripComment(line string) string {
	inQuote := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return line[:i]
			}
		}
	}
	return line
}

// tokenise splits a sensors.conf line into space-separated fields,
// honouring double-quote-delimited strings (which are themselves
// stripped of the surrounding quotes in the returned slice).
func tokenise(line string) ([]string, error) {
	var (
		out     []string
		current strings.Builder
		inQuote bool
	)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch c {
		case '"':
			inQuote = !inQuote
			// Don't emit quote chars into the field — the caller
			// receives the inner string only.
		case ' ', '\t':
			if inQuote {
				current.WriteByte(c)
				continue
			}
			if current.Len() > 0 {
				out = append(out, current.String())
				current.Reset()
			}
		case '\\':
			// Backslash-escape — copy next char verbatim.
			if i+1 < len(line) {
				current.WriteByte(line[i+1])
				i++
			}
		default:
			current.WriteByte(c)
		}
	}
	if inQuote {
		return nil, fmt.Errorf("unterminated quoted string in %q", line)
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out, nil
}
