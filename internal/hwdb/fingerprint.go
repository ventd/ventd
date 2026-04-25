package hwdb

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"strings"
)

// DMI holds the v1 fingerprint input tuple read from /sys and /proc.
// The tuple is frozen: changing any field breaks existing per-platform
// state directories. See docs/hwdb-schema.md §Fingerprint hash.
type DMI struct {
	SysVendor    string
	ProductName  string
	BoardVendor  string
	BoardName    string
	BoardVersion string
	CPUModelName string
	CPUCoreCount int
}

// Fingerprint returns the v1 fingerprint hash for the given DMI tuple.
// The function is pure: same input always produces the same 16-character
// lowercase hex string. No I/O is performed.
//
// Hash = hex(sha256(tuple)[:8]) where tuple is the pipe-joined canonicalised
// fields in the order: sys_vendor, product_name, board_vendor, board_name,
// board_version, cpu_model_name, cpu_core_count.
func Fingerprint(dmi DMI) string {
	input := strings.Join([]string{
		canonicalise(dmi.SysVendor),
		canonicalise(dmi.ProductName),
		canonicalise(dmi.BoardVendor),
		canonicalise(dmi.BoardName),
		canonicalise(dmi.BoardVersion),
		canonicalise(dmi.CPUModelName),
		fmt.Sprintf("%d", dmi.CPUCoreCount),
	}, "|")
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:8])
}

// ReadDMI reads the v1 fingerprint input tuple from the given filesystem root.
// In production, root is os.DirFS("/"); tests inject a testing/fstest.MapFS.
//
// DMI fields come from /sys/class/dmi/id/*. CPU fields come from /proc/cpuinfo.
// Missing files produce empty strings / zero counts rather than errors, so
// the function succeeds on hardware that omits some SMBIOS fields.
func ReadDMI(root fs.FS) (DMI, error) {
	read := func(path string) string {
		data, err := fs.ReadFile(root, path)
		if err != nil {
			return ""
		}
		return strings.TrimRight(string(data), "\n")
	}

	dmi := DMI{
		SysVendor:    read("sys/class/dmi/id/sys_vendor"),
		ProductName:  read("sys/class/dmi/id/product_name"),
		BoardVendor:  read("sys/class/dmi/id/board_vendor"),
		BoardName:    read("sys/class/dmi/id/board_name"),
		BoardVersion: read("sys/class/dmi/id/board_version"),
	}

	modelName, coreCount, err := parseCPUInfo(root)
	if err != nil {
		return DMI{}, fmt.Errorf("hwdb fingerprint: read cpuinfo: %w", err)
	}
	dmi.CPUModelName = modelName
	dmi.CPUCoreCount = coreCount
	return dmi, nil
}

// parseCPUInfo extracts the first "model name" and the total processor count
// from /proc/cpuinfo in root.
func parseCPUInfo(root fs.FS) (modelName string, coreCount int, err error) {
	f, err := root.Open("proc/cpuinfo")
	if err != nil {
		// Missing /proc/cpuinfo is tolerated (e.g., non-Linux test envs).
		return "", 0, nil
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "model name":
			if modelName == "" {
				modelName = val
			}
		case "processor":
			coreCount++
		}
	}
	return modelName, coreCount, sc.Err()
}

// canonicalise normalises a DMI field value for inclusion in the fingerprint
// tuple. Rules applied in order:
//  1. Trim leading/trailing whitespace.
//  2. Collapse runs of internal whitespace to a single space.
//  3. Convert to lowercase.
//  4. Replace empty string with "<empty>" to preserve positional stability.
func canonicalise(s string) string {
	s = strings.TrimSpace(s)
	// Collapse internal whitespace runs.
	fields := strings.Fields(s)
	s = strings.Join(fields, " ")
	s = strings.ToLower(s)
	if s == "" {
		return "<empty>"
	}
	return s
}
