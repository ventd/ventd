package platformprofile

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Controller is the active driver: poll loop reads hardware + live inputs,
// asks the Selector for a profile, and writes it to sysfs. Hysteresis
// prevents thrashing. External writes (operator manually echoes a profile
// into the sysfs file) are detected and respected with a back-off window.
type Controller struct {
	logger   *slog.Logger
	selector *Selector
	store    *LearningStore
	hw       HardwareSummary

	pollInterval         time.Duration
	minDwell             time.Duration
	backoffAfterExternal time.Duration

	mu              sync.Mutex
	lastWritten     string
	lastSeenCurrent string // for external-write detection between ticks
	lastSwitchAt    time.Time
	externalSeenAt  time.Time

	// Live input readers (swappable for tests).
	tempReader  func() (float64, error)
	rpmReader   func() (int, error)
	loadReader  func() (float64, error)
	powerReader func() (float64, error)
	snapReader  func() (*Snapshot, error)
	writeFn     func(profile string) error
}

// ControllerOptions packages caller-supplied dependencies.
type ControllerOptions struct {
	Logger               *slog.Logger
	Selector             *Selector
	Store                *LearningStore
	Hardware             HardwareSummary
	PollInterval         time.Duration
	MinDwell             time.Duration
	BackoffAfterExternal time.Duration

	TempReader  func() (float64, error)
	RPMReader   func() (int, error)
	LoadReader  func() (float64, error)
	PowerReader func() (float64, error)
	SnapReader  func() (*Snapshot, error)
	WriteFn     func(profile string) error
}

// NewController wires a controller. Callers populate readers from the
// production helpers below (DefaultTempReader etc.) or test fakes.
func NewController(opts ControllerOptions) *Controller {
	if opts.PollInterval == 0 {
		opts.PollInterval = 15 * time.Second
	}
	if opts.MinDwell == 0 {
		opts.MinDwell = 60 * time.Second
	}
	if opts.BackoffAfterExternal == 0 {
		opts.BackoffAfterExternal = 10 * time.Minute
	}
	if opts.SnapReader == nil {
		opts.SnapReader = Read
	}
	if opts.WriteFn == nil {
		opts.WriteFn = Write
	}
	return &Controller{
		logger:               opts.Logger,
		selector:             opts.Selector,
		store:                opts.Store,
		hw:                   opts.Hardware,
		pollInterval:         opts.PollInterval,
		minDwell:             opts.MinDwell,
		backoffAfterExternal: opts.BackoffAfterExternal,
		tempReader:           opts.TempReader,
		rpmReader:            opts.RPMReader,
		loadReader:           opts.LoadReader,
		powerReader:          opts.PowerReader,
		snapReader:           opts.SnapReader,
		writeFn:              opts.WriteFn,
	}
}

// Run blocks until ctx is cancelled, polling every PollInterval and
// applying the selector's choice via writeFn when allowed by hysteresis +
// external-write back-off rules.
func (c *Controller) Run(ctx context.Context) {
	c.logger.Info("platform_profile controller starting",
		"cpu_model", c.hw.CPUModel,
		"tjmax_c", c.hw.TJmaxC,
		"tdp_w", c.hw.TDPWatts,
		"fan_max_rpm", c.hw.FanMaxRPM,
		"chassis_class", c.hw.ChassisClass,
		"poll_interval", c.pollInterval.String(),
		"min_dwell", c.minDwell.String())

	quietest, mid, hottest := c.selector.Anchors()
	c.logger.Info("platform_profile selector anchors",
		"quietest", quietest, "mid", mid, "hottest", hottest)

	t := time.NewTicker(c.pollInterval)
	defer t.Stop()
	persistT := time.NewTicker(5 * time.Minute)
	defer persistT.Stop()

	c.tick(ctx) // immediate first tick

	for {
		select {
		case <-ctx.Done():
			if err := c.store.Persist(); err != nil {
				c.logger.Warn("platform_profile learning store: persist on shutdown failed", "err", err)
			}
			return
		case <-t.C:
			c.tick(ctx)
		case <-persistT.C:
			if err := c.store.Persist(); err != nil {
				c.logger.Warn("platform_profile learning store: periodic persist failed", "err", err)
			}
		}
	}
}

// tick is one observation+decision+(optional)write cycle.
func (c *Controller) tick(ctx context.Context) {
	snap, err := c.snapReader()
	if err != nil || !snap.Present {
		return
	}

	in := c.readInputs()
	d := c.selector.Pick(in)

	// External-write detection: track the live profile across ticks. If it
	// changed between two ticks AND ventd wasn't the one that wrote (the
	// new value doesn't match lastWritten), an external tool (or the user)
	// changed the profile. Honour that for backoffAfterExternal and
	// observe under that profile until the window expires.
	c.mu.Lock()
	prevSeen := c.lastSeenCurrent
	c.lastSeenCurrent = snap.Current
	external := prevSeen != "" && snap.Current != "" && snap.Current != prevSeen && snap.Current != c.lastWritten
	if external {
		c.externalSeenAt = time.Now()
		c.logger.Info("platform_profile: external write detected — backing off auto-control",
			"observed", snap.Current, "previous", prevSeen,
			"we_last_wrote", c.lastWritten, "backoff", c.backoffAfterExternal.String())
	}
	inBackoff := !c.externalSeenAt.IsZero() && time.Since(c.externalSeenAt) < c.backoffAfterExternal
	dwellMet := time.Since(c.lastSwitchAt) >= c.minDwell
	prevWritten := c.lastWritten
	c.mu.Unlock()

	// Always observe — even when not writing — so the learning store
	// accumulates data across profile transitions made externally.
	c.store.Observe(snap.Current, in, d.PressureScore, c.hw.TJmaxC)

	if inBackoff {
		c.logger.Debug("platform_profile: in external-write backoff, observing only",
			"current", snap.Current, "would_pick", d.Profile, "pressure", d.PressureScore)
		return
	}

	// No change needed if selector's choice matches the live profile.
	if d.Profile == snap.Current {
		return
	}

	if !dwellMet {
		c.logger.Debug("platform_profile: dwell not yet met, skipping switch",
			"current", snap.Current, "would_pick", d.Profile, "pressure", d.PressureScore,
			"dwell_remaining", (c.minDwell - time.Since(c.lastSwitchAt)).String())
		return
	}

	if err := c.writeFn(d.Profile); err != nil {
		c.logger.Warn("platform_profile: write failed",
			"target", d.Profile, "current", snap.Current, "err", err.Error())
		return
	}

	c.mu.Lock()
	c.lastWritten = d.Profile
	c.lastSwitchAt = time.Now()
	c.mu.Unlock()

	c.logger.Info("platform_profile: switched",
		"from", snap.Current, "to", d.Profile, "pressure", d.PressureScore,
		"reason", d.Reason, "temp_c", in.CurrentTempC, "rpm", in.CurrentRPM,
		"load_pct", in.CPULoadPct, "tdp_w_now", in.CurrentTDPWatts)
	c.store.RecordTransition(prevWritten, d.Profile, d.PressureScore, d.Reason)
}

func (c *Controller) readInputs() Inputs {
	var in Inputs
	if c.tempReader != nil {
		if v, err := c.tempReader(); err == nil {
			in.CurrentTempC = v
		}
	}
	if c.rpmReader != nil {
		if v, err := c.rpmReader(); err == nil {
			in.CurrentRPM = v
		}
	}
	if c.loadReader != nil {
		if v, err := c.loadReader(); err == nil {
			in.CPULoadPct = v
		}
	}
	if c.powerReader != nil {
		if v, err := c.powerReader(); err == nil {
			in.CurrentTDPWatts = v
		}
	}
	return in
}

// ----- Production helpers below — best-effort readers for the live inputs.

// DefaultTempReader returns a reader that scans /sys/class/hwmon for a
// "coretemp" or "k10temp" chip and returns its temp1_input in degrees C.
// Returns the first matching chip — fine for laptops (single package).
func DefaultTempReader() func() (float64, error) {
	return func() (float64, error) {
		root := "/sys/class/hwmon"
		entries, err := os.ReadDir(root)
		if err != nil {
			return 0, err
		}
		for _, e := range entries {
			nameBytes, err := os.ReadFile(filepath.Join(root, e.Name(), "name"))
			if err != nil {
				continue
			}
			name := strings.TrimSpace(string(nameBytes))
			if name != "coretemp" && name != "k10temp" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, e.Name(), "temp1_input"))
			if err != nil {
				continue
			}
			milliC, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				continue
			}
			return float64(milliC) / 1000.0, nil
		}
		return 0, errors.New("no coretemp/k10temp chip found")
	}
}

// DefaultLoadReader returns a reader for the 1-minute load average as a
// percentage of GOMAXPROCS (or os.NumCPU).
func DefaultLoadReader() func() (float64, error) {
	ncpu := numCPU()
	return func() (float64, error) {
		data, err := os.ReadFile("/proc/loadavg")
		if err != nil {
			return 0, err
		}
		fields := strings.Fields(string(data))
		if len(fields) == 0 {
			return 0, errors.New("empty /proc/loadavg")
		}
		load1, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return 0, err
		}
		if ncpu <= 0 {
			return load1 * 100, nil
		}
		return (load1 / float64(ncpu)) * 100, nil
	}
}

// DefaultPowerReader returns a reader for the live RAPL package power
// (averaged via incremental energy_uj sampling). Best-effort: if RAPL is
// unavailable returns 0, nil so the selector falls back to other inputs.
func DefaultPowerReader() func() (float64, error) {
	const energyPath = "/sys/class/powercap/intel-rapl/intel-rapl:0/energy_uj"
	var (
		lastUJ uint64
		lastAt time.Time
		warmed bool
	)
	return func() (float64, error) {
		raw, err := os.ReadFile(energyPath)
		if err != nil {
			return 0, nil // RAPL absent; not an error
		}
		uj, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil {
			return 0, nil
		}
		now := time.Now()
		defer func() { lastUJ = uj; lastAt = now }()
		if !warmed {
			warmed = true
			return 0, nil
		}
		dt := now.Sub(lastAt).Seconds()
		if dt <= 0 {
			return 0, nil
		}
		// Handle wrap (uint64 counter, microJoules).
		var deltaUJ uint64
		if uj >= lastUJ {
			deltaUJ = uj - lastUJ
		} else {
			// counter wrap-around: assume it wrapped once
			deltaUJ = (^uint64(0)) - lastUJ + uj + 1
		}
		watts := float64(deltaUJ) / 1_000_000.0 / dt
		return watts, nil
	}
}

// numCPU is os.NumCPU with a sane minimum so divide-by-zero is impossible.
func numCPU() int {
	n := 1
	if v := osNumCPU(); v > 0 {
		n = v
	}
	return n
}
