package web

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// LastApplyOutcome captures the most recent /api/v1/update/apply
// invocation that produced an in-process error visible to the
// daemon — typically the spawned transient `ventd-update.service`
// failing to execute (script ENOENT, exec error, install.sh script
// returning non-zero before it could swap the binary).
//
// Surfaced via /api/v1/update/check so the in-UI Update modal can
// say "last attempt failed" instead of the silent "scheduled, then
// nothing happens" UX that operators see today.
//
// The success case is intentionally NOT recorded here: when the
// install.sh script swaps the binary and runs `systemctl restart
// ventd`, the daemon dies and the new daemon comes back with empty
// lastApplyOutcomePtr (and the new version visible in `current`).
// That transition itself is the user-visible "success" signal.
type LastApplyOutcome struct {
	At          string `json:"at"`                     // RFC3339Nano
	Version     string `json:"version"`                // v0.5.28
	Status      string `json:"status"`                 // "failed" | "timed_out"
	Detail      string `json:"detail,omitempty"`       // "transient unit X result=failed"
	JournalTail string `json:"journal_tail,omitempty"` // last 30 journal lines from the unit
}

// lastApplyOutcomePtr holds the most recent failure observation.
// Lock-free reads from /update/check via atomic.Pointer; writes from
// the watcher goroutine are atomic. On daemon restart (success case)
// this is nil (fresh daemon), which is the desired semantics.
var lastApplyOutcomePtr atomic.Pointer[LastApplyOutcome]

// updateOutcomeWatchTimeout caps how long the watcher polls the
// transient unit before giving up. RuntimeMaxSec on the unit is 600s
// (10 min worst-case install), but a real failure surfaces in
// seconds. 60s is a balance: long enough for slow disk installs to
// settle, short enough that operators don't wait forever to see a
// failure surface.
var updateOutcomeWatchTimeout = 60 * time.Second

// updateOutcomePollInterval — how often the watcher checks the unit.
// 1s ticks are well below the cost-gate budget of any single
// systemctl invocation; nothing else uses this resource.
var updateOutcomePollInterval = 1 * time.Second

// systemctlIsFailedFn is the package-level seam for the
// `systemctl show --property=Result,SubState <unit>` call. The
// returned status is the Result value (success | failed |
// resources | timeout | exit-code | core-dump | watchdog |
// start-limit-hit) per systemd.exec(5). The substate gives the
// transient unit's runtime state (running | exited | failed |
// dead) per systemd.unit(5). The watcher trips on failed.
//
// Production swaps in realSystemctlIsFailed; tests stub to drive
// the watcher's branches deterministically.
var systemctlIsFailedFn = realSystemctlIsFailed

// journalctlTailFn is the package-level seam for the
// `journalctl -u <unit> -n N --no-pager` call that captures the
// transient unit's last few log lines on failure. Tail is what the
// operator sees in the UI surface — the install.sh exit reason is
// usually in the last 5-10 lines.
var journalctlTailFn = realJournalctlTail

// realSystemctlIsFailed is the production query.
//
// Returns:
//   - failed=true when systemd's Result is anything other than
//     "success" (failed | timeout | exit-code | resources | etc.)
//   - finished=true when the unit's SubState is "exited" or "dead"
//     (the unit has completed, success or fail)
//   - status=the Result value, useful for diagnostics
//   - err on systemctl invocation failure
//
// A unit that doesn't exist yet (operator clicked Update microseconds
// ago, transient unit hasn't materialised) returns no error but an
// empty SubState — caller treats as "still running, try again".
func realSystemctlIsFailed(unit string) (failed, finished bool, status string, err error) {
	out, err := exec.Command("systemctl", "show",
		"--property=Result", "--property=SubState",
		unit).Output()
	if err != nil {
		return false, false, "", err
	}
	result, sub := "", ""
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		switch {
		case strings.HasPrefix(line, "Result="):
			result = strings.TrimPrefix(line, "Result=")
		case strings.HasPrefix(line, "SubState="):
			sub = strings.TrimPrefix(line, "SubState=")
		}
	}
	finished = sub == "exited" || sub == "dead" || sub == "failed"
	failed = finished && result != "" && result != "success"
	return failed, finished, result, nil
}

// realJournalctlTail returns the last n lines of `journalctl -u <unit>`.
// Best-effort: any error returns an empty string rather than failing the
// outcome capture, because the journal may not be readable from the
// daemon's user (NoNewPrivileges environments) and the failure surface
// is more useful with status only than not at all.
func realJournalctlTail(unit string, n int) string {
	out, err := exec.Command("journalctl", "-u", unit,
		"--no-pager", "-n", strconv.Itoa(n)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// watchUpdateApplyOutcome watches the spawned transient unit for up
// to updateOutcomeWatchTimeout. On a failure, captures the journal
// tail and stores it in lastApplyOutcomePtr; on success, returns
// silently (the daemon's restart handles the success surface). On
// timeout (unit never finished), returns silently — that's not a
// surface-able outcome here; the operator can re-poll.
//
// Designed to be invoked from handleUpdateApply via `go ...` after
// realUpdateRun returns nil and the spawn primitive was systemd-run.
// The nohup-fallback path doesn't have a transient unit to watch and
// doesn't call this function.
func watchUpdateApplyOutcome(version, unitName, scriptPath string, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), updateOutcomeWatchTimeout)
	defer cancel()
	ticker := time.NewTicker(updateOutcomePollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Timeout: unit either still running (operator just clicked,
			// install in flight) or never materialised (systemd-run
			// returned success but the unit got rejected later). Either
			// way, no outcome to surface — the operator can re-poll
			// /update/check to see the next state.
			return
		case <-ticker.C:
			failed, finished, status, err := systemctlIsFailedFn(unitName)
			if err != nil {
				// Transient query error — systemd may be reloading,
				// dbus may be busy, etc. Try again on the next tick.
				continue
			}
			if !finished {
				// Unit is queued or running. Keep polling.
				continue
			}
			if failed {
				tail := journalctlTailFn(unitName, 30)
				outcome := &LastApplyOutcome{
					At:          time.Now().UTC().Format(time.RFC3339Nano),
					Version:     version,
					Status:      "failed",
					Detail:      "transient unit " + unitName + " result=" + status + " (script=" + scriptPath + ")",
					JournalTail: tail,
				}
				lastApplyOutcomePtr.Store(outcome)
				if logger != nil {
					logger.Warn("update: in-UI apply failed",
						"unit", unitName, "version", version,
						"result", status,
						"script", scriptPath)
				}
				return
			}
			// finished && !failed → unit completed successfully.
			// install.sh has already swapped the binary and asked
			// systemd to restart ventd; the daemon will die any
			// moment. Don't store a success outcome — the new
			// daemon's startup is the success signal.
			return
		}
	}
}

// resetLastApplyOutcomeForTest clears the package-level state so
// tests can run hermetic without cross-contamination. Test-only.
func resetLastApplyOutcomeForTest() {
	lastApplyOutcomePtr.Store(nil)
}

// updateNohupLogPath is the install log path the nohup wrapper writes
// to. Tail of this file is captured into LastApplyOutcome.JournalTail
// on a non-zero sentinel — the operator-facing equivalent of the
// systemd-run path's `journalctl -u ventd-update.service` tail.
// Overridable for tests.
var updateNohupLogPath = "/var/log/ventd-update.log"

// updateNohupLogTailLines is the number of trailing lines from
// updateNohupLogPath to attach to LastApplyOutcome.JournalTail when
// the sentinel reports a non-zero exit. Matches the systemd-run
// path's `journalctl -u ... -n 30` budget so the in-UI surface is
// symmetrical across the two spawn shapes.
const updateNohupLogTailLines = 30

// watchUpdateApplyOutcomeNohup polls the nohup-path exit-code
// sentinel (/run/ventd/update.exitcode by default) for up to
// updateOutcomeWatchTimeout. On non-zero exit, captures the tail of
// /var/log/ventd-update.log and stores the outcome in
// lastApplyOutcomePtr so /api/v1/update/check surfaces it. On
// timeout or sentinel never appearing, returns silently — the
// operator can re-poll /update/check.
//
// Mirrors watchUpdateApplyOutcome for the systemd-run path so
// OpenRC / runit / Gentoo-openrc operators get the same in-UI
// failure surface as systemd hosts. (Issue #1305.)
//
// sentinelDir is the directory the nohup wrapper writes the
// exitcode file to. Injected for tests; production threads
// updateSentinelDir from update.go.
func watchUpdateApplyOutcomeNohup(version, scriptPath string, logger *slog.Logger) {
	watchNohupSentinelInDir(version, scriptPath, updateSentinelDir, logger)
}

// watchNohupSentinelInDir is the directory-injectable form of
// watchUpdateApplyOutcomeNohup, factored out so tests can drive the
// poll loop against a t.TempDir() without mocking the package-level
// sentinel-dir seam.
func watchNohupSentinelInDir(version, scriptPath, sentinelDir string, logger *slog.Logger) {
	sentinel := filepath.Join(sentinelDir, "update.exitcode")
	ctx, cancel := context.WithTimeout(context.Background(), updateOutcomeWatchTimeout)
	defer cancel()
	ticker := time.NewTicker(updateOutcomePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Sentinel never showed up. Two paths land here:
			// (a) install.sh swapped the binary + asked for a service
			// restart and the daemon is winding down (sentinel write
			// hasn't reached us yet); (b) genuine wedge that timeout(1)
			// also couldn't kill. Either way, no surface-able outcome.
			return
		case <-ticker.C:
			data, err := os.ReadFile(sentinel)
			if err != nil {
				continue
			}
			rcStr := strings.TrimSpace(string(data))
			rc, parseErr := strconv.Atoi(rcStr)
			if parseErr != nil {
				if logger != nil {
					logger.Warn("update: nohup sentinel unparseable; ignoring",
						"path", sentinel, "raw", rcStr, "err", parseErr)
				}
				return
			}
			if rc == 0 {
				// Success — install.sh has already swapped the binary
				// and is signalling the init system to restart ventd.
				// Don't store a success outcome; the new daemon's
				// startup is the user-visible success signal.
				return
			}
			tail := tailFile(updateNohupLogPath, updateNohupLogTailLines)
			outcome := &LastApplyOutcome{
				At:          time.Now().UTC().Format(time.RFC3339Nano),
				Version:     version,
				Status:      "failed",
				Detail:      "nohup install exited rc=" + rcStr + " (script=" + scriptPath + ", log=" + updateNohupLogPath + ")",
				JournalTail: tail,
			}
			lastApplyOutcomePtr.Store(outcome)
			if logger != nil {
				logger.Warn("update: in-UI nohup apply failed",
					"version", version,
					"rc", rc,
					"script", scriptPath,
					"log", updateNohupLogPath)
			}
			return
		}
	}
}

// tailFile returns the last n lines of path. Best-effort: errors
// (missing log, EACCES, etc.) surface as an empty string so the
// caller still records a useful LastApplyOutcome with status + rc.
func tailFile(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
