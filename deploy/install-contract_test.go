package deploy_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// parseUnit parses a systemd unit file into a map of key → list of values.
// Section headers, blank lines, and # comments are skipped.
// Multi-value keys (e.g. OnFailure, DeviceAllow) accumulate every occurrence.
func parseUnit(data string) map[string][]string {
	out := make(map[string][]string)
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '[' {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		out[strings.TrimSpace(k)] = append(out[strings.TrimSpace(k)], strings.TrimSpace(v))
	}
	return out
}

func loadUnit(t *testing.T, name string) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return parseUnit(string(data))
}

// parseSysusersUsers returns the set of usernames declared by 'u' lines.
func parseSysusersUsers(data string) map[string]bool {
	users := make(map[string]bool)
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "u" {
			users[fields[1]] = true
		}
	}
	return users
}

func loadSysusers(t *testing.T, name string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return parseSysusersUsers(string(data))
}

// TestInstallContract_UserDeclared — RULE-INSTALL-01
// Every User= directive in deploy/*.service must have a matching sysusers.d drop-in.
func TestInstallContract_UserDeclared(t *testing.T) {
	cases := []struct {
		unit     string
		user     string
		sysusers string
	}{
		{"ventd.service", "ventd", "sysusers.d-ventd.conf"},
		{"ventd-ipmi.service", "ventd-ipmi", "sysusers.d-ventd-ipmi.conf"},
	}
	for _, tc := range cases {
		t.Run(tc.unit+"/user="+tc.user, func(t *testing.T) {
			u := loadUnit(t, tc.unit)
			if !slices.Contains(u["User"], tc.user) {
				t.Fatalf("%s: User=%s not found; got %v", tc.unit, tc.user, u["User"])
			}
			users := loadSysusers(t, tc.sysusers)
			if !users[tc.user] {
				t.Errorf("%s: user %q not declared in %s", tc.unit, tc.user, tc.sysusers)
			}
		})
	}
}

// TestInstallContract_OnFailureResolves — RULE-INSTALL-02
// Every OnFailure= directive must reference a unit file present in deploy/.
func TestInstallContract_OnFailureResolves(t *testing.T) {
	u := loadUnit(t, "ventd.service")
	for _, dep := range u["OnFailure"] {
		t.Run(dep, func(t *testing.T) {
			if _, err := os.Stat(dep); err != nil {
				t.Errorf("OnFailure=%s: unit file not found in deploy/: %v", dep, err)
			}
		})
	}
}

// TestInstallContract_WebListenDefault — RULE-INSTALL-03
// The web.listen default must not bind to 0.0.0.0 without TLS.
func TestInstallContract_WebListenDefault(t *testing.T) {
	w := config.Empty().Web
	if err := w.RequireTransportSecurity(); err != nil {
		t.Errorf("default web.listen %q trips TLS gate on fresh boot: %v", w.Listen, err)
	}
}

// TestInstallContract_AppArmorProfileShipped — RULE-INSTALL-04
// Every AppArmorProfile= directive must reference a profile in deploy/apparmor.d/.
func TestInstallContract_AppArmorProfileShipped(t *testing.T) {
	units := []string{"ventd.service", "ventd-ipmi.service"}
	for _, unit := range units {
		u := loadUnit(t, unit)
		for _, profile := range u["AppArmorProfile"] {
			t.Run(unit+"/profile="+profile, func(t *testing.T) {
				path := "apparmor.d/" + profile
				if _, err := os.Stat(path); err != nil {
					t.Errorf("AppArmorProfile=%s declared in %s but %s not found: %v",
						profile, unit, path, err)
				}
			})
		}
	}
}

// TestInstallContract_AppArmorHILValidated — RULE-INSTALL-05
// Every shipped AppArmor profile must have a HIL validation log under enforce mode.
func TestInstallContract_AppArmorHILValidated(t *testing.T) {
	entries, err := os.ReadDir("apparmor.d")
	if err != nil {
		t.Fatalf("read apparmor.d: %v", err)
	}
	distros := []string{"ubuntu", "debian"}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "README.md" || e.Name() == "FIREWALL.md" {
			continue
		}
		profile := e.Name()
		for _, distro := range distros {
			distro := distro
			profile := profile
			t.Run(profile+"/"+distro, func(t *testing.T) {
				pattern := "../validation/apparmor-smoke-" + distro + "-*.md"
				matches, err := filepath.Glob(pattern)
				if err != nil {
					t.Fatalf("glob %s: %v", pattern, err)
				}
				if len(matches) == 0 {
					t.Errorf("no HIL validation log found for profile %q on %s (expected %s)",
						profile, distro, pattern)
					return
				}
				for _, match := range matches {
					data, err := os.ReadFile(match)
					if err != nil {
						t.Fatalf("read %s: %v", match, err)
					}
					content := string(data)
					for line := range strings.SplitSeq(content, "\n") {
						if !strings.Contains(line, `apparmor="DENIED"`) {
							continue
						}
						// Expected deny paths: /dev/mem, /dev/kmem, /sys/kernel/
						isExpected := strings.Contains(line, "/dev/mem") ||
							strings.Contains(line, "/dev/kmem") ||
							strings.Contains(line, "/sys/kernel/")
						if !isExpected {
							t.Errorf("%s: unexpected DENIED line: %s", match, line)
						}
					}
				}
			})
		}
	}
}
