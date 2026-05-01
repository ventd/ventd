package smartmode_test

// Cross-spec integration test for spec-smart-mode.md §16 success criterion #9:
//
//   "No code path branches on 'catalog matched vs not' downstream of the
//    probe layer."
//
// RULE-PROBE-05 unit-tests this for ClassifyOutcome with synthetic
// ProbeResult values. This test drives the actual Prober end-to-end with
// identical hwmon enumeration but two different DMI fixtures — one that
// matches a catalog board profile, one that does not — and asserts that
// downstream behaviour is equivalent.
//
// "Equivalent" means: same Outcome, same number of ThermalSources, same
// number of ControllableChannels, same set of PWMPaths. The CatalogMatch
// field and the CapabilityHint annotation are *expected* to differ; that
// is the catalog overlay's only legitimate output. A regression that
// added or removed a channel based on catalog state would fail this test.

import (
	"context"
	"sort"
	"testing"
	"testing/fstest"

	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/probe/fixtures"
)

// asrockDMIRootFS produces a synthetic / RootFS that matches the
// ASRock X670E Taichi catalog board profile. Combined with hwmon=nct6796,
// the catalog overlay should populate CatalogMatch and annotate channels.
func asrockDMIRootFS() fstest.MapFS {
	return fstest.MapFS{
		"sys/class/dmi/id/sys_vendor":    {Data: []byte("ASRock\n")},
		"sys/class/dmi/id/product_name":  {Data: []byte("X670E Taichi\n")},
		"sys/class/dmi/id/board_vendor":  {Data: []byte("ASRock\n")},
		"sys/class/dmi/id/board_name":    {Data: []byte("X670E Taichi\n")},
		"sys/class/dmi/id/board_version": {Data: []byte("\n")},
		"sys/class/dmi/id/bios_version":  {Data: []byte("9.99\n")},
		"proc/cpuinfo":                   {Data: []byte("processor\t: 0\nmodel name\t: AMD Ryzen 9 7950X\n\n")},
	}
}

// unmatchedDMIRootFS produces a synthetic / RootFS whose vendor/board
// strings appear in no shipped catalog entry. Combined with the same
// hwmon enumeration, the overlay must skip the match path silently.
func unmatchedDMIRootFS() fstest.MapFS {
	return fstest.MapFS{
		"sys/class/dmi/id/sys_vendor":    {Data: []byte("UnknownVendorCorp\n")},
		"sys/class/dmi/id/product_name":  {Data: []byte("Imaginary 9000\n")},
		"sys/class/dmi/id/board_vendor":  {Data: []byte("UnknownVendorCorp\n")},
		"sys/class/dmi/id/board_name":    {Data: []byte("UnknownBoard XYZ\n")},
		"sys/class/dmi/id/board_version": {Data: []byte("\n")},
		"sys/class/dmi/id/bios_version":  {Data: []byte("0.0\n")},
		"proc/cpuinfo":                   {Data: []byte("processor\t: 0\nmodel name\t: Generic CPU\n\n")},
	}
}

// runProbeForCatalog runs Probe with the given RootFS (vendor varies). The
// hwmon SysFS, ProcFS, and ExecFn are identical across both calls so that
// any non-overlay difference in the result is a §16 #9 regression.
func runProbeForCatalog(t *testing.T, root fstest.MapFS) *probe.ProbeResult {
	t.Helper()
	p := probe.New(probe.Config{
		SysFS:      fixtures.SysWithThermalAndPWM(),
		ProcFS:     fixtures.ProcForBareMetal(),
		RootFS:     root,
		ExecFn:     makeExecFn("none", "none"),
		WriteCheck: stubWriteCheck(true),
	})
	r, err := p.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	return r
}

// pwmPaths returns the sorted set of PWMPaths present on a result. Used as
// the set-equality oracle for §16 #9: catalog state must not change which
// channels are visible.
func pwmPaths(r *probe.ProbeResult) []string {
	paths := make([]string, 0, len(r.ControllableChannels))
	for _, ch := range r.ControllableChannels {
		paths = append(paths, ch.PWMPath)
	}
	sort.Strings(paths)
	return paths
}

// thermalIDs returns the sorted set of thermal source IDs present on a
// result. Catalog overlay must not add or remove sensors.
func thermalIDs(r *probe.ProbeResult) []string {
	ids := make([]string, 0, len(r.ThermalSources))
	for _, ts := range r.ThermalSources {
		ids = append(ids, ts.SourceID)
	}
	sort.Strings(ids)
	return ids
}

// equalStringSlices reports whether two sorted slices are byte-equal.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSmartmode_NoCatalogBranching_DownstreamUniform asserts that two probe
// runs against identical hwmon inputs but different DMI fixtures produce:
//
//   - the same Outcome from ClassifyOutcome,
//   - the same set of ControllableChannel PWMPaths,
//   - the same set of ThermalSource IDs,
//   - the same RuntimeEnvironment flags,
//
// regardless of whether one call matched a catalog board profile and the
// other did not. RULE-PROBE-05 covers this for ClassifyOutcome alone; this
// test extends to the full ProbeResult shape so a regression that added a
// channel only when the catalog matched would fail here.
func TestSmartmode_NoCatalogBranching_DownstreamUniform(t *testing.T) {
	matched := runProbeForCatalog(t, asrockDMIRootFS())
	missed := runProbeForCatalog(t, unmatchedDMIRootFS())

	// Sanity gate: ensure the test fixtures actually exercise the two
	// different code paths. Without this, a refactor that broke the
	// catalog overlay entirely could produce identical results for
	// unrelated reasons and silently pass.
	matchPresent := matched.CatalogMatch != nil && matched.CatalogMatch.Matched
	missedNil := missed.CatalogMatch == nil || !missed.CatalogMatch.Matched
	if !matchPresent {
		t.Fatalf("setup error: matched-DMI fixture did not produce CatalogMatch=true; overlay or fixture is broken (got %+v)", matched.CatalogMatch)
	}
	if !missedNil {
		t.Fatalf("setup error: missed-DMI fixture unexpectedly produced a catalog match: %+v", missed.CatalogMatch)
	}

	// (a) Outcome is invariant.
	if got, want := probe.ClassifyOutcome(matched), probe.ClassifyOutcome(missed); got != want {
		t.Errorf("ClassifyOutcome differs: matched=%v missed=%v — §16 #9 violated", got, want)
	}

	// (b) Channel set is invariant.
	mPaths, nPaths := pwmPaths(matched), pwmPaths(missed)
	if !equalStringSlices(mPaths, nPaths) {
		t.Errorf("ControllableChannel PWMPaths differ across catalog states\n  matched: %v\n  missed:  %v", mPaths, nPaths)
	}
	if got, want := len(matched.ControllableChannels), len(missed.ControllableChannels); got != want {
		t.Errorf("ControllableChannels count differs: matched=%d missed=%d", got, want)
	}

	// (c) Thermal source set is invariant.
	mIDs, nIDs := thermalIDs(matched), thermalIDs(missed)
	if !equalStringSlices(mIDs, nIDs) {
		t.Errorf("ThermalSource IDs differ across catalog states\n  matched: %v\n  missed:  %v", mIDs, nIDs)
	}

	// (d) Runtime environment is invariant — virt/container detection runs
	// before the overlay and must not depend on it.
	if matched.RuntimeEnvironment.Virtualised != missed.RuntimeEnvironment.Virtualised {
		t.Errorf("Virtualised flag differs across catalog states: matched=%v missed=%v",
			matched.RuntimeEnvironment.Virtualised,
			missed.RuntimeEnvironment.Virtualised)
	}
	if matched.RuntimeEnvironment.Containerised != missed.RuntimeEnvironment.Containerised {
		t.Errorf("Containerised flag differs across catalog states: matched=%v missed=%v",
			matched.RuntimeEnvironment.Containerised,
			missed.RuntimeEnvironment.Containerised)
	}

	// (e) Per-channel polarity defaults to "unknown" regardless of catalog
	// state (RULE-PROBE-06). Catalog cannot promote a channel to "normal"
	// without the polarity probe (spec-v0_5_2) running.
	for i := range matched.ControllableChannels {
		if matched.ControllableChannels[i].Polarity != missed.ControllableChannels[i].Polarity {
			t.Errorf("channel %d polarity differs: matched=%q missed=%q",
				i,
				matched.ControllableChannels[i].Polarity,
				missed.ControllableChannels[i].Polarity)
		}
	}
}
