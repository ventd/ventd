// hwmonsim materializes a faithful, *live* synthetic hwmon tree so ventd can be
// run end-to-end against fake hardware. Point the daemon at it with
// VENTD_HWMON_ROOT=<dir> (see internal/hwmon.RootOverrideEnv) and the whole
// stack — enumeration, the control loop, calibration sweeps, the polarity
// probe, the web UI, doctor — drives these files instead of real fans.
//
// It is two things in one process:
//
//  1. A materialiser: writes one hwmonN directory with `name`, and per fan a
//     pwmN / pwmN_enable / fanN_input triple plus a couple of tempN_input
//     sensors — exactly the shape internal/hwmon.classifyDevice recognises as a
//     controllable (ClassPrimary) device.
//
//  2. A live model: every tick it reads back the pwmN / pwmN_enable files the
//     daemon writes and recomputes fanN_input (RPM) and tempN_input from a
//     simple but monotonic thermal/aerodynamic model — so a calibration sweep
//     sees RPM rise with PWM, the polarity probe sees a real stop threshold,
//     and smart mode sees temperature fall as airflow rises. A static tree
//     (--once) only exercises enumeration and the UI; the live loop is what
//     makes control/calibration meaningful.
//
// This is dev tooling, not production code — no RULE bindings.
//
// Usage:
//
//	go run ./tools/hwmonsim --out /tmp/vsim          # 3 fans, live, nct6687
//	go run ./tools/hwmonsim --out /tmp/vsim --fans 5 --model spinup
//	go run ./tools/hwmonsim --out /tmp/vsim --once    # materialise and exit
//	VENTD_HWMON_ROOT=/tmp/vsim ./ventd --config ...   # drive it
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	out      string
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

func main() {
	cfg := config{}
	flag.StringVar(&cfg.out, "out", "", "directory to materialise the synthetic hwmon tree in (required)")
	flag.IntVar(&cfg.fans, "fans", 3, "number of controllable pwm fans")
	flag.IntVar(&cfg.temps, "temps", 2, "number of temperature sensors")
	flag.StringVar(&cfg.chip, "chip", "nct6687", "hwmon `name` value (must not be \"nvidia\")")
	flag.IntVar(&cfg.maxRPM, "max-rpm", 2200, "RPM at full duty")
	flag.IntVar(&cfg.minRPM, "min-rpm", 500, "RPM the instant the fan spins")
	flag.IntVar(&cfg.stopPWM, "stop-pwm", 25, "duty at/below which the fan stalls (0-255)")
	flag.IntVar(&cfg.startPWM, "start-pwm", 40, "duty a stalled fan needs to start (0-255)")
	flag.StringVar(&cfg.model, "model", "spinup", "rpm model: linear | spinup")
	flag.DurationVar(&cfg.tick, "tick", 200*time.Millisecond, "model update cadence")
	flag.BoolVar(&cfg.once, "once", false, "materialise the tree and exit (no live loop)")
	flag.Parse()

	if cfg.out == "" {
		fmt.Fprintln(os.Stderr, "hwmonsim: --out is required")
		flag.Usage()
		os.Exit(2)
	}
	if cfg.chip == "nvidia" {
		fmt.Fprintln(os.Stderr, "hwmonsim: --chip nvidia would be skipped by the enumerator; pick another name")
		os.Exit(2)
	}
	if cfg.fans < 1 {
		cfg.fans = 1
	}

	dev := filepath.Join(cfg.out, "hwmon0")
	if err := materialise(dev, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "hwmonsim: materialise:", err)
		os.Exit(1)
	}
	fmt.Printf("hwmonsim: materialised %d-fan %q device at %s\n", cfg.fans, cfg.chip, dev)
	fmt.Printf("hwmonsim: drive it with:  VENTD_HWMON_ROOT=%s ./ventd ...\n", cfg.out)
	if cfg.once {
		return
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	fmt.Printf("hwmonsim: live model running (model=%s, tick=%s) — Ctrl-C to stop\n", cfg.model, cfg.tick)
	run(dev, cfg, stop)
}

// materialise writes the faithful directory. pwm starts at 0 (off), enable at 1
// (manual) so the device classifies as controllable immediately; the live loop
// takes over from there.
func materialise(dev string, cfg config) error {
	if err := os.MkdirAll(dev, 0o755); err != nil {
		return err
	}
	if err := writeVal(filepath.Join(dev, "name"), cfg.chip); err != nil {
		return err
	}
	for i := 1; i <= cfg.fans; i++ {
		if err := writeVal(filepath.Join(dev, fmt.Sprintf("pwm%d", i)), "0"); err != nil {
			return err
		}
		if err := writeVal(filepath.Join(dev, fmt.Sprintf("pwm%d_enable", i)), "1"); err != nil {
			return err
		}
		if err := writeVal(filepath.Join(dev, fmt.Sprintf("fan%d_input", i)), "0"); err != nil {
			return err
		}
	}
	for i := 1; i <= cfg.temps; i++ {
		if err := writeVal(filepath.Join(dev, fmt.Sprintf("temp%d_input", i)), "40000"); err != nil {
			return err
		}
	}
	return nil
}

// run is the live model loop: read the daemon's pwm/enable writes, recompute
// rpm + temperature, write them back, until a stop signal arrives.
func run(dev string, cfg config, stop <-chan os.Signal) {
	spinning := make([]bool, cfg.fans+1) // 1-indexed; hysteresis state per fan
	ticker := time.NewTicker(cfg.tick)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			fmt.Println("\nhwmonsim: stopped (tree left in place)")
			return
		case <-ticker.C:
			totalDuty := 0.0
			for i := 1; i <= cfg.fans; i++ {
				pwm := readInt(filepath.Join(dev, fmt.Sprintf("pwm%d", i)), 0)
				enable := readInt(filepath.Join(dev, fmt.Sprintf("pwm%d_enable", i)), 1)
				rpm := rpmFor(clampByte(pwm), enable, cfg, &spinning[i])
				_ = writeVal(filepath.Join(dev, fmt.Sprintf("fan%d_input", i)), strconv.Itoa(rpm))
				totalDuty += float64(rpm) / float64(cfg.maxRPM)
			}
			// Temperature: more average airflow → cooler. Monotonic and
			// bounded so a sweep produces a clean temp-vs-pwm curve.
			avgDuty := totalDuty / float64(cfg.fans)
			milliC := tempMilliC(avgDuty)
			for i := 1; i <= cfg.temps; i++ {
				_ = writeVal(filepath.Join(dev, fmt.Sprintf("temp%d_input", i)), strconv.Itoa(milliC))
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
