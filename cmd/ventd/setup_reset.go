package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/state"
	"github.com/ventd/ventd/internal/web/authpersist"
)

// Setup-reset exit codes. Distinct from the daemon's own exit codes —
// this is a short-lived recovery CLI.
const (
	setupResetExitOK    = 0
	setupResetExitError = 1
	setupResetExitUsage = 2
)

// runSetupResetCLI implements `ventd setup --reset` — the lost-password
// recovery the login screen has advertised since the screen shipped, but
// which never existed as a real command (the positional arg was silently
// swallowed and the binary tried to start the daemon).
//
// Scope is deliberately password-only: it removes auth.json (and its
// .bak — both hold the forgotten hash), scrubs any legacy
// web.password_hash still in config.yaml (otherwise migrateAuthToFile
// would resurrect the old password on the next start), and restarts the
// daemon so the in-memory hash is dropped. Fan configuration and
// calibration are untouched — forgetting a password should not cost a
// calibrated setup. On the next start the daemon's lost-credentials
// integrity guard re-opens the wizard's password-set step
// (RULE-CLI-SETUP-RESET).
func runSetupResetCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ventd setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	reset := fs.Bool("reset", false, "clear the admin password and re-open the setup wizard's password step")
	cfgPath := fs.String("config", "/etc/ventd/config.yaml", "path to YAML config file")
	if err := fs.Parse(args); err != nil {
		return setupResetExitUsage
	}
	if !*reset {
		_, _ = fmt.Fprintln(stderr, "usage: ventd setup --reset")
		_, _ = fmt.Fprintln(stderr, "  clears the admin password (fan config and calibration are kept);")
		_, _ = fmt.Fprintln(stderr, "  the web setup wizard then asks for a new one.")
		_, _ = fmt.Fprintln(stderr, "For the interactive hardware wizard, run: ventd --setup")
		return setupResetExitUsage
	}
	if os.Geteuid() != 0 {
		_, _ = fmt.Fprintln(stderr, "ventd setup --reset: must run as root (try: sudo ventd setup --reset)")
		return setupResetExitError
	}
	authPath := authpersist.DefaultPath(filepath.Dir(*cfgPath))
	return runSetupReset(*cfgPath, authPath, state.EffectiveDir(), systemctlTryRestart, stdout, stderr)
}

// systemctlTryRestart bounces ventd.service so the running daemon drops
// its in-memory password hash. try-restart is a no-op when the unit is
// not active, but runSetupReset only calls it when the pidfile shows a
// live daemon anyway.
func systemctlTryRestart() error {
	out, err := exec.Command("systemctl", "try-restart", "ventd.service").CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl try-restart: %v: %s", err, out)
	}
	return nil
}

// runSetupReset performs the reset against injected paths so tests can
// drive it against a temp tree; restart is injected for the same reason.
// stateDir is the resolved state directory (state.EffectiveDir, honouring
// VENTD_STATE_DIR) used only for the read-only pidfile liveness probe.
func runSetupReset(configPath, authPath, stateDir string, restart func() error, stdout, stderr io.Writer) int {
	cleared := false
	for _, p := range []string{authPath, authPath + ".bak"} {
		err := os.Remove(p)
		switch {
		case err == nil:
			cleared = true
		case !os.IsNotExist(err):
			_, _ = fmt.Fprintf(stderr, "ventd setup --reset: remove %s: %v\n", p, err)
			return setupResetExitError
		}
	}

	// A config carrying a legacy web.password_hash predates auth.json;
	// left in place, migrateAuthToFile would write it straight back into
	// a fresh auth.json on the next start and the operator would still
	// be locked out. Scrub it. A config that fails to load can't feed
	// the migration either, so that case is safe to skip.
	if cfg, err := config.Load(configPath); err == nil && cfg.Web.PasswordHash != "" {
		cfg.Web.PasswordHash = ""
		if _, saveErr := config.Save(cfg, configPath); saveErr != nil {
			_, _ = fmt.Fprintf(stderr, "ventd setup --reset: could not clear the legacy password_hash from %s: %v\n", configPath, saveErr)
			_, _ = fmt.Fprintln(stderr, "  the old password would come back on the next start — fix the file and re-run")
			return setupResetExitError
		}
		cleared = true
	}

	if !cleared {
		_, _ = fmt.Fprintln(stdout, "no admin password was set — nothing to reset.")
	} else {
		_, _ = fmt.Fprintln(stdout, "admin password cleared. Fan configuration and calibration are untouched.")
	}

	// The running daemon keeps the old hash in memory; restart so the
	// reset takes effect now rather than at some future reboot.
	if pid, running := state.RunningPID(stateDir); running {
		if err := restart(); err != nil {
			_, _ = fmt.Fprintf(stdout, "ventd is running (pid %d) but could not be restarted automatically (%v)\n", pid, err)
			_, _ = fmt.Fprintln(stdout, "restart it yourself: sudo systemctl restart ventd")
		} else {
			_, _ = fmt.Fprintln(stdout, "ventd restarted.")
		}
	} else {
		_, _ = fmt.Fprintln(stdout, "ventd is not running; the reset takes effect on the next start.")
	}

	_, _ = fmt.Fprintln(stdout, `
Next: open the ventd web UI — the setup wizard will ask for a new
password. If you connect from another machine (not localhost), the
wizard also asks for the one-time setup token minted at restart:
  sudo cat /run/ventd/setup-token
(also printed in journalctl -u ventd).`)
	return setupResetExitOK
}
