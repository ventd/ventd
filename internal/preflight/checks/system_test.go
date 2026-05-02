package checks

import (
	"context"
	"strings"
	"testing"
)

func okSystemProbes() SystemProbes {
	return SystemProbes{
		IsContainerised:        func() bool { return false },
		HaveRootOrPasswordless: func() bool { return true },
		LibModulesWritable:     func(string) bool { return true },
		KernelRelease:          func() string { return "6.8.0-1-generic" },
	}
}

func TestSystemChecks(t *testing.T) {
	t.Run("RULE-PREFLIGHT-SYS-01_container_is_blocker_no_autofix", func(t *testing.T) {
		// A container can't run calibration. The Check MUST have
		// nil AutoFix because there's no safe way to "exit the
		// container" from inside it. Operator must run on host.
		p := okSystemProbes()
		p.IsContainerised = func() bool { return true }
		c := findByName(t, SystemChecks(p), "containerised_environment")
		if c.AutoFix != nil {
			t.Fatalf("containerised_environment MUST be docs-only")
		}
		tr, _ := c.Detect(context.Background())
		if !tr {
			t.Fatal("not triggered when in container")
		}
	})

	t.Run("RULE-PREFLIGHT-SYS-02_no_sudo_no_autofix", func(t *testing.T) {
		// sudoers config is too sensitive to auto-mutate. Docs-only.
		p := okSystemProbes()
		p.HaveRootOrPasswordless = func() bool { return false }
		c := findByName(t, SystemChecks(p), "no_sudo_no_root")
		if c.AutoFix != nil {
			t.Fatalf("no_sudo_no_root MUST be docs-only")
		}
	})

	t.Run("RULE-PREFLIGHT-SYS-03_lib_modules_ro_detail_names_release", func(t *testing.T) {
		// The detail string MUST include the kernel release so the
		// operator knows which path to investigate. A bare
		// "/lib/modules read-only" message would be useless on a
		// system with multiple kernels installed.
		p := okSystemProbes()
		p.LibModulesWritable = func(string) bool { return false }
		c := findByName(t, SystemChecks(p), "lib_modules_read_only")
		_, detail := c.Detect(context.Background())
		if !strings.Contains(detail, "6.8.0-1-generic") {
			t.Fatalf("detail missing kernel release: %s", detail)
		}
	})

	t.Run("RULE-PREFLIGHT-SYS-04_kernel_too_new_with_empty_max_skips", func(t *testing.T) {
		// MaxSupportedKernel="" disables the check entirely. Used
		// by the synthetic preflight invocation (legacy
		// --preflight-check) where the caller doesn't know the
		// driver-specific ceiling.
		c := KernelTooNewCheck(KernelTooNewProbes{
			KernelRelease:      func() string { return "99.99.0" },
			MaxSupportedKernel: "",
		})
		tr, _ := c.Detect(context.Background())
		if tr {
			t.Fatal("kernel_too_new triggered with empty MaxSupportedKernel")
		}
	})

	t.Run("RULE-PREFLIGHT-SYS-05_kernel_too_new_compares_dotted_versions", func(t *testing.T) {
		// 6.10.0 > 6.6.0 must trigger; 6.5.0 < 6.6.0 must not.
		// Boundary: equal versions (6.6.0 == 6.6.0) MUST NOT
		// trigger. The driver's MaxSupportedKernel is inclusive.
		cases := []struct {
			running string
			ceiling string
			want    bool
		}{
			{"6.10.0", "6.6.0", true},
			{"6.5.0", "6.6.0", false},
			{"6.6.0", "6.6.0", false},
			{"6.6.1", "6.6.0", true},
		}
		for _, c := range cases {
			ck := KernelTooNewCheck(KernelTooNewProbes{
				KernelRelease:      func() string { return c.running },
				MaxSupportedKernel: c.ceiling,
				ChipName:           "TEST",
			})
			tr, _ := ck.Detect(context.Background())
			if tr != c.want {
				t.Errorf("running=%s ceiling=%s: got %v, want %v", c.running, c.ceiling, tr, c.want)
			}
		}
	})

	t.Run("RULE-PREFLIGHT-SYS-06_port_held_uses_addr", func(t *testing.T) {
		// PortHeldCheck reads the configured listen address. An
		// empty addr disables the check. A non-empty addr that
		// fails to bind triggers the blocker.
		// We swap the live impl so we don't actually try to bind.
		orig := portFreeFn
		t.Cleanup(func() { portFreeFn = orig })

		portFreeFn = func(addr string) bool { return false }
		c := PortHeldCheck("127.0.0.1:9999")
		tr, detail := c.Detect(context.Background())
		if !tr {
			t.Fatal("port_held not triggered when bind fails")
		}
		if !strings.Contains(detail, "127.0.0.1:9999") {
			t.Fatalf("detail missing addr: %s", detail)
		}

		// Empty addr never triggers — surfaces as not-applicable
		// for installs that don't yet have a config.
		empty := PortHeldCheck("")
		tr2, _ := empty.Detect(context.Background())
		if tr2 {
			t.Fatal("empty addr triggered port_held")
		}
	})
}
