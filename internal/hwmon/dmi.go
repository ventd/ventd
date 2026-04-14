package hwmon

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultDMIRoot is the sysfs directory that exposes SMBIOS/DMI fields. Tests
// pass an alternate root into ReadDMI to exercise the classifier against a
// synthetic tree.
const DefaultDMIRoot = "/sys/class/dmi/id"

// DMIInfo is a trimmed snapshot of the SMBIOS board/system identifiers used
// for the Tier 3 candidate-module proposal. Fields are lowercase and trimmed
// to make containment matching case-insensitive without re-normalising at
// every comparison site. Missing files become "" — callers treat that as a
// non-match, never an error.
type DMIInfo struct {
	BoardVendor string
	BoardName   string
	ProductName string
	SysVendor   string
}

// ReadDMI reads the four DMI identifiers used by ProposeModulesByDMI. root
// defaults to DefaultDMIRoot when empty so production callers can pass "".
// Values are whitespace-trimmed and lowercased once up-front.
func ReadDMI(root string) DMIInfo {
	if root == "" {
		root = DefaultDMIRoot
	}
	read := func(field string) string {
		data, err := os.ReadFile(filepath.Join(root, field))
		if err != nil {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(string(data)))
	}
	return DMIInfo{
		BoardVendor: read("board_vendor"),
		BoardName:   read("board_name"),
		ProductName: read("product_name"),
		SysVendor:   read("sys_vendor"),
	}
}

// matches reports whether every non-empty needle in t is a substring of the
// corresponding DMIInfo field. An empty needle is a wildcard. A trigger with
// every needle empty is a no-op and does not match anything, so the seed
// table never proposes modules on zero-signal DMI.
func (t DMITrigger) matches(info DMIInfo) bool {
	empty := true
	check := func(needle, hay string) bool {
		if needle == "" {
			return true
		}
		empty = false
		return strings.Contains(hay, strings.ToLower(needle))
	}
	ok := check(t.BoardVendorContains, info.BoardVendor) &&
		check(t.BoardNameContains, info.BoardName) &&
		check(t.ProductContains, info.ProductName) &&
		check(t.SysVendorContains, info.SysVendor)
	if empty {
		return false
	}
	return ok
}

// ProposeModulesByDMI returns the subset of knownDriverNeeds whose DMITriggers
// match the given DMI snapshot. Ordering is stable by DriverNeed.Key so test
// expectations and UI listings don't churn.
//
// The proposal is advisory. Ventd never auto-modprobes on the basis of DMI —
// setup surfaces each candidate as a diagnostic and waits for a user click.
func ProposeModulesByDMI(info DMIInfo) []DriverNeed {
	seen := map[string]bool{}
	var out []DriverNeed
	for _, nd := range knownDriverNeeds {
		if seen[nd.Key] {
			continue
		}
		for _, trig := range nd.DMITriggers {
			if trig.matches(info) {
				out = append(out, nd)
				seen[nd.Key] = true
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
