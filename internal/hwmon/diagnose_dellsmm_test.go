package hwmon

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestParseModinfoVersion(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "ventd fork",
			input: `filename:       /lib/modules/7.0.8/extra/dell-smm-hwmon.ko.xz
alias:          i8k
license:        GPL
description:    Dell laptop SMM BIOS hwmon driver
author:         Pali Rohár <pali@kernel.org>
version:        7.0.0-ventd.3
`,
			want: "7.0.0-ventd.3",
		},
		{
			name:  "in-tree, no version",
			input: "filename:       /lib/modules/7.0.8/kernel/drivers/hwmon/dell-smm-hwmon.ko.xz\nalias:          i8k\nlicense:        GPL\n",
			want:  "",
		},
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name:  "in-tree with version",
			input: "filename:       foo\nversion:        7.0.0\n",
			want:  "7.0.0",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseModinfoVersion(c.input)
			if got != c.want {
				t.Errorf("parseModinfoVersion: want %q, got %q", c.want, got)
			}
		})
	}
}

func TestCompareVentdForkVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"7.0.0-ventd.2", "7.0.0-ventd.3", -1},
		{"7.0.0-ventd.3", "7.0.0-ventd.3", 0},
		{"7.0.0-ventd.4", "7.0.0-ventd.3", 1},
		{"7.0.0-ventd.10", "7.0.0-ventd.3", 1},
	}
	for _, c := range cases {
		got := compareVentdForkVersion(c.a, c.b)
		if got != c.want {
			t.Errorf("compareVentdForkVersion(%q,%q): want %d, got %d", c.a, c.b, c.want, got)
		}
	}
}

// loadedTrue / loadedFalse are reusable loadedFn fakes for the diagnose tests.
func loadedTrue() bool  { return true }
func loadedFalse() bool { return false }

func TestDiagnoseDellSMMVersion_InTreeDriverWarns(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	diagnoseDellSMMVersion(logger, func(string) (string, error) {
		return "version:        7.0.0\n", nil
	}, loadedTrue)
	if !strings.Contains(buf.String(), "in-tree driver detected") {
		t.Errorf("expected in-tree WARN; log: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "v"+MinDellSMMVentdFork) {
		t.Errorf("expected recommended version in WARN; log: %s", buf.String())
	}
}

func TestDiagnoseDellSMMVersion_InTreeNoVersionLineLoadedWarns(t *testing.T) {
	// The in-tree dell_smm_hwmon driver doesn't set MODULE_VERSION, so
	// modinfo returns success with no `version:` line. The previous
	// behaviour was a silent Debug skip. This regression test pins the
	// fix: when the module is loaded, WARN with the install pointer.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	diagnoseDellSMMVersion(logger, func(string) (string, error) {
		return "filename:       /lib/modules/x/kernel/drivers/hwmon/dell-smm-hwmon.ko.xz\nalias:          i8k\nlicense:        GPL\n", nil
	}, loadedTrue)
	if !strings.Contains(buf.String(), "in-tree driver loaded") {
		t.Errorf("expected in-tree-loaded WARN; log: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "v"+MinDellSMMVentdFork) {
		t.Errorf("expected recommended version in WARN; log: %s", buf.String())
	}
}

func TestDiagnoseDellSMMVersion_NoVersionLineNotLoadedIsDebug(t *testing.T) {
	// Same modinfo output as the in-tree case, but the module isn't bound
	// to the running kernel. Non-Dell hosts that happen to ship the .ko
	// shouldn't get a spurious WARN about "install the fork."
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	diagnoseDellSMMVersion(logger, func(string) (string, error) {
		return "filename:       /lib/modules/x/kernel/drivers/hwmon/dell-smm-hwmon.ko.xz\n", nil
	}, loadedFalse)
	if strings.Contains(buf.String(), "WARN") {
		t.Errorf("did not expect WARN when module not loaded; log: %s", buf.String())
	}
}

func TestDiagnoseDellSMMVersion_OlderVentdForkWarns(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	diagnoseDellSMMVersion(logger, func(string) (string, error) {
		return "version:        7.0.0-ventd.2\n", nil
	}, loadedTrue)
	if !strings.Contains(buf.String(), "older than recommended") {
		t.Errorf("expected older-fork WARN; log: %s", buf.String())
	}
}

func TestDiagnoseDellSMMVersion_RecommendedVersionInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	diagnoseDellSMMVersion(logger, func(string) (string, error) {
		return "version:        " + MinDellSMMVentdFork + "\n", nil
	}, loadedTrue)
	if !strings.Contains(buf.String(), "ventd fork installed") {
		t.Errorf("expected satisfied INFO; log: %s", buf.String())
	}
	if strings.Contains(buf.String(), "WARN") {
		t.Errorf("did not expect a WARN at min version; log: %s", buf.String())
	}
}

func TestDiagnoseDellSMMVersion_NewerVentdForkInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	diagnoseDellSMMVersion(logger, func(string) (string, error) {
		return "version:        7.0.0-ventd.5\n", nil
	}, loadedTrue)
	if !strings.Contains(buf.String(), "ventd fork installed") {
		t.Errorf("expected satisfied INFO; log: %s", buf.String())
	}
}

func TestDiagnoseDellSMMVersion_ModuleAbsentIsDebug(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	diagnoseDellSMMVersion(logger, func(string) (string, error) {
		return "", errors.New("modinfo: ERROR: Module dell_smm_hwmon not found.")
	}, loadedFalse)
	if !strings.Contains(buf.String(), "skipping version check") {
		t.Errorf("expected skip-debug; log: %s", buf.String())
	}
	if strings.Contains(buf.String(), "WARN") {
		t.Errorf("did not expect a WARN when module absent; log: %s", buf.String())
	}
}
