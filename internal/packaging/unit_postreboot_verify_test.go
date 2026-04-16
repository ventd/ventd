// Package packaging holds regression tests over the shipping artifacts
// under deploy/. These tests read source files from the repository
// tree, not built binaries, and do not execute systemd — so they run
// on any developer host and in CI without requiring root or a live
// init system.
package packaging_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVentdPostrebootVerifyService_ShippingLayout enforces the
// structural invariants the install path and runtime rely on:
//
//   - Each directive lives in the systemd section that actually
//     accepts it. Misplaced directives are silently ignored; the
//     README v0.2.0 rig-verify run surfaced this in the form of a
//     decorative OnFailure= under [Service]. Mirror the guard for
//     this unit before we ship it.
//   - ExecStart invokes the sbin path the installer writes to, and
//     ConditionPathExists= points at the same file so a partial
//     install fails closed.
//   - Documentation= does not leak a file:///home/... path baked
//     into the dev-only validation/ copy.
func TestVentdPostrebootVerifyService_ShippingLayout(t *testing.T) {
	path := filepath.Join(repoRoot(t), "deploy", "ventd-postreboot-verify.service")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	unit := parseUnit(t, string(data))

	for section, kvs := range unit {
		for key := range kvs {
			if want, known := directiveSection[key]; known && want != section {
				t.Errorf("directive %q in [%s]; must be in [%s]", key, section, want)
			}
		}
	}

	const wantExec = "/usr/local/sbin/ventd-postreboot-verify.sh"
	if got := unit["Service"]["ExecStart"]; len(got) != 1 || got[0] != wantExec {
		t.Errorf("ExecStart = %v; want [%q]", got, wantExec)
	}
	if got := unit["Unit"]["ConditionPathExists"]; len(got) != 1 || got[0] != wantExec {
		t.Errorf("ConditionPathExists = %v; want [%q]", got, wantExec)
	}

	if got := unit["Service"]["Type"]; len(got) != 1 || got[0] != "oneshot" {
		t.Errorf("Type = %v; want [oneshot]", got)
	}
	if got := unit["Service"]["RemainAfterExit"]; len(got) != 1 || got[0] != "yes" {
		t.Errorf("RemainAfterExit = %v; want [yes]", got)
	}
	if got := unit["Install"]["WantedBy"]; len(got) != 1 || got[0] != "multi-user.target" {
		t.Errorf("WantedBy = %v; want [multi-user.target]", got)
	}

	for _, doc := range unit["Unit"]["Documentation"] {
		if strings.HasPrefix(doc, "file:///home/") {
			t.Errorf("Documentation=%q — absolute /home/... dev path leaked into shipping unit", doc)
		}
	}
}

// directiveSection is a deny-list of directives that have an
// unambiguous home in one systemd section. Only keys that routinely
// get misplaced are listed — the full systemd vocabulary is not
// mirrored. Anything not listed is accepted in any section.
var directiveSection = map[string]string{
	// [Unit]-only directives. OnFailure= is the one that bit us in
	// ventd.service (issue #58); the rest round out the class.
	"After":               "Unit",
	"Before":              "Unit",
	"Wants":               "Unit",
	"Requires":            "Unit",
	"Requisite":           "Unit",
	"BindsTo":             "Unit",
	"PartOf":              "Unit",
	"Conflicts":           "Unit",
	"OnFailure":           "Unit",
	"OnSuccess":           "Unit",
	"ConditionPathExists": "Unit",
	"AssertPathExists":    "Unit",

	// [Service]-only directives that commonly get pasted into [Unit]
	// by accident.
	"Type":             "Service",
	"ExecStart":        "Service",
	"ExecStartPre":     "Service",
	"ExecStartPost":    "Service",
	"ExecStop":         "Service",
	"ExecStopPost":     "Service",
	"ExecReload":       "Service",
	"RemainAfterExit":  "Service",
	"Restart":          "Service",
	"RestartSec":       "Service",
	"User":             "Service",
	"Group":            "Service",
	"WatchdogSec":      "Service",
	"NotifyAccess":     "Service",
	"SyslogIdentifier": "Service",

	// [Install]-only.
	"WantedBy":   "Install",
	"RequiredBy": "Install",
	"Alias":      "Install",
	"Also":       "Install",
}

// parseUnit is a deliberately small systemd-unit parser: sections of
// the form [Name], key=value lines, # / ; comments. Multi-value keys
// accumulate. Sufficient for the assertion set above; not a general
// systemd parser (no drop-ins, no \-line-continuations, no quoting).
func parseUnit(t *testing.T, body string) map[string]map[string][]string {
	t.Helper()
	out := map[string]map[string][]string{}
	section := ""
	for i, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			if _, ok := out[section]; !ok {
				out[section] = map[string][]string{}
			}
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			t.Errorf("line %d: %q has no '='", i+1, line)
			continue
		}
		if section == "" {
			t.Errorf("line %d: %q before any [Section] header", i+1, line)
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		out[section][key] = append(out[section][key], val)
	}
	return out
}

// repoRoot resolves the ventd repository root from the test binary's
// working directory. `go test ./internal/packaging/` runs with cwd
// at that package, so two levels up lands at the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(cwd, "..", ".."))
}
