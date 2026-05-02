package grub

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempDropIn redirects DropInPath to a tempfile for the duration
// of t. The const DropInPath is replaced via a package-level var
// stub so tests can swap it; production keeps the constant.
func withTempDropIn(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := DropInPath
	t.Cleanup(func() { setDropInPath(orig) })
	p := filepath.Join(dir, "grub.d", "ventd.cfg")
	setDropInPath(p)
	return p
}

// Each subtest binds 1:1 to a RULE-GRUB-* invariant.

func TestGrubCmdline(t *testing.T) {
	t.Run("RULE-GRUB-01_first_call_writes_drop_in_and_invokes_regen", func(t *testing.T) {
		// First AddParam on a clean system MUST create the drop-in
		// AND invoke the regenerator. Skipping regen would leave
		// the change un-applied at next boot.
		path := withTempDropIn(t)
		regenCalled := 0
		err := AddParam("acpi_enforce_resources=lax", func() error { regenCalled++; return nil })
		if err != nil {
			t.Fatalf("AddParam: %v", err)
		}
		if regenCalled != 1 {
			t.Fatalf("regenerator: got %d calls, want 1", regenCalled)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read drop-in: %v", err)
		}
		if !strings.Contains(string(body), "acpi_enforce_resources=lax") {
			t.Fatalf("drop-in missing param:\n%s", body)
		}
		if !strings.Contains(string(body), "$GRUB_CMDLINE_LINUX_DEFAULT") {
			t.Fatalf("drop-in not stacking on existing default:\n%s", body)
		}
	})

	t.Run("RULE-GRUB-02_second_call_with_same_param_is_noop", func(t *testing.T) {
		// Idempotent: calling AddParam twice with the same param
		// MUST NOT rewrite the file or invoke the regenerator on
		// the second call. Operators retry from the wizard often;
		// triggering update-grub repeatedly would slow the install
		// and create flap in the drop-in's mtime.
		_ = withTempDropIn(t)
		regenCalled := 0
		regen := func() error { regenCalled++; return nil }
		if err := AddParam("acpi_enforce_resources=lax", regen); err != nil {
			t.Fatalf("first AddParam: %v", err)
		}
		if err := AddParam("acpi_enforce_resources=lax", regen); err != nil {
			t.Fatalf("second AddParam: %v", err)
		}
		if regenCalled != 1 {
			t.Fatalf("regenerator: got %d calls, want 1 (second call must be no-op)", regenCalled)
		}
	})

	t.Run("RULE-GRUB-03_different_param_appends_not_overwrites", func(t *testing.T) {
		// Adding a second, distinct param MUST append it to the
		// existing list, not overwrite the first. The drop-in's
		// param list is the union of all AddParam calls.
		path := withTempDropIn(t)
		_ = AddParam("acpi_enforce_resources=lax", func() error { return nil })
		_ = AddParam("intel_iommu=on", func() error { return nil })
		body, _ := os.ReadFile(path)
		s := string(body)
		if !strings.Contains(s, "acpi_enforce_resources=lax") {
			t.Fatalf("first param missing after second add:\n%s", s)
		}
		if !strings.Contains(s, "intel_iommu=on") {
			t.Fatalf("second param missing:\n%s", s)
		}
	})

	t.Run("RULE-GRUB-04_invalid_param_rejected", func(t *testing.T) {
		// Shell-special characters MUST be refused. The drop-in is
		// sourced by /etc/default/grub, so a param containing `;`
		// or backticks could execute arbitrary commands at boot.
		// validParam is the gate; AddParam returns an error
		// without writing.
		_ = withTempDropIn(t)
		bad := []string{
			"foo;rm -rf /",
			"`whoami`",
			"a\"b",
			"a'b",
			"$(touch x)",
			strings.Repeat("a", 65), // length cap
			"",                      // empty
		}
		for _, p := range bad {
			err := AddParam(p, func() error { return nil })
			if err == nil {
				t.Errorf("AddParam(%q) returned nil; want error", p)
			}
		}
	})

	t.Run("RULE-GRUB-05_regen_failure_propagates", func(t *testing.T) {
		// If the regenerator returns an error, AddParam MUST
		// propagate it so the caller (HTTP handler) returns a
		// meaningful failure to the operator. The drop-in is left
		// written — re-running AddParam will see it and skip the
		// idempotent path, but the next regen attempt will run.
		_ = withTempDropIn(t)
		want := errors.New("regen kaboom")
		got := AddParam("acpi_enforce_resources=lax", func() error { return want })
		if !errors.Is(got, want) {
			t.Fatalf("got %v, want wrapped %v", got, want)
		}
	})

	t.Run("RULE-GRUB-06_HasParam_reports_presence", func(t *testing.T) {
		// HasParam is the short-circuit for callers that want to
		// surface "already done — just reboot" rather than
		// re-invoking the writer. Returns false when drop-in is
		// absent; true when present and contains param; false when
		// present but missing param.
		_ = withTempDropIn(t)
		if HasParam("acpi_enforce_resources=lax") {
			t.Fatal("HasParam true with no drop-in")
		}
		_ = AddParam("acpi_enforce_resources=lax", func() error { return nil })
		if !HasParam("acpi_enforce_resources=lax") {
			t.Fatal("HasParam false after AddParam")
		}
		if HasParam("intel_iommu=on") {
			t.Fatal("HasParam true for unadded param")
		}
	})
}

// setDropInPath swaps the package-level var so tests can redirect
// the on-disk path. Production code uses the const DropInPath
// indirectly through the package-level var dropInPath.
func setDropInPath(p string) { dropInPath = p }
