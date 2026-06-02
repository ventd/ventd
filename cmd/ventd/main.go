package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ventd/ventd/internal/acoustic/budget"
	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/confidence/aggregator"
	"github.com/ventd/ventd/internal/confidence/drift"
	"github.com/ventd/ventd/internal/confidence/gate"
	"github.com/ventd/ventd/internal/confidence/layer_a"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/coupling"
	"github.com/ventd/ventd/internal/coupling/signguard"
	"github.com/ventd/ventd/internal/doctor/detectors"
	"github.com/ventd/ventd/internal/ebusy"
	"github.com/ventd/ventd/internal/envelope"
	"github.com/ventd/ventd/internal/experimental"
	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/hwdiag"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/idle"
	"github.com/ventd/ventd/internal/lastfatal"
	"github.com/ventd/ventd/internal/marginal"
	"github.com/ventd/ventd/internal/massstall"
	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/observation"
	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/probe/opportunistic"
	"github.com/ventd/ventd/internal/proc"
	"github.com/ventd/ventd/internal/sdnotify"
	"github.com/ventd/ventd/internal/sensorfreeze"
	setupmgr "github.com/ventd/ventd/internal/setup"
	"github.com/ventd/ventd/internal/signature"
	"github.com/ventd/ventd/internal/state"
	"github.com/ventd/ventd/internal/sysclass"
	"github.com/ventd/ventd/internal/validity"
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

// nvmlInitTimeout caps NVML library load + nvmlInit_v2 at startup so
// a partial NVIDIA driver install (mismatched DKMS, stale .so symbols,
// kernel module wedge) cannot hang daemon startup past systemd's
// TimeoutStartSec. Per RULE-GPU-PR2D-09. 2 s is well above the typical
// cold-load wall time (~50-200 ms) and tight enough that a hung dlopen
// surfaces a recoverable failure rather than a wedged daemon.
const nvmlInitTimeout = 2 * time.Second

func main() {
	if err := run(); err != nil {
		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
		logger.Error("ventd: fatal", "err", err)
		// Issue #1165: persist a one-line summary so the next
		// successful start (or an operator running ventd --health)
		// can name what went wrong. Without this, restart-looping
		// daemons hit systemd's "Start request repeated too quickly"
		// with no surface other than `journalctl -u ventd`.
		lastfatal.Write(state.EffectiveDir(), version, err)
		os.Exit(1)
	}
}

// defaultLoopbackListen is the Empty()-defaulted bind: loopback only, satisfying
// RULE-INSTALL-03 (no plaintext bind on 0.0.0.0).
const defaultLoopbackListen = "127.0.0.1:9999"

// promoteLoopbackDefaultToWildcard rebinds the loopback default to the LAN
// wildcard so the URL install.sh prints is reachable from other machines. It
// promotes ONLY the loopback default (an operator-provisioned bind is left
// untouched) and ONLY when TLS is active (so the promoted bind is never
// plaintext — RULE-INSTALL-03). Applied identically on first-boot and on every
// later start, so the LAN bind persists across reboots rather than being lost
// with the in-memory first-boot promotion. Returns true if it promoted.
// RULE-INSTALL-LAN-PROMOTION-PERSISTS.
func promoteLoopbackDefaultToWildcard(cfg *config.Config, logger *slog.Logger) bool {
	if !cfg.Web.TLSEnabled() || cfg.Web.Listen != defaultLoopbackListen {
		return false
	}
	_, port, err := net.SplitHostPort(cfg.Web.Listen)
	if err != nil {
		return false
	}
	cfg.Web.Listen = net.JoinHostPort("0.0.0.0", port)
	logger.Info("promoting listen address to LAN wildcard (TLS active)", "listen", cfg.Web.Listen)
	return true
}

// fileExists reports whether path names an existing regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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

	// `ventd calibrate --acoustic <mic_device>` runs R30's mic-
	// calibration capture (v0.5.12 PR-D). Subcommand owns its flag
	// parsing for the same reason as preflight — the daemon flags
	// would error on --acoustic etc.
	if len(os.Args) >= 2 && os.Args[1] == "calibrate" {
		logger := buildLogger("info")
		return runCalibrateAcoustic(os.Args[2:], logger)
	}

	// `ventd import-sensors-conf <path>` — T1.4 — parses an
	// lm-sensors sensors.conf community file and emits a ventd
	// hwdb chip-overlay YAML document. Stdout by default; `--out
	// <path>` writes to the pending-overlay directory at
	// /var/lib/ventd/profiles-pending/<name>.yaml alongside the
	// existing `ventd hwdb capture` outputs.
	if len(os.Args) >= 2 && os.Args[1] == "import-sensors-conf" {
		logger := buildLogger("info")
		os.Exit(runImportSensorsConf(os.Args[2:], logger))
	}

	// `ventd power-profile [get | set <name>]` exposes the optional
	// hal.PowerProfileBackend surface (#1166). Today this is msi-ec's
	// shift_mode (eco / comfort / turbo); future thinkpad / asus /
	// Legion HAL backends extend the same subcommand without code
	// changes here.
	if len(os.Args) >= 2 && os.Args[1] == "power-profile" {
		powerProfileMain()
	}

	// `ventd version` mirrors the --version flag for operators who
	// type the subcommand instinctively. Both forms must short-circuit
	// before any subsystem init — they MUST NOT load /etc/ventd/config.yaml
	// (which is mode 0600 since v0.5.8.1's User=root flip; an unprivileged
	// invocation would fatal-error with "permission denied" on a command
	// that should just print the version and exit). Accepts an optional
	// `--json` arg to mirror `--version --json`.
	if len(os.Args) >= 2 && os.Args[1] == "version" {
		emitJSON := false
		for _, a := range os.Args[2:] {
			if a == "--json" || a == "-json" {
				emitJSON = true
			}
		}
		return printVersion(os.Stdout, emitJSON)
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
	enableAMDOverdrive := flag.Bool("enable-amd-overdrive", false, "enable AMD OverDrive fan curve interface (experimental; requires amd_overdrive precondition)")
	enableNVIDIACoolbits := flag.Bool("enable-nvidia-coolbits", false, "enable NVIDIA Coolbits fan control (experimental; requires nvidia_coolbits precondition)")
	enableILO4Unlocked := flag.Bool("enable-ilo4-unlocked", false, "enable HPE iLO4 unlocked fan control (experimental; requires ilo4_unlocked precondition)")
	enableIDRAC9LegacyRaw := flag.Bool("enable-idrac9-legacy-raw", false, "enable Dell iDRAC9 legacy raw IPMI fan commands (experimental; requires idrac9_legacy_raw precondition)")
	strictIdleGate := flag.Bool("strict-idle-gate", false, "revert OpportunisticGate to the v0.5.x strict evaluator (600s durability + tight PSI thresholds); default v0.6.0+ is the soft-idle gate so smart-mode can learn under realistic workload (RULE-OPP-IDLE-SOFT-MODE)")
	micDevice := flag.String("mic", "", "with --setup: ALSA mic device for opt-in R30 acoustic calibration (e.g. hw:CARD=USB,DEV=0). Empty = skip the calibrate_acoustic phase.")
	micRefSPL := flag.Float64("mic-ref-spl", 94.0, "with --setup --mic: reference-tone SPL at the mic in dB (default 94, the standard pistonphone)")
	micSeconds := flag.Int("mic-seconds", 30, "with --setup --mic: mic capture duration in seconds (5..60)")
	micOut := flag.String("mic-out", "", "with --setup --mic: write the calibration JSON to this path; empty = print summary only")
	showVersion := flag.Bool("version", false, "print version information and exit")
	versionJSON := flag.Bool("json", false, "with --version: emit JSON instead of plain text")
	shadowMode := flag.Bool("shadow", false, "shadow mode: run the full smart-mode + reactive pipeline (calibration learning, decisions, recent-decisions feed) but issue NO hardware writes — every PWM/platform_profile write is logged as a would-write instead. Migration on-ramp for evaluating ventd alongside an existing controller. Equivalent to apply.shadow: true in config.yaml")
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
		return runSetup(*configPath, logger, acousticOptionsFromFlags(*micDevice, *micRefSPL, *micSeconds, *micOut), *enableAMDOverdrive)
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

	// Safety-gate (#600, RULE-CAL-ALLOW-STOP-GATED): refuse to start if
	// any fan has allow_stop=true without stall calibration backing it.
	// MinPWM=0 + allow_stop=true means the daemon will write PWM=0 on
	// the curve's bottom — but unless calibrate measured both pwm_min_start
	// and pwm_min_running, the daemon has no way to know whether PWM=0
	// gracefully stops or unsafely stalls the fan. Fresh installs without
	// AllowStop fans pass vacuously.
	if !firstBoot {
		if err := calibrate.VerifyAllowStopGate(cfg, calibrate.DefaultCalibrationPath); err != nil {
			return fmt.Errorf("startup safety: %w", err)
		}
	}

	// Shadow mode: CLI --shadow forces it on regardless of config; the
	// config knob (apply.shadow) holds otherwise. Overlay onto the loaded
	// config so every live-config reader (controllers, web server,
	// platform_profile controller) sees one source of truth. Log loudly —
	// an operator who forgot they're in shadow mode and wonders why their
	// fans aren't responding should find the answer in journald.
	if *shadowMode {
		cfg.Apply.Shadow = true
	}
	if cfg.Apply.Shadow {
		logger.Warn("shadow mode active: ventd will NOT write any fan PWM or platform_profile values; "+
			"the full pipeline runs and the recent-decisions feed shows what ventd WOULD do. "+
			"Calibration is disabled. Remove --shadow / apply.shadow and restart to take over fan control.",
			"event", "shadow_mode_active")
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
			// RULE-INSTALL-03 (no plaintext bind on 0.0.0.0). Now that TLS is
			// active, promote loopback to the LAN wildcard so the URL install.sh
			// prints resolves from other machines. First-boot LAN enrolment is
			// gated by the one-time setup token (#1466), so the open window is
			// not a takeover risk.
			promoteLoopbackDefaultToWildcard(cfg, logger)
		}
	}

	// On a later start (no longer first-boot) the first-boot promotion is gone —
	// it was applied in memory above and never persisted — so without this the
	// daemon falls back to loopback after a reboot and the LAN URL that worked
	// during setup breaks (RULE-INSTALL-LAN-PROMOTION-PERSISTS). Re-adopt the
	// self-signed cert generated during first-boot (present at the default paths
	// but possibly not named in config.yaml), then re-apply the same promotion
	// so the LAN bind persists across restarts.
	if !firstBoot && !cfg.Web.TLSEnabled() {
		dir := filepath.Dir(*configPath)
		certPath := filepath.Join(dir, "tls.crt")
		keyPath := filepath.Join(dir, "tls.key")
		if fileExists(certPath) && fileExists(keyPath) {
			cfg.Web.TLSCert = certPath
			cfg.Web.TLSKey = keyPath
			logger.Info("adopting self-signed cert generated on a prior first-boot", "cert", certPath)
		}
	}
	if !firstBoot {
		promoteLoopbackDefaultToWildcard(cfg, logger)
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

	// Provision the profiles-pending directory and register the post-calibration
	// capture hook. Capture is best-effort: directory creation failure is logged
	// but does not abort startup. RULE-HWDB-CAPTURE-01.
	pendingDir := hwdb.CaptureDir()
	if mkErr := os.MkdirAll(pendingDir, 0o750); mkErr != nil {
		logger.Warn("capture: cannot create profiles-pending dir", "dir", pendingDir, "err", mkErr)
	}
	captureDMI, capDMIErr := hwdb.ReadDMI(os.DirFS("/"))
	captureCat, capCatErr := hwdb.LoadCatalog()
	if capDMIErr == nil && capCatErr == nil && captureCat != nil {
		// Publish the matched board entry to smart_builders so the
		// acoustic-budget builder picks per-channel FanClass +
		// diameter from the curated catalog when present (#1283).
		// Heuristics-only when no entry matches — no regression.
		dmiFP := hwdb.DMIFingerprint{
			SysVendor:    captureDMI.SysVendor,
			ProductName:  captureDMI.ProductName,
			BoardVendor:  captureDMI.BoardVendor,
			BoardName:    captureDMI.BoardName,
			BoardVersion: captureDMI.BoardVersion,
		}
		if entry := findMatchingBoardEntry(captureCat, dmiFP); entry != nil {
			budget.SetFanProfileCatalog(entry)
			logger.Info("acoustic: fan-profile catalog wired",
				"board_id", entry.ID, "fan_profiles", len(entry.FanProfiles))
		}
	}
	if capDMIErr != nil || capCatErr != nil {
		logger.Warn("capture: hook disabled (DMI or catalog unavailable)",
			"dmi_err", capDMIErr, "cat_err", capCatErr)
	} else {
		validity.SetCaptureHook(func(run *hwdb.CalibrationRun) {
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
	// The dir is resolved via EffectiveDir so VENTD_STATE_DIR redirects the
	// pidfile and every store together (RULE-STATE-11).
	stateDir := state.EffectiveDir()
	if state.DirIsOverridden() {
		logger.Warn("state: VENTD_STATE_DIR override active — using a non-default state directory, NOT "+state.DefaultDir,
			"dir", stateDir, "env", state.DirOverrideEnv)
	}
	releasePID, pidErr := state.AcquirePID(stateDir)
	if pidErr != nil {
		return fmt.Errorf("acquire pid: %w", pidErr)
	}
	defer releasePID()
	st, stErr := state.Open(stateDir, logger)
	if stErr != nil {
		// Issue #1090: branch on the documented state sentinels so the
		// operator-facing diagnostic names the actual remediation,
		// rather than the generic "open state: ..." wrap that always
		// resolves to ClassUnknown. The sentinels are already
		// errors.Is-compatible; the production daemon just wasn't
		// consulting them.
		switch {
		case errors.Is(stErr, state.ErrDowngrade):
			logger.Error("ventd: refusing to start — on-disk state was written by a newer ventd",
				"err", stErr,
				"remediation", "reinstall the newer ventd version, or run 'ventd state reset' to discard the on-disk state")
		case errors.Is(stErr, state.ErrCorruptState):
			logger.Error("ventd: refusing to start — state.yaml failed to parse",
				"err", stErr,
				"remediation", "inspect /var/lib/ventd/state.yaml for hand-editing or partial-write damage, or run 'ventd state reset' to discard and re-bootstrap")
		case errors.Is(stErr, state.ErrTransactionPersistFailed):
			logger.Error("ventd: refusing to start — last transaction failed to persist",
				"err", stErr,
				"remediation", "check disk space (df -h /var/lib/ventd) and dmesg for I/O errors; the in-memory rollback is intact but the next start hit the same issue")
		}
		return fmt.Errorf("open state: %w", stErr)
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("state close", "err", err)
		}
	}()
	// Issue #1165: startup has progressed past the modes the last-
	// fatal sentinel covers (config load, state.Open). Any sentinel
	// from a prior boot would lie if it survived — clear it.
	lastfatal.Clear(state.EffectiveDir())
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
	//
	// InitWithDeadline guards against a hung purego.Dlopen on partial
	// driver installs (mismatched DKMS, stale libnvidia-ml.so.1 symbols,
	// kernel module wedge). Without the deadline, daemon startup can
	// hang past systemd's TimeoutStartSec with no diagnostic the
	// operator can act on. Per RULE-GPU-PR2D-09: timeout fire returns
	// wrapped ErrLibraryUnavailable; the orphaned dlopen goroutine
	// leaks for process lifetime, by design.
	if err := nvidia.InitWithDeadline(context.Background(), logger, nvmlInitTimeout); err == nil {
		defer nvidia.Shutdown()
	}

	// Register fan backends with the HAL registry. The controllers and
	// watchdog construct their own per-instance backends for scoped
	// logging; the registry entries here exist so hal.Enumerate /
	// hal.Resolve can drive Phase 2 features (IPMI / liquidctl / cros_ec
	// / pwmsys / asahi inventory in the web UI, diagnostics probes) off
	// a single source of truth.
	registerHALBackends(logger, expFlags.AMDOverdrive)
	if channels, err := hal.Enumerate(context.Background()); err != nil {
		logger.Warn("hal: initial enumerate failed", "err", err)
	} else {
		logger.Info("hal: enumerated fan backends", "channels", len(channels))
	}

	// Diagnose hwmon state at startup. READ-ONLY — the daemon runs
	// under ProtectKernelModules=yes and cannot modprobe; modules are
	// loaded by `ventd --probe-modules` at install time. Runs after
	// registerHALBackends so the "no PWM visible" path can consult
	// hal.Enumerate and downgrade to INFO when a non-hwmon backend
	// (msi-ec, thinkpad, ipmi, …) owns the fan-control surface (#1163).
	hwmon.DiagnoseHwmon(logger)
	hwmon.DiagnoseDellSMMVersion(logger)

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
	// Construct the watchdog before any goroutine that may write to PWM
	// channels — the first-boot polarity probe below writes to pwm* even
	// when cfg.Fans is empty, and without watchdog coverage those writes
	// leak pwm_enable=1 across daemon exit (issue #1312). Owned by run()
	// so the deferred Restore fires AFTER the polarity goroutine has
	// fully exited (defer LIFO: probeCancel → polarityWG.Wait → Restore).
	// runDaemonInternal receives this same instance via runDaemon's
	// parameter list and registers cfg.Fans / IPMI / NBFC channels onto
	// it in the usual way.
	//
	// LastKnownStore (#1331) is wired through a state.KVDB wrapper so
	// the post-stable-identity persistence shape is alive on every
	// production daemon — without this the watchdog's prior-crash
	// recovery walks SafePreDaemonEnableSequence on every restart.
	wd := watchdog.NewWithStore(logger, newKVDBLastKnownStore(st.KV, logger))
	defer wd.Restore()

	// Restore persisted polarity classifications onto live channels
	// (RULE-POLARITY-08). Without this, every restart re-classifies
	// polarity from "unknown" — the wizard's classification is wasted
	// and the controller's polarity.WritePWM path refuses every write
	// on inverted-polarity hardware until the wizard re-runs. Issue #1037.
	needsPolarityProbe := false
	if np, _, applyErr := polarity.ApplyOnStart(st.KV, channels, logger, time.Now()); applyErr != nil {
		logger.Warn("polarity: apply-on-start failed; channels remain unknown", "err", applyErr)
	} else {
		needsPolarityProbe = np
	}
	// Empty polarity KV → no consumer was running ApplyOnStart's
	// needsProbe signal. Without an auto-probe the daemon stays up
	// but refuses every controller write indefinitely (polarity
	// stuck "unknown"); the only recovery was a full wizard re-run.
	// Run the probe in a background goroutine so daemon startup
	// stays inside the systemd Type=notify ready window — controllers
	// spawning concurrently refuse writes via the existing unknown-
	// polarity guard until the probe persists results, then the next
	// tick succeeds. Probe takes ~115 s for 8 channels (post-#1110).
	// (#1250.)
	var polarityWG sync.WaitGroup
	if needsPolarityProbe {
		logger.Warn("polarity: KV empty, running inline auto-probe in background",
			"channels", len(channels))
		probeCtx, probeCancel := context.WithCancel(context.Background())
		// Defer order (LIFO with the wd.Restore defer above): probeCancel
		// signals the goroutine to wind up; polarityWG.Wait blocks until
		// the goroutine has fully exited; wd.Restore then writes orig
		// pwm_enable back. Without polarityWG.Wait the goroutine's last
		// PWM write could race the daemon-exit Restore.
		defer polarityWG.Wait()
		defer probeCancel()
		polarityWG.Add(1)
		go func() {
			defer polarityWG.Done()
			runPolarityAutoProbe(probeCtx, wd, st.KV, channels, &polarity.HwmonProber{}, logger)
		}()
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
	calibrationWiper := func() error { return probe.WipeCalibration(st.KV) }

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
		return buildOpportunisticScheduler(channels, sysDet, st, obsWriter, liveCfg, sguDet, logger, *strictIdleGate)
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
		LayerA:     buildLayerAEstimator(channels, st.Dir, dmiFingerprint, logger),
		Aggregator: buildAggregator(channels, logger),
		Blended:    buildBlendedController(channels, cfg, logger),
		Decisions:  controller.NewDecisionCache(),
		Channels:   channels,
		MassStall:  buildMassStallTracker(channels, logger),
		Drift:      buildDriftDetector(channels, logger),
	}
	if obsWriter != nil {
		// Wire the per-tick controller observation feed into Layer-A
		// conf_A coverage, Layer-B coupling, and Layer-C marginal
		// alongside the existing persistence path. The bridge is a
		// no-op equivalent to buildObsAppend when every runtime is
		// nil; otherwise it satisfies RULE-CONFA-WIRING-01 +
		// RULE-CPL-WIRING-04 + RULE-CMB-WIRING-04 (closes the
		// v0.5.7–v0.5.9 ghost-code wiring gaps surfaced as issues
		// #1033 + #1035).
		smartMode.ObsAppend = buildSmartObsBridge(obsWriter,
			smartMode.Coupling, smartMode.Marginal, smartMode.LayerA)
	}

	return runDaemon(context.Background(), cfg, *configPath, authPath, logger, sigCh, wd, expFlags, kvWiper, calibrationWiper, oppFactory, smartMode)
}

// polarityProber is the seam HwmonProber satisfies. Tests substitute a
// fake whose ProbeAll completes without writing to live sysfs paths.
type polarityProber interface {
	ProbeAll(ctx context.Context, channels []*probe.ControllableChannel) ([]polarity.ChannelResult, error)
}

// runPolarityAutoProbe runs the first-boot auto-probe synchronously.
// Caller is responsible for spawning a goroutine and bounding the
// lifetime via ctx (see run()).
//
// Watchdog coverage (issue #1312): every channel is Registered before
// the prober's first write so RULE-WD-RESTORE-EXIT covers the probe on
// daemon exit during a probe in first-boot mode (where cfg.Fans is
// empty and controllers never register). On normal probe completion
// each channel's pre-probe pwm_enable is restored and the entry is
// deregistered so the controllers that register after the wizard
// captures pwm_enable=auto as their own orig — leaving the daemon-exit
// Restore consistent for the long-running case. Stacking polarity
// entries under the eventual controller entries (without restoring
// here) would leave Restore writing two different orig values per
// path, with the last-write-wins outcome racing on the parallel
// restore goroutines.
func runPolarityAutoProbe(
	ctx context.Context,
	wd *watchdog.Watchdog,
	kv *state.KVDB,
	channels []*probe.ControllableChannel,
	prober polarityProber,
	logger *slog.Logger,
) {
	for _, ch := range channels {
		wd.Register(ch.PWMPath, "hwmon")
	}
	results, probeErr := prober.ProbeAll(ctx, channels)
	for _, ch := range channels {
		wd.RestoreOne(ch.PWMPath)
		wd.Deregister(ch.PWMPath)
	}
	if probeErr != nil {
		logger.Error("polarity: auto-probe failed; channels remain unknown",
			"err", probeErr)
		return
	}
	if persistErr := polarity.Persist(kv, results); persistErr != nil {
		logger.Error("polarity: auto-probe persist failed; channels remain unknown",
			"err", persistErr)
		return
	}
	if _, _, reapplyErr := polarity.ApplyOnStart(kv, channels, logger, time.Now()); reapplyErr != nil {
		logger.Warn("polarity: auto-probe re-apply failed", "err", reapplyErr)
	}
	logger.Info("polarity: auto-probe complete; controllers can now write",
		"channels", len(results))
}

// buildOpportunisticScheduler constructs the v0.5.5 scheduler from the

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
	wd *watchdog.Watchdog,
	expFlags experimental.Flags,
	kvWiper func() error,
	calibrationWiper func() error,
	oppFactory OpportunisticFactory,
	smartMode *SmartModeBundle,
) error {
	restartCh := make(chan struct{}, 1)
	return runDaemonInternal(parentCtx, cfg, configPath, authPath, logger, sigCh, restartCh, wd, expFlags, kvWiper, calibrationWiper, oppFactory, smartMode)
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

	// Decisions caches the most-recent BlendedResult per channel so
	// the web /api/v1/smart/channels handler can show the controller's
	// next-tick PWM target alongside Layer-C's MarginalSlope. Hot-loop
	// safe (atomic per-channel pointer-swap). Updated by the BlendFn
	// closure in runDaemonInternal after every Compute call.
	Decisions *controller.DecisionCache

	// Channels is the live probe.ControllableChannel slice built once
	// in run() and consulted by runDaemonInternal when constructing
	// each controller's WithPolarityChannel option. The wiring closes
	// the v0.5.2 polarity-helper gap on the controller hot path so
	// inverted-polarity fans (NCT6683 on MSI, IT87 on some Gigabyte)
	// receive the correctly-flipped PWM byte. Issue #1037.
	Channels []*probe.ControllableChannel

	// MassStall is the R11 system-wide concurrent-stall tracker. Each
	// controller reports (channel, committed PWM, observed RPM) every
	// tick via WithStallReporter; the gate evaluator reads MassStalled.
	// nil in monitor-only mode (no controllable channels).
	MassStall *massstall.Tracker

	// Gate is the v0.5.9 w_pred_system global gate evaluator. Built in
	// runDaemonInternal (it closes over liveCfg) and stored back here so
	// the controller spawner and the web surface can read it. Its Run
	// goroutine refreshes the gate snapshot every gate.RefreshInterval.
	// nil in monitor-only mode.
	Gate *gate.Evaluator

	// Drift is the R16 per-(channel, layer) EWMA-control-chart drift
	// detector that feeds the aggregator's drift_flags. The blend hook
	// calls Observe inline each tick; the web surface reads Snapshot.
	// nil in monitor-only mode.
	Drift *drift.Detector
}

// configLoader is the function used to load a config from disk on each
// in-process reload. Tests that exercise the first-boot → configured reload
// branch substitute a stub here so they can inject a *config.Config with
// temp-dir sysfs paths that would otherwise fail config.Parse's /sys prefix
// guard. Must be set before the daemon goroutine starts; package-scoped so
// tests in the same package can reach it without an export.
var configLoader = config.Load

// logConfigReloadFailure routes a failed in-process config reload to
// the appropriate log level. A missing config file is the expected
// outcome of the wizard-reset flow (handleSetupReset removes
// configPath then triggers a reload) — those land at INFO so operators
// don't read every successful factory-reset as a fault. Everything
// else (malformed YAML, validation error, disk full, permission
// denied) stays at WARN with the original wording (#1164).
func logConfigReloadFailure(logger *slog.Logger, err error) {
	if errors.Is(err, os.ErrNotExist) {
		logger.Info("config removed (wizard reset?); daemon continues with last loaded config until restart",
			"err", err)
		return
	}
	logger.Warn("config reload failed; keeping current config", "err", err)
}

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
	wd *watchdog.Watchdog,
	expFlags experimental.Flags,
	kvWiper func() error,
	calibrationWiper func() error,
	oppFactory OpportunisticFactory,
	smartMode *SmartModeBundle,
) error {
	// liveCfg is swapped atomically on SIGHUP. Controllers read it on every tick.
	var liveCfg atomic.Pointer[config.Config]
	liveCfg.Store(cfg)

	// wd is owned by run() so the deferred Restore fires AFTER background
	// goroutines (polarity probe, envelope probe) have finished. Tests
	// that drive runDaemonInternal directly pass their own wd; production
	// always passes the one constructed in run(). Lazy-construct here to
	// keep legacy test call sites that pass nil compatible while every
	// production path covers PWM-writing goroutines correctly (#1312).
	if wd == nil {
		wd = watchdog.New(logger)
		defer wd.Restore()
	}
	// Register all PWM paths with the watchdog before starting any controllers.
	for _, fan := range cfg.Fans {
		wd.Register(fan.PWMPath, fan.Type)
	}
	// Reconcile stranded manual fans (RULE-CTRL-RECONCILE-STRANDED): a prior
	// config may have flipped a fan to manual mode (pwm_enable=1) that THIS
	// config no longer controls — it would otherwise sit frozen at the dead
	// config's last PWM, unresponsive to temperature, because no controller
	// drives it and the watchdog never registered it. Hand any such channel
	// back to firmware auto, scoped to the hwmon chips we actually control, so
	// no fan is ever left stranded across a re-setup or config edit.
	controlledPWM := make(map[string]bool)
	for _, ctrl := range cfg.Controls {
		for i := range cfg.Fans {
			f := cfg.Fans[i]
			if f.Name == ctrl.Fan && (f.Type == "hwmon" || f.Type == "") && f.PWMPath != "" {
				controlledPWM[f.PWMPath] = true
			}
		}
	}
	if n := hwmon.ReconcileUnmanagedManual(controlledPWM, logger); n > 0 {
		logger.Info("reconcile: handed stranded manual fans back to firmware auto on startup", "count", n)
	}
	// Route every enumerated IPMI channel through the watchdog so the
	// cross-cutting RULE-WD-RESTORE-EXIT safety contract covers IPMI
	// too (issue #1043). The actual SET_FAN_MODE / Dell auto-enable
	// command stays in the IPMI backend; the watchdog only routes the
	// call from its canonical exit path.
	registerIPMIWatchdogEntries(wd, logger)
	// NBFC channels also use a closure-based Restore path; route them
	// through the same generic watchdog primitive so RULE-WD-RESTORE-EXIT
	// covers laptop EC fans on every documented shutdown path.
	registerNBFCWatchdogEntries(wd, logger)

	// Top-level panic recover. The controller package's own
	// per-tick recover catches in-loop panics; this guard catches
	// anything that escapes (e.g. panic in a goroutine outside a
	// controller, library code, runtime). wd.Restore is armed by
	// run()'s defer (or by the nil-wd fallback above for legacy test
	// callers); the panic propagates out through this defer + up
	// through runDaemon, so run()'s defer chain executes its
	// probeCancel → polarityWG.Wait → wd.Restore sequence before the
	// process exits non-zero and systemd's OnFailure= triggers
	// ventd-recover as the belt to our braces.
	defer func() {
		if r := recover(); r != nil {
			logger.Error("ventd: top-level panic recovered, restoring PWM and re-raising",
				"panic", fmt.Sprintf("%v", r))
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
	// and controllers are running. Pairs with WatchdogSec= in the unit.
	//
	// The heartbeat is GATED on control-loop liveness (RULE-WD-HEARTBEAT-
	// LIVENESS): it pings WATCHDOG=1 only while a controller has ticked
	// within livenessWindow. If every controller goroutine wedges, the ping
	// is withheld so WatchdogSec fires → SIGKILL → OnFailure=ventd-recover
	// hands fans back to firmware — a free-running ping would instead keep
	// the daemon "healthy" while fans sat at their last PWM forever. Monitor-
	// only mode (no controllers ⇒ no ticks ⇒ LastSensorRead stays zero) and
	// pre-first-tick startup both read as alive, so the daemon is never killed
	// when there's no control loop to stall. Both are no-ops off systemd.
	livenessWindow := 5 * cfg.PollInterval.Duration
	if livenessWindow < watchdogStallFloor {
		livenessWindow = watchdogStallFloor
	}
	stopHeartbeat := sdnotify.StartHeartbeat(func() bool {
		return controlLoopAlive(readyState.LastSensorRead(), time.Now(), livenessWindow, logger)
	})
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
	// v0.8.x: calibration.json moved from /etc/ventd/ to /var/lib/ventd/setup/
	// so the orchestrator's sanitize phase has a single canonical wipe target
	// and /etc holds only user-curated config. MigrateLegacyPath is idempotent
	// and a no-op on fresh installs; on upgrade it relocates the legacy file
	// and leaves a tombstone in /etc/ventd/ for one release cycle.
	if err := calibrate.MigrateLegacyPath(calibrate.DefaultCalibrationPath, calibrate.LegacyCalibrationPath, logger); err != nil {
		logger.Warn("calibrate: legacy path migration failed; daemon continues with new path",
			"err", err)
	}
	cal := calibrate.New(calibrate.DefaultCalibrationPath, logger, wd)
	// Wire the HAL channel resolver so calibration sweeps drive fans via the
	// backend abstraction instead of direct hwmon/NVML imports (P1-HAL-02).
	// Shared with runSetup via newChannelResolver — issue #1025 fix.
	cal.SetChannelResolver(newChannelResolver())

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
	// Wire the polarity prober so the wizard's Phase 5b polarity probe
	// actually runs. Without this, the prober is nil and the entire
	// `if prober != nil { ... }` block at internal/setup/setup.go:1097
	// is dead code — RULE-POLARITY-03's |delta| < 150 RPM phantom cap
	// never fires, and phantom channels (RPM=0 at every PWM) flow
	// through to Phase 6 calibration on Phase 5a's RPM-correlation
	// alone. Issue #1026.
	setupMgr.SetPolarityProber(&polarity.HwmonProber{})
	// Wire the calibration-namespace KV store so Phase 5b persists
	// polarity results via polarity.Persist (RULE-POLARITY-08).
	// Without this the wizard's classification is lost across daemon
	// restarts and inverted-polarity hardware runs in the wrong
	// direction on every reboot. Issue #1037. State access goes via
	// the SmartModeBundle since `st` is owned by run() — same indirection
	// used by SetReProber below.
	if smartMode != nil && smartMode.State != nil {
		setupMgr.SetStateKV(smartMode.State.KV)
	}
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
	// RULE-AGG-WIRING-01: wire the wizard's calibration-complete
	// notification into the confidence aggregator's cold-start hard
	// pin (RULE-AGG-COLDSTART-01). Without this the SetEnvelopeCDoneAt
	// timestamp stayed at its zero value through every v0.5.x release
	// and the pin was structurally inert (elapsed > 5 min always true
	// → gate never engaged; issue #1035 row 4). A nil aggregator
	// (monitor-only / tests) makes the wiring a no-op.
	if smartMode != nil && smartMode.Aggregator != nil {
		agg := smartMode.Aggregator
		setupMgr.SetCalibrationCompleteFn(func(t time.Time) {
			logger.Info("aggregator: SetEnvelopeCDoneAt fired from wizard calibration-complete",
				"at", t.Format(time.RFC3339))
			agg.SetEnvelopeCDoneAt(t)
		})
	}
	// Wire the daemon-reload trigger fired by ApplyPhase Success so the
	// wizard's atomic config write is followed by the same restartCh
	// signal handleSetupReset uses (#1229). Without this, a fresh-install
	// operator sees the dashboard read stale state and (on a host whose
	// pre-wizard config had no Controls) no controller binds to the new
	// fans — leaving the CPU climbing unmitigated until a manual
	// `systemctl restart ventd` (#1232). Non-blocking send pattern
	// mirrors the other restartCh writers (rebindTrigger, web handlers).
	setupMgr.SetReloadTrigger(func() {
		select {
		case restartCh <- struct{}{}:
			logger.Info("setup: reload triggered after ApplyPhase success")
		default:
			logger.Info("setup: reload trigger pending; another reload already queued")
		}
	})

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
	// Gated on cfg.Hwmon.DynamicRebind (default true since #1265 — see
	// DynamicRebindEnabled). An explicit dynamic_rebind=false reverts to the
	// older diagnostic-only behaviour: the watcher still emits hardware-change
	// diagnostics, but the reload/rebind path is disabled.
	var watcherOpts []hwmon.Option
	if os.Getenv("VENTD_DISABLE_UEVENT") == "1" {
		watcherOpts = append(watcherOpts, hwmon.WithoutUevents())
	}
	if cfg.Hwmon.DynamicRebindEnabled() {
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

	// RULE-HWMON-SWAP-MONITOR: spawn a goroutine that periodically
	// re-resolves every controllable channel's PWMPath against its
	// stable-device anchor. The doctor surface's hwmon-swap detector
	// catches index swaps on the next sweep; this goroutine surfaces
	// the same condition in real time at WARN level. No-op when
	// there are no eligible hwmon channels.
	//
	// onSwap (#1265): when the periodic re-resolve detects a moved
	// hwmonN path AND dynamic rebind is enabled, signal restartCh so
	// the controllers + web server tear down and re-enter runDaemon
	// against the new path. This is the safety net for hosts where the
	// uevent netlink path didn't surface the move (containers without
	// /sys/bus permissions; netlink subscription failure; ProtectControlGroups
	// quirk). The uevent-driven RebindTrigger handles the fast path;
	// the swap monitor's tick handles the slow path.
	if smartMode != nil {
		var onSwap hwmon.SwapHandler
		if cfg.Hwmon.DynamicRebindEnabled() {
			onSwap = func(d hwmon.SwapDetection) {
				logger.Info("hwmon: swap-monitor detected path change; triggering rebind restart",
					"stored", d.StoredPath,
					"resolved", d.ResolvedPath,
					"stable_device", d.StableDevice)
				select {
				case restartCh <- struct{}{}:
				default:
					// A restart is already pending; the in-flight
					// restart will pick up the new topology.
				}
			}
		}
		startHwmonSwapMonitor(ctx, &wg, smartMode.Channels, onSwap, logger)
	}

	// Platform-profile auto-control: ventd actively drives the kernel's
	// generic platform_profile interface based on hardware capabilities
	// (TJmax, TDP, fan max RPM) and live inputs (CPU temp, load, RAPL
	// draw). Per feedback-ventd-zero-config-smart this is on by default;
	// the only way to disable is to remove the platform_profile sysfs
	// interface (or use an OS that doesn't expose one).
	startPlatformProfileController(ctx, &wg, logger, liveCfg.Load().ShadowMode())

	// Start the web status server. It reads from &liveCfg on every request so
	// it always reflects the current configuration without restart.
	// Tracked by wg so shutdown waits for Shutdown() to drain in-flight
	// requests before run() returns — otherwise the HTTP handler goroutines
	// outlive wd.Restore() and can observe a half-torn-down daemon.
	webSrv := web.New(web.Deps{
		Ctx:        ctx,
		Cfg:        &liveCfg,
		ConfigPath: configPath,
		AuthPath:   authPath,
		Logger:     logger,
		Calibrate:  cal,
		Setup:      setupMgr,
		RestartCh:  restartCh,
		Diag:       diagStore,
		// Fail fast at boot if any production-required Set* below is ever
		// dropped, instead of silently degrading the endpoint (assertWired).
		RequireWiring: true,
	})
	webSrv.SetVersionInfo(web.NewVersionInfo(version, commit, buildDate))
	webSrv.SetReadyState(readyState)
	webSrv.SetKVWiper(kvWiper)
	webSrv.SetCalibrationWiper(calibrationWiper)
	webSrv.SetFactoryResetHook(makeFactoryResetHook(wd, logger))
	// #1285: wire the chassis cooling-capacity-W estimator so
	// /api/v1/smart/status surfaces (capacity_w, cpu_tdp_w, adequate)
	// for the dashboard's "chassis cooling capacity" panel and the
	// doctor's capacity-tight warning.
	webSrv.SetCoolingCapacityFn(newCoolingResolver(&liveCfg, "", nil))
	// Wire polarity channels so the panic handler routes MaxPWM writes
	// through polarity.WritePWM (RULE-POLARITY-05). Without this, an
	// inverted-polarity fan flipped MaxPWM→MinPWM (the opposite of the
	// safety intent) on PANIC, MAX COOLING. Issue #1037 / pass-6-web.md M4.
	if smartMode != nil && len(smartMode.Channels) > 0 {
		webSrv.SetPolarityChannels(smartMode.Channels)
	}

	// v0.5.9 w_pred_system global gate (R11). Built here — before the web
	// server starts serving and before the controllers spawn — because it
	// closes over liveCfg, the daemon's KV state, and the mass-stall
	// tracker. Stored back on the bundle so SetGate (below), the spawner's
	// blend hook, and the web surface all read the same atomic snapshot. A
	// daemon-lifetime goroutine refreshes the gate every
	// gate.RefreshInterval so the expensive precondition probe never runs
	// on the per-fan hot path. Built only when there's a blend hook to read
	// it (Blended != nil) and KV state to back the schema/wizard terms —
	// nil otherwise, and the blend hook then reads the gate as open,
	// preserving pre-gate behaviour.
	if smartMode != nil && smartMode.Blended != nil && smartMode.State != nil {
		smartMode.Gate = buildGateEvaluator(smartMode.State, smartMode.MassStall, &liveCfg, logger)
		gateCtx, gateCancel := context.WithCancel(ctx)
		defer gateCancel()
		wg.Add(1)
		go func() {
			defer wg.Done()
			smartMode.Gate.Run(gateCtx)
		}()
	}

	// v0.5.9: expose the aggregator + LayerA estimator to the web
	// layer so the dashboard's 5-state confidence pill has a data
	// source. Both arguments are nil-safe — monitor-only mode
	// skips construction and the endpoint reports enabled=false.
	if smartMode != nil {
		webSrv.SetConfidence(smartMode.Aggregator, smartMode.LayerA)
		// v0.5.12 #104: wire the deeper coupling + marginal runtimes
		// so /api/v1/smart/{status,channels} can return per-channel
		// RLS state for the dashboard + doctor surfaces.
		webSrv.SetSmartRuntimes(smartMode.Coupling, smartMode.Marginal)
		// #790: wire the controller's per-channel decision cache so
		// /api/v1/smart/channels can show the next-tick PWM target +
		// refusal flags alongside Layer-C's MarginalSlope.
		webSrv.SetDecisions(smartMode.Decisions)
		// R11: expose the w_pred_system gate snapshot so
		// /api/v1/confidence/status reports its open/closed verdict +
		// refusal reason. nil-safe (monitor-only skips it).
		webSrv.SetGate(smartMode.Gate)
		// R11 PR-2: expose the per-layer drift detector so
		// /api/v1/confidence/status reports per-layer drift evidence.
		webSrv.SetDriftDetector(smartMode.Drift)
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
		// RULE-SIG-WIRING-01 — single named-method dispatch into the
		// signature warm-restart path. The body of the read sequence
		// lives in loadSignatureState (smart_builders.go) so the rule
		// binding tests the same code path the production caller
		// exercises, rather than replaying LoadManifest + LoadLabels in
		// isolation. (#1075)
		loadSignatureState(sigLib, smartMode.State.KV, logger)

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

	// v0.5.9: launch the Layer-A periodic-save goroutine. Mirrors the
	// Coupling / Marginal lifecycle pattern at PersistEvery (1 min)
	// cadence — without it, Save is never called and every restart
	// cold-starts the conf_A histogram from zero (RULE-CONFA-PERSIST-RUNNER-01).
	// buildLayerAEstimator stamps stateDir + hwmonFingerprint onto the
	// Estimator via SetPersistContext; Run reads them back.
	if smartMode != nil && smartMode.LayerA != nil {
		layerACtx, layerACancel := context.WithCancel(ctx)
		defer layerACancel()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := smartMode.LayerA.Run(layerACtx); err != nil && err != context.Canceled {
				logger.Warn("layer_a: runtime exited with error", "err", err)
			}
		}()
	}

	// Shared EBUSY-storm collector: every controller's hwmon backend pushes its
	// per-channel rolling-window snapshots here (RULE-HWMON-EBUSY-RATE-
	// OBSERVABILITY), so the doctor's ebusy_storm detector can surface a BIOS
	// contesting manual mode across all controllers' separate backends.
	ebusyCollector := ebusy.New()
	webSrv.SetEBUSYCollector(ebusyCollector)

	// Shared sensor-freeze tracker: every controller feeds its per-tick hwmon
	// temp readings here (RULE-DOCTOR-DETECTOR-STUCK-SENSOR) so the doctor's
	// stuck_sensor detector can flag a sensor frozen at a plausible value while
	// the rest of the box is thermally active — the stuck-but-plausible failure
	// the per-sample sentinel / low-temp filters cannot catch.
	stuckSensorTracker := sensorfreeze.New()
	webSrv.SetStuckSensorTracker(stuckSensorTracker)

	// Daemon-start baselines for the doctor's baseline-requiring detectors
	// (apparmor_profile_drift, dmi_fingerprint): snapshot the AppArmor attach
	// mode and the resolved board-catalog match ONCE here, so the detectors can
	// later compare the live system against the state ventd started with. These
	// are cheap read-only probes; the detectors themselves still run lazily on
	// the first /api/v1/doctor GET.
	baselines := web.DoctorBaselines{AppArmorMode: detectors.ReadAppArmorMode("ventd")}
	if bdmi, bErr := hwdb.ReadDMI(os.DirFS("/")); bErr == nil {
		baselines.HasDMI = true
		if bcat, cErr := hwdb.LoadCatalog(); cErr == nil && bcat != nil {
			dmiFP := hwdb.DMIFingerprint{
				SysVendor:    bdmi.SysVendor,
				ProductName:  bdmi.ProductName,
				BoardVendor:  bdmi.BoardVendor,
				BoardName:    bdmi.BoardName,
				BoardVersion: bdmi.BoardVersion,
			}
			if entry := findMatchingBoardEntry(bcat, dmiFP); entry != nil {
				baselines.DMIMatched = true
				baselines.DMIBoardName = entry.ID
			}
		}
	}
	webSrv.SetDoctorBaselines(baselines)

	// sp owns the controller wiring shared by the startup loop below and the
	// SIGHUP/restart reload loop. Constructing it once means both paths build
	// identical controller.Options by construction (see controllerSpawner) —
	// the bug class behind #1240 and #1037 can no longer recur.
	sp := &controllerSpawner{
		errCh:        errCh,
		liveCfg:      &liveCfg,
		wd:           wd,
		cal:          cal,
		readyState:   readyState,
		panicCheck:   webSrv,
		smartMode:    smartMode,
		sigLib:       sigLib,
		logger:       logger,
		ebusy:        ebusyCollector,
		stuckSensors: stuckSensorTracker,
	}
	// Controller cohort: the fan controllers run under their own context +
	// waitgroup (sp.ctx/sp.cancel/sp.wg), derived from the daemon ctx but
	// cancellable on their own, so a runtime hwmon renumber can drain and
	// respawn just the controllers at their new sysfs paths without disturbing
	// the web server, hwmon watcher, swap monitor, or scheduler (all on the
	// daemon ctx/wg). Because the cohort derives from ctx, SIGTERM/cancel()
	// stops it transitively; the explicit sp.drainCohort() at each exit path
	// (and the deferred backstop) Wait on the cohort so controller goroutines
	// can't outlive the daemon. RULE-CTRL-REBIND-FOLLOW.
	sp.newCohort(ctx)
	defer sp.drainCohort()

	// Only start controllers if there are controls defined (not first-boot).
	// Startup keeps its resolve-control fatality (a malformed control at boot is
	// a hard error), so it can't use the lenient reconcile() the reload paths
	// share; the option wiring still funnels through sp.spawn/sp.options.
	if len(cfg.Controls) > 0 {
		calMap := loadCalibrationByChannel(logger)
		resolvePWMUnitMax := makePWMUnitMaxResolver(logger)
		for _, ctrl := range cfg.Controls {
			fanCfg, err := resolveControl(cfg, ctrl)
			if err != nil {
				// No controllers started yet; wd.Restore() via defer is harmless.
				// Shut the web server down before returning so its goroutine exits.
				cancel()
				webSrv.Shutdown()
				sp.drainCohort()
				wg.Wait()
				return fmt.Errorf("resolve control: %w", err)
			}

			sp.spawn(ctrl, fanCfg, sp.options(ctrl, fanCfg, calMap, resolvePWMUnitMax), cfg.PollInterval.Duration)
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
			sp.drainCohort()
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
				sp.drainCohort()
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
			sp.drainCohort()
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
				logConfigReloadFailure(logger, reloadErr)
				continue
			}
			moves := resolveHwmonPaths(newCfg, cal, logger)
			oldCfg := liveCfg.Load()
			liveCfg.Store(newCfg)
			logger.Info("config reloaded",
				"poll_interval", newCfg.PollInterval.Duration,
				"controls", len(newCfg.Controls),
			)
			poll := newCfg.PollInterval.Duration
			switch {
			case len(oldCfg.Controls) == 0 && len(newCfg.Controls) > 0:
				// First-boot → configured transition: register the now-configured
				// fans (startup had none) and start controllers for the new config.
				for _, fan := range newCfg.Fans {
					wd.Register(fan.PWMPath, fan.Type)
				}
				sp.reconcile(newCfg, poll)
				logger.Info("controllers started after first-boot config reload",
					"count", len(newCfg.Controls))

			case anyControlledFanPWMChanged(moves, newCfg):
				// RULE-CTRL-REBIND-FOLLOW: a controlled fan's hwmon path moved at
				// runtime (renumber). A running controller binds its fan by an
				// immutable pwmPath, so it cannot follow on its own — it would hit
				// the reconcile-stranded handback and go inert. Drain the cohort
				// (each controller restores its fan to firmware on the way out),
				// drop the stale watchdog entries + abort any in-flight calibration
				// keyed by the old paths, then respawn every controller against the
				// new config under a fresh cohort. Drain-before-respawn is the race
				// guard: the old controller has fully exited before the new one
				// starts, so they can never both write the channel.
				logger.Warn("hwmon renumber affects a controlled fan; respawning controllers at new paths",
					"moves", len(moves))
				sp.drainCohort()
				if ctx.Err() != nil {
					// Daemon shutdown began while draining; don't respawn into a
					// dying process. The deferred wd.Restore() handles cleanup.
					return nil
				}
				for _, m := range moves {
					if m.Kind != "fan" || m.OldPWM == m.NewPWM {
						continue
					}
					// Swap the watchdog entry from the vanished path to the new
					// one. The drained controller's defer already restored the old
					// path (a harmless error now that it's gone); Register(new)
					// captures the renumbered chip's current (firmware-default)
					// pwm_enable as the restore baseline. Abort any in-flight
					// calibration keyed by the old path. Unmoved fans keep their
					// startup entry untouched.
					wd.Deregister(m.OldPWM)
					wd.Register(m.NewPWM, "hwmon")
					if m.OldRPM != "" && m.OldRPM != m.NewRPM {
						wd.Deregister(m.OldRPM)
					}
					cal.Abort(m.OldPWM)
				}
				sp.newCohort(ctx)
				sp.reconcile(newCfg, poll)
				logger.Info("controllers respawned after hwmon renumber",
					"count", len(newCfg.Controls))

			default:
				// Running system, no controlled fan moved: existing controllers
				// pick up new curve parameters from liveCfg on their next tick.
			}
		}
	}
}

// pathMove records one hwmon sysfs path that resolveHwmonPaths rebased during a
// reload. Kind is "fan" or "sensor". For a fan, OldPWM/NewPWM are the pwm path
// and OldRPM/NewRPM the tach path (equal when that one didn't move); for a
// sensor, the moved path is in OldPWM/NewPWM. The restartCh handler consults
// these to decide whether a *controlled* fan moved and so the controllers must
// be respawned at their new paths (RULE-CTRL-REBIND-FOLLOW).
type pathMove struct {
	Kind   string
	Name   string
	OldPWM string
	NewPWM string
	OldRPM string
	NewRPM string
}

// resolveHwmonPaths fixes up any hwmon sysfs paths that have moved due to
// hwmonX renumbering (across reboots, or at runtime via rmmod+modprobe / DKMS
// upgrade / hotplug). Uses the HwmonDevice field (stable /sys/devices/... path)
// stored in the config to find the current hwmonX dir. Remaps calibration cache
// keys so stored results survive the renumber, and returns the set of moves so
// the caller can respawn controllers bound to a moved path.
func resolveHwmonPaths(cfg *config.Config, cal *calibrate.Manager, logger *slog.Logger) []pathMove {
	var moves []pathMove
	for i := range cfg.Fans {
		f := &cfg.Fans[i]
		if f.Type != "hwmon" {
			continue
		}
		if f.HwmonDevice == "" {
			// No stable anchor to rebase against. If the path has vanished, a
			// renumber happened that we can't follow — warn so the operator
			// re-runs setup; the controller will hand the fan back to firmware.
			if _, err := os.Stat(f.PWMPath); err != nil {
				logger.Warn("hwmon: fan path missing and no stable device anchor; cannot follow a renumber — re-run setup",
					"fan", f.Name, "path", f.PWMPath)
			}
			continue
		}
		m := pathMove{Kind: "fan", Name: f.Name, OldPWM: f.PWMPath, NewPWM: f.PWMPath, OldRPM: f.RPMPath, NewRPM: f.RPMPath}
		if resolved, changed := hwmon.ResolvePath(f.PWMPath, f.HwmonDevice); changed {
			logger.Info("hwmon path moved, updating", "fan", f.Name, "old", f.PWMPath, "new", resolved)
			cal.RemapKey(f.PWMPath, resolved)
			m.NewPWM = resolved
			f.PWMPath = resolved
		}
		if f.RPMPath != "" {
			if resolved, changed := hwmon.ResolvePath(f.RPMPath, f.HwmonDevice); changed {
				m.NewRPM = resolved
				f.RPMPath = resolved
			}
		}
		if m.OldPWM != m.NewPWM || m.OldRPM != m.NewRPM {
			moves = append(moves, m)
		}
	}
	for i := range cfg.Sensors {
		s := &cfg.Sensors[i]
		if s.Type != "hwmon" || s.HwmonDevice == "" {
			continue
		}
		if resolved, changed := hwmon.ResolvePath(s.Path, s.HwmonDevice); changed {
			logger.Info("hwmon path moved, updating", "sensor", s.Name, "old", s.Path, "new", resolved)
			moves = append(moves, pathMove{Kind: "sensor", Name: s.Name, OldPWM: s.Path, NewPWM: resolved})
			s.Path = resolved
		}
	}
	return moves
}

// anyControlledFanPWMChanged reports whether any move is a fan whose pwm path
// changed and that fan is bound to a control — the precise condition under
// which a running controller (which binds its fan by an immutable pwmPath)
// can't follow on its own and must be respawned. RULE-CTRL-REBIND-FOLLOW.
func anyControlledFanPWMChanged(moves []pathMove, cfg *config.Config) bool {
	for _, m := range moves {
		if m.Kind != "fan" || m.OldPWM == m.NewPWM {
			continue
		}
		for _, ctrl := range cfg.Controls {
			if ctrl.Fan == m.Name {
				return true
			}
		}
	}
	return false
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

// makePWMUnitMaxResolver returns a per-chip pwmUnitMax lookup keyed by the
// hwmon "name" file value (e.g. "thinkpad", "nct6798"). Issue #1044:
// hwdb.InvertPWM(cal, pwm, pwmUnitMax) returns pwmUnitMax-pwm for inverted
// channels; hard-coding 255 produces out-of-range writes on step_0_N /
// cooling_level drivers (e.g. thinkpad_acpi levels 0..7 → InvertPWM(_, 3, 255)
// = 252 written to a register that accepts 0..7).
//
// The resolver loads the embedded catalog + live DMI fingerprint once, then
// memoises matcher lookups per chip name. Any failure (catalog missing, DMI
// unreadable, no chip match, no PWMUnitMax in the resolved profile) falls
// back to 255 — the historical default that's correct for the vast majority
// (duty_0_255) of channels. The fix surfaces ONLY on the step_0_N /
// cooling_level lane where the catalog actually pins a non-255 value.
func makePWMUnitMaxResolver(logger *slog.Logger) func(chipName string) int {
	const defaultPWMUnitMax = 255

	cat, catErr := hwdb.LoadCatalog()
	if catErr != nil {
		logger.Warn("controller: pwm_unit_max resolver: catalog load failed, defaulting to 255", "err", catErr)
		return func(string) int { return defaultPWMUnitMax }
	}
	dmi, dmiErr := hwdb.ReadDMI(os.DirFS("/"))
	if dmiErr != nil {
		logger.Warn("controller: pwm_unit_max resolver: DMI read failed, defaulting to 255", "err", dmiErr)
	}
	dmiFP := hwdb.DMIFingerprint{
		SysVendor:    dmi.SysVendor,
		ProductName:  dmi.ProductName,
		BoardVendor:  dmi.BoardVendor,
		BoardName:    dmi.BoardName,
		BoardVersion: dmi.BoardVersion,
	}

	cache := map[string]int{}
	return func(chipName string) int {
		if chipName == "" {
			return defaultPWMUnitMax
		}
		if v, ok := cache[chipName]; ok {
			return v
		}
		v := defaultPWMUnitMax
		ecp, matchErr := hwdb.MatchV1(cat, chipName, dmiFP)
		if matchErr == nil && ecp != nil && ecp.PWMUnitMax != nil && *ecp.PWMUnitMax > 0 {
			v = *ecp.PWMUnitMax
			logger.Info("controller: pwm_unit_max resolved from catalog",
				"chip", chipName, "pwm_unit_max", v)
		}
		// v1.4 catalog surface: announce state-quantized fan detection and
		// the NBFC dead-end flag so operators can grep journalctl for these
		// hardware classes. Logged once per chip via the resolver's cache.
		if matchErr == nil && ecp != nil {
			if ecp.StateQuantizedN != nil {
				logger.Info("controller: state-quantized fan channel detected",
					"chip", chipName,
					"module", ecp.Module,
					"state_quantized_n", *ecp.StateQuantizedN,
					"polling_latency_hint", ecp.PollingLatencyHint.String(),
					"signature", fmt.Sprintf("state_quantized_%d", *ecp.StateQuantizedN))
			}
			if ecp.DirectECPWMUnavailable {
				logger.Info("controller: direct EC PWM control unavailable on this board (do not offer NBFC install)",
					"chip", chipName,
					"module", ecp.Module,
					"board_id", ptrStringOr(ecp.BoardID, ""))
			}
		}
		cache[chipName] = v
		return v
	}
}

// ptrStringOr returns the string the pointer references, or fallback if nil.
// Used by structured-log helpers that consume optional *string fields on
// the effective controller profile.
func ptrStringOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
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

	store := validity.NewStore("/var/lib/ventd/calibration")
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
// findPolarityChannel returns the live probe.ControllableChannel whose
// PWMPath matches the controller's fan PWMPath. nil when no matching
// channel exists — the controller then falls back to the pre-#1037
// pass-through write semantics. RULE-POLARITY-05 / RULE-POLARITY-11.
func findPolarityChannel(channels []*probe.ControllableChannel, pwmPath string) *probe.ControllableChannel {
	for _, ch := range channels {
		if ch != nil && ch.PWMPath == pwmPath {
			return ch
		}
	}
	return nil
}

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
// makeFactoryResetHook returns the daemon-resident half of factory
// reset, fired AFTER the web handler has wiped all state files and
// flushed its HTTP response:
//
//  1. Watchdog.RestoreCtx — every controlled fan handed back to BIOS
//     auto (RULE-WD-RESTORE-EXIT). Runs first so a hang in the next
//     steps doesn't leave fans pinned.
//  2. OOT driver cleanup — rmmod + DKMS deregister +
//     /etc/modules-load.d entry removal for whichever module ventd
//     installed under /lib/modules/<release>/extra/. Best-effort: any
//     error is logged and the next step proceeds.
//  3. systemctl disable --now ventd.service — stops the running unit
//     and prevents auto-start on next boot. The disable inevitably
//     terminates the daemon mid-step, so this MUST be last; the
//     deferred wd.Restore() owned by run() (#1312) runs as the
//     process unwinds, which is a no-op when step 1 already ran
//     successfully.
//
// nil-safe: a non-systemd host (PID 1 ventd, OpenRC, runit) gets a
// best-effort fan restore + driver cleanup, with the systemctl step
// returning the underlying exec error so the operator sees it in the
// journal.
func makeFactoryResetHook(wd *watchdog.Watchdog, logger *slog.Logger) func(context.Context) error {
	return func(ctx context.Context) error {
		// Step 1: hand fans back to BIOS auto. RestoreCtx honours its
		// own per-channel goroutine budget; ctx here only bounds the
		// wait for ALL channels — a hung fan no longer blocks the
		// other steps after its goroutine times out.
		if wd != nil {
			wd.RestoreCtx(ctx)
		}
		// Step 2: rmmod + DKMS cleanup for whichever OOT driver ventd
		// installed. guessInstalledOOTModule walks /lib/modules/<rel>/extra/
		// to identify the module; an empty result means nothing was
		// installed (catalog row never required an OOT build) and we
		// skip cleanup silently.
		module := guessFactoryResetModule()
		release := strings.TrimSpace(execCommandOutput("uname", "-r"))
		if module != "" && release != "" {
			report, err := hwmon.CleanupOrphanInstall(
				hwmon.DriverNeed{Module: module}, release, logger,
			)
			if err != nil {
				logger.Warn("factory reset: OOT driver cleanup failed (continuing to disable)",
					"module", module, "err", err)
			} else if report != nil {
				logger.Info("factory reset: OOT driver removed",
					"module", module,
					"modules_removed", report.ModulesRemoved,
					"dkms_removed", report.DKMSRemoved,
					"modules_loadd_clean", report.ModulesLoadDClean)
			}
		}
		// Step 3: stop+disable the systemd unit. Spawn via systemd-run
		// in a transient unit so the systemctl process lives OUTSIDE
		// ventd.service's cgroup. Without --no-block, the systemctl
		// child inherited ventd.service's cgroup and got SIGTERM'd
		// by the very stop it issued — the hook then logged a
		// confusing "signal: terminated" error even though the unit
		// disable+stop had in fact succeeded. With systemd-run the
		// transient unit runs to completion regardless of ventd's
		// fate; --collect garbage-collects the unit after it exits;
		// --no-block returns immediately so this goroutine can clean
		// up before the daemon is torn down.
		cmd := exec.Command("systemd-run",
			"--collect", "--no-block",
			"--unit", "ventd-factory-reset-disable",
			"--",
			"systemctl", "disable", "--now", "ventd.service")
		if err := cmd.Run(); err != nil {
			// Fall back to inline systemctl when systemd-run isn't on
			// PATH (non-systemd hosts shouldn't reach this branch but
			// belt-and-braces). The inline path is the v1.0.2/v1.0.3
			// behaviour — error logging stays in case something else
			// goes wrong.
			logger.Warn("factory reset: systemd-run unavailable; falling back to inline systemctl (cgroup self-kill expected)",
				"err", err)
			inline := exec.Command("systemctl", "disable", "--now", "ventd.service")
			if err := inline.Run(); err != nil {
				logger.Error("factory reset: systemctl disable --now ventd.service failed",
					"err", err)
				return err
			}
		}
		return nil
	}
}

// guessFactoryResetModule re-exports the web package's
// /lib/modules/<release>/extra/ probe under a stable name so the
// factory-reset hook in main.go doesn't reach across package boundaries
// for an internal helper.
func guessFactoryResetModule() string {
	release := strings.TrimSpace(execCommandOutput("uname", "-r"))
	if release == "" {
		return ""
	}
	entries, err := os.ReadDir("/lib/modules/" + release + "/extra")
	if err != nil {
		return ""
	}
	var first string
	for _, e := range entries {
		mod := strippedModuleName(e.Name())
		if mod == "" {
			continue
		}
		if mod == "it87" {
			return mod
		}
		if first == "" {
			first = mod
		}
	}
	return first
}

// strippedModuleName returns the bare module name for a /lib/modules
// /<release>/extra/ entry, peeling any kernel-module compression suffix
// (xz, zst, gz) before stripping the .ko. Empty when the entry is not
// a kernel module. Modern distros ship compressed modules: Fedora and
// Arch default to xz (Fedora 41) or zst (Arch); Debian/Ubuntu use xz;
// only legacy or custom-built kernels ship bare .ko. The old "strip
// .ko" path silently skipped every compressed module on every distro
// except hand-built kernels, which is how factory reset missed the
// dell-smm-hwmon.ko.xz under /lib/modules/<rel>/extra/ on fedora.
func strippedModuleName(filename string) string {
	name := filename
	for _, ext := range []string{".xz", ".zst", ".gz"} {
		if strings.HasSuffix(name, ext) {
			name = strings.TrimSuffix(name, ext)
			break
		}
	}
	if !strings.HasSuffix(name, ".ko") {
		return ""
	}
	return strings.TrimSuffix(name, ".ko")
}

func execCommandOutput(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

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
