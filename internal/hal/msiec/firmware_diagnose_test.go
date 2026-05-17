// SPDX-License-Identifier: GPL-3.0-or-later
package msiec

import (
	"context"
	"errors"
	"testing"
)

func TestDiagnoseUnsupportedFirmware_Captures(t *testing.T) {
	cases := []struct {
		name string
		log  string
		want string
	}{
		// Upstream wording — msi-ec.c pr_err with single quotes.
		{
			name: "upstream quoted",
			log:  "May 17 14:00:00 host kernel: msi_ec: Firmware version is not supported: '16R8IMS1.130'\n",
			want: "16R8IMS1.130",
		},
		// Unquoted (defence — older driver revs).
		{
			name: "unquoted",
			log:  "[   3.123] msi_ec: Firmware version is not supported: 16R8IMS1.130\n",
			want: "16R8IMS1.130",
		},
		// Multiple log lines with the target in the middle.
		{
			name: "embedded",
			log: "kernel: starting up\n" +
				"msi_ec: probing\n" +
				"msi_ec: Firmware version is not supported: '17F2EMS1.999'\n" +
				"kernel: continuing\n",
			want: "17F2EMS1.999",
		},
		// Hyphen rather than underscore in upstream slug (forward compat).
		{
			name: "hyphen prefix",
			log:  "msi-ec: Firmware version is not supported: '14C1EMS1.500'\n",
			want: "14C1EMS1.500",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := FirmwareDiagnoseSource
			t.Cleanup(func() { FirmwareDiagnoseSource = prev })
			FirmwareDiagnoseSource = func(context.Context) (string, error) { return tc.log, nil }
			got, err := DiagnoseUnsupportedFirmware(context.Background())
			if err != nil {
				t.Fatalf("DiagnoseUnsupportedFirmware: %v", err)
			}
			if got != tc.want {
				t.Fatalf("DiagnoseUnsupportedFirmware = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDiagnoseUnsupportedFirmware_NoMatch(t *testing.T) {
	prev := FirmwareDiagnoseSource
	t.Cleanup(func() { FirmwareDiagnoseSource = prev })
	FirmwareDiagnoseSource = func(context.Context) (string, error) {
		return "kernel: msi_ec: loaded successfully\nkernel: msi_ec: registered platform device\n", nil
	}
	_, err := DiagnoseUnsupportedFirmware(context.Background())
	if !errors.Is(err, ErrNoUnsupportedFirmwareLog) {
		t.Fatalf("DiagnoseUnsupportedFirmware err = %v; want ErrNoUnsupportedFirmwareLog", err)
	}
}

func TestSuggestFirmwarePins_PrefersSameFamily(t *testing.T) {
	// Hudson's MS-16R8 firmware shape — 16R8IMS family. There are
	// several CONF_G* groups carrying 16R8IMS firmwares; the closest
	// revision should rank first regardless of which CONF_G group it
	// belongs to.
	sugs := SuggestFirmwarePins("16R8IMS1.999", 3)
	if len(sugs) == 0 {
		t.Fatal("SuggestFirmwarePins: empty result; catalogue not loaded?")
	}
	if pfx := firmwareFamilyPrefix(sugs[0].Firmware); pfx != "16R8IMS" {
		t.Fatalf("top suggestion family = %q, want 16R8IMS (top result %+v)", pfx, sugs[0])
	}
}

func TestSuggestFirmwarePins_FallsBackAcrossFamilies(t *testing.T) {
	// Unknown family — every suggestion comes from a different group.
	sugs := SuggestFirmwarePins("ZZZZZZZ1.000", 5)
	if len(sugs) == 0 {
		t.Fatal("SuggestFirmwarePins fallback: empty result")
	}
	seen := make(map[string]struct{})
	for _, s := range sugs {
		if _, dup := seen[s.Group]; dup {
			t.Fatalf("fallback duplicated group %q in suggestions %+v", s.Group, sugs)
		}
		seen[s.Group] = struct{}{}
	}
}

func TestSuggestFirmwarePins_HonoursMax(t *testing.T) {
	got := SuggestFirmwarePins("16R8IMS1.999", 2)
	if len(got) != 2 {
		t.Fatalf("max=2 returned %d suggestions; want 2", len(got))
	}
	if got := SuggestFirmwarePins("16R8IMS1.999", 0); got != nil {
		t.Fatalf("max=0 returned %v; want nil", got)
	}
}

func TestLexicalDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"16R8IMS1.117", "16R8IMS1.117", 0},
		{"16R8IMS1.115", "16R8IMS1.117", 2},
		{"16R8IMS1.999", "16R8IMS1.117", 882},
	}
	for _, tc := range cases {
		if got := lexicalDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("lexicalDistance(%q,%q) = %d; want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
