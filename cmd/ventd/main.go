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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/hal"
	halasahi "github.com/ventd/ventd/internal/hal/asahi"
	halcrosec "github.com/ventd/ventd/internal/hal/crosec"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
	halipmi "github.com/ventd/ventd/internal/hal/ipmi"
	halnvml "github.com/ventd/ventd/internal/hal/nvml"
	halpwmsys "github.com/ventd/ventd/internal/hal/pwmsys"
	"github.com/ventd/ventd/internal/hwdiag"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/sdnotify"
	setupmgr "github.com/ventd/ventd/internal/setup"
	"github.com/ventd/ventd/internal/watchdog"
	"github.com/ventd/ventd/internal/web"
)

// Build-time metadata populated by -ldflags -X from .goreleaser.yml.
// Defaults keep `go build` and `go run` producing a sensible string.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// errRestart is returned by run() when a self-exec restart is requested
// (e.g. after the web setup wizard writes the initial config).
var errRestart = errors.New("restart")

func main() {
	if err := run(); err != nil {
		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
		if errors.Is(err, errRestart) {
			// All defers in run() have fired (PWM restored, NVML shut down).
			// Replace the current process image with a fresh instance.
			if execErr := syscall.Exec(os.Args[0], os.Args, os.Environ()); execErr != nil {
				logger.Error("ventd: restart failed", "err", execErr)
				os.Exit(1)
			}
		}
		// At this point, run()'s defers have already fired (including wd.Restore),
		// so it is safe to os.Exit.
		logger.Error("ventd: fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "/etc/ventd/config.yaml", "path to YAML config file")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	doSetup := flag.Bool("setup", false, "run interactive setup wizard, write initial config, then exit")
	doProbeModules := flag.Bool("probe-modules", false, "probe and load hwmon kernel modules, persist via /etc/modules-load.d, then exit (run by the installer at root, not by the sandboxed service)")
	doRecover := flag.Bool("recover", false, "reset every pwm_enable to 1 (automatic mode) and exit; runs as the OnFailure= oneshot if the main daemon exits unexpectedly")
	doRescan := flag.Bool("rescan-hwmon", false, "rerun hwmon module probing for hardware added since the original install (modprobe + persist /etc/modules-load.d), then exit")
	doListFans := flag.Bool("list-fans-probe", false, "validation helper: enumerate every hwmon device, classify, probe writability, and print PASS/FAIL")
	doPreflight := flag.Bool("preflight-check", false, "validation helper: run preflight against a synthetic DriverNeed and print the Reason as JSON")
	preflightMaxKernel := flag.String("preflight-max-kernel", "", "with --preflight-check: synthetic MaxSupportedKernel ceiling (e.g. 6.6)")
	showVersion := flag.Bool("version", false, "print version information and exit")
	versionJSON := flag.Bool("json", false, "with --version: emit JSON instead of plain text")
	flag.Parse()

	// --version must short-circuit before any logging or subsystem init so it
	// prints cleanly on stdout and never touches hwmon / NVML. Exits 0 by
	// returning nil from run(); main() then returns without os.Exit.
	if *showVersion {
		return printVersion(os.Stdout, *versionJSON)
	}

	logger := buildLogger(*logLevel)

	if *doProbeModules {
		// Privileged one-shot invoked from scripts/install.sh and
		// scripts/postinstall.sh. Runs as root, outside the systemd
		// sandbox, so it can modprobe and write /etc/modules-load.d.
		// The long-running daemon NEVER does this work — under
		// ProtectKernelModules=yes those operations would EPERM.
		hwmon.AutoloadModules(logger)
		return nil
	}

	if *doRescan {
		// Same code path as --probe-modules; named differently for
		// operator clarity ("I added new hardware, what do I do?").
		// Idempotent — re-running over an already-loaded module is
		// a fast no-op fast-path.
		hwmon.AutoloadModules(logger)
		return nil
	}

	if *doRecover {
		// Best-effort one-shot fired by ventd-recover.service when
		// the main daemon exits unexpectedly (SIGKILL, OOM,
		// hardware-watchdog timeout, panic that escapes the defer
		// chain). Walks /sys/class/hwmon and writes 1 to every
		// pwm<N>_enable to hand control back to the BIOS/firmware.
		// Never fails — exit 0 keeps the OnFailure= chain from
		// retriggering.
		hwmon.RecoverAllPWM(logger)
		return nil
	}

	if *doListFans {
		// Folded-in former cmd/list-fans-probe. Returns non-zero on
		// regression vs the pre-Tier-2 enumeration; the validation
		// matrix uses the exit code as a PASS/FAIL gate.
		os.Exit(runListFansProbe())
	}

	if *doPreflight {
		// Folded-in former cmd/preflight-check. Emits JSON; exit 0
		// always unless encoder fails. The Reason field is what the
		// validation matrix asserts on.
		os.Exit(runPreflightCheck(*preflightMaxKernel))
	}

	if *doSetup {
		return runSetup(*configPath, logger)
	}

	logger.Info("ventd starting")
	warnIfUnconfined(logger)

	// LoadForStartup discriminates three outcomes: first-boot (no config
	// file), successful load, or a real startup failure. The helper's
	// os.Stat gate is the critical fix for issue #103: before it, cmd/ventd
	// used errors.Is(err, os.ErrNotExist) on Load's return, which also
	// matched on transient hwmon_device ENOENT and silently dropped the
	// daemon into first-boot mode on cold-boot udev races. The bounded
	// retry inside LoadForStartup absorbs that race in-band so systemd's
	// Restart=on-failure stays reserved for real startup failures.
	cfg, firstBoot, err := config.LoadForStartup(*configPath, config.StartupOptions{
		Timeout: config.DefaultStartupTimeout,
	})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if firstBoot {
		logger.Info("no config found, starting in first-boot mode", "path", *configPath)
	} else {
		logger.Info("config loaded", "path", *configPath, "controls", len(cfg.Controls))
	}

	// On first-boot, auto-generate a self-signed cert under the config dir
	// so the setup wizard (passwords + tokens in flight) runs over TLS
	// out of the box. If cert gen genuinely cannot succeed, fall back to a
	// loopback-only bind — never let the daemon serve the setup wizard
	// (admin password in flight) over plaintext on the LAN.
	if firstBoot && !cfg.Web.TLSEnabled() {
		dir := filepath.Dir(*configPath)
		if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
			logger.Warn("first-boot: cannot create config dir for TLS cert",
				"dir", dir, "err", mkErr,
				"remediation", "create "+dir+" writable by the ventd user, or pre-provision tls.crt/tls.key")
		}
		certPath := filepath.Join(dir, "tls.crt")
		keyPath := filepath.Join(dir, "tls.key")
		fp, genErr := web.EnsureSelfSignedCert(certPath, keyPath, logger)
		if genErr != nil {
			_, port, splitErr := net.SplitHostPort(cfg.Web.Listen)
			if splitErr != nil {
				port = "9999"
			}
			fallback := net.JoinHostPort("127.0.0.1", port)
			logger.Warn("first-boot: self-signed cert generation failed; restricting to loopback-only bind",
				"configured_listen", cfg.Web.Listen,
				"fallback_listen", fallback,
				"err", genErr,
				"remediation", "pre-provision tls.crt/tls.key under "+dir+", or reach the wizard via `ssh -L "+port+":localhost:"+port+" <host>`")
			cfg.Web.Listen = fallback
		} else {
			cfg.Web.TLSCert = certPath
			cfg.Web.TLSKey = keyPath
			logger.Info("first-boot: TLS enabled with self-signed cert", "sha256", fp)
		}
	}

	// Enforce the transport-security guard unconditionally. First-boot is
	// precisely when the admin password crosses the wire — it must run
	// over TLS (auto-gen above) or be constrained to loopback (fallback above).
	if err := cfg.Web.RequireTransportSecurity(); err != nil {
		return err
	}

	// Generate a one-time setup token if no password is configured yet.
	// The token is a first-boot credential: anyone who can read it can set
	// the admin password. Never write the plaintext to slog/journald — it
	// would be retrievable by any journal reader. Instead: print to the
	// controlling TTY when present (operator running ventd manually), and
	// always drop a 0600 file under /run/ventd for systemd deployments.
	var setupToken string
	if cfg.Web.PasswordHash == "" {
		tok, tokErr := web.GenerateSetupToken()
		if tokErr != nil {
			return fmt.Errorf("generate setup token: %w", tokErr)
		}
		setupToken = tok
		publishSetupToken(setupToken, cfg.Web.Listen, cfg.Web.TLSEnabled(), logger)
	}

	// Diagnose hwmon state at startup. This is READ-ONLY — the daemon
	// runs under ProtectKernelModules=yes and cannot modprobe. Kernel
	// modules are loaded by `ventd --probe-modules`, invoked once by
	// scripts/install.sh at install time and persisted via
	// /etc/modules-load.d/ventd.conf for subsequent boots.
	//
	// If no PWM channels are visible at startup, log a clear remediation
	// message rather than failing — the setup wizard's hardware
	// diagnostics will surface the same finding to the operator.
	hwmon.DiagnoseHwmon(logger)

	// NVML init is always attempted. The shim silently disables GPU
	// features when libnvidia-ml.so.1 is absent or nvmlInit_v2 fails, and
	// logs the outcome. Never fatal: hwmon fan control must keep working
	// either way. Shutdown is only scheduled when Init succeeded so we
	// don't release a refcount we didn't acquire.
	if err := nvidia.Init(logger); err == nil {
		defer nvidia.Shutdown()
	}

	// Register fan backends with the HAL registry. The controllers and
	// watchdog construct their own per-instance backends for scoped
	// logging; the registry entries here exist so hal.Enumerate /
	// hal.Resolve can drive Phase 2 features (IPMI / liquidctl / cros_ec
	// / pwmsys / asahi inventory in the web UI, diagnostics probes) off
	// a single source of truth.
	hal.Register(halasahi.BackendName, halasahi.NewBackend(logger))
	hal.Register(halhwmon.BackendName, halhwmon.NewBackend(logger))
	hal.Register(halnvml.BackendName, halnvml.NewBackend(logger))
	hal.Register(halpwmsys.BackendName, halpwmsys.NewBackend(logger))
	hal.Register(halipmi.BackendName, halipmi.NewBackend(logger))
	hal.Register(halcrosec.BackendName, halcrosec.NewBackend(logger))
	if channels, err := hal.Enumerate(context.Background()); err != nil {
		logger.Warn("hal: initial enumerate failed", "err", err)
	} else {
		logger.Info("hal: enumerated fan backends", "channels", len(channels))
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(sigCh)

	return runDaemon(context.Background(), cfg, *configPath, logger, setupToken, sigCh)
}

// runDaemon runs the daemon lifecycle: watchdog registration, hwmon watcher,
// web server, per-fan controllers, and the shutdown-coordinating select loop.
// It returns:
//   - nil on SIGTERM / SIGINT (or ctx cancellation when sigCh is nil)
//   - errRestart when the web server signals a restart via restartCh
//   - a wrapped controller error when a control goroutine fails
//
// Passing nil for sigCh is supported: a receive on a nil channel blocks
// forever, so the signal case never fires — used by the integration test
// so the test drives shutdown via ctx cancellation alone.
func runDaemon(
	parentCtx context.Context,
	cfg *config.Config,
	configPath string,
	logger *slog.Logger,
	setupToken string,
	sigCh <-chan os.Signal,
) error {
	// liveCfg is swapped atomically on SIGHUP. Controllers read it on every tick.
	var liveCfg atomic.Pointer[config.Config]
	liveCfg.Store(cfg)

	// Register all PWM paths with the watchdog before starting any controllers.
	wd := watchdog.New(logger)
	for _, fan := range cfg.Fans {
		wd.Register(fan.PWMPath, fan.Type)
	}
	// Always restore pwm_enable=2 when runDaemon exits — covers graceful shutdown.
	// Controller panic recovery also calls wd.Restore() before returning an error.
	defer wd.Restore()

	// Top-level panic recover. The controller package's own
	// per-tick recover catches in-loop panics; this guard catches
	// anything that escapes (e.g. panic in a goroutine outside a
	// controller, library code, runtime). wd.Restore via the defer
	// above is already armed — this re-raises the panic only after
	// PWM has been restored, so the systemd OnFailure= oneshot also
	// fires.
	defer func() {
		if r := recover(); r != nil {
			logger.Error("ventd: top-level panic recovered, restoring PWM and re-raising",
				"panic", fmt.Sprintf("%v", r))
			// wd.Restore() runs via the defer below us — order
			// matters: this defer is declared later, so it executes
			// FIRST and sets up the panic state, then wd.Restore
			// runs, then we re-panic so the process exits non-zero
			// and systemd's OnFailure= triggers ventd-recover as
			// the belt to our braces.
			panic(r)
		}
	}()

	// Readiness snapshot driven by this function and the controller tick.
	// Populated before web.New so the first /healthz hit after startup
	// already sees the correct state machine. Nil-safe in tests — the
	// web server tolerates SetReadyState(nil) and returns 503 until wired.
	readyState := web.NewReadyState()

	// systemd Type=notify integration. Tells systemd we're up so the
	// service transitions to "active" only after configs are loaded
	// and controllers are running. Pairs with WatchdogSec= in the
	// unit: a heartbeat goroutine pings every WATCHDOG_USEC/2 so the
	// kernel kills us if the main loop hangs. Both are no-ops when
	// running off systemd (sdnotify reads NOTIFY_SOCKET from env).
	stopHeartbeat := sdnotify.StartHeartbeat()
	defer stopHeartbeat()
	defer func() { _ = sdnotify.Notify(sdnotify.Stopping) }()
	// Latch the /readyz watchdog gate once the heartbeat (or its no-op
	// off-systemd equivalent) is armed. From here on, /readyz's second gate
	// is the sensor-read freshness check, which is the only signal that can
	// actually go stale at runtime.
	readyState.SetWatchdogPinged()

	// Calibration manager: persists results across restarts. The watchdog
	// is shared with controllers — calibrate registers each fan at sweep
	// start and deregisters on normal exit, so a daemon crash mid-sweep
	// still restores PWM via the daemon-exit Restore.
	cal := calibrate.New("/etc/ventd/calibration.json", logger, wd)
	// Wire the HAL channel resolver so calibration sweeps drive fans via the
	// backend abstraction instead of direct hwmon/NVML imports (P1-HAL-02).
	cal.SetChannelResolver(func(ctx context.Context, fan *config.Fan) (hal.FanBackend, hal.Channel, error) {
		backendName := fan.Type
		if backendName == "nvidia" {
			backendName = halnvml.BackendName
		}
		return hal.Resolve(backendName + ":" + fan.PWMPath)
	})

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

	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	// errCh is buffered to one slot per controller plus one for the web server.
	errCh := make(chan error, len(cfg.Controls)+1)
	var wg sync.WaitGroup

	// restartCh is signalled by the web server after setup applies a new
	// config, and by the hwmon watcher on `action=added` topology changes
	// that match a configured HwmonDevice (#86 Proposal 3 / #95 Option A).
	// Buffered to one so senders can non-blockingly drop duplicate signals
	// when a restart is already pending.
	restartCh := make(chan struct{}, 1)

	// Tier 0.3 — hardware change detection. Watches /sys/class/hwmon for
	// add/remove at runtime via netlink uevents plus a 5-minute periodic
	// rescan safety net; emits a ComponentHardware diagnostic with a
	// "Re-run setup" button when topology changes. Read-only: never writes
	// to sysfs, never modprobes. Set VENTD_DISABLE_UEVENT=1 to fall back
	// to periodic-only (used for container environments where netlink is
	// blocked, and by Tier 0.3 validation to exercise the safety net).
	//
	// On `action=added`, the watcher calls rebindTrigger below. The trigger
	// inspects liveCfg to see whether the added device's StableDevice path
	// matches any configured Fan/Sensor HwmonDevice. If it does, a restart
	// is requested via restartCh — runDaemon tears down controllers + web,
	// returns errRestart, and main() re-execs so ResolveHwmonPaths picks
	// the correct hwmonN for the (now-present) configured chip. During the
	// 1-2 s restart gap, pwm_enable is restored to 2 (kernel-automatic) by
	// the wd.Restore() defer — the documented Option A tradeoff (#98).
	//
	// Gated on cfg.Hwmon.DynamicRebind (default false) so the v0.2.x
	// "diagnostic-only" behaviour is preserved until an operator opts in.
	// When the flag is unset the watcher still emits hardware-change
	// diagnostics; only the re-exec path is disabled.
	var watcherOpts []hwmon.Option
	if os.Getenv("VENTD_DISABLE_UEVENT") == "1" {
		watcherOpts = append(watcherOpts, hwmon.WithoutUevents())
	}
	if cfg.Hwmon.DynamicRebind {
		rebindTrigger := newRebindTrigger(&liveCfg, restartCh, logger)
		watcherOpts = append(watcherOpts, hwmon.WithRebindTrigger(rebindTrigger))
	} else {
		logger.Info("hwmon: dynamic rebind disabled (hwmon.dynamic_rebind=false)")
	}
	watcher := hwmon.NewWatcher(diagStore, logger, watcherOpts...)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := watcher.Run(ctx); err != nil {
			logger.Warn("hwmon watcher exited", "err", err)
		}
	}()

	// Start the web status server. It reads from &liveCfg on every request so
	// it always reflects the current configuration without restart.
	// Tracked by wg so shutdown waits for Shutdown() to drain in-flight
	// requests before run() returns — otherwise the HTTP handler goroutines
	// outlive wd.Restore() and can observe a half-torn-down daemon.
	webSrv := web.New(ctx, &liveCfg, configPath, logger, cal, setupMgr, restartCh, setupToken, diagStore)
	webSrv.SetVersionInfo(web.NewVersionInfo(version, commit, buildDate))
	webSrv.SetReadyState(readyState)
	wg.Add(1)
	go func() {
		defer wg.Done()
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
				// Shut the web server down before returning so its goroutine exits.
				cancel()
				webSrv.Shutdown()
				wg.Wait()
				return fmt.Errorf("resolve control: %w", err)
			}

			c := controller.New(
				ctrl.Fan, ctrl.Curve,
				fanCfg.PWMPath, fanCfg.Type,
				&liveCfg, wd, cal, logger,
				controller.WithSensorReadHook(func() {
					readyState.MarkSensorRead(time.Now())
				}),
				controller.WithPanicChecker(webSrv),
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

	// All goroutines launched. Tell systemd we're ready so the unit
	// transitions from "activating" to "active" — and so dependent
	// units (timers, sockets, downstream services) can start.
	_ = sdnotify.Notify(sdnotify.Ready)
	// Flip /healthz from 503 to 200 at the same point systemd sees us
	// become active. Readiness (/readyz) still depends on the sensor-read
	// freshness check so it can go stale at runtime independently.
	readyState.SetHealthy()

	for {
		select {
		case <-ctx.Done():
			// Parent context cancelled (e.g. test teardown). Treat as graceful
			// shutdown — same as SIGTERM.
			logger.Info("context cancelled, shutting down")
			webSrv.Shutdown()
			wg.Wait()
			return nil

		case sig := <-sigCh:
			switch sig {
			case syscall.SIGHUP:
				newCfg, reloadErr := config.Load(configPath)
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
				webSrv.Shutdown()
				wg.Wait()
				// wd.Restore() runs via defer.
				return nil
			}

		case ctrlErr := <-errCh:
			// A controller goroutine failed (panic or startup error).
			// Cancel all other controllers and exit non-zero so systemd restarts us.
			logger.Error("controller failure, initiating emergency shutdown", "err", ctrlErr)
			cancel()
			webSrv.Shutdown()
			wg.Wait()
			// Drain any additional errors that other goroutines sent before
			// (or during) shutdown — otherwise concurrent failures are silent.
			// First error still determines the exit status.
			drainDeadline := time.After(2 * time.Second)
		drain:
			for {
				select {
				case extra, ok := <-errCh:
					if !ok {
						break drain
					}
					logger.Error("additional failure during shutdown", "err", fmt.Errorf("additional shutdown error: %w", extra))
				case <-drainDeadline:
					break drain
				}
			}
			// wd.Restore() runs via defer.
			return ctrlErr

		case <-restartCh:
			logger.Info("restarting to apply new configuration")
			cancel()
			// Gracefully drain in-flight HTTP responses and release the port
			// before wg.Wait, so the web goroutine can return.
			webSrv.Shutdown()
			wg.Wait()
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

// setupTokenPath is where the first-boot setup token is persisted for
// operators who can read files owned by the daemon user but cannot watch
// the TTY (the systemd case). Deleted automatically when the token TTL
// expires inside the web package, but the file itself lives for the
// lifetime of the daemon — /run is a tmpfs so the token never reaches
// persistent storage.
const setupTokenPath = "/run/ventd/setup-token"

// publishSetupToken makes the first-boot token available to the operator
// without leaking it into journald. It always logs the retrieval paths;
// the plaintext goes only to /dev/tty (if one is attached to the daemon)
// and to setupTokenPath with 0600 perms.
func publishSetupToken(tok, listen string, tls bool, logger *slog.Logger) {
	scheme := "http"
	if tls {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		host, port = "", "9999"
	}
	// Pick a display host the operator can actually paste into a browser:
	// wildcard binds → the machine's LAN IP; loopback binds → 127.0.0.1 as-is.
	displayHost := host
	switch host {
	case "", "0.0.0.0", "::":
		displayHost = localIP()
	case "127.0.0.1", "::1", "localhost":
		displayHost = "127.0.0.1"
	}
	url := scheme + "://" + net.JoinHostPort(displayHost, port)
	writtenToFile := false
	if err := os.MkdirAll(filepath.Dir(setupTokenPath), 0700); err != nil {
		logger.Warn("first-boot: create setup-token dir", "err", fmt.Errorf("mkdir %s: %w", filepath.Dir(setupTokenPath), err))
	} else {
		f, err := os.OpenFile(setupTokenPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			logger.Warn("first-boot: write setup token file", "path", setupTokenPath, "err", err)
		} else {
			if _, err := f.WriteString(tok + "\n"); err != nil {
				logger.Warn("first-boot: write setup token", "err", err)
			}
			_ = f.Close()
			writtenToFile = true
		}
	}

	// Best-effort TTY print. Fails silently under systemd where there is
	// no controlling TTY, which is intentional — the file path below
	// covers that case.
	if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		_, _ = fmt.Fprintf(tty, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		_, _ = fmt.Fprintf(tty, "  Ventd — First Boot\n")
		_, _ = fmt.Fprintf(tty, "  Open your browser to %s\n", url)
		_, _ = fmt.Fprintf(tty, "  Setup token: %s\n", tok)
		_, _ = fmt.Fprintf(tty, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
		_ = tty.Close()
	}

	logger.Info("first-boot: setup pending", "url", url, "ttl", web.SetupTokenTTL)
	if writtenToFile {
		logger.Info("first-boot: setup token written", "path", setupTokenPath, "hint", "sudo cat "+setupTokenPath)
	} else {
		logger.Warn("first-boot: setup token only available on the controlling TTY (file write failed)")
	}
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

// warnIfUnconfined surfaces a one-line slog.Warn when an AppArmor
// profile is shipped on disk for ventd but the current process is
// running unconfined. This is the only signal an operator running
// `journalctl -u ventd` will find that ties a silent confinement
// downgrade back to its root cause (usually a parser error during
// install — see #202 + #204 history). Best-effort: if either file is
// unreadable (kernel without AppArmor support, paranoid confinement
// that hides /proc/self/attr) the check falls through silently.
//
// Detection rule: if /etc/apparmor.d/usr.local.bin.ventd exists AND
// /proc/self/attr/current reads "unconfined" (or an empty string)
// we expected confinement but didn't get it. Any non-empty non-
// "unconfined" value — including profile-not-named-ventd — means a
// profile is active and we stay quiet; ops with custom profiles
// should not see WARN spam.
func warnIfUnconfined(logger *slog.Logger) {
	const profilePath = "/etc/apparmor.d/usr.local.bin.ventd"
	if _, err := os.Stat(profilePath); err != nil {
		return
	}
	// /proc/self/attr/current layout:
	//   "unconfined\n"                          → unconfined
	//   "ventd (enforce)\x00"                   → confined (note NUL)
	//   "/usr/local/bin/ventd (enforce)\x00"    → confined alt path
	raw, err := os.ReadFile("/proc/self/attr/current")
	if err != nil {
		return
	}
	current := strings.TrimRight(strings.TrimSpace(string(raw)), "\x00")
	if current == "" || current == "unconfined" {
		logger.Warn("apparmor: profile is installed on disk but process is unconfined — confinement was likely refused at install time",
			"profile", profilePath,
			"current", current,
			"hint", "check /var/log/ventd/install.log or run: sudo apparmor_parser -r "+profilePath)
	}
}
