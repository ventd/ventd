package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// StartupOptions tune the retry behaviour of LoadForStartup. Zero
// values mean "use the package default".
type StartupOptions struct {
	// Timeout is the upper bound on how long LoadForStartup waits for
	// a transient hwmon race to clear (ErrHwmonDeviceNotReady).
	// Zero disables the retry loop: one attempt, then surface the error.
	Timeout time.Duration
	// PollInterval is the sleep between retry attempts. Zero → default.
	PollInterval time.Duration
}

// Default startup retry parameters. Tuned against phoenix-MS-7D25's
// cold-boot behaviour: udev typically completes its second Super-I/O
// enumeration within ~2s of boot, with a long tail out to ~10s on
// slower hardware. A 30s deadline is well above the observed tail
// while short enough that a genuinely misconfigured hwmon_device still
// fails fast enough for the operator to notice.
const (
	DefaultStartupTimeout      = 30 * time.Second
	DefaultStartupPollInterval = 500 * time.Millisecond
)

// LoadForStartup resolves the config at path for daemon startup. It
// returns a three-value tuple that separates the three structurally
// distinct outcomes cmd/ventd has to reason about:
//
//   - (empty cfg, firstBoot=true, err=nil)
//     No config file on disk. Caller should start the daemon in
//     setup-wizard (first-boot) mode.
//
//   - (cfg, firstBoot=false, err=nil)
//     Config loaded and hwmon paths resolved cleanly.
//
//   - (nil, firstBoot=false, err=non-nil)
//     Config file exists but cannot be loaded. Caller must exit
//     non-zero so systemd restarts us (Restart=on-failure,
//     RestartSec=1s). Never fall back to first-boot mode on this path.
//
// First-boot detection is done via os.Stat on the config path before
// Load() is called. Before this helper existed, cmd/ventd used
// errors.Is(Load's err, os.ErrNotExist) as the first-boot predicate,
// which silently matched on transient hwmon_device ENOENT from
// EvalSymlinks — causing cold-boot races to wipe the operator's
// setup-wizard state. See issue #103.
//
// On ErrHwmonDeviceNotReady, LoadForStartup retries every
// opts.PollInterval up to opts.Timeout. Any other error (parse,
// schema, permanent resolver failure) bypasses the retry loop and
// surfaces immediately — the whole point of the gate is that we know
// the config is there, so waiting on anything but udev is pointless.
//
// SIGHUP reload must NOT call this helper — retry semantics only make
// sense at startup. Runtime callers keep calling Load() directly and
// treat every error as a reload failure.
func LoadForStartup(path string, opts StartupOptions) (*Config, bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Empty(), true, nil
		}
		return nil, false, fmt.Errorf("stat config %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, false, fmt.Errorf("config %s is not a regular file", path)
	}

	if opts.PollInterval <= 0 {
		opts.PollInterval = DefaultStartupPollInterval
	}

	start := time.Now()
	deadline := start.Add(opts.Timeout)
	var lastErr error
	for {
		cfg, err := Load(path)
		if err == nil {
			return cfg, false, nil
		}
		if !errors.Is(err, ErrHwmonDeviceNotReady) {
			return nil, false, fmt.Errorf("load config %s: %w", path, err)
		}
		lastErr = err
		if opts.Timeout <= 0 || !time.Now().Before(deadline) {
			return nil, false, fmt.Errorf("startup: hwmon not ready after %s: %w", time.Since(start).Round(time.Millisecond), lastErr)
		}
		time.Sleep(opts.PollInterval)
	}
}
