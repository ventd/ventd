package detectors

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// AppArmorProfilesFile is the kernel surface
// AppArmorProfileDriftDetector reads. Production wires the live
// path; tests inject a stub returning canned content.
type AppArmorProfilesFile interface {
	// ReadAll returns the contents of /sys/kernel/security/apparmor/profiles
	// (or equivalent). os.ErrNotExist signals AppArmor is not loaded.
	ReadAll() ([]byte, error)
}

// liveAppArmorProfiles reads the real kernel file.
type liveAppArmorProfiles struct{}

func (liveAppArmorProfiles) ReadAll() ([]byte, error) {
	return os.ReadFile("/sys/kernel/security/apparmor/profiles")
}

// AppArmorProfileDriftDetector watches for the ventd profile
// disappearing or flipping mode (enforce → complain) since daemon
// start. Either condition implies someone reloaded AppArmor with a
// different profile or invalidated the daemon's policy attach point.
//
// On AppArmor-less hosts (Fedora's SELinux, Arch without apparmor
// installed) the kernel file is absent — detector emits nothing per
// RULE-DOCTOR-04 graceful degrade.
type AppArmorProfileDriftDetector struct {
	// ProfileName is the canonical attach name written by the
	// shipped profile under deploy/apparmor.d/ventd. Defaults to
	// "ventd" when empty.
	ProfileName string

	// ExpectedMode is the mode the wizard observed at install/
	// daemon-start time. Per RULE-INSTALL-06 v0.5.8.1+ ships the
	// profile but does NOT auto-load it, so the operational baseline
	// for most users is "absent". Operators who attached via
	// `systemctl edit ventd` populate this with the snapshot they
	// took then. Empty = "any present mode is acceptable".
	ExpectedMode string

	// File is the kernel-surface reader. Defaults to liveAppArmorProfiles.
	File AppArmorProfilesFile
}

// NewAppArmorProfileDriftDetector constructs a detector for the
// given profile + expected-mode pair. file nil → live kernel read.
func NewAppArmorProfileDriftDetector(profileName, expectedMode string, file AppArmorProfilesFile) *AppArmorProfileDriftDetector {
	if file == nil {
		file = liveAppArmorProfiles{}
	}
	if profileName == "" {
		profileName = "ventd"
	}
	return &AppArmorProfileDriftDetector{
		ProfileName:  profileName,
		ExpectedMode: expectedMode,
		File:         file,
	}
}

// Name returns the stable detector ID.
func (d *AppArmorProfileDriftDetector) Name() string { return "apparmor_profile_drift" }

// Probe reads the kernel profiles file and compares against the
// expected attach state. Emits at most one Fact (the operator only
// cares whether SOMETHING changed, not the per-line diff).
func (d *AppArmorProfileDriftDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	raw, err := d.File.ReadAll()
	if err != nil {
		// AppArmor not loaded → no surface; don't surface an error.
		return nil, nil
	}

	mode, present := lookupAppArmorProfile(string(raw), d.ProfileName)
	now := timeNowFromDeps(deps)

	// Case A: expected absent → present. Surface as Warning.
	// Case B: expected mode mismatched → Warning.
	// Case C: expected present + same mode → no fact.
	// Case D: expected unknown ("" passes) → no fact.

	if d.ExpectedMode == "" {
		// No baseline pinned → can't detect drift; the install-contract
		// rule (RULE-INSTALL-06) covers the "should this be loaded"
		// question. Don't fire.
		return nil, nil
	}

	if d.ExpectedMode == "absent" && present {
		return []doctor.Fact{{
			Detector: d.Name(),
			Severity: doctor.SeverityWarning,
			Class:    recovery.ClassApparmorDenied,
			Title:    fmt.Sprintf("AppArmor profile %s appeared since daemon start", d.ProfileName),
			Detail: fmt.Sprintf(
				"Profile %s is now loaded in mode %q. Daemon was started with the profile absent. If the daemon's binary path or capability set changed since the wizard's HIL log was captured, this attach may produce DENIED audit lines on legitimate operations. Run `aa-status` to confirm; consult validation/apparmor-smoke-*.md for the expected baseline.",
				d.ProfileName, mode,
			),
			EntityHash: doctor.HashEntity("apparmor_profile_appeared", d.ProfileName, mode),
			Observed:   now,
		}}, nil
	}

	if d.ExpectedMode != "absent" && !present {
		return []doctor.Fact{{
			Detector: d.Name(),
			Severity: doctor.SeverityWarning,
			Class:    recovery.ClassApparmorDenied,
			Title:    fmt.Sprintf("AppArmor profile %s is no longer loaded", d.ProfileName),
			Detail: fmt.Sprintf(
				"Profile was expected in mode %q but is now absent from the kernel. Either AppArmor was reloaded without the ventd profile or the apparmor service stopped. Re-attach via `sudo apparmor_parser -r /etc/apparmor.d/%s`.",
				d.ExpectedMode, d.ProfileName,
			),
			EntityHash: doctor.HashEntity("apparmor_profile_unloaded", d.ProfileName),
			Observed:   now,
		}}, nil
	}

	if d.ExpectedMode != mode && d.ExpectedMode != "absent" && present {
		return []doctor.Fact{{
			Detector: d.Name(),
			Severity: doctor.SeverityWarning,
			Class:    recovery.ClassApparmorDenied,
			Title:    fmt.Sprintf("AppArmor profile %s mode changed: expected %q, observed %q", d.ProfileName, d.ExpectedMode, mode),
			Detail: fmt.Sprintf(
				"Mode-shift between enforce and complain modes is operator-controlled (e.g. `aa-complain` / `aa-enforce`). If this wasn't intentional, the profile may have been re-parsed without the ventd attach surviving — re-run `apparmor_parser -r /etc/apparmor.d/%s`.",
				d.ProfileName,
			),
			EntityHash: doctor.HashEntity("apparmor_profile_mode_drift", d.ProfileName, mode),
			Observed:   now,
		}}, nil
	}

	return nil, nil
}

// lookupAppArmorProfile parses the kernel's profiles file format —
// one profile per line: `<name> (<mode>)` — and returns
// (mode, true) if the named profile is present.
//
// Example content:
//
//	ventd (enforce)
//	docker-default (complain)
//	unconfined (unconfined)
func lookupAppArmorProfile(content, name string) (mode string, present bool) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: "<name> (<mode>)"
		open := strings.LastIndex(line, "(")
		close := strings.LastIndex(line, ")")
		if open < 0 || close < 0 || close <= open {
			continue
		}
		profileName := strings.TrimSpace(line[:open])
		profileMode := strings.TrimSpace(line[open+1 : close])
		if profileName == name {
			return profileMode, true
		}
	}
	return "", false
}
