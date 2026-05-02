package checks

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/preflight"
)

func okBuildProbes() *BuildProbes {
	r := &recordingRunner{}
	return &BuildProbes{
		HasBinary:      func(string) bool { return true },
		BuildDirExists: func(string) bool { return true },
		KernelRelease:  func() string { return "6.8.0-1-generic" },
		Distro:         hwmon.DistroInfo{ID: "ubuntu", IDLike: "debian"},
		Run:            r.run,
	}
}

func TestBuildChecks(t *testing.T) {
	t.Run("RULE-PREFLIGHT-BUILD-01_gcc_missing_dispatches_build_tools_install", func(t *testing.T) {
		// gcc absent → AutoFix runs `apt-get install build-essential`
		// (or per-family equivalent). build-essential is the umbrella
		// because gcc + make are typically installed together; an
		// AutoFix that installed just gcc would leave make missing
		// for the very next check.
		p := okBuildProbes()
		p.HasBinary = func(name string) bool { return name != "gcc" }
		r := &recordingRunner{}
		p.Run = r.run

		checks := BuildChecks(*p)
		gcc := findByName(t, checks, "gcc_missing")
		tr, _ := gcc.Detect(context.Background())
		if !tr {
			t.Fatal("gcc_missing not triggered")
		}
		if err := gcc.AutoFix(context.Background()); err != nil {
			t.Fatalf("AutoFix: %v", err)
		}
		if len(r.commands) != 1 {
			t.Fatalf("commands: got %d, want 1", len(r.commands))
		}
		if !strings.Contains(r.commands[0], "build-essential") {
			t.Fatalf("expected build-essential install: %s", r.commands[0])
		}
	})

	t.Run("RULE-PREFLIGHT-BUILD-02_kernel_headers_use_release_specific_pkg", func(t *testing.T) {
		// On Debian/Ubuntu the headers package name encodes the
		// running kernel release. Installing the unversioned
		// `linux-headers` would fetch the metapackage that tracks
		// HWE kernels, not necessarily the running kernel.
		p := okBuildProbes()
		p.BuildDirExists = func(string) bool { return false }
		r := &recordingRunner{}
		p.Run = r.run
		checks := BuildChecks(*p)
		hdrs := findByName(t, checks, "kernel_headers_missing")
		if err := hdrs.AutoFix(context.Background()); err != nil {
			t.Fatalf("AutoFix: %v", err)
		}
		if !strings.Contains(r.commands[0], "linux-headers-6.8.0-1-generic") {
			t.Fatalf("expected release-specific pkg: %s", r.commands[0])
		}
	})

	t.Run("RULE-PREFLIGHT-BUILD-03_dkms_check_severity_blocker", func(t *testing.T) {
		// DKMS missing MUST be Blocker (not Warning) — the install
		// path needs DKMS to rebuild after kernel updates. A
		// non-blocker dkms would let the install succeed and then
		// silently break on the next kernel apt upgrade.
		p := okBuildProbes()
		p.HasBinary = func(name string) bool { return name != "dkms" }
		dkms := findByName(t, BuildChecks(*p), "dkms_missing")
		if dkms.Severity != preflight.SeverityBlocker {
			t.Fatalf("severity: got %v, want Blocker", dkms.Severity)
		}
	})

	t.Run("RULE-PREFLIGHT-BUILD-04_release_unknown_returns_actionable_err", func(t *testing.T) {
		// When KernelRelease() returns "" (typically because uname
		// is unavailable or a test fixture forgot to wire it), the
		// kernel-headers AutoFix MUST refuse rather than installing
		// the wrong package. The error string should be
		// recognisable so the orchestrator surfaces it cleanly.
		p := okBuildProbes()
		p.BuildDirExists = func(string) bool { return false }
		p.KernelRelease = func() string { return "" }
		hdrs := findByName(t, BuildChecks(*p), "kernel_headers_missing")
		err := hdrs.AutoFix(context.Background())
		if err == nil {
			t.Fatal("expected error when release unknown")
		}
		if !errors.Is(err, errReleaseUnknown) {
			t.Fatalf("err: %v, want errReleaseUnknown", err)
		}
	})

	t.Run("RULE-PREFLIGHT-BUILD-05_unknown_distro_returns_docs_only", func(t *testing.T) {
		// On a distro family the dispatch table doesn't recognise,
		// the AutoFix MUST surface a docs-only error rather than
		// running an empty install command. Currently this fires
		// for non-systemd Linux variants the package-manager table
		// doesn't cover.
		p := okBuildProbes()
		p.HasBinary = func(name string) bool { return name != "gcc" }
		p.Distro = hwmon.DistroInfo{ID: "weirdix"}
		gcc := findByName(t, BuildChecks(*p), "gcc_missing")
		err := gcc.AutoFix(context.Background())
		if err == nil {
			t.Fatal("expected docs-only error on unknown family")
		}
	})
}

func findByName(t *testing.T, checks []preflight.Check, name string) preflight.Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q not in set", name)
	return preflight.Check{}
}
