package setup

import (
	"io"
	"log/slog"
	"testing"

	"github.com/ventd/ventd/internal/hwdiag"
	hwmonpkg "github.com/ventd/ventd/internal/hwmon"
)

// TestEmitDMICandidates is the load-bearing unit for Tier 3's DMI pathway.
// On the canary hosts the capability-first pass always finds controllable
// fans, so the DMI emitter never fires in production today. This test
// exercises the mapping directly.
func TestEmitDMICandidates(t *testing.T) {
	tests := []struct {
		name       string
		info       hwmonpkg.DMIInfo
		wantIDs    []string // IDs expected after emission
		wantAutoFx hwdiag.AutoFixID
	}{
		{
			name:       "MSI MAG → propose nct6687d",
			info:       hwmonpkg.DMIInfo{BoardVendor: "micro-star international co., ltd.", BoardName: "mag b550 tomahawk"},
			wantIDs:    []string{hwdiag.IDDMICandidatePrefix + "nct6687d"},
			wantAutoFx: hwdiag.AutoFixTryModuleLoad,
		},
		{
			name:       "Gigabyte → propose it8688e",
			info:       hwmonpkg.DMIInfo{BoardVendor: "gigabyte technology co., ltd.", BoardName: "b550 aorus elite"},
			wantIDs:    []string{hwdiag.IDDMICandidatePrefix + "it8688e"},
			wantAutoFx: hwdiag.AutoFixTryModuleLoad,
		},
		{
			name:    "no DMI match → single no_match entry",
			info:    hwmonpkg.DMIInfo{BoardVendor: "asustek computer inc.", BoardName: "prime x570-p"},
			wantIDs: []string{hwdiag.IDDMINoMatch},
		},
		{
			name:    "empty DMI → no_match with empty context",
			info:    hwmonpkg.DMIInfo{},
			wantIDs: []string{hwdiag.IDDMINoMatch},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := hwdiag.NewStore()
			m := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			m.SetDiagnosticStore(store)

			m.emitDMICandidatesFor(tc.info)

			snap := store.Snapshot(hwdiag.Filter{Component: hwdiag.ComponentDMI})
			gotIDs := make([]string, len(snap.Entries))
			for i, e := range snap.Entries {
				gotIDs[i] = e.ID
			}
			if len(gotIDs) != len(tc.wantIDs) {
				t.Fatalf("IDs = %v, want %v", gotIDs, tc.wantIDs)
			}
			for i, want := range tc.wantIDs {
				if gotIDs[i] != want {
					t.Errorf("IDs[%d] = %q, want %q", i, gotIDs[i], want)
				}
			}
			if tc.wantAutoFx != "" {
				e := snap.Entries[0]
				if e.Remediation == nil || e.Remediation.AutoFixID != tc.wantAutoFx {
					t.Errorf("Remediation = %+v, want AutoFixID=%q", e.Remediation, tc.wantAutoFx)
				}
				if e.Remediation.Endpoint != "" {
					t.Errorf("Endpoint = %q, want empty (UI wiring deferred)", e.Remediation.Endpoint)
				}
			}
		})
	}
}

// TestEmitDMICandidates_Idempotent verifies re-emission replaces the entries
// instead of accumulating duplicates — critical because setup can re-run.
func TestEmitDMICandidates_Idempotent(t *testing.T) {
	store := hwdiag.NewStore()
	m := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.SetDiagnosticStore(store)

	info := hwmonpkg.DMIInfo{BoardVendor: "gigabyte technology co., ltd."}
	m.emitDMICandidatesFor(info)
	m.emitDMICandidatesFor(info)

	snap := store.Snapshot(hwdiag.Filter{Component: hwdiag.ComponentDMI})
	if len(snap.Entries) != 1 {
		t.Errorf("after two emissions got %d entries, want 1", len(snap.Entries))
	}
}

// TestEmitDMICandidates_ClearsStaleNoMatch verifies that a successful match
// overwrites an earlier no_match entry in the same store.
func TestEmitDMICandidates_ClearsStaleNoMatch(t *testing.T) {
	store := hwdiag.NewStore()
	m := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.SetDiagnosticStore(store)

	// First run: unknown DMI → no_match.
	m.emitDMICandidatesFor(hwmonpkg.DMIInfo{BoardVendor: "unknown"})
	// Second run: matching DMI — no_match should be gone, candidate present.
	m.emitDMICandidatesFor(hwmonpkg.DMIInfo{BoardVendor: "gigabyte technology co., ltd."})

	snap := store.Snapshot(hwdiag.Filter{Component: hwdiag.ComponentDMI})
	if len(snap.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(snap.Entries))
	}
	if snap.Entries[0].ID != hwdiag.IDDMICandidatePrefix+"it8688e" {
		t.Errorf("stale no_match not cleared: got %q", snap.Entries[0].ID)
	}
}

// TestEmitDMICandidates_NilStore is a safety check — emitter must not panic
// if called before SetDiagnosticStore (current main.go wires it, but the
// contract should tolerate a nil store).
func TestEmitDMICandidates_NilStore(t *testing.T) {
	m := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.emitDMICandidatesFor(hwmonpkg.DMIInfo{BoardVendor: "gigabyte"})
}
