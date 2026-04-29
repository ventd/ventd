package sysclass

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// detectNAS returns true when the system has ≥1 rotational block device AND at
// least one active pool indicator (zpool, mdraid array, or btrfs pool).
func detectNAS(d deps) (bool, []string) {
	if !hasRotationalDrive(d) {
		return false, nil
	}
	poolEv := detectPool(d)
	if len(poolEv) == 0 {
		return false, nil
	}
	ev := append([]string{"rotational_drive"}, poolEv...)
	return true, ev
}

// hasRotationalDrive checks /sys/block/*/queue/rotational for any drive with
// rotational=1 (excludes loop, ram, nbd devices).
func hasRotationalDrive(d deps) bool {
	blockGlob := sysPath(d, "block/*/queue/rotational")
	matches, err := filepath.Glob(blockGlob)
	if err != nil || len(matches) == 0 {
		return false
	}
	for _, p := range matches {
		dev := filepath.Base(filepath.Dir(filepath.Dir(p)))
		// Skip pseudo-devices.
		if strings.HasPrefix(dev, "loop") ||
			strings.HasPrefix(dev, "ram") ||
			strings.HasPrefix(dev, "nbd") ||
			strings.HasPrefix(dev, "zram") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == "1" {
			return true
		}
	}
	return false
}

// detectPool checks for active storage pools: zpool, mdraid, btrfs.
func detectPool(d deps) []string {
	var ev []string

	// ZFS: /proc/spl/kstat/zfs/<pool>/state (any pool directory exists).
	zpoolGlob := procPath(d, "spl/kstat/zfs/*/state")
	if m, err := filepath.Glob(zpoolGlob); err == nil && len(m) > 0 {
		ev = append(ev, "pool:zfs")
	}

	// mdraid: /proc/mdstat contains active arrays.
	if hasMDRAIDArray(d) {
		ev = append(ev, "pool:mdraid")
	}

	// BTRFS: /sys/fs/btrfs/<uuid>/ directories exist.
	btrfsGlob := sysPath(d, "fs/btrfs/*/")
	if m, err := filepath.Glob(btrfsGlob); err == nil && len(m) > 0 {
		ev = append(ev, "pool:btrfs")
	}

	return ev
}

// hasMDRAIDArray returns true when /proc/mdstat contains at least one active
// md array line (lines starting with "md").
func hasMDRAIDArray(d deps) bool {
	path := procPath(d, "mdstat")
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "md") && strings.Contains(line, ": active") {
			return true
		}
	}
	return false
}
