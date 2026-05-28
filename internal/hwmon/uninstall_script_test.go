package hwmon_test

import (
	"os"
	"strings"
	"testing"
)

// TestUninstallScript_CoversIssue1320Artifacts is a static regression
// guard for the #1320 uninstall completeness fix. The shipped
// scripts/uninstall.sh removes a documented set of artifact classes;
// this test asserts each class is referenced by the script so a
// future edit that drops one surfaces as a CI failure rather than as
// a "ventd-uninstall left ten artifacts behind" bug report.
//
// The check is on substring presence — it does NOT execute the script
// (executing would require root + a live host) and does NOT validate
// the rm sequence is correct. The bash syntax + rm command shapes are
// covered by the script's existing CRLF self-heal + `bash -n` parse
// on every install.
func TestUninstallScript_CoversIssue1320Artifacts(t *testing.T) {
	data, err := os.ReadFile("../../scripts/uninstall.sh")
	if err != nil {
		t.Fatalf("read scripts/uninstall.sh: %v", err)
	}
	body := string(data)

	want := []struct {
		name    string
		fixture string
	}{
		// 1. sibling systemd units (ventd-recover.service is enabled-
		// on-install so leaving it behind is the worst-case artifact).
		{"ventd-recover.service stop+disable", "ventd-recover.service"},
		{"ventd-postreboot-verify.service stop+disable", "ventd-postreboot-verify.service"},
		// 2. /etc/modules-load.d entry — installer writes ventd.conf
		// (no dash); the pre-#1320 script only matched ventd-*.conf.
		{"modules-load.d entry without dash", "/etc/modules-load.d/ventd.conf"},
		// 3. helper binaries the installer drops alongside ventd.
		{"ventd-nvml-helper", "ventd-nvml-helper"},
		{"ventd-postreboot-verify.sh", "ventd-postreboot-verify.sh"},
		{"ventd-recover binary", "ventd-recover"},
		{"ventd-wait-hwmon", "ventd-wait-hwmon"},
		// 4. udev rules — both rules.d trees, with reload.
		{"udev rule under /etc", "/etc/udev/rules.d/90-ventd-hwmon.rules"},
		{"udev rule under /usr/lib", "/usr/lib/udev/rules.d/90-ventd-hwmon.rules"},
		{"udevadm reload", "udevadm control --reload-rules"},
		// 5. apparmor profiles — and the parser -R unload step.
		{"apparmor profile ventd", "/etc/apparmor.d/ventd"},
		{"apparmor profile ventd-ipmi", "/etc/apparmor.d/ventd-ipmi"},
		{"apparmor profile ventd.compat", "/etc/apparmor.d/ventd.compat"},
		{"apparmor_parser -R unload", "apparmor_parser -R"},
		// 6. var/log cleanup.
		{"/var/log/ventd cleanup", "/var/log/ventd"},
		// 7. wants symlink that survives unit-file removal.
		{"multi-user.target.wants/ventd-recover.service", "multi-user.target.wants/ventd-recover.service"},
		// 8. polkit rule for in-UI update (#1306).
		{"polkit rule /usr/share/polkit-1/rules.d/50-ventd-update.rules", "/usr/share/polkit-1/rules.d/50-ventd-update.rules"},
		// 9. compressed kernel modules — Fedora ships .ko.xz, Arch .ko.zst (#1407).
		{".ko.xz glob match", ".ko.xz"},
		{".ko.zst glob match", ".ko.zst"},
		// 10. sbin install prefix — ventd-recover and ventd-wait-hwmon
		// land in /usr/local/sbin, not the bin tree (#1407).
		{"sbin install prefix", "VENTD_SBIN_DIR"},
		// 11. ventd system user + group removal (#1407).
		{"ventd user removal", "userdel"},
		{"ventd group removal", "groupdel"},
		{"--keep-user flag", "--keep-user"},
	}
	for _, w := range want {
		if !strings.Contains(body, w.fixture) {
			t.Errorf("uninstall.sh missing reference to %s (fixture=%q) — #1320 coverage regression", w.name, w.fixture)
		}
	}
}
