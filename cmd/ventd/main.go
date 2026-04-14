package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/hwdiag"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/nvidia"
	setupmgr "github.com/ventd/ventd/internal/setup"
	"github.com/ventd/ventd/internal/watchdog"
	"github.com/ventd/ventd/internal/web"
)

// errRestart is returned by run() when a self-exec restart is requested
// (e.g. after the web setup wizard writes the initial config).
var errRestart = errors.New("restart")

func main() {
	if err := run(); err != nil {
		if errors.Is(err, errRestart) {
			// All defers in run() have fired (PWM restored, NVML shut down).
			// Replace the current process image with a fresh instance.
			if execErr := syscall.Exec(os.Args[0], os.Args, os.Environ()); execErr != nil {
				fmt.Fprintf(os.Stderr, "ventd: restart failed: %v\n", execErr)
				os.Exit(1)
			}
		}
		// At this point, run()'s defers have already fired (including wd.Restore),
		// so it is safe to os.Exit.
		fmt.Fprintf(os.Stderr, "ventd: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "/etc/ventd/config.yaml", "path to YAML config file")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	doSetup := flag.Bool("setup", false, "run interactive setup wizard, write initial config, then exit")
	flag.Parse()

	logger := buildLogger(*logLevel)

	if *doSetup {
		return runSetup(*configPath, logger)
	}

	logger.Info("ventd starting")

	cfg, err := config.Load(*configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load config: %w", err)
		}
		// No config yet — first-boot mode. Start with an empty config so the
		// web UI can serve the setup wizard.
		logger.Info("no config found, starting in first-boot mode", "path", *configPath)
		cfg = config.Empty()
	} else {
		logger.Info("config loaded", "path", *configPath, "controls", len(cfg.Controls))
	}

	// Generate a one-time setup token if no password is configured yet.
	// Printed clearly to stdout so the user can find it via journalctl.
	var setupToken string
	if cfg.Web.PasswordHash == "" {
		tok, tokErr := web.GenerateSetupToken()
		if tokErr != nil {
			return fmt.Errorf("generate setup token: %w", tokErr)
		}
		setupToken = tok
		logger.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		logger.Info("  Ventd — First Boot")
		logger.Info("  Open your browser to http://" + localIP() + ":9999")
		logger.Info("  Setup token: " + setupToken)
		logger.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	}

	// Probe and load the kernel module that exposes hwmon PWM channels.
	// Must run before NVML init and before resolveHwmonPaths so that sysfs
	// entries are present when the setup wizard or controllers need them.
	hwmon.AutoloadModules(logger)

	// NVML init is always attempted. The shim silently disables GPU
	// features when libnvidia-ml.so.1 is absent or nvmlInit_v2 fails, and
	// logs the outcome. Never fatal: hwmon fan control must keep working
	// either way. Shutdown is only scheduled when Init succeeded so we
	// don't release a refcount we didn't acquire.
	if err := nvidia.Init(logger); err == nil {
		defer nvidia.Shutdown()
	}

	// liveCfg is swapped atomically on SIGHUP. Controllers read it on every tick.
	var liveCfg atomic.Pointer[config.Config]
	liveCfg.Store(cfg)

	// Register all PWM paths with the watchdog before starting any controllers.
	wd := watchdog.New(logger)
	for _, fan := range cfg.Fans {
		wd.Register(fan.PWMPath, fan.Type)
	}
	// Always restore pwm_enable=2 when run() exits — covers graceful shutdown.
	// Controller panic recovery also calls wd.Restore() before returning an error.
	defer wd.Restore()

	// Calibration manager: persists results across restarts. The watchdog
	// is shared with controllers — calibrate registers each fan at sweep
	// start and deregisters on normal exit, so a daemon crash mid-sweep
	// still restores PWM via the daemon-exit Restore.
	cal := calibrate.New("/etc/ventd/calibration.json", logger, wd)

	// Process-wide hardware-diagnostics store. Tier 5: every subsystem that
	// detects a non-fatal condition (future calibration schema, missing
	// modules, Secure Boot, etc.) emits into this store and the web UI
	// surfaces it via /api/hwdiag.
	diagStore := hwdiag.NewStore()
	cal.SetDiagnosticStore(diagStore)

	// Resolve hwmon sysfs paths in case hwmonX indices changed since last boot.
	// Done after cal is created so stale keys in calibration.json can be remapped.
	resolveHwmonPaths(cfg, cal, logger)

	// Setup wizard manager: handles first-boot fan discovery and calibration via web UI.
	setupMgr := setupmgr.New(cal, logger)
	setupMgr.SetDiagnosticStore(diagStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// errCh is buffered to one slot per controller plus one for the web server.
	errCh := make(chan error, len(cfg.Controls)+1)
	var wg sync.WaitGroup

	// Tier 0.3 — hardware change detection. Watches /sys/class/hwmon for
	// add/remove at runtime via netlink uevents plus a 5-minute periodic
	// rescan safety net; emits a ComponentHardware diagnostic with a
	// "Re-run setup" button when topology changes. Read-only: never writes
	// to sysfs, never modprobes. Set VENTD_DISABLE_UEVENT=1 to fall back
	// to periodic-only (used for container environments where netlink is
	// blocked, and by Tier 0.3 validation to exercise the safety net).
	var watcherOpts []hwmon.Option
	if os.Getenv("VENTD_DISABLE_UEVENT") == "1" {
		watcherOpts = append(watcherOpts, hwmon.WithoutUevents())
	}
	watcher := hwmon.NewWatcher(diagStore, logger, watcherOpts...)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := watcher.Run(ctx); err != nil {
			logger.Warn("hwmon watcher exited", "err", err)
		}
	}()

	// restartCh is signalled by the web server after setup applies a new config.
	restartCh := make(chan struct{}, 1)

	// Start the web status server. It reads from &liveCfg on every request so
	// it always reflects the current configuration without restart.
	webSrv := web.New(ctx, &liveCfg, *configPath, logger, cal, setupMgr, restartCh, setupToken, diagStore)
	go func() {
		if err := webSrv.ListenAndServe(cfg.Web.Listen, cfg.Web.TLSCert, cfg.Web.TLSKey); err != nil {
			errCh <- fmt.Errorf("web server: %w", err)
		}
	}()

	// Only start controllers if there are controls defined (not first-boot).
	if len(cfg.Controls) > 0 {
		for _, ctrl := range cfg.Controls {
			fanCfg, err := resolveControl(cfg, ctrl)
			if err != nil {
				// No controllers started yet; wd.Restore() via defer is harmless.
				return fmt.Errorf("resolve control: %w", err)
			}

			c := controller.New(
				ctrl.Fan, ctrl.Curve,
				fanCfg.PWMPath, fanCfg.Type,
				&liveCfg, wd, cal, logger,
			)
			wg.Add(1)
			go func(c *controller.Controller) {
				defer wg.Done()
				if runErr := c.Run(ctx, cfg.PollInterval.Duration); runErr != nil {
					errCh <- runErr
				}
			}(c)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	for {
		select {
		case sig := <-sigCh:
			switch sig {
			case syscall.SIGHUP:
				newCfg, reloadErr := config.Load(*configPath)
				if reloadErr != nil {
					// Keep running with the current config — never crash on a bad reload.
					logger.Error("config reload failed, keeping current config", "err", reloadErr)
					continue
				}
				liveCfg.Store(newCfg)
				logger.Info("config reloaded",
					"poll_interval", newCfg.PollInterval.Duration,
					"controls", len(newCfg.Controls),
				)

			case syscall.SIGTERM, syscall.SIGINT:
				logger.Info("shutdown signal received", "signal", sig)
				cancel()
				wg.Wait()
				// wd.Restore() runs via defer.
				return nil
			}

		case ctrlErr := <-errCh:
			// A controller goroutine failed (panic or startup error).
			// Cancel all other controllers and exit non-zero so systemd restarts us.
			logger.Error("controller failure, initiating emergency shutdown", "err", ctrlErr)
			cancel()
			wg.Wait()
			// wd.Restore() runs via defer.
			return ctrlErr

		case <-restartCh:
			logger.Info("restarting to apply new configuration")
			cancel()
			wg.Wait()
			// Gracefully drain in-flight HTTP responses and release the port
			// before exec, so the new process can bind immediately.
			webSrv.Shutdown()
			// wd.Restore() and nvidia.Shutdown() run via defer before main() calls Exec.
			return errRestart
		}
	}
}

// resolveHwmonPaths fixes up any hwmon sysfs paths that have moved due to
// hwmonX renumbering across reboots. Uses the HwmonDevice field (stable
// /sys/devices/... path) stored in the config to find the current hwmonX dir.
// Remaps calibration cache keys so stored results survive the renumber.
func resolveHwmonPaths(cfg *config.Config, cal *calibrate.Manager, logger *slog.Logger) {
	for i := range cfg.Fans {
		f := &cfg.Fans[i]
		if f.Type != "hwmon" || f.HwmonDevice == "" {
			continue
		}
		if resolved, changed := hwmon.ResolvePath(f.PWMPath, f.HwmonDevice); changed {
			logger.Info("hwmon path moved, updating", "fan", f.Name, "old", f.PWMPath, "new", resolved)
			cal.RemapKey(f.PWMPath, resolved)
			f.PWMPath = resolved
		}
		if f.RPMPath != "" {
			if resolved, changed := hwmon.ResolvePath(f.RPMPath, f.HwmonDevice); changed {
				f.RPMPath = resolved
			}
		}
	}
	for i := range cfg.Sensors {
		s := &cfg.Sensors[i]
		if s.Type != "hwmon" || s.HwmonDevice == "" {
			continue
		}
		if resolved, changed := hwmon.ResolvePath(s.Path, s.HwmonDevice); changed {
			logger.Info("hwmon path moved, updating", "sensor", s.Name, "old", s.Path, "new", resolved)
			s.Path = resolved
		}
	}
}

// resolveControl looks up the Fan for a Control definition.
// Validation has already confirmed all names exist, so missing entries here
// indicate a programmer error.
func resolveControl(cfg *config.Config, ctrl config.Control) (config.Fan, error) {
	for _, f := range cfg.Fans {
		if f.Name == ctrl.Fan {
			return f, nil
		}
	}
	return config.Fan{}, fmt.Errorf("fan %q not found (should have been caught by validation)", ctrl.Fan)
}

// localIP returns the machine's preferred outbound IP address.
// It uses a UDP "connect" (no packets are sent) to pick the interface
// that would be used to reach the internet, which is the address a user
// on the same LAN would type into their browser.
func localIP() string {
	conn, err := net.Dial("udp4", "8.8.8.8:53")
	if err != nil {
		return "<this-machine-ip>"
	}
	defer func() { _ = conn.Close() }()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func buildLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	// Write to stdout. systemd captures this and routes it to journald.
	// View with: journalctl -u ventd -f
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}
