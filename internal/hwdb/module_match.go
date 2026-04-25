// Package hwdb matches a board's hardware fingerprint against a curated
// profile database and returns the kernel modules that should be tried at
// install-time probe.
//
// The database is an embedded YAML file (profiles.yaml) compiled in via
// the embed package so the daemon has no runtime filesystem dependency on
// the profile data. Remote refresh from ventd/hardware-profiles is
// deliberately out of scope here — see P1-FP-02.
//
// # Resolution order
//
// Match(fp) runs the stage sweep twice: first over verified profiles only,
// then (only if nothing matched) over unverified profiles. Within a pass,
// it stops at the first successful stage:
//
//  1. Exact: every non-empty field in ModuleProfile.Match is equal (case-
//     insensitive) to the corresponding field in fp.
//  2. Prefix: every non-empty field in ModuleProfile.Match is a prefix (case-
//     insensitive) of the corresponding field in fp.
//  3. Wildcard: every non-empty field in ModuleProfile.Match is a substring
//     (case-insensitive) of the corresponding field in fp. A profile
//     whose Match struct is entirely empty never matches, to avoid zero-
//     signal blanket proposals.
//
// This ordering means a profile that names a specific board overrides a
// vendor-only wildcard, and a verified profile always wins over an
// unverified one regardless of file order. Callers receive the most
// specific, most trusted match available.
package hwdb

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed profiles.yaml
var profilesYAML []byte

// HardwareFingerprint identifies a board via DMI/SMBIOS fields (populated
// from /sys/class/dmi/id/) plus optional hwmon chip and PCI/CPU details.
// Empty fields contribute no matching signal.
type HardwareFingerprint struct {
	BoardVendor   string `yaml:"board_vendor"`
	BoardName     string `yaml:"board_name"`
	BoardVersion  string `yaml:"board_version"`
	ProductFamily string `yaml:"product_family"`
	ChipHexID     string `yaml:"chip_hex_id,omitempty"`
	PCISubsystem  string `yaml:"pci_subsystem,omitempty"`
	CPUMicrocode  string `yaml:"cpu_microcode,omitempty"`
}

// ModuleProfile is one entry in the module-matching database. Match fields are
// merged in-line with Modules/Notes/Unverified so profiles.yaml stays flat:
//
//   - board_vendor: "MSI"
//     board_name: "MEG X570 ACE"
//     modules: [nct6687, it87]
//     notes: "Dual Super I/O"
type ModuleProfile struct {
	Match      HardwareFingerprint `yaml:",inline"`
	Modules    []string            `yaml:"modules"`
	Notes      string              `yaml:"notes,omitempty"`
	Unverified bool                `yaml:"unverified,omitempty"`
}

// ErrNoMatch is returned by Match when no profile matches the fingerprint.
// Callers distinguish it from parse errors via errors.Is.
var ErrNoMatch = errors.New("hwdb: no profile matched fingerprint")

// LoadModules parses the embedded profiles.yaml into a ModuleProfile slice.
// Exposed for callers that want the full list (e.g. diagnostic UI) and for
// Match's own initialisation path. The function re-parses on every call;
// callers that need to hot-loop should cache the result.
func LoadModules() ([]ModuleProfile, error) {
	var profiles []ModuleProfile
	dec := yaml.NewDecoder(bytes.NewReader(profilesYAML))
	if err := dec.Decode(&profiles); err != nil {
		return nil, fmt.Errorf("hwdb: parse profiles.yaml: %w", err)
	}
	return profiles, nil
}

// Match resolves fp against the merged database (embedded + any remote
// profiles loaded via RefreshFromRemote). See the package doc for the
// exact > prefix > wildcard resolution order.
//
// Verified profiles are always preferred: the pipeline runs first over
// profiles where Unverified is false, then (only if nothing matched) over
// profiles where Unverified is true. An unverified profile early in the
// YAML therefore cannot shadow a verified one that appears later.
//
// Within a stage, the first matching profile wins. Authors adding a profile
// that overlaps an existing entry MUST place the more-specific profile first.
// This applies most to prefix matches — if two profiles both prefix-match a
// fingerprint's board_name, file order determines the winner.
//
// Returns ErrNoMatch (not nil profile, nil error) when no entry matches.
func Match(fp HardwareFingerprint) (*ModuleProfile, error) {
	profiles, err := mergedProfiles()
	if err != nil {
		return nil, err
	}
	needle := lowerFP(fp)

	// First pass: verified profiles only.
	if p := walkStages(profiles, needle, func(p *ModuleProfile) bool { return !p.Unverified }); p != nil {
		return p, nil
	}
	// Second pass: unverified profiles as fallback.
	if p := walkStages(profiles, needle, func(p *ModuleProfile) bool { return p.Unverified }); p != nil {
		return p, nil
	}
	return nil, ErrNoMatch
}

// walkStages runs the exact → prefix → wildcard stage sweep over profiles,
// skipping entries where filter returns false. Returns the first match or nil.
func walkStages(profiles []ModuleProfile, needle HardwareFingerprint, filter func(*ModuleProfile) bool) *ModuleProfile {
	for stage := 0; stage < 3; stage++ {
		for i := range profiles {
			if !filter(&profiles[i]) {
				continue
			}
			m := lowerFP(profiles[i].Match)
			if isZeroFP(m) {
				continue
			}
			if matchStage(m, needle, stage) {
				return &profiles[i]
			}
		}
	}
	return nil
}

// matchStage returns true if every non-empty field in m relates to the
// corresponding field in n by the relation specified by stage:
//
//	0: equal
//	1: prefix
//	2: substring (wildcard)
//
// Fields of m that are empty are treated as wildcards (always match).
func matchStage(m, n HardwareFingerprint, stage int) bool {
	cmp := func(a, b string) bool {
		if a == "" {
			return true
		}
		switch stage {
		case 0:
			return a == b
		case 1:
			return strings.HasPrefix(b, a)
		default:
			return strings.Contains(b, a)
		}
	}
	return cmp(m.BoardVendor, n.BoardVendor) &&
		cmp(m.BoardName, n.BoardName) &&
		cmp(m.BoardVersion, n.BoardVersion) &&
		cmp(m.ProductFamily, n.ProductFamily) &&
		cmp(m.ChipHexID, n.ChipHexID) &&
		cmp(m.PCISubsystem, n.PCISubsystem) &&
		cmp(m.CPUMicrocode, n.CPUMicrocode)
}

func lowerFP(fp HardwareFingerprint) HardwareFingerprint {
	return HardwareFingerprint{
		BoardVendor:   strings.ToLower(strings.TrimSpace(fp.BoardVendor)),
		BoardName:     strings.ToLower(strings.TrimSpace(fp.BoardName)),
		BoardVersion:  strings.ToLower(strings.TrimSpace(fp.BoardVersion)),
		ProductFamily: strings.ToLower(strings.TrimSpace(fp.ProductFamily)),
		ChipHexID:     strings.ToLower(strings.TrimSpace(fp.ChipHexID)),
		PCISubsystem:  strings.ToLower(strings.TrimSpace(fp.PCISubsystem)),
		CPUMicrocode:  strings.ToLower(strings.TrimSpace(fp.CPUMicrocode)),
	}
}

func isZeroFP(fp HardwareFingerprint) bool {
	return fp.BoardVendor == "" && fp.BoardName == "" && fp.BoardVersion == "" &&
		fp.ProductFamily == "" && fp.ChipHexID == "" && fp.PCISubsystem == "" &&
		fp.CPUMicrocode == ""
}
