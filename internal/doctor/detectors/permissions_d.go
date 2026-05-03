package detectors

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// PermissionsFS is the read-only filesystem surface
// PermissionsDetector needs. Production wires the live filesystem;
// tests inject a stub.
type PermissionsFS interface {
	// Stat returns os.FileInfo. Tests stub specific paths to return
	// canned modes; missing paths return os.ErrNotExist.
	Stat(name string) (os.FileInfo, error)

	// LookupUser reports whether the named user exists. Wraps
	// os/user.Lookup; tests stub directly.
	LookupUser(name string) bool

	// LookupGroup reports whether the named group exists.
	LookupGroup(name string) bool
}

// livePermissionsFS reads the real filesystem.
type livePermissionsFS struct{}

func (livePermissionsFS) Stat(name string) (os.FileInfo, error) { return os.Stat(name) }
func (livePermissionsFS) LookupUser(name string) bool {
	// Minimal /etc/passwd parser to avoid pulling os/user (which
	// would link cgo on some Go builds). The doctor binary needs
	// CGO_ENABLED=0 cleanliness.
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return false
	}
	return looksUpAccount(string(data), name)
}
func (livePermissionsFS) LookupGroup(name string) bool {
	data, err := os.ReadFile("/etc/group")
	if err != nil {
		return false
	}
	return looksUpAccount(string(data), name)
}

// looksUpAccount reports whether the colon-separated /etc/passwd or
// /etc/group content contains an entry whose first field is name.
func looksUpAccount(content, name string) bool {
	for i := 0; i < len(content); {
		j := i
		for j < len(content) && content[j] != '\n' {
			j++
		}
		line := content[i:j]
		colon := -1
		for k := 0; k < len(line); k++ {
			if line[k] == ':' {
				colon = k
				break
			}
		}
		if colon > 0 && line[:colon] == name {
			return true
		}
		i = j + 1
	}
	return false
}

// PermissionsDetector audits ventd's filesystem ownership/mode +
// account presence. Surfaces three classes of issue:
//   - ventd user/group missing → Warning (post-install drift; the
//     wizard's sysusers.d drop-in re-creates them on next install).
//   - /var/lib/ventd missing or wrong mode → Warning (RULE-STATE-09
//     repairs mode drift on next daemon start, but worth surfacing).
//   - Permission-denied during the audit itself → Warning per
//     RULE-DOCTOR-04 graceful-degrade ("rerun as root for full check").
//
// The detector does NOT check AppArmor profile state — that's
// AppArmorProfileDriftDetector's job.
type PermissionsDetector struct {
	// FS is the env reader. Defaults to livePermissionsFS{} when nil.
	FS PermissionsFS
}

// NewPermissionsDetector constructs a detector. fs nil → live filesystem.
func NewPermissionsDetector(fs PermissionsFS) *PermissionsDetector {
	if fs == nil {
		fs = livePermissionsFS{}
	}
	return &PermissionsDetector{FS: fs}
}

// Name returns the stable detector ID.
func (d *PermissionsDetector) Name() string { return "permissions" }

// Probe runs the audit. Each problem is its own Fact so the
// suppression store can scope dismissals per-issue.
func (d *PermissionsDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	now := timeNowFromDeps(deps)
	var facts []doctor.Fact

	// 1. ventd user/group — both required by the shipped systemd
	//    units' User=/Group= directives (RULE-INSTALL-01).
	if !d.FS.LookupUser("ventd") {
		facts = append(facts, doctor.Fact{
			Detector:   d.Name(),
			Severity:   doctor.SeverityWarning,
			Class:      recovery.ClassUnknown,
			Title:      "ventd user account is missing",
			Detail:     "The shipped systemd unit's User=ventd directive will fail with exit status 217/USER on next start. The wizard's sysusers.d drop-in (deploy/sysusers.d-ventd.conf) creates this account on next install — re-run the install script.",
			EntityHash: doctor.HashEntity("permissions_user_missing", "ventd"),
			Observed:   now,
		})
	}
	if !d.FS.LookupGroup("ventd") {
		facts = append(facts, doctor.Fact{
			Detector:   d.Name(),
			Severity:   doctor.SeverityWarning,
			Class:      recovery.ClassUnknown,
			Title:      "ventd group is missing",
			Detail:     "The shipped unit's Group=ventd will fail at startup. Same remediation as the user account: re-run the install script to re-create via sysusers.d.",
			EntityHash: doctor.HashEntity("permissions_group_missing", "ventd"),
			Observed:   now,
		})
	}

	// 2. /var/lib/ventd directory mode (0755 ventd:ventd per RULE-STATE-09).
	info, err := d.FS.Stat("/var/lib/ventd")
	if err != nil {
		if !os.IsNotExist(err) {
			facts = append(facts, doctor.Fact{
				Detector: d.Name(),
				Severity: doctor.SeverityWarning,
				Class:    recovery.ClassUnknown,
				Title:    "Cannot stat /var/lib/ventd (permission denied?)",
				Detail: fmt.Sprintf(
					"%v. RULE-DOCTOR-04 graceful-degrade — rerun `sudo ventd doctor` for the full audit.",
					err,
				),
				EntityHash: doctor.HashEntity("permissions_stat_denied", "/var/lib/ventd"),
				Observed:   now,
			})
		}
		// Not-exist is benign — the dir is created on first daemon start (RULE-STATE-10).
	} else if info.IsDir() {
		mode := info.Mode().Perm()
		const expected fs.FileMode = 0o755
		if mode != expected {
			facts = append(facts, doctor.Fact{
				Detector:   d.Name(),
				Severity:   doctor.SeverityWarning,
				Class:      recovery.ClassUnknown,
				Title:      fmt.Sprintf("/var/lib/ventd has mode 0%o, expected 0%o", mode, expected),
				Detail:     "RULE-STATE-09 expects state directories at 0755. The daemon's openKV() repairs file modes on read but doesn't touch directory modes. Run `sudo chmod 0755 /var/lib/ventd` to fix.",
				EntityHash: doctor.HashEntity("permissions_dir_mode", "/var/lib/ventd"),
				Observed:   now,
			})
		}
	}

	// 3. /var/lib/ventd/state.yaml file mode (0640 per RULE-STATE-09).
	//    The daemon repairs this on read — surface for visibility.
	info, err = d.FS.Stat("/var/lib/ventd/state.yaml")
	if err == nil {
		mode := info.Mode().Perm()
		const expected fs.FileMode = 0o640
		if mode != expected {
			facts = append(facts, doctor.Fact{
				Detector:   d.Name(),
				Severity:   doctor.SeverityWarning,
				Class:      recovery.ClassUnknown,
				Title:      fmt.Sprintf("state.yaml has mode 0%o, expected 0%o", mode, expected),
				Detail:     "RULE-STATE-09 expects 0640 ventd:ventd. The daemon repairs this on next read (Chmod-after-write recovery), but persistent drift means a non-ventd process wrote the file. Investigate or `sudo chmod 0640 /var/lib/ventd/state.yaml`.",
				EntityHash: doctor.HashEntity("permissions_file_mode", "/var/lib/ventd/state.yaml"),
				Observed:   now,
			})
		}
	}

	return facts, nil
}
