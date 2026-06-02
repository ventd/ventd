package main

import (
	"fmt"
	"io"

	"github.com/ventd/ventd/internal/lastfatal"
	"github.com/ventd/ventd/internal/state"
)

// Health exit codes. 0 = nothing wrong to report; 1 = a startup fatal is
// recorded (the operator-actionable case). Distinct from the daemon's own
// exit codes — this is an out-of-process probe.
const (
	healthExitOK    = 0
	healthExitFatal = 1
)

// runHealth implements the `ventd health` subcommand (#1165). It is the
// out-of-process surface for the last-startup-fatal sentinel: when the daemon
// repeatedly fails to start, systemd gives up and the web UI never binds, so
// the operator who didn't think to run `journalctl -u ventd` has nothing to
// look at. `ventd health` reads the sentinel ventd writes on a fatal exit
// (lastfatal) and the pidfile, and prints a one-screen verdict.
//
// dir is the resolved state directory (state.EffectiveDir, honouring
// VENTD_STATE_DIR); stdout is injected for testability. Returns the process
// exit code.
//
// Verdict precedence:
//   - a recorded startup fatal is the headline (exit 1) even if a daemon is now
//     running — it means the LAST start failed and the operator should know why;
//   - otherwise report whether a live daemon owns the pidfile (exit 0).
func runHealth(dir string, stdout io.Writer) int {
	fatal := lastfatal.Read(dir)
	pid, running := state.RunningPID(dir)

	if fatal != "" {
		_, _ = fmt.Fprintf(stdout, "ventd: last start failed\n  %s", fatal)
		if running {
			_, _ = fmt.Fprintf(stdout, "  (a daemon is running now, pid %d — the line above is from an earlier failed start)\n", pid)
		} else {
			_, _ = fmt.Fprintf(stdout, "  ventd is not running. See `journalctl -u ventd` for the full trace.\n")
		}
		return healthExitFatal
	}

	if running {
		_, _ = fmt.Fprintf(stdout, "ventd: healthy (running, pid %d; no recorded startup fatal)\n", pid)
	} else {
		_, _ = fmt.Fprintf(stdout, "ventd: not running; no recorded startup fatal\n")
	}
	return healthExitOK
}
