package recovery

import "testing"

// TestWithoutDiagnosticBundle verifies the helper strips only the generic
// diagnostic-bundle escalation card, preserving order and class-specific
// cards, without mutating the input (#1510).
func TestWithoutDiagnosticBundle(t *testing.T) {
	t.Parallel()
	in := RemediationFor(ClassMissingHeaders) // [install-kernel-headers, bundle]
	if len(in) < 2 || in[len(in)-1].ActionURL != DiagnosticBundleActionURL {
		t.Fatalf("precondition: expected catalogue ending in the bundle, got %+v", in)
	}

	out := WithoutDiagnosticBundle(in)

	for _, r := range out {
		if r.ActionURL == DiagnosticBundleActionURL {
			t.Errorf("bundle not stripped: %+v", out)
		}
	}
	if len(out) != len(in)-1 || out[0].ActionURL != "/api/hwdiag/install-kernel-headers" {
		t.Errorf("class-specific card not preserved in order: %+v", out)
	}
	// Input slice unchanged.
	if in[len(in)-1].ActionURL != DiagnosticBundleActionURL {
		t.Errorf("input slice was mutated: %+v", in)
	}

	// Unknown class collapses to empty once the bundle is removed.
	if got := WithoutDiagnosticBundle(RemediationFor(ClassUnknown)); len(got) != 0 {
		t.Errorf("unknown-class without bundle should be empty, got %+v", got)
	}
}
