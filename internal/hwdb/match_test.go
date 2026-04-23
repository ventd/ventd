package hwdb

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/testfixture/fakedmi"
)

// fpFromDMIRoot reads board_vendor, board_name, and board_version from a
// /sys/class/dmi/id-style directory (as produced by fakedmi.New) and returns
// a HardwareFingerprint. Missing files silently yield empty strings.
func fpFromDMIRoot(t *testing.T, root string) HardwareFingerprint {
	t.Helper()
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
	return HardwareFingerprint{
		BoardVendor:  read("board_vendor"),
		BoardName:    read("board_name"),
		BoardVersion: read("board_version"),
	}
}

func TestHWDB_Match(t *testing.T) {
	resetRemote(t)

	// Pre-build fakedmi-backed fingerprints. Each preset populates real DMI
	// files so fpFromDMIRoot exercises the same read path as production.
	msiMegX570FP := fpFromDMIRoot(t, fakedmi.New(t, &fakedmi.BoardMSIMegX570).Root())
	gigabyteX870EFP := fpFromDMIRoot(t, fakedmi.New(t, &fakedmi.BoardGigabyteX870E).Root())
	asusPrimeX670EFP := fpFromDMIRoot(t, fakedmi.New(t, &fakedmi.BoardASUSPrimeX670E).Root())
	supermicroX11FP := fpFromDMIRoot(t, fakedmi.New(t, &fakedmi.BoardSupermicroX11).Root())
	dellR750FP := fpFromDMIRoot(t, fakedmi.New(t, &fakedmi.BoardDellPowerEdgeR750).Root())
	framework13FP := fpFromDMIRoot(t, fakedmi.New(t, &fakedmi.BoardFramework13).Root())
	framework16FP := fpFromDMIRoot(t, fakedmi.New(t, &fakedmi.BoardFramework16).Root())
	rpi5FP := fpFromDMIRoot(t, fakedmi.New(t, &fakedmi.BoardRPi5).Root())
	macbookFP := fpFromDMIRoot(t, fakedmi.New(t, &fakedmi.BoardMacBookPro14Asahi).Root())

	tests := []struct {
		name        string
		fp          HardwareFingerprint
		wantMatch   bool
		wantModules []string
		// wantBoardName is the board_name of the profile actually returned.
		// For shadowed entries this differs from the fingerprint's board_name.
		wantBoardName string
	}{
		// ── profiles.yaml entry 0: MSI MAG (verified, prefix on name) ──────────
		// Exact match: fingerprint board_name equals the profile's "MAG" token.
		{
			name:          "msi_mag_exact_verified",
			fp:            HardwareFingerprint{BoardVendor: "Micro-Star International", BoardName: "MAG"},
			wantMatch:     true,
			wantModules:   []string{"nct6687"},
			wantBoardName: "MAG",
		},
		// Prefix match: "MAG" is a prefix of "MAG Z490 TOMAHAWK" (stage 1).
		// Demonstrates exact > prefix resolution: if the above row passes at
		// stage 0, this row must reach stage 1.
		{
			name:          "msi_mag_prefix_verified",
			fp:            HardwareFingerprint{BoardVendor: "Micro-Star International", BoardName: "MAG Z490 TOMAHAWK"},
			wantMatch:     true,
			wantModules:   []string{"nct6687"},
			wantBoardName: "MAG",
		},

		// ── profiles.yaml entry 1: MSI MPG (verified, prefix on name) ──────────
		{
			name:          "msi_mpg_prefix_verified",
			fp:            HardwareFingerprint{BoardVendor: "Micro-Star International", BoardName: "MPG X570 Gaming Plus"},
			wantMatch:     true,
			wantModules:   []string{"nct6687"},
			wantBoardName: "MPG",
		},

		// ── profiles.yaml entry 2: Gigabyte generic (unverified, vendor-only) ──
		// The generic Gigabyte profile has an empty board_name, so it matches
		// any board_name at the exact stage via the empty-field wildcard rule.
		// Tested via fakedmi BoardGigabyteX870E whose vendor prefix matches.
		{
			name:          "gigabyte_generic_fakedmi_via_x870e",
			fp:            gigabyteX870EFP,
			wantMatch:     true,
			wantModules:   []string{"it87"},
			wantBoardName: "",
		},
		// Synthesized variant: exact vendor match, arbitrary board name.
		{
			name:          "gigabyte_generic_synthesized",
			fp:            HardwareFingerprint{BoardVendor: "Gigabyte Technology", BoardName: "B450M DS3H"},
			wantMatch:     true,
			wantModules:   []string{"it87"},
			wantBoardName: "",
		},

		// ── profiles.yaml entry 3: MSI MEG X570 ACE (unverified, exact) ────────
		// no fakedmi preset; synthesized from profiles.yaml
		{
			name:          "msi_meg_x570_ace_exact",
			fp:            HardwareFingerprint{BoardVendor: "Micro-Star International", BoardName: "MEG X570 ACE"},
			wantMatch:     true,
			wantModules:   []string{"nct6687"},
			wantBoardName: "MEG X570 ACE",
		},

		// ── profiles.yaml entry 4: MSI MEG X570 UNIFY (unverified) ─────────────
		// fakedmi BoardMSIMegX570 has the longer vendor "Micro-Star International
		// Co., Ltd." which prefix-matches the profile's "Micro-Star International".
		{
			name:          "msi_meg_x570_unify_fakedmi_prefix",
			fp:            msiMegX570FP,
			wantMatch:     true,
			wantModules:   []string{"nct6687"},
			wantBoardName: "MEG X570 UNIFY",
		},

		// ── profiles.yaml entry 5: MSI MEG Z790 ACE (unverified, exact) ────────
		// no fakedmi preset; synthesized from profiles.yaml
		{
			name:          "msi_meg_z790_ace_exact",
			fp:            HardwareFingerprint{BoardVendor: "Micro-Star International", BoardName: "MEG Z790 ACE"},
			wantMatch:     true,
			wantModules:   []string{"nct6687"},
			wantBoardName: "MEG Z790 ACE",
		},

		// ── profiles.yaml entry 6: MSI MAG Z790 TOMAHAWK (unverified) ───────────
		// SHADOWED: any "MAG*" board_name from MSI is caught by the verified MAG
		// prefix entry (entry 0) in the first pass, so entry 6 is never reached.
		// This row documents the actual runtime behaviour — the verified entry wins —
		// and also demonstrates the verified > unverified resolution order.
		{
			name:          "msi_mag_z790_tomahawk_shadowed_by_verified_mag",
			fp:            HardwareFingerprint{BoardVendor: "Micro-Star International", BoardName: "MAG Z790 TOMAHAWK"},
			wantMatch:     true,
			wantModules:   []string{"nct6687"},
			wantBoardName: "MAG", // verified MAG prefix entry wins
		},

		// ── profiles.yaml entry 7: Gigabyte X870E AORUS MASTER (unverified) ────
		// SHADOWED: the generic Gigabyte vendor-only entry (entry 2) has an empty
		// board_name, so at the exact stage it matches any Gigabyte board_name
		// before entry 7 is reached. The fingerprint below uses the exact vendor
		// string from profiles.yaml; generic Gigabyte still wins at stage 0.
		// no fakedmi preset; synthesized from profiles.yaml
		{
			name:          "gigabyte_x870e_aorus_master_shadowed_by_generic",
			fp:            HardwareFingerprint{BoardVendor: "Gigabyte Technology", BoardName: "X870E AORUS MASTER"},
			wantMatch:     true,
			wantModules:   []string{"it87"},
			wantBoardName: "", // generic Gigabyte entry (board_name empty)
		},

		// ── profiles.yaml entry 8: Gigabyte B650E AORUS ELITE (unverified) ─────
		// SHADOWED: same vendor-only wildcard issue as entry 7 above.
		// no fakedmi preset; synthesized from profiles.yaml
		{
			name:          "gigabyte_b650e_aorus_elite_shadowed_by_generic",
			fp:            HardwareFingerprint{BoardVendor: "Gigabyte Technology", BoardName: "B650E AORUS ELITE"},
			wantMatch:     true,
			wantModules:   []string{"it87"},
			wantBoardName: "", // generic Gigabyte entry
		},

		// ── profiles.yaml entry 9: ASUS PRIME X670E (unverified) ───────────────
		// fakedmi BoardASUSPrimeX670E has board_name "PRIME X670E-PRO WIFI" which
		// prefix-matches the profile's "PRIME X670E".
		{
			name:          "asus_prime_x670e_fakedmi_prefix",
			fp:            asusPrimeX670EFP,
			wantMatch:     true,
			wantModules:   []string{"nct6775", "asus_ec_sensors"},
			wantBoardName: "PRIME X670E",
		},
		// Synthesized exact variant.
		{
			name:          "asus_prime_x670e_exact",
			fp:            HardwareFingerprint{BoardVendor: "ASUSTeK COMPUTER INC.", BoardName: "PRIME X670E"},
			wantMatch:     true,
			wantModules:   []string{"nct6775", "asus_ec_sensors"},
			wantBoardName: "PRIME X670E",
		},

		// ── profiles.yaml entry 10: ASUS ROG STRIX X670E (unverified, exact) ───
		// no fakedmi preset; synthesized from profiles.yaml
		{
			name:          "asus_rog_strix_x670e_exact",
			fp:            HardwareFingerprint{BoardVendor: "ASUSTeK COMPUTER INC.", BoardName: "ROG STRIX X670E"},
			wantMatch:     true,
			wantModules:   []string{"nct6775", "asus_ec_sensors"},
			wantBoardName: "ROG STRIX X670E",
		},

		// ── profiles.yaml entry 11: ASUS ProArt X670E-CREATOR (unverified, exact)
		// no fakedmi preset; synthesized from profiles.yaml
		{
			name:          "asus_proart_x670e_creator_exact",
			fp:            HardwareFingerprint{BoardVendor: "ASUSTeK COMPUTER INC.", BoardName: "ProArt X670E-CREATOR"},
			wantMatch:     true,
			wantModules:   []string{"nct6775", "asus_ec_sensors"},
			wantBoardName: "ProArt X670E-CREATOR",
		},

		// ── profiles.yaml entry 12: ASRock X670E Taichi (unverified, exact) ────
		// no fakedmi preset; synthesized from profiles.yaml
		{
			name:          "asrock_x670e_taichi_exact",
			fp:            HardwareFingerprint{BoardVendor: "ASRock", BoardName: "X670E Taichi"},
			wantMatch:     true,
			wantModules:   []string{"nct6775"},
			wantBoardName: "X670E Taichi",
		},

		// ── profiles.yaml entry 13: ASRock B650 PG Lightning (unverified, exact)
		// no fakedmi preset; synthesized from profiles.yaml
		{
			name:          "asrock_b650_pg_lightning_exact",
			fp:            HardwareFingerprint{BoardVendor: "ASRock", BoardName: "B650 PG Lightning"},
			wantMatch:     true,
			wantModules:   []string{"nct6775"},
			wantBoardName: "B650 PG Lightning",
		},

		// ── profiles.yaml entry 14: Supermicro X11SCH-F (unverified, exact) ────
		// no fakedmi preset; synthesized from profiles.yaml
		{
			name:          "supermicro_x11sch_f_exact",
			fp:            HardwareFingerprint{BoardVendor: "Supermicro", BoardName: "X11SCH-F"},
			wantMatch:     true,
			wantModules:   []string{"nct6775"},
			wantBoardName: "X11SCH-F",
		},

		// ── profiles.yaml entry 15: Supermicro X12STH (unverified, exact) ──────
		// no fakedmi preset; synthesized from profiles.yaml
		{
			name:          "supermicro_x12sth_exact",
			fp:            HardwareFingerprint{BoardVendor: "Supermicro", BoardName: "X12STH"},
			wantMatch:     true,
			wantModules:   []string{"nct6775"},
			wantBoardName: "X12STH",
		},

		// ── Dell PowerEdge R740 (IPMI entry, unverified, exact) ──────────────
		// IPMI profiles section now provides R740/R750 with IPMI modules.
		// The new entries appear before the legacy hwmon-only entries in the
		// file, so they win the first-match-wins unverified stage sweep.
		{
			name:          "dell_poweredge_r740_exact",
			fp:            HardwareFingerprint{BoardVendor: "Dell Inc.", BoardName: "PowerEdge R740"},
			wantMatch:     true,
			wantModules:   []string{"dell_smm_hwmon", "ipmi_devintf", "ipmi_si"},
			wantBoardName: "PowerEdge R740",
		},

		// ── Dell PowerEdge R750 (IPMI entry, unverified, exact) ──────────────
		{
			name:          "dell_poweredge_r750_exact",
			fp:            HardwareFingerprint{BoardVendor: "Dell Inc.", BoardName: "PowerEdge R750"},
			wantMatch:     true,
			wantModules:   []string{"dell_smm_hwmon", "ipmi_devintf", "ipmi_si"},
			wantBoardName: "PowerEdge R750",
		},

		// ── profiles.yaml entry 18: Framework Laptop 13 AMD (unverified, exact) ─
		// no fakedmi preset; synthesized from profiles.yaml
		// fakedmi BoardFramework13 uses "AMD Ryzen AI 300" which does not match
		// the profile's "AMD Ryzen 7040Series" at any stage, so it is used as a
		// negative case below instead.
		{
			name:          "framework_13_amd_7040series_exact",
			fp:            HardwareFingerprint{BoardVendor: "Framework", BoardName: "Laptop 13 (AMD Ryzen 7040Series)"},
			wantMatch:     true,
			wantModules:   []string{"cros_ec_sensors", "cros_ec_lpcs"},
			wantBoardName: "Laptop 13 (AMD Ryzen 7040Series)",
		},

		// ── profiles.yaml entry 19: Framework Laptop 16 (unverified) ───────────
		// fakedmi BoardFramework16 has vendor "Framework Computer Inc" which
		// prefix-matches the profile's "Framework".
		{
			name:          "framework_16_fakedmi_prefix",
			fp:            framework16FP,
			wantMatch:     true,
			wantModules:   []string{"cros_ec_sensors", "cros_ec_lpcs"},
			wantBoardName: "Laptop 16",
		},

		// ── profiles.yaml entry 20: Intel NUC13 (unverified, prefix on name) ───
		// "NUC13" is a prefix of "NUC13ANHi7".
		// no fakedmi preset; synthesized from profiles.yaml
		{
			name:          "intel_nuc13_prefix",
			fp:            HardwareFingerprint{BoardVendor: "Intel Corporation", BoardName: "NUC13ANHi7"},
			wantMatch:     true,
			wantModules:   []string{},
			wantBoardName: "NUC13",
		},

		// ── profiles.yaml entry 21: Raspberry Pi 5 (unverified, prefix on name) ─
		// "Raspberry Pi 5" is a prefix of "Raspberry Pi 5 Model B".
		// no fakedmi preset; synthesized from profiles.yaml
		{
			name:          "raspberry_pi_5_prefix",
			fp:            HardwareFingerprint{BoardVendor: "Raspberry Pi Foundation", BoardName: "Raspberry Pi 5 Model B"},
			wantMatch:     true,
			wantModules:   []string{"pwm-fan", "gpio-fan"},
			wantBoardName: "Raspberry Pi 5",
		},

		// ── Negative cases: fingerprints that match no profile ──────────────────

		// fakedmi BoardSupermicroX11 has board_name "X11DPi-N". The new IPMI
		// profiles section adds a prefix entry for board_name "X11", so this
		// board now matches (stage 1 prefix in the unverified pass).
		{
			name:          "supermicro_x11_prefix_fakedmi",
			fp:            supermicroX11FP,
			wantMatch:     true,
			wantModules:   []string{"nct6775", "ipmi_devintf", "ipmi_si"},
			wantBoardName: "X11",
		},
		// fakedmi BoardDellPowerEdgeR750 stores the model in ProductName, not
		// BoardName. Its board_name is "0WMJTH" which matches no profile entry.
		{
			name:      "dell_r750_wrong_board_name_fakedmi",
			fp:        dellR750FP,
			wantMatch: false,
		},
		// fakedmi BoardFramework13 uses "AMD Ryzen AI 300"; the profile requires
		// "AMD Ryzen 7040Series" — no match at any stage.
		{
			name:      "framework_13_ai300_no_profile_fakedmi",
			fp:        framework13FP,
			wantMatch: false,
		},
		// fakedmi BoardRPi5 has vendor "Raspberry Pi Ltd"; the profile uses
		// "Raspberry Pi Foundation" — shorter haystack, prefix/substring both fail.
		{
			name:      "rpi5_vendor_mismatch_fakedmi",
			fp:        rpi5FP,
			wantMatch: false,
		},
		// No Apple/Asahi profile exists in profiles.yaml.
		{
			name:      "apple_asahi_no_profile_fakedmi",
			fp:        macbookFP,
			wantMatch: false,
		},
		// Fully unknown board.
		{
			name:      "unknown_board_no_match",
			fp:        HardwareFingerprint{BoardVendor: "Unknown Vendor Inc.", BoardName: "UnknownBoard X9999"},
			wantMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Match(tc.fp)
			if !tc.wantMatch {
				if !errors.Is(err, ErrNoMatch) {
					t.Fatalf("Match() error = %v, want ErrNoMatch", err)
				}
				if got != nil {
					t.Fatalf("Match() returned non-nil profile with ErrNoMatch")
				}
				return
			}
			if err != nil {
				t.Fatalf("Match() unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("Match() returned nil profile, want match")
			}
			if !slices.Equal(got.Modules, tc.wantModules) {
				t.Errorf("Modules = %v, want %v", got.Modules, tc.wantModules)
			}
			if got.Match.BoardName != tc.wantBoardName {
				t.Errorf("profile BoardName = %q, want %q", got.Match.BoardName, tc.wantBoardName)
			}
		})
	}
}
