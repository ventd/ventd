package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ventd/ventd/internal/calibrate"
	calibstore "github.com/ventd/ventd/internal/calibration"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/experimental"
	"github.com/ventd/ventd/internal/hal"
	halasahi "github.com/ventd/ventd/internal/hal/asahi"
	halcrosec "github.com/ventd/ventd/internal/hal/crosec"
	halgpu "github.com/ventd/ventd/internal/hal/gpu"
	halhwmon "github.com/ventd/ventd/internal/hal/hwmon"
	halipmi "github.com/ventd/ventd/internal/hal/ipmi"
	halcorsair "github.com/ventd/ventd/internal/hal/liquid/corsair"
	halnvml "github.com/ventd/ventd/internal/hal/nvml"
	halpwmsys "github.com/ventd/ventd/internal/hal/pwmsys"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/hwdiag"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/sdnotify"
	setupmgr "github.com/ventd/ventd/internal/setup"
	"github.com/ventd/ventd/internal/state"
	"github.com/ventd/ventd/internal/watchdog"
	"github.com/ventd/ventd/internal/web"
	"github.com/ventd/ventd/internal/web/authpersist"
)

// Build-time metadata populated by -ldflags -X from .goreleaser.yml.
// Defaults keep `go build` and `go run` producing a sensible string.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if err := run(); err != nil {
		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
		logger.Error("ventd: fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	// Positional subcommand dispatch must happen before flag.Parse() because
	// "diag bundle" args include its own flag set that conflicts with main's.
	if len(os.Args) >= 3 && os.Args[1] == "diag" && os.Args[2] == "bundle" {
		logger := buildLogger("info")
		return runDiagBundle(os.Args[3:], logger)
	}
	if len(os.Args) >= 2 && os.Args[1] == "diag" {
		fmt.Fprintln(os.Stderr, "Usage: ventd diag bundle [flags]")
		sub := ""
		if len(os.Args) >= 3 {
			sub = os.Args[2]
		}
		return fmt.Errorf("unknown diag subcommand %q", sub)
	}

	configPath := flag.String("config", "/etc/ventd/config.yaml", "path to YAML config file")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	doSetup := flag.Bool("setup", false, "run interactive setup wizard, write initial config, then exit")
	doProbeModules := flag.Bool("probe-modules", false, "probe and load hwmon kernel modules, persist via /etc/modules-load.d, then exit (run by the installer at root, not by the sandboxed service)")
	doRecover := flag.Bool("recover", false, "reset every pwm_enable to 1 (automatic mode) and exit; runs as the OnFailure= oneshot if the main daemon exits unexpectedly")
	doRescan := flag.Bool("rescan-hwmon", false, "rerun hwmon module probing for hardware added since the original install (modprobe + persist /etc/modules-load.d), then exit")
	doListFans := flag.Bool("list-fans-probe", false, "validation helper: enumerate every hwmon device, classify, probe writability, and print PASS/FAIL")
	doCalibrateProbe := flag.Bool("calibrate-probe", false, "run the PR 2b channel-validity probe (polarity, stall, BIOS-override) and write calibration JSON, then exit")
	doPreflight := flag.Bool("preflight-check", false, "validation helper: run preflight against a synthetic DriverNeed and print the Reason as JSON")
	preflightMaxKernel := flag.String("preflight-max-kernel", "", "with --preflight-check: synthetic MaxSupportedKernel ceiling (e.g. 6.6)")
	enableGPUWrite := flag.Bool("enable-gpu-write", false, "enable fan write commands for NVIDIA/AMDGPU GPUs; requires per-device capability probe success (RULE-GPU-PR2D-01)")
	enableAMDOverdrive := flag.Bool("enable-amd-overdrive", false, "enable AMD OverDrive fan curve interface (experimental; requires amd_overdrive precondition)")
	enableNVIDIACoolbits := flag.Bool("enable-nvidia-coolbits", false, "enable NVIDIA Coolbits fan control (experimental; requires nvidia_coolbits precondition)")
	enableILO4Unlocked := flag.Bool("enable-ilo4-unlocked", false, "enable HPE iLO4 unlocked fan control (experimental; requires ilo4_unlocked precondition)")
	enableIDRAC9LegacyRaw := flag.Bool("enable-idrac9-legacy-raw", false, "enable Dell iDRAC9 legacy raw IPMI fan commands (experimental; requires idrac9_legacy_raw precondition)")
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

	if *doCalibrateProbe {
		// PR 2b channel-validity probe. Full channel discovery wiring lands
		// in the setup wizard (PR 2c); this flag is the CLI entry-point hook.
		// The store is initialised so the integration path is exercised at
		// build time even before the wizard calls runCalibrationProbe.
		_ = initCalibrationStore()
		logger.Info("calibrate-probe: channel discovery wiring pending PR 2c setup wizard integration")
		return nil
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

	// Resolve active experimental flags: CLI > config > default (all-false).
	cliExpFlags := experimental.Flags{
		AMDOverdrive:    *enableAMDOverdrive,
		NVIDIACoolbits:  *enableNVIDIACoolbits,
		ILO4Unlocked:    *enableILO4Unlocked,
		IDRAC9LegacyRaw: *enableIDRAC9LegacyRaw,
	}
	expFlags := experimental.Merge(cliExpFlags, experimental.ParseConfig(cfg.Experimental))

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

	authPath := authpersist.DefaultPath(filepath.Dir(*configPath))

	// Migrate hash from config.yaml to auth.json on the first startup after
	// an upgrade. This is a one-time operation; subsequent starts skip it
	// because auth.json already exists.
	migratedHash, migrateErr := migrateAuthToFile(*configPath, authPath, logger)
	if migrateErr != nil {
		logger.Error("auth migration failed; credentials may have been lost",
			"auth_path", authPath, "err", migrateErr)
	}

	// Load the admin hash from auth.json. Fall back to the migrated hash if
	// auth.json was just written above and has not yet been read back.
	authHash := migratedHash
	if authHash == "" {
		if auth, loadErr := authpersist.Load(authPath); loadErr == nil && auth != nil {
			authHash = auth.Admin.BcryptHash
		}
	}

	// Integrity guard: if a full config exists but no auth hash is loadable,
	// admin credentials were lost (e.g. the config was written without them
	// by a bug in a prior version). Fall back to first-boot so the operator
	// can set a new password via the wizard rather than being permanently
	// locked out.
	if !firstBoot && authHash == "" {
		logger.Error("auth.json missing or unreadable but config.yaml exists — "+
			"admin credentials were lost; falling back to first-boot wizard. "+
			"Check auth.json.bak if it exists, or complete the wizard again.",
			"auth_path", authPath)
	}

	// Generate a one-time setup token if no password is configured yet.
	// The token is a first-boot credential: anyone who can read it can set
	// the admin password. Never write the plaintext to slog/journald — it
	// would be retrievable by any journal reader. Instead: print to the
	// controlling TTY when present (operator running ventd manually), and
	// always drop a 0600 file under /run/ventd for systemd deployments.
	var setupToken string
	if authHash == "" {
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

	// Provision the profiles-pending directory and register the post-calibration
	// capture hook. Capture is best-effort: directory creation failure is logged
	// but does not abort startup. RULE-HWDB-CAPTURE-01.
	pendingDir := hwdb.CaptureDir()
	if mkErr := os.MkdirAll(pendingDir, 0o750); mkErr != nil {
		logger.Warn("capture: cannot create profiles-pending dir", "dir", pendingDir, "err", mkErr)
	}
	captureDMI, capDMIErr := hwdb.ReadDMI(os.DirFS("/"))
	captureCat, capCatErr := hwdb.LoadCatalog()
	if capDMIErr != nil || capCatErr != nil {
		logger.Warn("capture: hook disabled (DMI or catalog unavailable)",
			"dmi_err", capDMIErr, "cat_err", capCatErr)
	} else {
		calibstore.SetCaptureHook(func(run *hwdb.CalibrationRun) {
			path, err := hwdb.Capture(run, captureDMI, captureCat, pendingDir)
			if err != nil {
				logger.Warn("capture: failed to write pending profile", "err", err)
				return
			}
			logger.Info("capture: wrote pending profile", "path", path)
		})
	}

	// Persistent state (KV, blob, log stores). AcquirePID prevents two
	// daemon instances from racing over the same files (RULE-STATE-06).
	// Open bootstraps the directory hierarchy on first boot (RULE-STATE-10).
	releasePID, pidErr := state.AcquirePID(state.DefaultDir)
	if pidErr != nil {
		return fmt.Errorf("acquire pid: %w", pidErr)
	}
	defer releasePID()
	st, stErr := state.Open(state.DefaultDir, logger)
	if stErr != nil {
		return fmt.Errorf("open state: %w", stErr)
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("state close", "err", err)
		}
	}()
	_ = st // consumers wired in subsequent PRs

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
	halcorsair.RegisterAll(logger, halcorsair.ProbeOptions{})
	halgpu.RegisterAll(logger, halgpu.ProbeOptions{EnableGPUWrite: *enableGPUWrite})
	hal.Register(halcrosec.BackendName, halcrosec.NewBackend(logger))
	hal.Register(halhwmon.BackendName, halhwmon.NewBackend(logger))
	hal.Register(halipmi.BackendName, halipmi.NewBackend(logger))
	hal.Register(halnvml.BackendName, halnvml.NewBackend(logger))
	hal.Register(halpwmsys.BackendName, halpwmsys.NewBackend(logger))
	if channels, err := hal.Enumerate(context.Background()); err != nil {
		logger.Warn("hal: initial enumerate failed", "err", err)
	} else {
		logger.Info("hal: enumerated fan backends", "channels", len(channels))
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(sigCh)

	return runDaemon(context.Background(), cfg, *configPath, authPath, logger, setupToken, sigCh, expFlags)
}

// runDaemon runs the daemon lifecycle: watchdog registration, hwmon watcher,
// web server, per-fan controllers, and the shutdown-coordinating select loop.
// It returns:
//   - nil on SIGTERM / SIGINT (or ctx cancellation when sigCh is nil)
//   - a wrapped controller error when a control goroutine fails
//
// restartCh signals from the web server or hwmon watcher are handled as
// in-process config reloads: the config file is re-read, liveCfg is swapped
// atomically, and — on first-boot transition — new controllers are started.
// The daemon never exits on a failed reload; it logs and continues.
//
// Passing nil for sigCh is supported: a receive on a nil channel blocks
// forever, so the signal case never fires — used by the integration test
// so the test drives shutdown via ctx cancellation alone.
func runDaemon(
	parentCtx context.Context,
	cfg *config.Config,
	configPath string,
	authPath string,
	logger *slog.Logger,
	setupToken string,
	sigCh <-chan os.Signal,
	expFlags experimental.Flags,
) error {
	restartCh := make(chan struct{}, 1)
	return runDaemonInternal(parentCtx, cfg, configPath, authPath, logger, setupToken, sigCh, restartCh, expFlags)
}

// configLoader is the function used to load a config from disk on each
// in-process reload. Tests that exercise the first-boot → configured reload
// branch substitute a stub here so they can inject a *config.Config with
// temp-dir sysfs paths that would otherwise fail config.Parse's /sys prefix
// guard. Must be set before the daemon goroutine starts; package-scoped so
// tests in the same package can reach it without an export.
var configLoader = config.Load

// runDaemonInternal is the concrete daemon implementation with an injectable
// restartCh. Production callers use runDaemon; tests call this directly via
// runDaemonWithRestart to send reload signals.
func runDaemonInternal(
	parentCtx context.Context,
	cfg *config.Config,
	configPath string,
	authPath string,
	logger *slog.Logger,
	setupToken string,
	sigCh <-chan os.Signal,
	restartCh chan struct{},
	expFlags experimental.Flags,
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

	// Publish active experimental flags to the diagnostic store and log once per 24h.
	experimental.Publish(diagStore, expFlags)
	expStatePath := "/var/lib/ventd/experimental-startup.state"
	if os.Getuid() != 0 {
		xdgState := os.Getenv("XDG_STATE_HOME")
		if xdgState == "" {
			xdgState = filepath.Join(os.Getenv("HOME"), ".local/state")
		}
		expStatePath = filepath.Join(xdgState, "ventd/experimental-startup.state")
	}
	experimental.LogActiveFlagsOnce(expFlags, expStatePath, logger, time.Now)

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
	// matches any configured Fan/Sensor HwmonDevice. If it does, a reload
	// is signalled via restartCh — runDaemon re-reads the config and swaps
	// liveCfg so ResolveHwmonPaths picks the correct hwmonN for the
	// (now-present) configured chip.
	//
	// Gated on cfg.Hwmon.DynamicRebind (default false) so the v0.2.x
	// "diagnostic-only" behaviour is preserved until an operator opts in.
	// When the flag is unset the watcher still emits hardware-change
	// diagnostics; only the reload path is disabled.
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
	webSrv := web.New(ctx, &liveCfg, configPath, authPath, logger, cal, setupMgr, restartCh, setupToken, diagStore)
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
		calMap := loadCalibrationByChannel(logger)
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

			opts := []controller.Option{
				controller.WithSensorReadHook(func() {
					readyState.MarkSensorRead(time.Now())
				}),
				controller.WithPanicChecker(webSrv),
			}
			if fanCfg.Type == "hwmon" {
				if hwmonName, idx, ok := parseHwmonChannel(fanCfg.PWMPath); ok {
					if calCh, found := calMap[hwdb.ChannelKey{Hwmon: hwmonName, Index: idx}]; found {
						opts = append(opts, controller.WithCalibration(calCh, 255))
					}
				}
			}
			c := controller.New(
				ctrl.Fan, ctrl.Curve,
				fanCfg.PWMPath, fanCfg.Type,
				&liveCfg, wd, cal, logger,
				opts...,
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
				newCfg, reloadErr := configLoader(configPath)
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
			// In-process config reload replaces the former re-exec path (#466).
			// A failed reload is non-fatal: log and keep running with the current config.
			newCfg, reloadErr := configLoader(configPath)
			if reloadErr != nil {
				logger.Warn("config reload failed; keeping current config", "err", reloadErr)
				continue
			}
			resolveHwmonPaths(newCfg, cal, logger)
			oldCfg := liveCfg.Load()
			liveCfg.Store(newCfg)
			logger.Info("config reloaded",
				"poll_interval", newCfg.PollInterval.Duration,
				"controls", len(newCfg.Controls),
			)
			// First-boot → configured transition: start controllers for the new config.
			// On a running system (oldCfg already has controls) existing controllers
			// pick up new curve parameters from liveCfg on their next tick.
			if len(oldCfg.Controls) == 0 && len(newCfg.Controls) > 0 {
				for _, fan := range newCfg.Fans {
					wd.Register(fan.PWMPath, fan.Type)
				}
				reloadCalMap := loadCalibrationByChannel(logger)
				for _, ctrl := range newCfg.Controls {
					fanCfg, err := resolveControl(newCfg, ctrl)
					if err != nil {
						logger.Error("resolve control after config reload",
							"fan", ctrl.Fan, "err", err)
						continue
					}
					reloadOpts := []controller.Option{
						controller.WithSensorReadHook(func() {
							readyState.MarkSensorRead(time.Now())
						}),
						controller.WithPanicChecker(webSrv),
					}
					if fanCfg.Type == "hwmon" {
						if hwmonName, idx, ok := parseHwmonChannel(fanCfg.PWMPath); ok {
							if calCh, found := reloadCalMap[hwdb.ChannelKey{Hwmon: hwmonName, Index: idx}]; found {
								reloadOpts = append(reloadOpts, controller.WithCalibration(calCh, 255))
							}
						}
					}
					c := controller.New(
						ctrl.Fan, ctrl.Curve,
						fanCfg.PWMPath, fanCfg.Type,
						&liveCfg, wd, cal, logger,
						reloadOpts...,
					)
					wg.Add(1)
					go func(c *controller.Controller) {
						defer wg.Done()
						if runErr := c.Run(ctx, newCfg.PollInterval.Duration); runErr != nil {
							errCh <- runErr
						}
					}(c)
				}
				logger.Info("controllers started after first-boot config reload",
					"count", len(newCfg.Controls))
			}
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

// migrateAuthToFile moves the password hash from config.yaml to auth.json on
// the first startup after an upgrade from a pre-auth.json version.
//
// It is a no-op when:
//   - authPath is empty
//   - config.yaml has no hash field (nothing to migrate)
//   - auth.json already exists (already migrated or freshly written)
//
// On success the hash is removed from config.yaml and the migrated hash is
// returned so the caller can use it without re-reading auth.json.
func migrateAuthToFile(configPath, authPath string, logger *slog.Logger) (string, error) {
	if authPath == "" {
		return "", nil
	}
	// Skip if auth.json already exists — it is authoritative.
	if _, err := os.Stat(authPath); err == nil {
		return "", nil
	}
	cfg, err := config.Load(configPath)
	if err != nil || cfg.Web.PasswordHash == "" {
		return "", nil // no config or no hash — nothing to migrate
	}
	hash := cfg.Web.PasswordHash
	if err := authpersist.Save(authPath, &authpersist.Auth{
		Admin: authpersist.AdminCreds{
			Username:   "admin",
			BcryptHash: hash,
			CreatedAt:  time.Now(),
		},
	}); err != nil {
		return "", fmt.Errorf("write auth.json: %w", err)
	}
	// Clear the hash from config.yaml so it is not exposed there going forward.
	cfg.Web.PasswordHash = ""
	if _, saveErr := config.Save(cfg, configPath); saveErr != nil {
		// Non-fatal: auth.json is written and authoritative. Log the failure but
		// do not undo the migration — the stale field in config.yaml is harmless.
		logger.Warn("auth migration: failed to clear hash from config.yaml (non-fatal)",
			"err", saveErr, "config_path", configPath)
	}
	logger.Info("auth migration: moved admin hash from config.yaml to auth.json",
		"auth_path", authPath)
	return hash, nil
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

// publishSetupToken makes the first-boot token available to the operator
// without leaking it into journald. It always logs the retrieval paths;
// the plaintext goes only to /dev/tty (if one is attached to the daemon)
// and to the two token files (runtime + persistent) with 0640 perms.
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

	// Write the token to both the tmpfs runtime path and the persistent state
	// path so it survives daemon restarts. Each write is atomic (temp+rename).
	if err := web.WriteSetupTokenFiles(tok, web.SetupTokenRuntimePath, web.SetupTokenPersistPath); err != nil {
		logger.Warn("first-boot: write setup token files", "err", err)
	}

	// Best-effort TTY print. Fails silently under systemd where there is
	// no controlling TTY, which is intentional — the file paths below
	// cover that case.
	if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		_, _ = fmt.Fprintf(tty, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		_, _ = fmt.Fprintf(tty, "  Ventd — First Boot\n")
		_, _ = fmt.Fprintf(tty, "  Open your browser to %s\n", url)
		_, _ = fmt.Fprintf(tty, "  Setup token: %s\n", tok)
		_, _ = fmt.Fprintf(tty, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
		_ = tty.Close()
	}

	logger.Info("first-boot: setup pending", "url", url, "ttl", web.SetupTokenTTL)
	logger.Info("first-boot pending — setup token available",
		"command", "sudo cat "+web.SetupTokenRuntimePath,
		"ttl", web.SetupTokenTTL,
	)
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

// loadCalibrationByChannel reads the most recently written calibration run for
// this machine from the default store path and returns a per-channel map keyed
// by ChannelKey. A missing or empty store is not an error — it returns an empty
// map so callers can treat uncalibrated channels identically to calibrated ones
// with no data.
func loadCalibrationByChannel(logger *slog.Logger) map[hwdb.ChannelKey]*hwdb.ChannelCalibration {
	result := make(map[hwdb.ChannelKey]*hwdb.ChannelCalibration)

	dmi, err := hwdb.ReadDMI(os.DirFS("/"))
	if err != nil {
		logger.Warn("calibration: cannot read DMI fingerprint, skipping calibration data", "err", err)
		return result
	}
	fingerprint := hwdb.Fingerprint(dmi)

	biosData, readErr := os.ReadFile("/sys/class/dmi/id/bios_version")
	if readErr != nil {
		logger.Warn("calibration: cannot read BIOS version, skipping calibration data", "err", readErr)
		return result
	}
	biosVersion := strings.TrimRight(string(biosData), "\n\r")

	store := calibstore.NewStore("/var/lib/ventd/calibration")
	run, loadErr := store.Load(fingerprint, biosVersion)
	if loadErr != nil {
		logger.Warn("calibration: store load failed, skipping calibration data", "err", loadErr)
		return result
	}
	if run == nil {
		return result
	}

	for i := range run.Channels {
		ch := &run.Channels[i]
		key := hwdb.ChannelKey{Hwmon: ch.HwmonName, Index: ch.ChannelIndex}
		result[key] = ch
	}
	return result
}

// parseHwmonChannel parses a sysfs PWM path of the form
// /sys/class/hwmon/hwmonN/pwmM into the hwmon chip name (read from the
// sibling "name" file) and channel index M. Returns ok=false when the path
// does not match the expected shape or the chip name file cannot be read.
func parseHwmonChannel(pwmPath string) (chipName string, idx int, ok bool) {
	dir := filepath.Dir(pwmPath)
	base := filepath.Base(pwmPath)
	if !strings.HasPrefix(base, "pwm") {
		return "", 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(base, "pwm"))
	if err != nil || n < 1 {
		return "", 0, false
	}
	nameData, err := os.ReadFile(filepath.Join(dir, "name"))
	if err != nil {
		return "", 0, false
	}
	chip := strings.TrimRight(string(nameData), "\n\r")
	if chip == "" {
		return "", 0, false
	}
	return chip, n, true
}
