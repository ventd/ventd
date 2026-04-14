package hwmon

import "testing"

func TestParseOSReleaseAndMOKCommand(t *testing.T) {
	cases := []struct {
		name, content, wantInstallSubstring string
	}{
		{"debian", `ID=debian` + "\n" + `ID_LIKE=""`, "apt-get"},
		{"ubuntu_via_id_like", `ID=ubuntu` + "\n" + `ID_LIKE="debian"`, "apt-get"},
		{"fedora", `ID=fedora`, "dnf"},
		{"arch", `ID=arch`, "pacman"},
		{"tumbleweed", `ID="opensuse-tumbleweed"` + "\n" + `ID_LIKE="suse opensuse"`, "zypper"},
		{"alpine", `ID=alpine`, "apk"},
		{"unknown", `ID=plan9`, "mokutil"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := parseOSRelease(tc.content)
			cmd := d.MOKInstallCommand()
			if !contains(cmd, tc.wantInstallSubstring) {
				t.Errorf("MOKInstallCommand()=%q want substring %q", cmd, tc.wantInstallSubstring)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
