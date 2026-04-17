package main

// list-fans-probe was historically a separate binary at
// cmd/list-fans-probe. It compares the pre-Tier-2 chip-blind sysfs
// walk against the Tier 2 capability-first enumeration on the live
// host and prints classification, tuples, regressions, and a PASS/
// FAIL line.
//
// Folded into the main ventd binary as a subcommand
// (`ventd --list-fans-probe`) so distros only need to package one
// binary, and so the validation tooling stays bisectable with the
// daemon code that backs it.
//
// The writability probe sets pwm_enable to manual then restores the
// original value. It does NOT change PWM duty cycle. Safe on live
// hosts.

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdb"
	hwmonpkg "github.com/ventd/ventd/internal/hwmon"
)

// doRefreshHWDB wires ventd --list-fans-probe --refresh-hwdb=<sha256> so
// the hwdb remote refresh can be exercised alongside the DMI probe without
// requiring a separate binary. Empty string means "skip refresh".
var doRefreshHWDB = flag.String("refresh-hwdb", "", "with --list-fans-probe: fetch remote hwdb, verify SHA-256, merge (requires hwdb.allow_remote=true in config)")

type lfpTuple struct {
	hwmonDir string
	control  string // pwm* or fan*_target
	fanInput string // companion fan*_input or "" if none
	kind     string // "pwm" | "rpm_target"
}

func (t lfpTuple) key() string { return t.kind + "\x00" + t.control }

// runListFansProbe is the subcommand entry point. Returns the exit
// code main should pass to os.Exit. Caller is responsible for
// converting non-zero into a process exit.
func runListFansProbe() int {
	if pinnedSHA := *doRefreshHWDB; pinnedSHA != "" {
		cfg, err := config.Load(flag.Lookup("config").Value.String())
		if err != nil {
			fmt.Fprintf(os.Stderr, "refresh-hwdb: load config: %v\n", err)
			return 1
		}
		if !cfg.HWDB.AllowRemote {
			fmt.Fprintln(os.Stderr, "refresh-hwdb: skipped (hwdb.allow_remote=false in config)")
		} else {
			n, err := hwdb.RefreshFromRemote(context.Background(), http.DefaultClient, pinnedSHA)
			if err != nil {
				fmt.Fprintf(os.Stderr, "refresh-hwdb: %v\n", err)
				return 1
			}
			fmt.Printf("hwdb: refreshed %d remote profiles\n", n)
		}
	}

	devices := hwmonpkg.EnumerateDevices(hwmonpkg.DefaultHwmonRoot)

	dmi := hwmonpkg.ReadDMI("")
	fmt.Println("=== DMI (Tier 3 inputs) ===")
	fmt.Printf("board_vendor=%q board_name=%q product_name=%q sys_vendor=%q\n",
		dmi.BoardVendor, dmi.BoardName, dmi.ProductName, dmi.SysVendor)
	candidates := hwmonpkg.ProposeModulesByDMI(dmi)
	if len(candidates) == 0 {
		fmt.Println("Tier 3 proposal: no DMI match in seed table")
	} else {
		keys := make([]string, len(candidates))
		for i, c := range candidates {
			keys[i] = c.Key
		}
		fmt.Printf("Tier 3 proposal: %v\n", keys)
	}
	fmt.Println()

	fmt.Println("=== hwmon classification ===")
	for _, d := range devices {
		fmt.Printf("%s\t%-18s\tclass=%s\tpwm=%d  fan_input=%d  fan_target=%d  temp=%d\n",
			filepath.Base(d.Dir), d.ChipName, d.Class,
			len(d.PWM), len(d.FanInputs), len(d.RPMTargets), len(d.TempInputs))
	}
	fmt.Println()

	pre := lfpDiscoverPreTier2()
	post := lfpDiscoverPostTier2(devices)
	lfpSortTuples(pre)
	lfpSortTuples(post)

	fmt.Println("=== pre-Tier-2 tuples (hwmon_dir | control | fan_input) ===")
	for _, t := range pre {
		fmt.Printf("%s\t%s\t%s\t%s\n", t.kind, t.hwmonDir, t.control, lfpOrDash(t.fanInput))
	}
	fmt.Println()
	fmt.Println("=== post-Tier-2 tuples (hwmon_dir | control | fan_input) ===")
	for _, t := range post {
		fmt.Printf("%s\t%s\t%s\t%s\n", t.kind, t.hwmonDir, t.control, lfpOrDash(t.fanInput))
	}
	fmt.Println()

	preKey := map[string]lfpTuple{}
	for _, t := range pre {
		preKey[t.key()] = t
	}
	postKey := map[string]lfpTuple{}
	for _, t := range post {
		postKey[t.key()] = t
	}

	var regressions, additions []lfpTuple
	for _, t := range pre {
		if _, ok := postKey[t.key()]; !ok {
			regressions = append(regressions, t)
		}
	}
	for _, t := range post {
		if _, ok := preKey[t.key()]; !ok {
			additions = append(additions, t)
		}
	}

	fmt.Println("=== net-new coverage (not a regression) ===")
	if len(additions) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, t := range additions {
			fmt.Printf("+ %s\t%s\t%s\t%s\n", t.kind, t.hwmonDir, t.control, lfpOrDash(t.fanInput))
		}
	}
	fmt.Println()

	fmt.Println("=== regressions (Primary-class devices missing from post-Tier-2) ===")
	if len(regressions) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, t := range regressions {
			fmt.Printf("- %s\t%s\t%s\t%s\n", t.kind, t.hwmonDir, t.control, lfpOrDash(t.fanInput))
		}
	}
	fmt.Println()

	if len(regressions) == 0 {
		fmt.Println("=== PASS ===")
		return 0
	}
	fmt.Println("=== FAIL ===")
	return 1
}

// lfpDiscoverPreTier2 replicates setup.discoverHwmonControls as it
// stood at commit c1e925f — raw /sys/class/hwmon walk, chip-name
// "nvidia" skip, writability probe per pwm channel, fan_target
// fallback per index.
func lfpDiscoverPreTier2() []lfpTuple {
	entries, err := os.ReadDir("/sys/class/hwmon")
	if err != nil {
		return nil
	}
	var tuples []lfpTuple
	for _, e := range entries {
		dir := filepath.Join("/sys/class/hwmon", e.Name())
		if lfpReadName(dir) == "nvidia" {
			continue
		}
		pwmChannels := map[string]bool{}
		matches, _ := filepath.Glob(filepath.Join(dir, "pwm[1-9]*"))
		for _, p := range matches {
			suffix := strings.TrimPrefix(filepath.Base(p), "pwm")
			if _, err := strconv.Atoi(suffix); err != nil {
				continue
			}
			if _, err := os.Stat(p); err == nil && lfpTestPWMWritable(p) {
				tuples = append(tuples, lfpTuple{
					hwmonDir: dir,
					control:  p,
					fanInput: lfpFanInputFor(dir, suffix),
					kind:     "pwm",
				})
				pwmChannels[suffix] = true
			}
		}
		targets, _ := filepath.Glob(filepath.Join(dir, "fan[1-9]*_target"))
		for _, p := range targets {
			base := filepath.Base(p)
			num := strings.TrimSuffix(strings.TrimPrefix(base, "fan"), "_target")
			if _, err := strconv.Atoi(num); err != nil {
				continue
			}
			if pwmChannels[num] {
				continue
			}
			if lfpTestFanTargetWritable(p) {
				tuples = append(tuples, lfpTuple{
					hwmonDir: dir,
					control:  p,
					fanInput: lfpFanInputFor(dir, num),
					kind:     "rpm_target",
				})
			}
		}
	}
	return tuples
}

// lfpDiscoverPostTier2 mirrors the new setup.discoverHwmonControls:
// classify first, then probe writability on every Primary/OpenLoop
// candidate.
func lfpDiscoverPostTier2(devices []hwmonpkg.HwmonDevice) []lfpTuple {
	var tuples []lfpTuple
	for _, dev := range devices {
		switch dev.Class {
		case hwmonpkg.ClassSkipNVIDIA, hwmonpkg.ClassNoFans, hwmonpkg.ClassReadOnly:
			continue
		}
		writableIdx := map[string]bool{}
		for _, ch := range dev.PWM {
			if ch.EnablePath == "" {
				continue
			}
			if !lfpTestPWMWritable(ch.Path) {
				continue
			}
			tuples = append(tuples, lfpTuple{
				hwmonDir: dev.Dir,
				control:  ch.Path,
				fanInput: ch.FanInput,
				kind:     "pwm",
			})
			writableIdx[ch.Index] = true
		}
		for _, t := range dev.RPMTargets {
			if writableIdx[t.Index] {
				continue
			}
			if lfpTestFanTargetWritable(t.Path) {
				tuples = append(tuples, lfpTuple{
					hwmonDir: dev.Dir,
					control:  t.Path,
					fanInput: t.InputPath,
					kind:     "rpm_target",
				})
			}
		}
	}
	return tuples
}

func lfpTestPWMWritable(p string) bool {
	current, err := hwmonpkg.ReadPWM(p)
	if err != nil {
		return false
	}
	orig, enableErr := hwmonpkg.ReadPWMEnable(p)
	if enableErr == nil {
		if err := hwmonpkg.WritePWMEnable(p, 1); err != nil {
			return false
		}
		_ = hwmonpkg.WritePWMEnable(p, orig)
	} else {
		if err := hwmonpkg.WritePWM(p, current); err != nil {
			return false
		}
	}
	return true
}

func lfpTestFanTargetWritable(p string) bool {
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	cur, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	b := strconv.AppendInt(nil, int64(cur), 10)
	b = append(b, '\n')
	return os.WriteFile(p, b, 0) == nil
}

func lfpReadName(dir string) string {
	data, _ := os.ReadFile(filepath.Join(dir, "name"))
	return strings.TrimSpace(string(data))
}

func lfpFanInputFor(dir, idx string) string {
	p := filepath.Join(dir, "fan"+idx+"_input")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func lfpSortTuples(ts []lfpTuple) {
	sort.Slice(ts, func(i, j int) bool {
		if ts[i].control != ts[j].control {
			return ts[i].control < ts[j].control
		}
		return ts[i].kind < ts[j].kind
	})
}

func lfpOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
