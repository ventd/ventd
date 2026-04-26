package hwdb

import (
	"bytes"
	"io/fs"
	"path"
	"strings"
)

// LiveDTData holds the device-tree values read from /proc/device-tree.
type LiveDTData struct {
	Compatible []string // null-separated list from /proc/device-tree/compatible
	Model      string   // /proc/device-tree/model (null-terminated)
}

// ReadDTData reads device-tree fingerprint data from the given filesystem root.
// root is expected to represent the filesystem root (e.g. os.DirFS("/")).
// Returns an empty LiveDTData (not an error) if /proc/device-tree is absent.
func ReadDTData(root fs.FS) LiveDTData {
	read := func(p string) []byte {
		data, err := fs.ReadFile(root, p)
		if err != nil {
			return nil
		}
		return data
	}

	// /proc/device-tree/compatible is a NUL-separated, NUL-terminated list.
	compatRaw := read(path.Join("proc", "device-tree", "compatible"))
	var compat []string
	if len(compatRaw) > 0 {
		for entry := range bytes.SplitSeq(compatRaw, []byte{0x00}) {
			s := string(entry)
			if s != "" {
				compat = append(compat, s)
			}
		}
	}

	// /proc/device-tree/model is a single NUL-terminated string.
	modelRaw := read(path.Join("proc", "device-tree", "model"))
	model := strings.TrimRight(string(modelRaw), "\x00")

	return LiveDTData{Compatible: compat, Model: model}
}

// IsDMIPresent reports whether live DMI data is available at sysRoot.
// Returns true when /sys/class/dmi/id/sys_vendor exists and is non-empty after
// trimming. ARM/SBC systems without SMBIOS return false.
func IsDMIPresent(sysRoot fs.FS) bool {
	data, err := fs.ReadFile(sysRoot, path.Join("sys", "class", "dmi", "id", "sys_vendor"))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) != ""
}

// MatchBoardDMI reports whether the live DMI data matches the board's
// dmi_fingerprint pattern. All non-empty pattern fields must glob-match the
// corresponding live field. Empty or "*" pattern fields match anything.
func MatchBoardDMI(pattern *BoardDMIFingerprint, live DMIFingerprint) bool {
	if pattern == nil {
		return false
	}
	return globMatch(pattern.SysVendor, live.SysVendor) &&
		globMatch(pattern.ProductName, live.ProductName) &&
		globMatch(pattern.BoardVendor, live.BoardVendor) &&
		globMatch(pattern.BoardName, live.BoardName) &&
		globMatch(pattern.BoardVersion, live.BoardVersion) &&
		globMatch(pattern.BiosVersion, live.BiosVersion)
}

// MatchBoardDT reports whether the live device-tree data matches the board's
// dt_fingerprint pattern.
// - compatible: matches if any entry in live.Compatible glob-matches the pattern.
// - model: matches if live.Model glob-matches the pattern.
// Both fields are optional; absent or empty pattern fields match anything.
func MatchBoardDT(pattern *DTFingerprint, live LiveDTData) bool {
	if pattern == nil {
		return false
	}
	if pattern.Compatible != "" {
		matched := false
		for _, c := range live.Compatible {
			if globMatch(pattern.Compatible, c) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if pattern.Model != "" {
		if !globMatch(pattern.Model, live.Model) {
			return false
		}
	}
	return true
}

// MatchBoardEntry reports whether entry matches the live hardware signals.
// If dmiPresent is true, only DMI-fingerprinted entries are considered.
// If dmiPresent is false, only DT-fingerprinted entries are considered.
// RULE-FINGERPRINT-06 (DT compatible glob), RULE-FINGERPRINT-07 (DT model glob),
// RULE-FINGERPRINT-04 (bios_version glob), RULE-FINGERPRINT-05 (absent bios_version).
func MatchBoardEntry(entry *BoardCatalogEntry, live DMIFingerprint, livedt LiveDTData, dmiPresent bool) bool {
	if dmiPresent {
		return entry.DMIFingerprint != nil && MatchBoardDMI(entry.DMIFingerprint, live)
	}
	return entry.DTFingerprint != nil && MatchBoardDT(entry.DTFingerprint, livedt)
}

// globMatch reports whether pattern glob-matches s. The only supported
// wildcard is '*' which matches any sequence of characters (including empty).
// An empty or "*"-only pattern matches anything.
func globMatch(pattern, s string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	// Fast path: no wildcard.
	if !strings.Contains(pattern, "*") {
		return strings.EqualFold(pattern, s)
	}
	return globMatchLower(strings.ToLower(pattern), strings.ToLower(s))
}

// globMatchLower is a recursive glob matcher operating on lowercase strings.
func globMatchLower(pattern, s string) bool {
	for len(pattern) > 0 {
		switch idx := strings.IndexByte(pattern, '*'); idx {
		case -1:
			// No more wildcards: must match exactly.
			return pattern == s
		case 0:
			// Leading '*': consume it and try all positions in s.
			pattern = pattern[1:]
			if len(pattern) == 0 {
				return true // trailing '*' matches everything
			}
			for i := 0; i <= len(s); i++ {
				if globMatchLower(pattern, s[i:]) {
					return true
				}
			}
			return false
		default:
			// Literal prefix before the next '*'.
			if len(s) < idx || s[:idx] != pattern[:idx] {
				return false
			}
			pattern = pattern[idx:]
			s = s[idx:]
		}
	}
	return s == ""
}
