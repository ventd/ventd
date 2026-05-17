package hwmon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsUnsubstitutedVersionPlaceholder(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// Sed-style: it87 ships PACKAGE_VERSION="#MODULE_VERSION#" (#785).
		{"it87 sed placeholder", "#MODULE_VERSION#", true},
		// Autoconf-style: msi-ec ships PACKAGE_VERSION="@VERSION@" (#1154).
		{"msi-ec autoconf placeholder", "@VERSION@", true},
		// Generic same-shape — adapt to any future upstream that ships
		// a sed/autoconf template.
		{"hash any token", "#PKG_VER#", true},
		{"at any token", "@PACKAGE_VERSION@", true},

		// Real version strings must pass through untouched.
		{"semver", "1.2.3", false},
		{"date stamp", "2026.05.17", false},
		{"git tag style", "v0.7.1", false},
		{"prerelease", "1.0.0-rc1", false},
		{"empty", "", false},
		// Mixed delimiters: ambiguous, treat as a real (weird) version
		// rather than silently substituting.
		{"hash open at close", "#FOO@", false},
		{"at open hash close", "@FOO#", false},
		// Edge: zero-length token between delimiters is not a real
		// placeholder — refuse so we don't accept "##" / "@@".
		{"double hash", "##", false},
		{"double at", "@@", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUnsubstitutedVersionPlaceholder(tc.in); got != tc.want {
				t.Fatalf("isUnsubstitutedVersionPlaceholder(%q) = %v, want %v",
					tc.in, got, tc.want)
			}
		})
	}
}

func TestRewriteDKMSVersion(t *testing.T) {
	// Round-trip the autoconf-style placeholder shape that broke msi-ec
	// in #1154 — after rewrite, `dkms add` would parse the substituted
	// value instead of registering literally as msi-ec/@VERSION@.
	t.Run("substitutes msi-ec @VERSION@ placeholder", func(t *testing.T) {
		dir := t.TempDir()
		conf := filepath.Join(dir, "dkms.conf")
		original := `PACKAGE_NAME="msi-ec"
PACKAGE_VERSION="@VERSION@"
BUILT_MODULE_NAME[0]="msi-ec"
DEST_MODULE_LOCATION[0]="/kernel/drivers/platform/x86"
AUTOINSTALL="yes"
`
		if err := os.WriteFile(conf, []byte(original), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}

		if err := rewriteDKMSVersion(conf, "2026.05.17"); err != nil {
			t.Fatalf("rewriteDKMSVersion: %v", err)
		}

		got, err := os.ReadFile(conf)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if want := `PACKAGE_VERSION="2026.05.17"`; !strings.Contains(string(got), want) {
			t.Errorf("rewritten conf missing %q\n---\n%s", want, got)
		}
		// Unrelated lines (BUILT_MODULE_NAME, AUTOINSTALL) must survive.
		for _, line := range []string{
			`PACKAGE_NAME="msi-ec"`,
			`BUILT_MODULE_NAME[0]="msi-ec"`,
			`AUTOINSTALL="yes"`,
		} {
			if !strings.Contains(string(got), line) {
				t.Errorf("rewritten conf dropped %q\n---\n%s", line, got)
			}
		}
		if strings.Contains(string(got), "@VERSION@") {
			t.Errorf("rewritten conf still contains literal placeholder\n---\n%s", got)
		}
	})

	t.Run("appends PACKAGE_VERSION when missing", func(t *testing.T) {
		dir := t.TempDir()
		conf := filepath.Join(dir, "dkms.conf")
		if err := os.WriteFile(conf, []byte("PACKAGE_NAME=\"foo\"\n"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := rewriteDKMSVersion(conf, "0.0.1"); err != nil {
			t.Fatalf("rewriteDKMSVersion: %v", err)
		}
		got, _ := os.ReadFile(conf)
		if !strings.Contains(string(got), `PACKAGE_VERSION="0.0.1"`) {
			t.Errorf("missing appended version line:\n%s", got)
		}
	})
}
