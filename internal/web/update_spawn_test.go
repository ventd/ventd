package web

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildUpdateCmd_PrefersSystemdRun pins the v0.5.26 fix for the
// in-UI updater silent-failure: when systemd is the running init AND
// systemd-run is on PATH, the spawn MUST use systemd-run with the
// sandbox-escape flags. Without these the install.sh inherits
// ventd.service's PrivateTmp + ProtectSystem=strict + restrictive
// ReadWritePaths and fails the moment it tries to stage the new
// binary at /usr/local/bin/.ventd.new or write the update log.
//
// The flag set is load-bearing:
//   - --no-block returns immediately so the HTTP handler can ack
//     the operator before install.sh starts.
//   - --collect frees the unit on completion so successive update
//     attempts don't accumulate failed transient units.
//   - --service-type=oneshot matches install.sh's start-do-exit
//     contract.
//   - --property=KillMode=process keeps install.sh alive when
//     install.sh itself triggers `systemctl try-restart ventd`,
//     which would otherwise SIGTERM the entire ventd cgroup.
//   - --setenv= entries propagate VENTD_VERSION + the preflight
//     skip set across the cgroup boundary (env vars on the parent
//     daemon process don't follow into a transient unit).
func TestBuildUpdateCmd_PrefersSystemdRun(t *testing.T) {
	prevPath := systemdRunPath
	prevAvail := systemdAvailable
	t.Cleanup(func() { systemdRunPath = prevPath; systemdAvailable = prevAvail })
	systemdRunPath = func() string { return "/usr/bin/systemd-run" }
	systemdAvailable = func() bool { return true }

	cmd := buildUpdateCmd("v0.5.26", "/tmp/ventd-install-fetched-1234.sh")
	if filepath.Base(cmd.Path) != "systemd-run" {
		t.Fatalf("cmd.Path = %q, want systemd-run", cmd.Path)
	}
	args := strings.Join(cmd.Args, " ")
	for _, must := range []string{
		"--no-block",
		"--collect",
		"--unit=ventd-update",
		"--service-type=oneshot",
		"--property=KillMode=process",
		"--setenv=VENTD_VERSION=v0.5.26",
		"--setenv=VENTD_SKIP_PREFLIGHT_CHECKS=" + inUIUpdateSkipChecks,
		"bash",
		"/tmp/ventd-install-fetched-1234.sh",
	} {
		if !strings.Contains(args, must) {
			t.Errorf("cmd.Args missing %q\nfull: %s", must, args)
		}
	}
}

// TestBuildUpdateCmd_FallsBackToNohup pins the non-systemd path. On
// OpenRC and runit hosts (Alpine, Void) /run/systemd/system doesn't
// exist; the spawn must fall back to the nohup-detached-bash pattern
// rather than fail. These hosts don't have ventd.service's sandbox so
// the fallback is correct on its own.
func TestBuildUpdateCmd_FallsBackToNohup(t *testing.T) {
	prevPath := systemdRunPath
	prevAvail := systemdAvailable
	t.Cleanup(func() { systemdRunPath = prevPath; systemdAvailable = prevAvail })

	// Either condition false → fallback. Cover both edges.
	for _, tc := range []struct {
		name      string
		runPath   string
		available bool
	}{
		{"systemd-not-running", "/usr/bin/systemd-run", false},
		{"systemd-run-absent", "", true},
		{"both-absent", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			systemdRunPath = func() string { return tc.runPath }
			systemdAvailable = func() bool { return tc.available }

			cmd := buildUpdateCmd("v0.5.26", "/tmp/foo.sh")
			if filepath.Base(cmd.Path) != "nohup" {
				t.Fatalf("cmd.Path = %q, want nohup", cmd.Path)
			}
			args := strings.Join(cmd.Args, " ")
			if !strings.Contains(args, "VENTD_VERSION='v0.5.26'") {
				t.Errorf("nohup args missing quoted VENTD_VERSION: %s", args)
			}
			if !strings.Contains(args, "VENTD_SKIP_PREFLIGHT_CHECKS='"+inUIUpdateSkipChecks+"'") {
				t.Errorf("nohup args missing quoted skip-checks: %s", args)
			}
			if !strings.Contains(args, "/var/log/ventd-update.log") {
				t.Errorf("nohup args missing log redirect: %s", args)
			}
		})
	}
}
