package hwdb

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultPendingDir = "/var/lib/ventd/profiles-pending"

// CaptureDir returns the resolved profiles-pending directory.
// Root processes use /var/lib/ventd/profiles-pending/.
// Unprivileged processes fall back to $XDG_STATE_HOME/ventd/profiles-pending/.
func CaptureDir() string {
	if os.Geteuid() == 0 {
		return defaultPendingDir
	}
	xdg := os.Getenv("XDG_STATE_HOME")
	if xdg == "" {
		home, _ := os.UserHomeDir()
		xdg = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(xdg, "ventd", "profiles-pending")
}

// Capture writes a candidate hardware profile to dir based on the supplied
// calibration run, DMI data, and catalog reference.
//
// Flow: build profile → Anonymise (PII-strip) → validate → atomic write.
// Returns the path written on success.
//
// Fails closed: if anonymisation fails, the function returns an error and
// writes nothing to disk. RULE-HWDB-CAPTURE-01: writes only to the pending
// dir, never to the live catalog. RULE-HWDB-CAPTURE-02: fail-closed on
// anonymise error.
func Capture(run *CalibrationRun, dmi DMI, cat *Catalog, dir string) (string, error) {
	profile, err := buildProfileFromCalibration(run, dmi, cat)
	if err != nil {
		return "", fmt.Errorf("capture: build profile: %w", err)
	}

	// RULE-HWDB-CAPTURE-02: anonymise before any write attempt. Fail closed.
	if err := callAnonymise(profile); err != nil {
		return "", fmt.Errorf("capture: anonymise: %w", err)
	}

	// Validate the anonymised profile through the standard RULE-HWDB-* pipeline.
	if err := validateSingle(profile); err != nil {
		return "", fmt.Errorf("capture: post-anonymise validation: %w", err)
	}

	data, err := marshalPendingProfile(profile)
	if err != nil {
		return "", fmt.Errorf("capture: marshal: %w", err)
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("capture: mkdir %s: %w", dir, err)
	}

	outPath := filepath.Join(dir, run.DMIFingerprint+".yaml")
	if err := writeAtomic(outPath, data, 0o640); err != nil {
		return "", fmt.Errorf("capture: write %s: %w", outPath, err)
	}

	return outPath, nil
}

// writeAtomic writes data to finalPath via a temp-file + rename.
// Prevents partial writes from producing a corrupt pending profile.
func writeAtomic(finalPath string, data []byte, mode os.FileMode) error {
	tmp := finalPath + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, finalPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// marshalPendingProfile marshals a single profile as a YAML list (one entry)
// for compatibility with the Load() function.
func marshalPendingProfile(profile *Profile) ([]byte, error) {
	return yaml.Marshal([]Profile{*profile})
}

// buildProfileFromCalibration constructs a candidate Profile from a calibration
// run, DMI tuple, and optional catalog. Returns an error if a valid pwm_control
// module cannot be determined from any channel.
func buildProfileFromCalibration(run *CalibrationRun, dmi DMI, cat *Catalog) (*Profile, error) {
	pwmControl, err := resolveDriverModule(run, cat)
	if err != nil {
		return nil, fmt.Errorf("resolve driver module: %w", err)
	}

	fanCount := 0
	for _, ch := range run.Channels {
		if !ch.Phantom {
			fanCount++
		}
	}

	fp := Fingerprint(dmi)
	return &Profile{
		ID:            "community-" + fp,
		SchemaVersion: 1,
		Fingerprint: BoardFingerprint{
			DMISysVendor:   dmi.SysVendor,
			DMIProductName: dmi.ProductName,
			DMIBoardVendor: dmi.BoardVendor,
			DMIBoardName:   dmi.BoardName,
		},
		Hardware: Hardware{
			PWMControl: pwmControl,
			FanCount:   fanCount,
		},
		ContributedBy: "anonymous",
		CapturedAt:    time.Now().UTC().Format("2006-01-02"),
		Verified:      false,
	}, nil
}

// resolveDriverModule returns the pwm_control module name for the calibration
// run. Checks calibrated channels against the chip catalog first, then falls
// back to checking whether the hwmon chip name is itself a known module.
func resolveDriverModule(run *CalibrationRun, cat *Catalog) (string, error) {
	for _, ch := range run.Channels {
		name := ch.HwmonName
		if cat != nil {
			// RULE-HWDB-PR2-02 guarantees InheritsDriver resolves.
			if cp, ok := cat.Chips[name]; ok {
				return cp.InheritsDriver, nil
			}
		}
		if _, ok := knownPWMModules[name]; ok {
			return name, nil
		}
	}
	return "", fmt.Errorf(
		"no channel produced a resolvable driver module (hwmon names: %v)",
		collectHwmonNames(run),
	)
}

func collectHwmonNames(run *CalibrationRun) []string {
	seen := make(map[string]struct{}, len(run.Channels))
	names := make([]string, 0, len(run.Channels))
	for _, ch := range run.Channels {
		if _, dup := seen[ch.HwmonName]; !dup {
			seen[ch.HwmonName] = struct{}{}
			names = append(names, ch.HwmonName)
		}
	}
	return names
}

// validateSingle runs the standard validate pipeline on a single profile.
func validateSingle(p *Profile) error {
	return validate([]Profile{*p})
}
