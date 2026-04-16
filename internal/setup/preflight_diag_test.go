package setup

import (
	"io"
	"log/slog"
	"testing"

	"github.com/ventd/ventd/internal/hwdiag"
	hwmonpkg "github.com/ventd/ventd/internal/hwmon"
)

// TestEmitPreflightDiag is the load-bearing test for the Tier 0.5
// preflight→hwdiag chain. The live daemon flow that fires emitPreflightDiag
// only triggers when a Super I/O chip is detected, which rules out VM-based
// end-to-end validation. This test substitutes by exercising the mapping
// directly for all four PreflightOOT.Reason values, including kernel-too-new
// with a synthetic MaxSupportedKernel ceiling (no production driver entry
// currently sets one — see HARDWARE-TODO.md).
func TestEmitPreflightDiag(t *testing.T) {
	nd := hwmonpkg.DriverNeed{
		Key:      "nct6687d",
		ChipName: "NCT6687D",
		Module:   "nct6687",
	}
	// ndCeiling carries a MaxSupportedKernel so the kernel-too-new case has a
	// realistic shape even though no real driver sets one today.
	ndCeiling := nd
	ndCeiling.MaxSupportedKernel = "6.6"

	tests := []struct {
		name         string
		need         hwmonpkg.DriverNeed
		pre          hwmonpkg.PreflightResult
		wantID       string
		wantComp     hwdiag.Component
		wantSev      hwdiag.Severity
		wantAutoFix  hwdiag.AutoFixID
		wantEndpoint string
	}{
		{
			name:         "kernel_headers_missing",
			need:         nd,
			pre:          hwmonpkg.PreflightResult{Reason: hwmonpkg.ReasonKernelHeadersMissing, Detail: "build dir missing"},
			wantID:       hwdiag.IDOOTKernelHeadersMissing,
			wantComp:     hwdiag.ComponentOOT,
			wantSev:      hwdiag.SeverityError,
			wantAutoFix:  hwdiag.AutoFixInstallKernelHdrs,
			wantEndpoint: "/api/hwdiag/install-kernel-headers",
		},
		{
			name:         "dkms_missing",
			need:         nd,
			pre:          hwmonpkg.PreflightResult{Reason: hwmonpkg.ReasonDKMSMissing, Detail: "dkms not on PATH"},
			wantID:       hwdiag.IDOOTDKMSMissing,
			wantComp:     hwdiag.ComponentOOT,
			wantSev:      hwdiag.SeverityWarn,
			wantAutoFix:  hwdiag.AutoFixInstallDKMS,
			wantEndpoint: "/api/hwdiag/install-dkms",
		},
		{
			name:         "secure_boot_blocks",
			need:         nd,
			pre:          hwmonpkg.PreflightResult{Reason: hwmonpkg.ReasonSecureBootBlocks, Detail: "SB enabled, unsigned module"},
			wantID:       hwdiag.IDOOTSecureBoot,
			wantComp:     hwdiag.ComponentSecureBoot,
			wantSev:      hwdiag.SeverityError,
			wantAutoFix:  hwdiag.AutoFixMOKEnroll,
			wantEndpoint: "/api/hwdiag/mok-enroll",
		},
		{
			name:     "kernel_too_new",
			need:     ndCeiling,
			pre:      hwmonpkg.PreflightResult{Reason: hwmonpkg.ReasonKernelTooNew, Detail: "kernel 6.10 > ceiling 6.6"},
			wantID:   hwdiag.IDOOTKernelTooNew,
			wantComp: hwdiag.ComponentOOT,
			wantSev:  hwdiag.SeverityError,
			// kernel-too-new deliberately has no remediation — there is no
			// server-side fix. The user must downgrade the kernel.
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := hwdiag.NewStore()
			m := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			m.SetDiagnosticStore(store)

			m.emitPreflightDiag(tc.need, tc.pre)

			snap := store.Snapshot(hwdiag.Filter{})
			if len(snap.Entries) != 1 {
				t.Fatalf("want exactly 1 entry, got %d", len(snap.Entries))
			}
			e := snap.Entries[0]
			if e.ID != tc.wantID {
				t.Errorf("ID = %q, want %q", e.ID, tc.wantID)
			}
			if e.Component != tc.wantComp {
				t.Errorf("Component = %q, want %q", e.Component, tc.wantComp)
			}
			if e.Severity != tc.wantSev {
				t.Errorf("Severity = %q, want %q", e.Severity, tc.wantSev)
			}
			if e.Detail != tc.pre.Detail {
				t.Errorf("Detail = %q, want %q", e.Detail, tc.pre.Detail)
			}
			if len(e.Affected) != 1 || e.Affected[0] != tc.need.Module {
				t.Errorf("Affected = %v, want [%q]", e.Affected, tc.need.Module)
			}
			if tc.wantAutoFix == "" {
				if e.Remediation != nil {
					t.Errorf("Remediation = %+v, want nil (kernel-too-new has no auto-fix)", e.Remediation)
				}
				return
			}
			if e.Remediation == nil {
				t.Fatalf("Remediation = nil, want AutoFixID=%q", tc.wantAutoFix)
			}
			if e.Remediation.AutoFixID != tc.wantAutoFix {
				t.Errorf("AutoFixID = %q, want %q", e.Remediation.AutoFixID, tc.wantAutoFix)
			}
			if e.Remediation.Endpoint != tc.wantEndpoint {
				t.Errorf("Endpoint = %q, want %q", e.Remediation.Endpoint, tc.wantEndpoint)
			}
			if e.Remediation.Label == "" {
				t.Error("Remediation.Label is empty")
			}
		})
	}
}

// TestEmitPreflightDiag_OKIsNoOp verifies that ReasonOK does not emit a
// diagnostic entry. The live run() branch only calls emitPreflightDiag on
// non-OK results, but guarding the mapping against accidental emission is
// cheap insurance.
func TestEmitPreflightDiag_OKIsNoOp(t *testing.T) {
	store := hwdiag.NewStore()
	m := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.SetDiagnosticStore(store)

	m.emitPreflightDiag(
		hwmonpkg.DriverNeed{Key: "nct6687d", ChipName: "NCT6687D", Module: "nct6687"},
		hwmonpkg.PreflightResult{Reason: hwmonpkg.ReasonOK},
	)

	if n := len(store.Snapshot(hwdiag.Filter{}).Entries); n != 0 {
		t.Fatalf("ReasonOK emitted %d entries, want 0", n)
	}
}

// TestEmitPreflightDiag_NoStoreIsNoOp verifies that when no diag store is
// attached (CLI --setup path), emitting is a silent no-op rather than a panic.
func TestEmitPreflightDiag_NoStoreIsNoOp(t *testing.T) {
	m := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// intentionally no SetDiagnosticStore
	m.emitPreflightDiag(
		hwmonpkg.DriverNeed{Key: "nct6687d", ChipName: "NCT6687D", Module: "nct6687"},
		hwmonpkg.PreflightResult{Reason: hwmonpkg.ReasonKernelHeadersMissing, Detail: "x"},
	)
}
