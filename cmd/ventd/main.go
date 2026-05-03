package main

import (
	"context"
	"encoding/json"
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
	"github.com/ventd/ventd/internal/confidence/aggregator"
	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/coupling"
	"github.com/ventd/ventd/internal/coupling/signguard"
	"github.com/ventd/ventd/internal/envelope"
	"github.com/ventd/ventd/internal/experimental"
	"github.com/ventd/ventd/internal/fallback"
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
	"github.com/ventd/ventd/internal/idle"
	"github.com/ventd/ventd/internal/marginal"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/observation"
	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/probe/opportunistic"
	"github.com/ventd/ventd/internal/proc"
	"github.com/ventd/ventd/internal/sdnotify"
	setupmgr "github.com/ventd/ventd/internal/setup"
	"github.com/ventd/ventd/internal/signature"
	"github.com/ventd/ventd/internal/state"
	"github.com/ventd/ventd/internal/sysclass"
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
	if len(os.Args) >= 3 && os.Args[1] == "diag" && os.Args[2] == "export-observations" {
		logger := buildLogger("info")
		return runDiagExportObservations(os.Args[3:], logger)
	}
	if len(os.Args) >= 2 && os.Args[1] == "diag" {
		fmt.Fprintln(os.Stderr, "Usage: ventd diag {bundle|export-observations} [flags]")
		sub := ""
		if len(os.Args) >= 3 {
			sub = os.Args[2]
		}
		return fmt.Errorf("unknown diag subcommand %q", sub)
	}
	if len(os.Args) >= 2 && os.Args[1] == "doctor" {
		logger := buildLogger("info")
		exitCode, err := runDoctor(os.Args[2:], logger)
		if err != nil {
			return err
		}
		os.Exit(exitCode)
	}

	// `ventd preflight` runs the install-time orchestrator. Args
	// after `preflight` are passed through; the subcommand owns its
	// own flag parsing because the global flag set has unrelated
	// daemon flags that would error on --interactive etc.
	if len(os.Args) >= 2 && os.Args[1] == "preflight" {
		logger := buildLogger("info")
		os.Exit(runPreflight(os.Args[2:], logger))
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
			// Empty() defaults Listen to 127.0.0.1:9999 to satisfy
			// RULE-INSTALL-03 (no plaintext bind on 0.0.0.0). Now that
			// TLS is active, promote loopback to wildcard so the LAN URL
			// install.sh prints actually resolves. Without this the
			// first-time user sees "connection refused" when they open
			// the printed URL from another machine — defeating the
			// "open the browser" promise in the README. Skip promotion
			// if the operator pre-provisioned a non-default Listen
			// (host:port other than 127.0.0.1:9999).
			if cfg.Web.Listen == "127.0.0.1:9999" {
				_, port, splitErr := net.SplitHostPort(cfg.Web.Listen)
				if splitErr == nil {
					cfg.Web.Listen = net.JoinHostPort("0.0.0.0", port)
					logger.Info("first-boot: promoting listen address to wildcard now that TLS is active",
						"listen", cfg.Web.Listen)
				}
			}
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

	// First-boot mode: when no admin password is configured yet, the
	// wizard's password-set step is open without auth (#765). The
	// daemon logs a security note so journald shows a clear record of
	// the first-boot window for audit.
	if authHash == "" {
		logger.Info("first-boot: no admin password set; wizard accepts password-set without auth — set a password promptly to lock the daemon")
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
	// Run the catalog-less hardware probe on first boot. Subsequent starts
	// consult the persisted wizard outcome directly (RULE-PROBE-08).
	if _, ok, _ := probe.LoadWizardOutcome(st.KV); !ok {
		if r, probeErr := probe.New(probe.Config{Logger: logger}).Probe(context.Background()); probeErr != nil {
			logger.Warn("probe: hardware probe failed; continuing", "err", probeErr)
		} else if persistErr := probe.PersistOutcome(st.KV, r); persistErr != nil {
			logger.Warn("probe: persist failed", "err", persistErr)
		}
	}
	// Gate on the persisted outcome (RULE-PROBE-08).
	// Refuse is non-fatal: the wizard surface is responsible for
	// explaining the situation to the operator. Continue startup so
	// the web server can bind and serve setup / dashboard pages.
	// RULE-PROBE-11.
	if outcome, ok, err := probe.LoadWizardOutcome(st.KV); err == nil && ok && outcome == probe.OutcomeRefuse {
		logger.Warn("probe: hardware refused (virtualised, containerised, or no sensors); continuing in monitor-only mode",
			"outcome", outcome)
	}

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

	// Synchronous system-class detection. Reads the probe result persisted
	// earlier in run() and classifies the system hardware.
	var sysProbeResult probe.ProbeResult
	if rawVal, ok, _ := st.KV.Get("probe", "result"); ok {
		if s, ok2 := rawVal.(string); ok2 {
			_ = json.Unmarshal([]byte(s), &sysProbeResult)
		}
	}
	sysDet, sysDetErr := sysclass.Detect(context.Background(), &sysProbeResult)
	if sysDetErr != nil {
		logger.Warn("sysclass: detection failed, using defaults", "err", sysDetErr)
		sysDet = &sysclass.Detection{Class: sysclass.ClassUnknown}
	}
	if sysDetPersistErr := sysclass.PersistDetection(st.KV, sysDet); sysDetPersistErr != nil {
		logger.Warn("sysclass: persist failed", "err", sysDetPersistErr)
	}
	logger.Info("sysclass: detected", "class", sysDet.Class, "evidence", sysDet.Evidence)

	// Passive observation log: constructed once per daemon start. Channels come
	// from the probe result; the DMI fingerprint is computed from live sysfs.
	// Non-fatal on error — observation loss is preferable to a failed daemon start.
	channels := make([]*probe.ControllableChannel, len(sysProbeResult.ControllableChannels))
	for i := range sysProbeResult.ControllableChannels {
		channels[i] = &sysProbeResult.ControllableChannels[i]
	}
	var dmiFingerprint string
	if dmi, dmiErr := hwdb.ReadDMI(os.DirFS("/")); dmiErr == nil {
		dmiFingerprint = hwdb.Fingerprint(dmi)
	}
	obsWriter, obsErr := observation.New(st.Log, st.KV, channels, dmiFingerprint, version, logger)
	if obsErr != nil {
		logger.Warn("observation: writer init failed; tick logging disabled", "err", obsErr)
	} else {
		logger.Info("observation: writer initialised", "channels", len(channels))
	}
	_ = obsWriter // consumed by controller tick wiring in a follow-up spec

	// Envelope C/D probe runs in background after the idle gate clears (RULE-IDLE-01).
	// Context is cancelled by defer so the goroutine exits cleanly when run() returns.
	envelopeCtx, envelopeCancel := context.WithCancel(context.Background())
	defer envelopeCancel()
	go runEnvelopeBackground(envelopeCtx, st, sysDet, &sysProbeResult, cfg, logger)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(sigCh)

	kvWiper := func() error { return probe.WipeNamespaces(st.KV) }

	// v0.5.8 sign-guard: shared per-channel sign-vote detector. Fed by
	// every successful opportunistic probe (from the prober's
	// SignguardSampleFn callback) and queried by marginal.Runtime
	// before seeding a Layer-C shard from a Layer-B b_ii prior. Live
	// for the lifetime of the daemon — the rolling 7-vote window
	// supports continuous re-confirmation per RULE-SGD-CONT-01.
	var sguDet *signguard.Detector
	if len(channels) > 0 {
		sguDet = signguard.NewDetector()
		logger.Info("signguard detector initialised", "channels", len(channels))
	}

	// v0.5.5: build the opportunistic-probe scheduler factory.
	// runDaemonInternal calls this once liveCfg is in scope so the
	// Disabled callback can read NeverActivelyProbeAfterInstall on
	// every tick — a flip in /api/config takes effect on the next
	// scheduler tick without a daemon restart.
	oppFactory := func(liveCfg *atomic.Pointer[config.Config]) *opportunistic.Scheduler {
		return buildOpportunisticScheduler(channels, sysDet, st, obsWriter, liveCfg, sguDet, logger)
	}

	// v0.5.6: bundle the runtime dependencies for signature learning
	// + observation log emission. Same closure-over-run-scope pattern
	// as oppFactory.
	smartMode := &SmartModeBundle{
		SigFactory: func(liveCfg *atomic.Pointer[config.Config]) *signature.Library {
			return buildSignatureLibrary(channels, sysDet, liveCfg, logger)
		},
		State:      st,
		Coupling:   buildCouplingRuntime(channels, st, dmiFingerprint, logger),
		Marginal:   buildMarginalRuntime(channels, st, dmiFingerprint, sguDet, logger),
		LayerA:     buildLayerAEstimator(channels, logger),
		Aggregator: buildAggregator(channels, logger),
		Blended:    buildBlendedController(channels, cfg, logger),
	}
	if obsWriter != nil {
		smartMode.ObsAppend = buildObsAppend(obsWriter)
	}

	return runDaemon(context.Background(), cfg, *configPath, authPath, logger, sigCh, expFlags, kvWiper, oppFactory, smartMode)
}

// buildOpportunisticScheduler constructs the v0.5.5 scheduler from the
// daemon's runtime state. Returns nil when there are no controllable
// channels (monitor-only mode); the daemon then never starts the
// scheduler goroutine.
func buildOpportunisticScheduler(
	channels []*probe.ControllableChannel,
	sysDet *sysclass.Detection,
	st *state.State,
	obsWriter *observation.Writer,
	liveCfg *atomic.Pointer[config.Config],
	sguDet *signguard.Detector,
	logger *slog.Logger,
) *opportunistic.Scheduler {
	if len(channels) == 0 {
		logger.Info("opportunistic: no controllable channels; scheduler not started")
		return nil
	}
	if obsWriter == nil {
		logger.Warn("opportunistic: observation writer unavailable; scheduler not started")
		return nil
	}
	tjmax := 100.0
	if sysDet != nil && sysDet.Tjmax > 0 {
		tjmax = sysDet.Tjmax
	}
	cls := sysclass.ClassMidDesktop
	if sysDet != nil {
		cls = sysDet.Class
	}
	rd := observation.NewReader(st.Log)
	det := opportunistic.NewDetector(rd, channels, nil)
	var sguFn opportunistic.SignguardSampleFn
	if sguDet != nil {
		sguFn = func(channelID string, deltaPWMSigned int, deltaT float64) {
			sguDet.Add(signguard.Sample{
				ChannelID: channelID,
				DeltaPWM:  int8(deltaPWMSigned),
				DeltaT:    deltaT,
			})
		}
	}
	depsFor := func(ch *probe.ControllableChannel) opportunistic.ProbeDeps {
		return opportunistic.ProbeDeps{
			Class:             cls,
			Tjmax:             tjmax,
			SensorFn:          opportunistic.SysfsSensorFn(),
			RPMFn:             opportunistic.SysfsRPMFn(ch),
			WriteFn:           opportunistic.SysfsWriteFn(ch),
			ObsAppend:         obsWriter.Append,
			Now:               time.Now,
			Logger:            logger,
			SignguardSampleFn: sguFn,
		}
	}
	disabledFn := func() bool {
		if liveCfg == nil {
			return false
		}
		c := liveCfg.Load()
		if c == nil {
			return false
		}
		return c.NeverActivelyProbeAfterInstall
	}
	manualFn := func(ch *probe.ControllableChannel) bool {
		// In ventd, "manual mode" is signalled per-Control by
		// ManualPWM being non-nil (a fixed duty override). The
		// scheduler refuses any channel where any matching Control
		// pins ManualPWM. Curve-mode channels (ManualPWM == nil)
		// are eligible for opportunistic probing.
		if liveCfg == nil {
			return false
		}
		c := liveCfg.Load()
		if c == nil {
			return false
		}
		for _, ctrl := range c.Controls {
			if ctrl.ManualPWM != nil {
				return true
			}
		}
		return false
	}
	idleCfg := idle.OpportunisticGateConfig{
		GateConfig: idle.GateConfig{
			ProcRoot:      "/proc",
			SysRoot:       "/sys",
			AllowOverride: false,
		},
	}
	cfg := opportunistic.SchedulerConfig{
		Channels:               channels,
		Detector:               det,
		ProbeDeps:              depsFor(channels[0]),
		DepsForChannel:         depsFor,
		IdleCfg:                idleCfg,
		FirstInstallMarkerPath: opportunistic.DefaultFirstInstallMarkerPath,
		Disabled:               disabledFn,
		IsManualMode:           manualFn,
		LastProbeAt:            opportunistic.NewKVLastProbeStore(st.KV),
		Logger:                 logger,
	}
	sched, err := opportunistic.NewScheduler(cfg)
	if err != nil {
		logger.Warn("opportunistic scheduler init failed", "err", err)
		return nil
	}
	logger.Info("opportunistic scheduler initialised", "channels", len(channels), "class", cls)
	return sched
}

// buildCouplingRuntime constructs the v0.5.7 Layer-B thermal coupling
// estimator runtime with one shard per controllable channel. Returns
// nil when there are no controllable channels (monitor-only mode);
// the daemon then never starts the coupling goroutine.
//
// N_coupled is fixed at 0 for v0.5.7 — the well-posed reduced-model
// case (R9 §U4) where each shard learns its own thermal time constant
// `a` and load coefficient `c`. v0.5.8 raises N_coupled to enable
// cross-fan b_ij learning once Layer C is in.
//
// hwmonFingerprint invalidates persisted shards on hardware change
// per RULE-CPL-PERSIST-01. dmiFingerprint is host-stable and
// changes when the motherboard changes — sufficient for v0.5.7;
// v0.5.10 doctor refines.
//
// RULE-CPL-WIRING-01: Runtime is constructed only when len(channels) > 0.
// RULE-CPL-WIRING-02: Exactly one shard per controllable channel is registered.
func buildCouplingRuntime(
	channels []*probe.ControllableChannel,
	st *state.State,
	hwmonFingerprint string,
	logger *slog.Logger,
) *coupling.Runtime {
	if len(channels) == 0 {
		logger.Info("coupling: no controllable channels; runtime not started")
		return nil
	}
	rt := coupling.NewRuntime(st.Dir, hwmonFingerprint, logger)
	for _, ch := range channels {
		shard, err := coupling.New(coupling.DefaultConfig(ch.PWMPath, 0))
		if err != nil {
			logger.Warn("coupling: shard init failed",
				"channel", ch.PWMPath, "err", err)
			continue
		}
		if addErr := rt.AddShard(shard); addErr != nil {
			logger.Warn("coupling: AddShard failed",
				"channel", ch.PWMPath, "err", addErr)
			continue
		}
	}
	logger.Info("coupling: runtime initialised",
		"channels", len(channels),
		"hwmon_fp", hwmonFingerprint)
	return rt
}

// runEnvelopeBackground waits for the idle gate then runs the Envelope C/D
// probe sequentially across all controllable channels (RULE-ENVELOPE-11).
// Called from run() as a goroutine; ctx is cancelled when run() returns.
// buildMarginalRuntime constructs the v0.5.8 Layer-C
// marginal-benefit estimator runtime. Returns nil when there are
// no controllable channels (monitor-only mode); the daemon then
// never starts the goroutine.
//
// Per spec-v0_5_8 §6.2: wiring-only. Shards are admitted lazily
// on the first OnObservation call carrying a non-fallback
// signature; v0.5.9 wires the actual feed (sensor readings live
// in the controller, not in the controller→obsWriter Record).
//
// hwmonFingerprint matches v0.5.7's choice — dmiFingerprint —
// for v0.5.8; v0.5.10 doctor refines.
//
// RULE-CMB-WIRING-01: Returns nil when len(channels) == 0.
// RULE-CMB-WIRING-03: Caller starts Run exactly once.
func buildMarginalRuntime(
	channels []*probe.ControllableChannel,
	st *state.State,
	hwmonFingerprint string,
	sguDet *signguard.Detector,
	logger *slog.Logger,
) *marginal.Runtime {
	if len(channels) == 0 {
		logger.Info("marginal: no controllable channels; runtime not started")
		return nil
	}
	// sguDet is passed as the SignguardLookup; nil falls back to
	// noSignguard{} (always-false). Layer-B parents stay nil for now
	// — that wiring gap is tracked separately.
	var sgu marginal.SignguardLookup
	if sguDet != nil {
		sgu = sguDet
	}
	rt := marginal.NewRuntime(st.Dir, hwmonFingerprint, nil, sgu, logger)
	logger.Info("marginal: runtime initialised",
		"channels", len(channels),
		"hwmon_fp", hwmonFingerprint,
		"signguard_wired", sgu != nil)
	return rt
}

// buildLayerAEstimator constructs the v0.5.9 conf_A estimator with one
// channel admitted per controllable PWM. Tier is selected via
// fallback.SelectTier — Tier 0 (tach present), Tier 4 (laptop EC), or
// Tier 7 (open-loop refusal). Per spec-v0_5_9 §2.4 + RULE-CONFA-FORMULA-01.
//
// Returns nil when there are no controllable channels (monitor-only).
func buildLayerAEstimator(
	channels []*probe.ControllableChannel,
	logger *slog.Logger,
) *layer_a.Estimator {
	if len(channels) == 0 {
		logger.Info("layer_a: no controllable channels; estimator not constructed")
		return nil
	}
	est, err := layer_a.New(layer_a.Config{})
	if err != nil {
		logger.Warn("layer_a: New failed", "err", err)
		return nil
	}
	now := time.Now()
	for _, ch := range channels {
		tier := fallback.SelectTier(ch)
		if err := est.Admit(ch.PWMPath, tier, layer_a.DefaultNoiseFloor, now); err != nil {
			logger.Warn("layer_a: Admit failed",
				"channel", ch.PWMPath, "tier", tier, "err", err)
			continue
		}
	}
	logger.Info("layer_a: estimator initialised", "channels", len(channels))
	return est
}

// buildAggregator constructs the v0.5.9 R12-locked confidence
// aggregator. Per-channel state is admitted lazily on first Tick;
// no per-channel pre-warm here.
//
// Returns nil when there are no controllable channels.
func buildAggregator(
	channels []*probe.ControllableChannel,
	logger *slog.Logger,
) *aggregator.Aggregator {
	if len(channels) == 0 {
		logger.Info("aggregator: no controllable channels; not constructed")
		return nil
	}
	a := aggregator.New(aggregator.Config{})
	logger.Info("aggregator: initialised", "channels", len(channels))
	return a
}

// buildBlendedController constructs the v0.5.9 IMC-PI blended
// controller. The Preset enum is resolved from Config.Smart.Preset
// at construction; SIGHUP-driven preset changes require restart for
// the gain cache to refresh (acceptable per spec §3.7 — gains are
// cached for 60-NSamples cycles already).
//
// Returns nil when there are no controllable channels.
func buildBlendedController(
	channels []*probe.ControllableChannel,
	cfg *config.Config,
	logger *slog.Logger,
) *controller.BlendedController {
	if len(channels) == 0 {
		logger.Info("blended: no controllable channels; not constructed")
		return nil
	}
	preset, ok := controller.PresetFromString(cfg.Smart.Preset)
	if !ok {
		logger.Warn("blended: unknown smart.preset; falling back to balanced",
			"got", cfg.Smart.Preset)
	}
	bc := controller.NewBlended(controller.BlendedConfig{
		Preset:     preset,
		PWMUnitMax: 255,
	})
	logger.Info("blended: controller initialised",
		"channels", len(channels), "preset", preset.String())
	return bc
}

// buildBlendFn returns a controller.BlendFn closure that bridges
// the per-controller hot path to the v0.5.9 BlendedController. It
// pulls Layer-B / Layer-C Snapshots, computes conf_A from the
// LayerA estimator, asks the aggregator for w_pred, and routes
// through BlendedController.Compute. PWM bounds come from the live
// fan config so SIGHUP reloads track operator changes.
//
// Returns nil when the smart bundle isn't fully populated (any of
// LayerA / Aggregator / Blended unset) — caller-side check; the
// produced closure assumes non-nil bundle pointers.
//
// The closure is cheap on the hot path: each Snapshot read is a
// single atomic.Pointer load; the aggregator + BlendedController
// each take a per-channel mutex briefly. Total per-call work fits
// in <100µs at d=2 IMC-PI math, well under the 2s controller tick.
func buildBlendFn(
	channelID string,
	fanCfg config.Fan,
	liveCfg *atomic.Pointer[config.Config],
	smart *SmartModeBundle,
	labelFn func() string,
	logger *slog.Logger,
) controller.BlendFn {
	if smart == nil || smart.LayerA == nil || smart.Aggregator == nil || smart.Blended == nil {
		return nil
	}
	// We capture the channel ID and fan config snapshot once. PWM
	// bounds and Setpoint are re-read on every call from the live
	// Config so SIGHUP-driven changes propagate immediately.
	return func(chID string, sensorTemp float64, reactivePWM uint8, dt time.Duration, now time.Time) uint8 {
		live := liveCfg.Load()
		if live == nil {
			return reactivePWM
		}
		setpoint, ok := live.Smart.Setpoints[chID]
		if !ok {
			// Fallback: setpoint matches the bound sensor's normal
			// operating range. Per spec §3.7's silence on absence,
			// we use a class-default of 70°C — a conservative
			// midpoint for desktop CPU/GPU temperatures. v0.6.x
			// can refine this from the system class.
			setpoint = 70.0
		}

		var couplingSnap *coupling.Snapshot
		if smart.Coupling != nil {
			if shard := smart.Coupling.Shard(chID); shard != nil {
				couplingSnap = shard.Read()
			}
		}
		var marginalSnap *marginal.Snapshot
		var sigLabel string
		if smart.Marginal != nil && labelFn != nil {
			sigLabel = labelFn()
			if shard := smart.Marginal.Shard(chID, sigLabel); shard != nil {
				marginalSnap = shard.Read()
			}
		}
		layerASnap := smart.LayerA.Read(chID)

		// conf_A from Layer-A's atomic snapshot.
		var confA float64
		if layerASnap != nil {
			confA = layerASnap.ConfA
		}
		// conf_B from Layer-B's R12 §Q1 four-term product.
		confB := couplingSnap.Confidence()
		// conf_C from Layer-C's per-(channel, signature) product.
		// Returns 0 when the active signature has no warmed shard
		// (R12 §Q6 active-signature collapse — accept the drop, the
		// LPF rides w_pred down at L_max).
		var confC float64
		if marginalSnap != nil && !marginalSnap.WarmingUp {
			cc := marginalSnap.Confidence
			if cc.SaturationAdmit {
				confC = cc.ResidualTerm * cc.CovarianceTerm * cc.SampleCountTerm
			}
		}

		// v0.5.9 ships drift detection as RFCV (R16) future work;
		// stub at false. Global gate is also a stub — true when the
		// daemon is up + has a config + has the wizard outcome
		// "control_mode". This is a coarse approximation of the
		// spec §2.5 4-term AND-gate; doctor-surface refinements
		// land in v0.5.10.
		driftFlags := [3]bool{}
		wPredSystem := true

		aggSnap := smart.Aggregator.Tick(chID, confA, confB, confC,
			driftFlags, wPredSystem, now)
		if aggSnap == nil {
			return reactivePWM
		}

		// Resolve the system load fraction once — same proxy that
		// drives Path-A re-derive at the controller call site.
		// Cheap: idle.CaptureLoadAvg reads /proc/loadavg.
		loadFrac := captureLoadFraction()

		res := smart.Blended.Compute(controller.BlendedInputs{
			ChannelID:    chID,
			SensorTemp:   sensorTemp,
			Setpoint:     setpoint,
			ReactivePWM:  reactivePWM,
			WPred:        aggSnap.Wpred,
			Coupling:     couplingSnap,
			Marginal:     marginalSnap,
			LayerA:       layerASnap,
			LoadFraction: loadFrac,
			DT:           dt,
			Now:          now,
			MinPWM:       fanCfg.MinPWM,
			MaxPWM:       fanCfg.MaxPWM,
		})

		// First-contact mark: persisted only after the clamped tick
		// succeeds — same lifecycle as RULE-CTRL-BLEND-02.
		if res.FirstContactClamp || (res.WPred > 0 && layerASnap != nil && !layerASnap.SeenFirstContact) {
			smart.LayerA.MarkFirstContact(chID, now)
		}
		return res.OutputPWM
	}
}

// captureLoadFraction returns the system's normalised load over
// the last minute, in [0, 1]. Reuses idle.CaptureLoadAvg +
// LoadAvgPerCPU so the v0.5.9 controller and the existing R5 idle
// gate share a single /proc/loadavg parser. Stub for v0.5.9 —
// v0.6.x can substitute PSI-based fractions for laptop / NAS
// classes where loadavg is noisy.
func captureLoadFraction() float64 {
	la := idle.CaptureLoadAvg("/proc")
	frac := idle.LoadAvgPerCPU(la)
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	return frac
}

func runEnvelopeBackground(
	ctx context.Context,
	st *state.State,
	det *sysclass.Detection,
	pr *probe.ProbeResult,
	cfg *config.Config,
	logger *slog.Logger,
) {
	if !sysclass.ServerProbeAllowed(det.Class, false, cfg.Envelope.AllowServerProbe) {
		logger.Info("envelope: server probe suppressed; pass --allow-server-probe to enable",
			"class", det.Class)
		return
	}
	if code, ok := sysclass.AmbientBoundsOK(det.AmbientSensor.Reading); !ok {
		logger.Warn("envelope: ambient reading outside [10,50]°C, probe deferred",
			"code", code, "reading_c", det.AmbientSensor.Reading)
		return
	}

	idleCfg := idle.GateConfig{
		ProcRoot:      "/proc",
		SysRoot:       "/sys",
		AllowOverride: cfg.Idle.AllowOverride,
	}
	if cfg.Idle.Durability.Duration > 0 {
		idleCfg.Durability = cfg.Idle.Durability.Duration
	}
	if cfg.Idle.TickInterval.Duration > 0 {
		idleCfg.TickInterval = cfg.Idle.TickInterval.Duration
	}
	gateOK, reason, _ := idle.StartupGate(ctx, idleCfg)
	if !gateOK {
		logger.Info("envelope: idle gate not met, probe deferred", "reason", reason)
		return
	}
	logger.Info("envelope: idle gate cleared, starting envelope C/D probe")

	type chanEntry struct {
		ch      *probe.ControllableChannel
		writeFn func(uint8) error
	}
	var entries []chanEntry
	for i := range pr.ControllableChannels {
		ch := &pr.ControllableChannels[i]
		if !polarity.IsControllable(ch) {
			continue
		}
		pwmPath := ch.PWMPath
		entries = append(entries, chanEntry{
			ch:      ch,
			writeFn: func(v uint8) error { return hwmon.WritePWM(pwmPath, v) },
		})
	}
	if len(entries) == 0 {
		logger.Info("envelope: no controllable channels, skipping probe")
		return
	}

	sensorFn := func(_ context.Context) (map[string]float64, error) {
		temps := make(map[string]float64)
		for _, ts := range pr.ThermalSources {
			for _, sc := range ts.Sensors {
				if v, err := hwmon.ReadValue(sc.Path); err == nil {
					temps[sc.Path] = v
				}
			}
		}
		return temps, nil
	}

	for _, e := range entries {
		tachPath := e.ch.TachPath
		rpmFn := func(_ context.Context) (uint32, error) {
			if tachPath == "" {
				return 0, nil
			}
			v, err := hwmon.ReadValue(tachPath)
			if err != nil {
				return 0, fmt.Errorf("read rpm %s: %w", tachPath, err)
			}
			return uint32(v), nil
		}
		p := envelope.NewProber(envelope.ProberConfig{
			State:    st,
			Class:    det.Class,
			Tjmax:    det.Tjmax,
			Ambient:  det.AmbientSensor.Reading,
			SensorFn: sensorFn,
			RPMFn:    rpmFn,
			IdleGate: idle.StartupGate,
			IdleCfg:  idleCfg,
			Logger:   logger,
		})
		ch := e.ch
		if probeErr := p.Probe(ctx,
			[]*probe.ControllableChannel{ch},
			[]func(uint8) error{e.writeFn},
		); probeErr != nil {
			logger.Warn("envelope: probe failed", "channel", ch.PWMPath, "err", probeErr)
		}
	}
	logger.Info("envelope: probe complete", "channels", len(entries))
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
//
// oppFactory builds the v0.5.5 opportunistic-probe scheduler once
// liveCfg is in scope; pass nil from tests that do not exercise the
// scheduler path.
func runDaemon(
	parentCtx context.Context,
	cfg *config.Config,
	configPath string,
	authPath string,
	logger *slog.Logger,
	sigCh <-chan os.Signal,
	expFlags experimental.Flags,
	kvWiper func() error,
	oppFactory OpportunisticFactory,
	smartMode *SmartModeBundle,
) error {
	restartCh := make(chan struct{}, 1)
	return runDaemonInternal(parentCtx, cfg, configPath, authPath, logger, sigCh, restartCh, expFlags, kvWiper, oppFactory, smartMode)
}

// OpportunisticFactory constructs the v0.5.5 opportunistic-probe
// scheduler. It receives a pointer to the live config atomic so the
// scheduler can react to in-process config reloads. Returns nil when
// the daemon is in monitor-only mode or the writer was unavailable;
// runDaemonInternal then skips the scheduler goroutine.
type OpportunisticFactory func(liveCfg *atomic.Pointer[config.Config]) *opportunistic.Scheduler

// SmartModeBundle bundles the v0.5.6+ runtime dependencies that
// runDaemonInternal needs but that are derived in run(): signature
// library, observation-log append closure, persistent state for
// signature persistence. All fields nil-safe — when the bundle is
// nil or any field is nil, runDaemonInternal skips the corresponding
// wiring.
type SmartModeBundle struct {
	// SigFactory builds the signature library against the live cfg.
	// Returns nil to skip the library entirely.
	SigFactory func(liveCfg *atomic.Pointer[config.Config]) *signature.Library
	// State is the spec-16 state handle used for signature
	// persistence (Save / SaveManifest).
	State *state.State
	// ObsAppend is the closure controllers call after every PWM
	// write. nil means the controller skips observation emission
	// (pre-v0.5.6 behaviour).
	ObsAppend func(*controller.ObsRecord)
	// Coupling is the v0.5.7 Layer-B thermal coupling estimator
	// runtime. Pre-built with one shard per controllable channel.
	// nil in monitor-only mode (no controllable channels) — daemon
	// then never starts the coupling goroutine. Snapshot.Read is
	// consumed by v0.5.9's confidence-gated controller.
	Coupling *coupling.Runtime
	// Marginal is the v0.5.8 Layer-C per-(channel, signature)
	// marginal-benefit estimator runtime. Pre-built but admits
	// shards lazily on observation. nil in monitor-only mode and
	// when SmartMarginalBenefitDisabled is true (toggle read by
	// runDaemonInternal). Snapshot.Read is consumed by v0.5.9's
	// confidence-gated controller.
	Marginal *marginal.Runtime

	// LayerA is the v0.5.9 conf_A estimator (R8 fallback tier ×
	// coverage × residual × recency). Pre-built with one channel
	// admitted per controllable channel via fallback.SelectTier.
	// nil in monitor-only mode.
	LayerA *layer_a.Estimator

	// Aggregator is the v0.5.9 R12-locked confidence aggregator
	// that collapses (conf_A, conf_B, conf_C) into a per-channel
	// w_pred ∈ [0,1] every controller tick. Lock-free reads via
	// atomic.Pointer. nil in monitor-only mode.
	Aggregator *aggregator.Aggregator

	// Blended is the v0.5.9 IMC-PI confidence-gated controller.
	// Compute() takes the upstream Snapshots + reactive PWM and
	// returns the blended PWM. nil in monitor-only mode and when
	// no controllable channels exist. Drives the per-controller
	// BlendFn closure constructed in runDaemonInternal.
	Blended *controller.BlendedController
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
	sigCh <-chan os.Signal,
	restartCh chan struct{},
	expFlags experimental.Flags,
	kvWiper func() error,
	oppFactory OpportunisticFactory,
	smartMode *SmartModeBundle,
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
	// Persistent applied-marker so a host that opted into monitor-only
	// mode (handleSetupApply's empty-fanset escape) stays out of the
	// /calibration redirect on every subsequent daemon restart even
	// though len(cfg.Controls) == 0 keeps Needed(cfg) saying yes.
	setupMgr.SetAppliedMarkerPath(setupmgr.DefaultAppliedMarkerPath)
	// Re-run the daemon-level hardware probe and persist the updated
	// outcome to KV after a successful driver install / module load
	// (#766). Without this, a fresh install whose driver populates pwm
	// channels mid-wizard leaves wizard.initial_outcome at "monitor_only"
	// until the next daemon restart, so the wizard's apply step (or any
	// other KV consumer) reads stale state. State access goes via the
	// SmartModeBundle since `st` is owned by run() and passed to
	// runDaemonInternal only through the bundle.
	if smartMode != nil && smartMode.State != nil {
		kv := smartMode.State.KV
		setupMgr.SetReProber(func(ctx context.Context) error {
			r, probeErr := probe.New(probe.Config{Logger: logger}).Probe(ctx)
			if probeErr != nil {
				return fmt.Errorf("re-probe: %w", probeErr)
			}
			return probe.PersistOutcome(kv, r)
		})
	}

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
	webSrv := web.New(ctx, &liveCfg, configPath, authPath, logger, cal, setupMgr, restartCh, diagStore)
	webSrv.SetVersionInfo(web.NewVersionInfo(version, commit, buildDate))
	webSrv.SetReadyState(readyState)
	webSrv.SetKVWiper(kvWiper)

	// v0.5.9: expose the aggregator + LayerA estimator to the web
	// layer so the dashboard's 5-state confidence pill has a data
	// source. Both arguments are nil-safe — monitor-only mode
	// skips construction and the endpoint reports enabled=false.
	if smartMode != nil {
		webSrv.SetConfidence(smartMode.Aggregator, smartMode.LayerA)
	}

	// v0.5.5: build and launch the opportunistic-probe scheduler when
	// the factory is set (production path). Tests pass a nil factory
	// and skip this entire block.
	if oppFactory != nil {
		oppSched := oppFactory(&liveCfg)
		if oppSched != nil {
			webSrv.SetOpportunisticScheduler(oppSched)
			oppCtx, oppCancel := context.WithCancel(ctx)
			defer oppCancel()
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := oppSched.Run(oppCtx); err != nil && err != context.Canceled {
					logger.Warn("opportunistic scheduler exited", "err", err)
				}
			}()
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := webSrv.ListenAndServe(cfg.Web.Listen, cfg.Web.TLSCert, cfg.Web.TLSKey); err != nil {
			errCh <- fmt.Errorf("web server: %w", err)
		}
	}()

	// In setup-wizard mode (no controls configured yet) the controllers
	// loop below is skipped, but systemd's Type=notify still expects a
	// READY=1 within TimeoutStartSec or it kills the daemon mid-wizard
	// (issue #694). Send READY=1 here for the no-controls path so the
	// service transitions to "active" as soon as the web server is
	// listening — the wizard then runs against an active unit, with
	// WatchdogSec only meaningful once the control loop is up. The
	// post-controllers Notify(Ready) below remains the canonical signal
	// for the configured-controls path; sd_notify is idempotent so a
	// duplicate is harmless.
	if len(cfg.Controls) == 0 {
		_ = sdnotify.Notify(sdnotify.Ready)
		readyState.SetHealthy()
	}

	// v0.5.6: build and launch the workload signature library via the
	// SmartModeBundle factory. Returns nil in monitor-only mode, in
	// containers/VMs (R1 Tier-2 BLOCK), on hardware-refused platforms
	// (R3), or when SignatureLearningDisabled is true. Controllers
	// thread the library's lock-free Label() reader into their
	// observation-log emission via WithObservation.
	var sigLib *signature.Library
	if smartMode != nil && smartMode.SigFactory != nil {
		sigLib = smartMode.SigFactory(&liveCfg)
	}
	if sigLib != nil && smartMode.State != nil {
		sigCtx, sigCancel := context.WithCancel(ctx)
		defer sigCancel()
		wg.Add(1)
		go func() {
			defer wg.Done()
			runSignatureTickLoop(sigCtx, sigLib, smartMode.State, logger)
		}()
	}

	// v0.5.7: launch the Layer-B thermal coupling estimator runtime.
	// Per spec §8.2 PR-B: wiring-only — the snapshot is read by
	// v0.5.9's confidence-gated controller, not the v0.5.7 hot loop.
	// RULE-CPL-WIRING-03: Runtime.Run goroutine started exactly once
	// per daemon lifetime, scoped to ctx.
	if smartMode != nil && smartMode.Coupling != nil {
		cplCtx, cplCancel := context.WithCancel(ctx)
		defer cplCancel()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := smartMode.Coupling.Run(cplCtx); err != nil && err != context.Canceled {
				logger.Warn("coupling: runtime exited with error", "err", err)
			}
		}()
	}

	// v0.5.8: launch the Layer-C marginal-benefit estimator runtime.
	// Same lifecycle pattern as Layer B — wiring-only, snapshot
	// consumed by v0.5.9's controller. Disabled by toggle.
	// RULE-CMB-WIRING-03.
	if smartMode != nil && smartMode.Marginal != nil &&
		!cfg.SmartMarginalBenefitDisabled {
		mgnCtx, mgnCancel := context.WithCancel(ctx)
		defer mgnCancel()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := smartMode.Marginal.Run(mgnCtx); err != nil && err != context.Canceled {
				logger.Warn("marginal: runtime exited with error", "err", err)
			}
		}()
	}

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
			// v0.5.6: stamp every successful PWM write into the
			// observation log with the current signature label.
			// Closes the v0.5.4 controller→obsWriter gap.
			if smartMode != nil && smartMode.ObsAppend != nil {
				labelFn := func() string { return signature.FallbackLabelDisabled }
				if sigLib != nil {
					labelFn = sigLib.Label
				}
				opts = append(opts, controller.WithObservation(
					smartMode.ObsAppend, labelFn,
				))
			}
			// v0.5.9: install the confidence-gated blend hook when
			// the smart-mode bundle has a BlendedController. The
			// closure pulls the per-channel Snapshots from the
			// upstream runtimes, computes w_pred via the aggregator,
			// and routes through BlendedController.Compute.
			if smartMode != nil && smartMode.Blended != nil {
				labelFn := func() string { return signature.FallbackLabelDisabled }
				if sigLib != nil {
					labelFn = sigLib.Label
				}
				blendFn := buildBlendFn(
					fanCfg.PWMPath, fanCfg, &liveCfg,
					smartMode, labelFn, logger,
				)
				if blendFn != nil {
					opts = append(opts, controller.WithBlend(blendFn))
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
					// v0.5.6: same observation wiring as the
					// initial-startup path.
					if smartMode != nil && smartMode.ObsAppend != nil {
						labelFn := func() string { return signature.FallbackLabelDisabled }
						if sigLib != nil {
							labelFn = sigLib.Label
						}
						reloadOpts = append(reloadOpts, controller.WithObservation(
							smartMode.ObsAppend, labelFn,
						))
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

// buildSignatureLibrary constructs the v0.5.6 workload signature
// library. Returns nil when the daemon is in monitor-only mode or
// the operator has set Config.SignatureLearningDisabled. Tier-2
// (R1) and hardware-refused (R3) inheritance is reflected via the
// disable gate; we don't gate explicitly here because the signature
// library's Disabled config is checked on every Tick.
func buildSignatureLibrary(
	channels []*probe.ControllableChannel,
	sysDet *sysclass.Detection,
	liveCfg *atomic.Pointer[config.Config],
	logger *slog.Logger,
) *signature.Library {
	if len(channels) == 0 {
		logger.Info("signature: no controllable channels; library not started")
		return nil
	}

	saltPath := signature.DefaultSaltPath
	salt, err := signature.LoadOrCreateSalt(saltPath)
	if err != nil {
		logger.Warn("signature: salt load failed; library not started", "err", err)
		return nil
	}
	hasher, err := signature.NewHasher(salt)
	if err != nil {
		logger.Warn("signature: hasher init failed; library not started", "err", err)
		return nil
	}

	cfg := signature.DefaultConfig()
	if c := liveCfg.Load(); c != nil && c.SignatureLearningDisabled {
		signature.ApplyDisableGate(&cfg, signature.DisableReasonOperatorToggle)
	}

	lib := signature.NewLibrary(cfg, hasher, signature.NewMaintenanceBlocklist(), logger)
	logger.Info("signature: library initialised",
		"channels", len(channels),
		"disabled", cfg.Disabled,
		"sysclass", sysDet.Class)
	return lib
}

// runSignatureTickLoop drives the signature library's 2-second
// EWMA tick. Walks /proc on every tick, feeds samples into Tick,
// persists buckets every minute. Exits cleanly on context cancel.
func runSignatureTickLoop(
	ctx context.Context,
	lib *signature.Library,
	st *state.State,
	logger *slog.Logger,
) {
	walker := proc.New("/proc", 0, 0)
	tickInterval := signature.DefaultHalfLife // 2 s
	persistEvery := 30 * tickInterval         // 60 s

	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	persistTicker := time.NewTicker(persistEvery)
	defer persistTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			samples, err := walker.Walk()
			if err != nil {
				logger.Debug("signature: proc walk error", "err", err)
				continue
			}
			lib.Tick(time.Now(), samples)
		case <-persistTicker.C:
			if err := lib.Save(st.KV); err != nil {
				logger.Warn("signature: persist failed", "err", err)
			}
			if err := lib.SaveManifest(st.KV); err != nil {
				logger.Warn("signature: manifest persist failed", "err", err)
			}
		}
	}
}

// buildObsAppend returns the closure that controllers call after
// every successful PWM write. Maps the controller's package-local
// ObsRecord shape to the real observation.Record (computing
// ChannelID from the path) and calls Writer.Append.
//
// SensorReadings is converted from map[string]float64 (sensor name →
// °C) to map[uint16]int16 (SensorID → centi-celsius) so the persisted
// schema obeys RULE-OBS-PRIVACY-02 (no unconstrained string keys).
// Readings outside [-150°C, 150°C] are filtered (RULE-HWMON-SENTINEL-
// TEMP-CAP plausibility bound) — defensive only; the controller's
// readAllSensors already filters sentinels before populating
// rawSensorsBuf.
//
// Errors from Append are logged at warn level and swallowed —
// observation loss is preferable to a stalled control loop.
func buildObsAppend(obsWriter *observation.Writer) func(*controller.ObsRecord) {
	return func(rec *controller.ObsRecord) {
		obsRec := &observation.Record{
			Ts:             rec.Ts,
			ChannelID:      observation.ChannelID(rec.PWMPath),
			PWMWritten:     rec.PWMWritten,
			RPM:            rec.RPM,
			SignatureLabel: rec.SignatureLabel,
			EventFlags:     rec.EventFlags,
			SensorReadings: convertSensorReadings(rec.SensorReadings),
		}
		_ = obsWriter.Append(obsRec)
	}
}

// convertSensorReadings translates the controller's name→°C map into
// the observation log's SensorID→centi-celsius shape. Skips readings
// outside the sensible plausibility band so a sentinel that escaped
// the controller's read-side filter cannot reach the persisted log.
func convertSensorReadings(readings map[string]float64) map[uint16]int16 {
	if len(readings) == 0 {
		return nil
	}
	out := make(map[uint16]int16, len(readings))
	for name, celsius := range readings {
		if celsius < -150 || celsius > 150 {
			continue
		}
		out[observation.SensorID(name)] = int16(celsius * 100)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
