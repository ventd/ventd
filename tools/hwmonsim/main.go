// hwmonsim materialises a faithful, *live* synthetic hwmon tree so ventd can be
// run end-to-end against fake hardware. Point the daemon at it with
// VENTD_HWMON_ROOT=<dir> (see internal/hwmon.RootOverrideEnv) and the whole
// stack — enumeration, the control loop, calibration sweeps, the polarity
// probe, the web UI, doctor — drives these files instead of real fans.
//
// It is two things in one process:
//
//  1. A materialiser: writes one hwmonN directory per controller with `name`,
//     and per fan a pwmN / pwmN_enable / fanN_input triple plus a couple of
//     tempN_input sensors — exactly the shape internal/hwmon.classifyDevice
//     recognises as a controllable (ClassPrimary) device.
//
//  2. A live model: every tick it reads back the pwmN / pwmN_enable files the
//     daemon writes and recomputes fanN_input (RPM) and tempN_input from a
//     simple but monotonic thermal/aerodynamic model — so a calibration sweep
//     sees RPM rise with PWM, the polarity probe sees a real stop threshold,
//     and smart mode sees temperature fall as airflow rises. A static tree
//     (--once) only exercises enumeration and the UI; the live loop is what
//     makes control/calibration meaningful.
//
// --board <id> seeds the chip name(s) and controller topology from a real
// entry in ventd's hardware catalog (internal/hwdb/catalog/boards), so the
// daemon's chip-family / hwdb matching runs against a real board's chips.
// --board list prints the available ids.
//
// This is dev tooling, not production code — no RULE bindings.
//
// Usage:
//
//	go run ./tools/hwmonsim --out /tmp/vsim                  # 3 fans, live, nct6687
//	go run ./tools/hwmonsim --out /tmp/vsim --fans 5 --model spinup
//	go run ./tools/hwmonsim --board list                     # list catalog board ids
//	go run ./tools/hwmonsim --out /tmp/vsim --board msi-pro-z690-a-ddr4
//	go run ./tools/hwmonsim --out /tmp/vsim --once           # materialise and exit
//	VENTD_HWMON_ROOT=/tmp/vsim ./ventd --config ...          # drive it
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ventd/ventd/internal/hwdb"
)

type config struct {
	out      string
	board    string
	preset   string
	fans     int
	temps    int
	chip     string
	maxRPM   int
	minRPM   int // RPM the moment the fan is spinning at all (just past stop)
	stopPWM  int // at/below this duty the fan stalls (spin-down threshold)
	startPWM int // a stalled fan needs at least this duty to start spinning
	model    string
	tick     time.Duration
	once     bool
}

// device is one materialised hwmonN directory.
type device struct {
	dir   string
	chip  string
	fans  int
	temps int
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.out, "out", "", "directory to materialise the synthetic hwmon tree in (required)")
	flag.StringVar(&cfg.board, "board", "", "seed chip name(s) from a catalog board id (or \"list\" to list ids)")
	flag.StringVar(&cfg.preset, "preset", "", "built-in multi-chip topology (or \"list\"): desktop | laptop | gpu")
	flag.IntVar(&cfg.fans, "fans", 3, "number of controllable pwm fans per device")
	flag.IntVar(&cfg.temps, "temps", 2, "number of temperature sensors per device")
	flag.StringVar(&cfg.chip, "chip", "nct6687", "hwmon `name` value when --board is not used (must not be \"nvidia\")")
	flag.IntVar(&cfg.maxRPM, "max-rpm", 2200, "RPM at full duty")
	flag.IntVar(&cfg.minRPM, "min-rpm", 500, "RPM the instant the fan spins")
	flag.IntVar(&cfg.stopPWM, "stop-pwm", 25, "duty at/below which the fan stalls (0-255)")
	flag.IntVar(&cfg.startPWM, "start-pwm", 40, "duty a stalled fan needs to start (0-255)")
	flag.StringVar(&cfg.model, "model", "spinup", "rpm model: linear | spinup")
	flag.DurationVar(&cfg.tick, "tick", 200*time.Millisecond, "model update cadence")
	flag.BoolVar(&cfg.once, "once", false, "materialise the tree and exit (no live loop)")
	flag.Parse()

	if cfg.board == "list" {
		listBoards()
		return
	}
	if cfg.preset == "list" {
		listPresets()
		return
	}
	if cfg.out == "" {
		fmt.Fprintln(os.Stderr, "hwmonsim: --out is required")
		flag.Usage()
		os.Exit(2)
	}
	if cfg.fans < 1 {
		cfg.fans = 1
	}

	devices, err := buildDevices(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hwmonsim:", err)
		os.Exit(2)
	}
	for _, d := range devices {
		if err := materialise(d.dir, d.chip, d.fans, d.temps); err != nil {
			fmt.Fprintln(os.Stderr, "hwmonsim: materialise:", err)
			os.Exit(1)
		}
		fmt.Printf("hwmonsim: materialised %d-fan %q device at %s\n", d.fans, d.chip, d.dir)
	}
	fmt.Printf("hwmonsim: drive it with:  VENTD_HWMON_ROOT=%s ./ventd ...\n", cfg.out)
	if cfg.once {
		return
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	fmt.Printf("hwmonsim: live model running (model=%s, tick=%s) — Ctrl-C to stop\n", cfg.model, cfg.tick)
	run(devices, cfg, stop)
}

// deviceSpec is a chip + fan/temp count, before an hwmonN dir is assigned.
// fans == 0 means a temp-only device (NVMe, ACPI thermal zone) — it enumerates
// as ClassNoFans (a sensor, not a controllable channel), which exercises the
// daemon's "skip this, it has no fans" path.
type deviceSpec struct {
	chip  string
	fans  int
	temps int
}

// presets are built-in multi-chip topologies that mirror common real machines,
// including the non-controllable devices a real /sys/class/hwmon carries — so
// the daemon's enumeration + classification (ClassPrimary / ClassNoFans /
// ClassSkipNVIDIA) is exercised, not just the all-fans happy path.
var presets = map[string][]deviceSpec{
	// Desktop: a super-I/O with 6 case/CPU fans, a controllable GPU, an NVMe
	// (temp-only), an ACPI thermal zone (temp-only), and an nvidia device the
	// enumerator must skip.
	"desktop": {
		{chip: "nct6687", fans: 6, temps: 4},
		{chip: "amdgpu", fans: 1, temps: 1},
		{chip: "nvme", fans: 0, temps: 1},
		{chip: "acpitz", fans: 0, temps: 1},
		{chip: "nvidia", fans: 1, temps: 1}, // ClassSkipNVIDIA — must be ignored
	},
	// Laptop: ACPI thermal zones + a single EC-driven fan.
	"laptop": {
		{chip: "acpitz", fans: 0, temps: 2},
		{chip: "thinkpad", fans: 1, temps: 1},
	},
	// GPU box: one controllable AMD GPU plus an nvidia device to skip.
	"gpu": {
		{chip: "amdgpu", fans: 1, temps: 2},
		{chip: "nvidia", fans: 1, temps: 1},
	},
}

// buildDevices resolves the device list from --preset, --board, or the default
// single --chip device.
func buildDevices(cfg config) ([]device, error) {
	switch {
	case cfg.preset != "":
		specs, ok := presets[cfg.preset]
		if !ok {
			return nil, fmt.Errorf("unknown preset %q (try --preset list)", cfg.preset)
		}
		devs := assignDirs(cfg.out, specs)
		fmt.Printf("hwmonsim: preset %q — %d device(s): %s\n", cfg.preset, len(devs), chipList(devs))
		return devs, nil

	case cfg.board != "":
		entry, err := findBoard(cfg.board)
		if err != nil {
			return nil, err
		}
		devs := devicesFromBoard(entry, cfg)
		if len(devs) == 0 {
			return nil, fmt.Errorf("board %q has no controllable controller chip to simulate (all unknown/fanless)", cfg.board)
		}
		fmt.Printf("hwmonsim: seeded from board %q — %d controller(s): %s\n",
			entry.ID, len(devs), chipList(devs))
		return devs, nil

	default:
		if cfg.chip == "nvidia" {
			return nil, fmt.Errorf("--chip nvidia would be skipped by the enumerator; pick another name")
		}
		return []device{{dir: filepath.Join(cfg.out, "hwmon0"), chip: cfg.chip, fans: cfg.fans, temps: cfg.temps}}, nil
	}
}

// devicesFromBoard derives one device per controller chip of a catalog board.
// The primary controller's fan count is taken from the board's fan_profiles or
// pwm_groups when present (the real channel count), else --fans; additional
// controllers use --fans. Chips with no controllable hwmon presence
// ("unknown" / empty / nvidia) are skipped.
func devicesFromBoard(entry *hwdb.BoardCatalogEntry, cfg config) []device {
	primaryFans := boardFanCount(entry, cfg.fans)
	specs := []deviceSpec{{chip: entry.PrimaryController.Chip, fans: primaryFans, temps: cfg.temps}}
	for _, c := range entry.AdditionalControllers {
		specs = append(specs, deviceSpec{chip: c.Chip, fans: cfg.fans, temps: cfg.temps})
	}
	var kept []deviceSpec
	for _, s := range specs {
		chip := strings.TrimSpace(s.chip)
		if chip == "" || chip == "unknown" || chip == "nvidia" {
			continue
		}
		s.chip = chip
		kept = append(kept, s)
	}
	return assignDirs(cfg.out, kept)
}

// boardFanCount returns the primary controller's channel count from the
// catalog: len(fan_profiles) when populated, else the distinct pwm_groups
// channel count, else the supplied default. (No current board populates these,
// so this is correct-when-present and future-proof.)
func boardFanCount(entry *hwdb.BoardCatalogEntry, dflt int) int {
	if n := len(entry.FanProfiles); n > 0 {
		return n
	}
	if n := len(entry.PWMGroups); n > 0 {
		return n
	}
	return dflt
}

// assignDirs turns specs into devices with contiguous hwmonN directories.
func assignDirs(out string, specs []deviceSpec) []device {
	devs := make([]device, 0, len(specs))
	for _, s := range specs {
		devs = append(devs, device{
			dir:   filepath.Join(out, fmt.Sprintf("hwmon%d", len(devs))),
			chip:  s.chip,
			fans:  s.fans,
			temps: s.temps,
		})
	}
	return devs
}

func listPresets() {
	names := make([]string, 0, len(presets))
	for n := range presets {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Println("hwmonsim: built-in presets:")
	for _, n := range names {
		fmt.Printf("  %-10s %s\n", n, chipList(assignDirs("", presets[n])))
	}
}

func chipList(devices []device) string {
	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = d.chip
	}
	return strings.Join(names, ", ")
}

func findBoard(id string) (*hwdb.BoardCatalogEntry, error) {
	entries, err := hwdb.LoadBoardCatalog()
	if err != nil {
		return nil, fmt.Errorf("load board catalog: %w", err)
	}
	for _, e := range entries {
		if e.ID == id {
			return e, nil
		}
	}
	return nil, fmt.Errorf("no board id %q in the catalog (try --board list)", id)
}

func listBoards() {
	entries, err := hwdb.LoadBoardCatalog()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hwmonsim: load board catalog:", err)
		os.Exit(1)
	}
	ids := make([]string, 0, len(entries))
	byID := map[string]*hwdb.BoardCatalogEntry{}
	for _, e := range entries {
		ids = append(ids, e.ID)
		byID[e.ID] = e
	}
	sort.Strings(ids)
	fmt.Printf("hwmonsim: %d board profiles in the catalog:\n", len(ids))
	for _, id := range ids {
		e := byID[id]
		chip := e.PrimaryController.Chip
		if n := len(e.AdditionalControllers); n > 0 {
			chip = fmt.Sprintf("%s +%d", chip, n)
		}
		fmt.Printf("  %-48s %s\n", id, chip)
	}
}

// materialise writes one faithful device directory. pwm starts at 0 (off),
// enable at 1 (manual) so the device classifies as controllable immediately;
// the live loop takes over from there.
func materialise(dir, chip string, fans, temps int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeVal(filepath.Join(dir, "name"), chip); err != nil {
		return err
	}
	for i := 1; i <= fans; i++ {
		if err := writeVal(filepath.Join(dir, fmt.Sprintf("pwm%d", i)), "0"); err != nil {
			return err
		}
		if err := writeVal(filepath.Join(dir, fmt.Sprintf("pwm%d_enable", i)), "1"); err != nil {
			return err
		}
		if err := writeVal(filepath.Join(dir, fmt.Sprintf("fan%d_input", i)), "0"); err != nil {
			return err
		}
	}
	for i := 1; i <= temps; i++ {
		if err := writeVal(filepath.Join(dir, fmt.Sprintf("temp%d_input", i)), "40000"); err != nil {
			return err
		}
	}
	return nil
}

// run is the live model loop across every device: read the daemon's pwm/enable
// writes, recompute rpm + temperature, write them back, until a stop signal.
func run(devices []device, cfg config, stop <-chan os.Signal) {
	// Per-device spin-up hysteresis state, 1-indexed by fan.
	spinning := make([][]bool, len(devices))
	for i, d := range devices {
		spinning[i] = make([]bool, d.fans+1)
	}
	ticker := time.NewTicker(cfg.tick)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			fmt.Println("\nhwmonsim: stopped (tree left in place)")
			return
		case <-ticker.C:
			for di, d := range devices {
				if d.fans == 0 {
					// Temp-only device (NVMe / ACPI zone): no fans to model;
					// its temps keep their materialised value.
					continue
				}
				totalDuty := 0.0
				for i := 1; i <= d.fans; i++ {
					pwm := readInt(filepath.Join(d.dir, fmt.Sprintf("pwm%d", i)), 0)
					enable := readInt(filepath.Join(d.dir, fmt.Sprintf("pwm%d_enable", i)), 1)
					rpm := rpmFor(clampByte(pwm), enable, cfg, &spinning[di][i])
					_ = writeVal(filepath.Join(d.dir, fmt.Sprintf("fan%d_input", i)), strconv.Itoa(rpm))
					totalDuty += float64(rpm) / float64(cfg.maxRPM)
				}
				avgDuty := totalDuty / float64(d.fans)
				milliC := tempMilliC(avgDuty)
				for i := 1; i <= d.temps; i++ {
					_ = writeVal(filepath.Join(d.dir, fmt.Sprintf("temp%d_input", i)), strconv.Itoa(milliC))
				}
			}
		}
	}
}

// rpmFor maps a duty byte to RPM under the chosen model, honouring pwm_enable
// (only manual mode == 1 follows the duty; firmware/auto holds a baseline) and
// spin-up hysteresis (a stalled fan needs startPWM to begin, then stalls again
// only below stopPWM).
func rpmFor(pwm uint8, enable int, cfg config, spinning *bool) int {
	if enable != 1 {
		// Firmware/auto mode: a fixed baseline, independent of the duty
		// byte — mirrors a BIOS curve the daemon hasn't taken over.
		*spinning = true
		return cfg.minRPM + (cfg.maxRPM-cfg.minRPM)/3
	}
	p := int(pwm)
	switch cfg.model {
	case "linear":
		if p <= 0 {
			*spinning = false
			return 0
		}
		*spinning = true
		return cfg.minRPM + (cfg.maxRPM-cfg.minRPM)*p/255
	default: // "spinup"
		if *spinning {
			if p <= cfg.stopPWM {
				*spinning = false
				return 0
			}
		} else {
			if p < cfg.startPWM {
				return 0
			}
			*spinning = true
		}
		// Spinning: interpolate from minRPM at stopPWM to maxRPM at 255.
		span := 255 - cfg.stopPWM
		if span <= 0 {
			span = 1
		}
		over := p - cfg.stopPWM
		if over < 0 {
			over = 0
		}
		return cfg.minRPM + (cfg.maxRPM-cfg.minRPM)*over/span
	}
}

// tempMilliC maps average airflow fraction (0..1) to a temperature in
// milli-°C: ~75 °C with no airflow down to ~35 °C at full airflow.
func tempMilliC(avgDuty float64) int {
	if avgDuty < 0 {
		avgDuty = 0
	}
	if avgDuty > 1 {
		avgDuty = 1
	}
	const hot, cool = 75.0, 35.0
	c := hot - (hot-cool)*avgDuty
	return int(c * 1000)
}

func clampByte(n int) uint8 {
	if n < 0 {
		return 0
	}
	if n > 255 {
		return 255
	}
	return uint8(n)
}

func writeVal(path, val string) error {
	return os.WriteFile(path, []byte(val+"\n"), 0o644)
}

func readInt(path string, dflt int) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return dflt
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return dflt
	}
	return n
}
