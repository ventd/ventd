// SPDX-License-Identifier: GPL-3.0-or-later

package msiec

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// firmwareNotSupportedRe matches the upstream msi-ec error logged when
// the EC reports a firmware-version string that no CONF_G* group
// catalogues. The driver still binds (the module loads cleanly) but
// refuses the platform device, leaving /sys/devices/platform/msi-ec/
// empty of the fan_mode + available_fan_modes attrs the daemon needs.
//
// Source: msi-ec.c upstream — pr_err("Firmware version is not
// supported: '%.6s'\n", ec_firmware_buf);
//
// The regex accepts either single quotes or unquoted strings to remain
// resilient across upstream wording tweaks.
var firmwareNotSupportedRe = regexp.MustCompile(
	`msi[_-]ec:\s*Firmware version is not supported:\s*['"]?([^'"\n]+?)['"]?\s*$`,
)

// ErrNoUnsupportedFirmwareLog is returned by DiagnoseUnsupportedFirmware
// when the kernel log carries no "Firmware version is not supported"
// message. Distinct from "couldn't read the log" so callers can tell
// "msi-ec bound successfully" apart from "we couldn't tell."
var ErrNoUnsupportedFirmwareLog = errors.New("msiec: no msi_ec firmware-not-supported message in kernel log")

// FirmwareDiagnoseSource is the test seam used by
// DiagnoseUnsupportedFirmware to read the kernel log. Production
// defaults to journalctl -k for the current boot; tests inject a
// canned string so the parser can be exercised hermetically.
var FirmwareDiagnoseSource = readKernelLog

// DiagnoseUnsupportedFirmware reads the current-boot kernel log and
// returns the firmware-version string the msi-ec driver refused. The
// returned string is what the operator can pass to the upstream
// `firmware=<rev>` modparam to force a closest-catalogue mapping (see
// SuggestFirmwarePins for ranked suggestions).
//
// Returns ErrNoUnsupportedFirmwareLog when no matching log line exists.
// Other I/O errors (journalctl missing, permission, etc.) surface
// verbatim so the caller can log them — the caller usually shouldn't
// fall into the firmware-pin recovery path on those.
func DiagnoseUnsupportedFirmware(ctx context.Context) (string, error) {
	log, err := FirmwareDiagnoseSource(ctx)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(log, "\n") {
		if m := firmwareNotSupportedRe.FindStringSubmatch(line); m != nil {
			return strings.TrimSpace(m[1]), nil
		}
	}
	return "", ErrNoUnsupportedFirmwareLog
}

// readKernelLog runs `journalctl -k -b 0 --no-pager` and returns the
// raw output. Falls back to `dmesg --no-pager` when journalctl is
// missing (some container / minimal-rootfs setups). Both invocations
// are bounded by a 5-second context timeout so a wedged kmsg ring
// can't stall the wizard.
func readKernelLog(ctx context.Context) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := exec.LookPath("journalctl"); err == nil {
		out, err := exec.CommandContext(cctx, "journalctl", "-k", "-b", "0", "--no-pager").Output()
		if err == nil {
			return string(out), nil
		}
		// journalctl present but failed (no /var/log, no perms) — fall
		// through to dmesg rather than surfacing journalctl's error.
	}
	out, err := exec.CommandContext(cctx, "dmesg", "--no-pager").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// FirmwareSuggestion names a single closest-catalogue firmware string
// the operator can pin via the upstream `firmware=<rev>` modparam. Each
// suggestion carries the CONF_G* group it belongs to so the wizard can
// surface "known-good on similar hardware" framing.
type FirmwareSuggestion struct {
	Firmware string // e.g. "16R8IMS1.117"
	Group    string // e.g. "CONF_G2_6"
}

// SuggestFirmwarePins returns up to max ranked candidates for pinning
// when the operator's firmware (detected) is not in any allowlist.
// Ranking layers:
//
//  1. Same 7-character firmware-family prefix wins over every other
//     family. MSI firmware strings encode the board model in the first
//     7 chars (e.g. "16R8IMS" = MS-16R8 / Thin GF63 12UDX); a pin to
//     the wrong board family is much riskier than a pin to an adjacent
//     revision of the same board.
//  2. Within a family, candidates are ranked by lexical proximity to
//     the detected string — adjacent revisions of the same firmware
//     usually share the register map.
//  3. Across families, candidates are deduplicated to one entry per
//     CONF_G* group (the group's first allowlisted firmware) so the
//     wizard's recovery card doesn't show twelve near-duplicate
//     suggestions from a single group.
//
// Returns an empty slice when the catalogue is empty (defensive — the
// generated firmware_catalogue.go always populates it) or when max
// ≤ 0.
func SuggestFirmwarePins(detected string, max int) []FirmwareSuggestion {
	if max <= 0 || len(firmwareGroups) == 0 {
		return nil
	}
	prefix := firmwareFamilyPrefix(detected)
	type ranked struct {
		FirmwareSuggestion
		samePrefix bool
		distance   int
	}
	var all []ranked
	for _, g := range firmwareGroups {
		// Layer 1: every same-family firmware in the group is a
		// candidate (most useful — they share the register map AND the
		// board).
		var bestSameFamily *FirmwareSuggestion
		bestDist := -1
		for _, fw := range g.Firmwares {
			if firmwareFamilyPrefix(fw) == prefix && prefix != "" {
				d := lexicalDistance(detected, fw)
				if bestDist < 0 || d < bestDist {
					bestDist = d
					sug := FirmwareSuggestion{Firmware: fw, Group: g.Name}
					bestSameFamily = &sug
				}
			}
		}
		if bestSameFamily != nil {
			all = append(all, ranked{
				FirmwareSuggestion: *bestSameFamily,
				samePrefix:         true,
				distance:           bestDist,
			})
			continue
		}
		// Layer 3: one entry per group when no same-family hit. The
		// first allowlisted firmware is the canonical representative.
		all = append(all, ranked{
			FirmwareSuggestion: FirmwareSuggestion{
				Firmware: g.Firmwares[0],
				Group:    g.Name,
			},
			samePrefix: false,
			distance:   0,
		})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].samePrefix != all[j].samePrefix {
			return all[i].samePrefix
		}
		if all[i].samePrefix {
			return all[i].distance < all[j].distance
		}
		// Different-family entries: stable source order.
		return false
	})
	if max > len(all) {
		max = len(all)
	}
	out := make([]FirmwareSuggestion, max)
	for i := 0; i < max; i++ {
		out[i] = all[i].FirmwareSuggestion
	}
	return out
}

// firmwareFamilyPrefix returns the 7-character board-family prefix of
// an MSI firmware string (e.g. "16R8IMS1.117" → "16R8IMS"). Returns
// the empty string when the input is shorter than 7 characters so
// callers can treat "no family detected" as "fall through to the
// cross-family ranking layer".
func firmwareFamilyPrefix(fw string) string {
	if len(fw) < 7 {
		return ""
	}
	return fw[:7]
}

// lexicalDistance is the simple integer distance between the trailing
// revision numbers of two firmware strings. MSI's revision pattern is
// always "<family>.NNN" (three digits, sometimes preceded by a single
// letter the parser ignores). Falls back to byte-wise difference of
// the trailing 3 chars when ParseInt fails so the ranking remains
// total even on unusual inputs.
func lexicalDistance(a, b string) int {
	an := tailNumber(a)
	bn := tailNumber(b)
	if an < 0 || bn < 0 {
		// fallback: bytewise diff over the trailing 3 chars
		return byteDiff(a, b, 3)
	}
	d := an - bn
	if d < 0 {
		d = -d
	}
	return d
}

func tailNumber(s string) int {
	if dot := strings.LastIndex(s, "."); dot >= 0 && dot+1 < len(s) {
		tail := s[dot+1:]
		var n int
		for _, c := range tail {
			if c < '0' || c > '9' {
				return -1
			}
			n = n*10 + int(c-'0')
		}
		return n
	}
	return -1
}

func byteDiff(a, b string, tail int) int {
	if len(a) < tail || len(b) < tail {
		return len(a) + len(b)
	}
	an := a[len(a)-tail:]
	bn := b[len(b)-tail:]
	diff := 0
	for i := 0; i < tail; i++ {
		if an[i] != bn[i] {
			diff++
		}
	}
	return diff
}
