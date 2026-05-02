package checks

import (
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/hwmon"
)

// Each subtest binds 1:1 to a RULE-PREFLIGHT-DISPATCH-* invariant in
// .claude/rules/preflight-orchestrator.md.

func TestDistroDispatch(t *testing.T) {
	t.Run("RULE-PREFLIGHT-DISPATCH-01_debian_uses_apt", func(t *testing.T) {
		// Debian/Ubuntu MUST use apt-get with -y and
		// --no-install-recommends. The flag set is load-bearing:
		// without -y the install hangs waiting for a confirm, and
		// without --no-install-recommends apt pulls in a kernel
		// metapackage we don't want.
		d := hwmon.DistroInfo{ID: "ubuntu", IDLike: "debian"}
		cmd, ok := installCommand(d, "kmod")
		if !ok {
			t.Fatal("debian family not recognised")
		}
		if !strings.Contains(cmd, "apt-get install -y") {
			t.Fatalf("missing apt-get -y: %s", cmd)
		}
		if !strings.Contains(cmd, "--no-install-recommends") {
			t.Fatalf("missing --no-install-recommends: %s", cmd)
		}
		if !strings.Contains(cmd, "DEBIAN_FRONTEND=noninteractive") {
			t.Fatalf("missing DEBIAN_FRONTEND: %s", cmd)
		}
	})

	t.Run("RULE-PREFLIGHT-DISPATCH-02_fedora_uses_dnf", func(t *testing.T) {
		// Fedora/RHEL/CentOS dispatches to dnf; -y is required for
		// non-interactive mode. yum on legacy systems is symlinked
		// to dnf so the same command works.
		d := hwmon.DistroInfo{ID: "fedora"}
		cmd, _ := installCommand(d, "kmod")
		if cmd != "dnf install -y kmod" {
			t.Fatalf("got %q", cmd)
		}
	})

	t.Run("RULE-PREFLIGHT-DISPATCH-03_arch_uses_pacman_needed", func(t *testing.T) {
		// Arch's --needed prevents reinstalling an already-present
		// package (which would unnecessarily bump the install
		// duration). --noconfirm is required for non-interactive.
		d := hwmon.DistroInfo{ID: "arch"}
		cmd, _ := installCommand(d, "mokutil")
		if !strings.Contains(cmd, "--needed") {
			t.Fatalf("missing --needed: %s", cmd)
		}
		if !strings.Contains(cmd, "--noconfirm") {
			t.Fatalf("missing --noconfirm: %s", cmd)
		}
	})

	t.Run("RULE-PREFLIGHT-DISPATCH-04_unknown_family_returns_docs_only", func(t *testing.T) {
		// An unknown ID/ID_LIKE MUST NOT produce a falsy install
		// command — installPackage returns an error so the
		// orchestrator surfaces a docs-only fallback instead of
		// running a no-op shell command.
		d := hwmon.DistroInfo{ID: "weirdix"}
		_, ok := installCommand(d, "anything")
		if ok {
			t.Fatalf("unknown family returned ok=true")
		}
	})

	t.Run("RULE-PREFLIGHT-DISPATCH-05_kernel_headers_package_per_family", func(t *testing.T) {
		// Debian/Ubuntu encode the release in the package name —
		// mismatched release means apt installs the wrong package.
		// Fedora uses kernel-devel-<rel>. Arch is rolling and uses
		// the bare linux-headers (always matches running kernel).
		release := "6.8.0-1-generic"
		cases := []struct {
			d    hwmon.DistroInfo
			want string
		}{
			{hwmon.DistroInfo{ID: "ubuntu", IDLike: "debian"}, "linux-headers-" + release},
			{hwmon.DistroInfo{ID: "fedora"}, "kernel-devel-" + release},
			{hwmon.DistroInfo{ID: "arch"}, "linux-headers"},
		}
		for _, c := range cases {
			got := kernelHeadersPackage(c.d, release)
			if got != c.want {
				t.Errorf("%s: got %q, want %q", c.d.ID, got, c.want)
			}
		}
	})

	t.Run("RULE-PREFLIGHT-DISPATCH-06_build_tools_package_per_family", func(t *testing.T) {
		// build-essential / base-devel / gcc make — each family's
		// canonical "give me a build environment" meta-package or
		// minimal set. A wrong name leaves the operator with cryptic
		// "command not found: gcc" mid-build.
		cases := []struct {
			d    hwmon.DistroInfo
			want string
		}{
			{hwmon.DistroInfo{ID: "debian"}, "build-essential"},
			{hwmon.DistroInfo{ID: "fedora"}, "gcc make"},
			{hwmon.DistroInfo{ID: "arch"}, "base-devel"},
			{hwmon.DistroInfo{ID: "alpine"}, "build-base"},
		}
		for _, c := range cases {
			if got := buildToolsPackage(c.d); got != c.want {
				t.Errorf("%s: got %q, want %q", c.d.ID, got, c.want)
			}
		}
	})
}
