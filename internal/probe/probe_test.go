package probe_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/probe/fixtures"
	"github.com/ventd/ventd/internal/state"
)

// makeExecFn builds a stub ExecFn that returns canned outputs for
// systemd-detect-virt --vm and --container, and "none" for everything else.
func makeExecFn(vmOut, containerOut string) probe.ExecFn {
	return func(_ context.Context, name string, args ...string) (string, error) {
		if name != "systemd-detect-virt" || len(args) < 1 {
			return "none", nil
		}
		switch args[0] {
		case "--vm":
			return vmOut, nil
		case "--container":
			return containerOut, nil
		}
		return "none", nil
	}
}

// stubWriteCheck returns a WriteChecker that records calls in called and
// returns writable. It never opens any real file descriptor.
func stubWriteCheck(writable bool, called *[]string) probe.WriteChecker {
	return func(sysPath string) bool {
		*called = append(*called, sysPath)
		return writable
	}
}

// openTestKV opens a fresh KVDB in t's temp directory.
func openTestKV(t *testing.T) *state.KVDB {
	t.Helper()
	st, err := state.Open(t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.KV
}

// findModuleRoot walks up from the current working directory to find the
// directory containing go.mod.
func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod from %s", dir)
		}
		dir = parent
	}
}

func TestProbe_Rules(t *testing.T) {

	// RULE-PROBE-01: Probe MUST be entirely read-only.
	// The injected WriteChecker stub is the only path that could issue
	// write-flavoured syscalls; hermetic MapFS fixtures guarantee no real
	// sysfs paths are opened. The stub records calls to confirm channel
	// detection ran.
	t.Run("RULE-PROBE-01_read_only", func(t *testing.T) {
		var checkedPaths []string
		p := probe.New(probe.Config{
			SysFS:      fixtures.SysWithThermalAndPWM(),
			ProcFS:     fixtures.ProcForBareMetal(),
			RootFS:     fixtures.BareMetalRoot(),
			ExecFn:     makeExecFn("none", "none"),
			WriteCheck: stubWriteCheck(true, &checkedPaths),
		})
		r, err := p.Probe(context.Background())
		if err != nil {
			t.Fatalf("Probe: %v", err)
		}
		if r == nil {
			t.Fatal("Probe returned nil result")
		}
		if len(checkedPaths) == 0 {
			t.Error("WriteCheck stub was never called; channel enumeration may have been skipped")
		}
		// Confirm the stub was used, not the real os.OpenFile path.
		for _, ch := range r.ControllableChannels {
			found := false
			for _, p := range checkedPaths {
				if p == ch.PWMPath {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("channel %s was not validated via WriteCheck stub", ch.PWMPath)
			}
		}
	})

	// RULE-PROBE-02: Virtualisation requires ≥3 independent sources.
	// 2 sources (DMI + /sys/hypervisor) MUST NOT set Virtualised=true.
	// 3 sources (DMI + /sys/hypervisor + systemd-detect-virt --vm) MUST set it.
	t.Run("RULE-PROBE-02_virt_requires_3_sources", func(t *testing.T) {
		virtSysFS := fstest.MapFS{
			"class/dmi/id/sys_vendor":   {Data: []byte("QEMU\n")},
			"class/dmi/id/product_name": {Data: []byte("Standard PC (Q35 + ICH9, 2009)\n")},
			"hypervisor":                {},
		}

		// 2 sources: DMI virt string + /sys/hypervisor dir. SDV says "none".
		p2 := probe.New(probe.Config{
			SysFS:      virtSysFS,
			ProcFS:     fixtures.ProcForBareMetal(),
			RootFS:     fixtures.BareMetalRoot(),
			ExecFn:     makeExecFn("none", "none"),
			WriteCheck: stubWriteCheck(false, new([]string)),
		})
		r2, _ := p2.Probe(context.Background())
		if r2.RuntimeEnvironment.Virtualised {
			t.Error("2 virt sources should not be sufficient: Virtualised must be false")
		}

		// 3 sources: same DMI + hypervisor + SDV --vm = "kvm".
		p3 := probe.New(probe.Config{
			SysFS:      virtSysFS,
			ProcFS:     fixtures.ProcForBareMetal(),
			RootFS:     fixtures.BareMetalRoot(),
			ExecFn:     makeExecFn("kvm", "none"),
			WriteCheck: stubWriteCheck(false, new([]string)),
		})
		r3, _ := p3.Probe(context.Background())
		if !r3.RuntimeEnvironment.Virtualised {
			t.Error("3 virt sources must set Virtualised=true")
		}
	})

	// RULE-PROBE-03: Containerisation requires ≥2 independent sources.
	// 1 source (/.dockerenv only) MUST NOT set Containerised=true.
	// 2 sources (/.dockerenv + /proc/1/cgroup:docker) MUST set it.
	// 2 sources (/.dockerenv + overlay root in /proc/mounts) MUST set it
	//   (cgroup v2 path where /proc/1/cgroup has no docker keyword).
	t.Run("RULE-PROBE-03_container_requires_2_sources", func(t *testing.T) {
		dockerenvOnly := fstest.MapFS{
			".dockerenv": {},
		}
		noCgroup := fstest.MapFS{"1/cgroup": {Data: []byte("0::/init.scope\n")}}
		dockerCgroup := fstest.MapFS{"1/cgroup": {Data: []byte("12:memory:/docker/abc123\n")}}

		// 1 source: only /.dockerenv; cgroup has no docker keyword; no overlay mounts.
		p1 := probe.New(probe.Config{
			SysFS:      fixtures.SysWithThermalAndPWM(),
			ProcFS:     noCgroup,
			RootFS:     dockerenvOnly,
			ExecFn:     makeExecFn("none", "none"),
			WriteCheck: stubWriteCheck(false, new([]string)),
		})
		r1, _ := p1.Probe(context.Background())
		if r1.RuntimeEnvironment.Containerised {
			t.Error("1 container source should not be sufficient: Containerised must be false")
		}

		// 2 sources (cgroup v1 path): /.dockerenv + /proc/1/cgroup mentions docker.
		p2 := probe.New(probe.Config{
			SysFS:      fixtures.SysWithThermalAndPWM(),
			ProcFS:     dockerCgroup,
			RootFS:     dockerenvOnly,
			ExecFn:     makeExecFn("none", "none"),
			WriteCheck: stubWriteCheck(false, new([]string)),
		})
		r2, _ := p2.Probe(context.Background())
		if !r2.RuntimeEnvironment.Containerised {
			t.Error("cgroup v1: 2 container sources must set Containerised=true")
		}

		// 2 sources (cgroup v2 path): /.dockerenv + overlay root in /proc/mounts.
		// On Ubuntu 22.04+ / Debian 12+, /proc/1/cgroup only has "0::/" (no docker
		// keyword), but Docker containers always have an overlay root filesystem.
		p3 := probe.New(probe.Config{
			SysFS:      fixtures.SysWithThermalAndPWM(),
			ProcFS:     fixtures.ProcForDockerCgroupV2(),
			RootFS:     dockerenvOnly,
			ExecFn:     makeExecFn("none", "none"),
			WriteCheck: stubWriteCheck(false, new([]string)),
		})
		r3, _ := p3.Probe(context.Background())
		if !r3.RuntimeEnvironment.Containerised {
			t.Error("cgroup v2: /.dockerenv + overlay root must set Containerised=true")
		}
	})

	// RULE-PROBE-04: ClassifyOutcome follows the §3.2 algorithm exactly.
	// virt → refuse; container → refuse; no sensors → refuse;
	// sensors only → monitor_only; sensors + channels → control.
	t.Run("RULE-PROBE-04_classify_outcome", func(t *testing.T) {
		cases := []struct {
			name    string
			result  *probe.ProbeResult
			wantOut probe.Outcome
		}{
			{
				name: "virt_refuses",
				result: &probe.ProbeResult{
					RuntimeEnvironment:   probe.RuntimeEnvironment{Virtualised: true},
					ThermalSources:       []probe.ThermalSource{{SourceID: "hwmon0"}},
					ControllableChannels: []probe.ControllableChannel{{PWMPath: "/sys/class/hwmon/hwmon0/pwm1"}},
				},
				wantOut: probe.OutcomeRefuse,
			},
			{
				name: "container_refuses",
				result: &probe.ProbeResult{
					RuntimeEnvironment:   probe.RuntimeEnvironment{Containerised: true},
					ThermalSources:       []probe.ThermalSource{{SourceID: "hwmon0"}},
					ControllableChannels: []probe.ControllableChannel{{PWMPath: "/sys/class/hwmon/hwmon0/pwm1"}},
				},
				wantOut: probe.OutcomeRefuse,
			},
			{
				name:    "no_sensors_refuses",
				result:  &probe.ProbeResult{},
				wantOut: probe.OutcomeRefuse,
			},
			{
				name: "sensors_no_channels_monitor_only",
				result: &probe.ProbeResult{
					ThermalSources: []probe.ThermalSource{{SourceID: "hwmon0"}},
				},
				wantOut: probe.OutcomeMonitorOnly,
			},
			{
				name: "sensors_and_channels_control",
				result: &probe.ProbeResult{
					ThermalSources:       []probe.ThermalSource{{SourceID: "hwmon0"}},
					ControllableChannels: []probe.ControllableChannel{{PWMPath: "/sys/class/hwmon/hwmon0/pwm1"}},
				},
				wantOut: probe.OutcomeControl,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := probe.ClassifyOutcome(tc.result)
				if got != tc.wantOut {
					t.Errorf("ClassifyOutcome: got %s, want %s", got, tc.wantOut)
				}
			})
		}
	})

	// RULE-PROBE-05: No downstream code branches on CatalogMatch==nil vs non-nil.
	// ClassifyOutcome and the Outcome string must be identical for two ProbeResults
	// that differ only in whether CatalogMatch is nil.
	t.Run("RULE-PROBE-05_channels_uniform_regardless_of_catalog_match", func(t *testing.T) {
		channels := []probe.ControllableChannel{{PWMPath: "/sys/class/hwmon/hwmon0/pwm1"}}
		thermals := []probe.ThermalSource{{SourceID: "hwmon0"}}

		rNilMatch := &probe.ProbeResult{
			ThermalSources:       thermals,
			ControllableChannels: channels,
			CatalogMatch:         nil,
		}
		rNonNilMatch := &probe.ProbeResult{
			ThermalSources:       thermals,
			ControllableChannels: channels,
			CatalogMatch:         &probe.CatalogMatch{Matched: true, Fingerprint: "abc123def456"},
		}

		outNil := probe.ClassifyOutcome(rNilMatch)
		outNonNil := probe.ClassifyOutcome(rNonNilMatch)
		if outNil != outNonNil {
			t.Errorf("outcome differs: nil CatalogMatch=%s, non-nil=%s", outNil, outNonNil)
		}
		if outNil != probe.OutcomeControl {
			t.Errorf("want OutcomeControl, got %s", outNil)
		}
		if len(rNilMatch.ControllableChannels) != len(rNonNilMatch.ControllableChannels) {
			t.Error("ControllableChannels count must be independent of CatalogMatch presence")
		}
	})

	// RULE-PROBE-06: All ControllableChannel.Polarity == "unknown" in v0.5.1.
	// Polarity is deferred to the calibration probe in v0.5.2.
	t.Run("RULE-PROBE-06_polarity_always_unknown", func(t *testing.T) {
		var checked []string
		p := probe.New(probe.Config{
			SysFS:      fixtures.SysWithThermalAndPWM(),
			ProcFS:     fixtures.ProcForBareMetal(),
			RootFS:     fixtures.BareMetalRoot(),
			ExecFn:     makeExecFn("none", "none"),
			WriteCheck: stubWriteCheck(true, &checked),
		})
		r, err := p.Probe(context.Background())
		if err != nil {
			t.Fatalf("Probe: %v", err)
		}
		if len(r.ControllableChannels) == 0 {
			t.Fatal("no controllable channels; SysWithThermalAndPWM fixture may be broken")
		}
		for _, ch := range r.ControllableChannels {
			if ch.Polarity != "unknown" {
				t.Errorf("channel %s: Polarity=%q, want \"unknown\"", ch.PWMPath, ch.Polarity)
			}
		}
	})

	// RULE-PROBE-07: PersistOutcome writes the expected KV keys to both namespaces.
	t.Run("RULE-PROBE-07_persist_outcome_writes_kv_keys", func(t *testing.T) {
		db := openTestKV(t)
		r := &probe.ProbeResult{
			SchemaVersion:  probe.SchemaVersion,
			ThermalSources: []probe.ThermalSource{{SourceID: "hwmon0"}},
			ControllableChannels: []probe.ControllableChannel{
				{PWMPath: "/sys/class/hwmon/hwmon0/pwm1"},
			},
		}
		if err := probe.PersistOutcome(db, r); err != nil {
			t.Fatalf("PersistOutcome: %v", err)
		}

		mustExist := func(ns, key string) {
			t.Helper()
			v, ok, err := db.Get(ns, key)
			if err != nil {
				t.Fatalf("db.Get(%s, %s): %v", ns, key, err)
			}
			if !ok {
				t.Errorf("key %s.%s absent after PersistOutcome", ns, key)
			}
			if v == nil {
				t.Errorf("key %s.%s is nil", ns, key)
			}
		}
		mustExist("probe", "schema_version")
		mustExist("probe", "last_run")
		mustExist("probe", "result")
		mustExist("wizard", "initial_outcome")
		mustExist("wizard", "outcome_reason")
		mustExist("wizard", "outcome_timestamp")
	})

	// RULE-PROBE-08: LoadWizardOutcome returns the correct Outcome after PersistOutcome.
	t.Run("RULE-PROBE-08_load_wizard_outcome", func(t *testing.T) {
		cases := []struct {
			name    string
			result  *probe.ProbeResult
			wantOut probe.Outcome
		}{
			{
				name: "control",
				result: &probe.ProbeResult{
					ThermalSources:       []probe.ThermalSource{{SourceID: "hwmon0"}},
					ControllableChannels: []probe.ControllableChannel{{PWMPath: "/sys/pwm1"}},
				},
				wantOut: probe.OutcomeControl,
			},
			{
				name: "monitor_only",
				result: &probe.ProbeResult{
					ThermalSources: []probe.ThermalSource{{SourceID: "hwmon0"}},
				},
				wantOut: probe.OutcomeMonitorOnly,
			},
			{
				name:    "refused",
				result:  &probe.ProbeResult{RuntimeEnvironment: probe.RuntimeEnvironment{Virtualised: true}},
				wantOut: probe.OutcomeRefuse,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				db := openTestKV(t)
				if err := probe.PersistOutcome(db, tc.result); err != nil {
					t.Fatalf("PersistOutcome: %v", err)
				}
				got, ok, err := probe.LoadWizardOutcome(db)
				if err != nil {
					t.Fatalf("LoadWizardOutcome: %v", err)
				}
				if !ok {
					t.Fatal("LoadWizardOutcome: ok=false after PersistOutcome")
				}
				if got != tc.wantOut {
					t.Errorf("LoadWizardOutcome: got %s, want %s", got, tc.wantOut)
				}
			})
		}
	})

	// RULE-PROBE-09: WipeNamespaces atomically empties both wizard and probe namespaces.
	// After WipeNamespaces, db.List must return empty maps for both namespaces.
	t.Run("RULE-PROBE-09_wipe_namespaces_empties_both", func(t *testing.T) {
		db := openTestKV(t)
		r := &probe.ProbeResult{
			ThermalSources: []probe.ThermalSource{{SourceID: "hwmon0"}},
		}
		if err := probe.PersistOutcome(db, r); err != nil {
			t.Fatalf("PersistOutcome: %v", err)
		}

		// Verify keys are present before wipe.
		for _, ns := range []string{"probe", "wizard"} {
			keys, err := db.List(ns)
			if err != nil {
				t.Fatalf("db.List(%s): %v", ns, err)
			}
			if len(keys) == 0 {
				t.Errorf("namespace %q is empty before WipeNamespaces; PersistOutcome may have failed", ns)
			}
		}

		if err := probe.WipeNamespaces(db); err != nil {
			t.Fatalf("WipeNamespaces: %v", err)
		}

		// Both namespaces must be empty after wipe.
		for _, ns := range []string{"probe", "wizard"} {
			keys, err := db.List(ns)
			if err != nil {
				t.Fatalf("db.List(%s): %v", ns, err)
			}
			if len(keys) != 0 {
				t.Errorf("namespace %q still has %d key(s) after WipeNamespaces", ns, len(keys))
			}
		}

		// LoadWizardOutcome must return ok=false after wipe.
		_, ok, err := probe.LoadWizardOutcome(db)
		if err != nil {
			t.Fatalf("LoadWizardOutcome after wipe: %v", err)
		}
		if ok {
			t.Error("LoadWizardOutcome returned ok=true after WipeNamespaces")
		}
	})

	// RULE-PROBE-10: internal/hwdb/bios_known_bad.go MUST NOT exist.
	// A per-board BIOS-version denylist in hwdb creates a false sense of
	// security and requires constant maintenance; probe uses catalog overlay
	// and precondition checks instead.
	t.Run("RULE-PROBE-10_no_bios_known_bad_file", func(t *testing.T) {
		root := findModuleRoot(t)
		bad := filepath.Join(root, "internal", "hwdb", "bios_known_bad.go")
		if _, err := os.Stat(bad); err == nil {
			t.Errorf("internal/hwdb/bios_known_bad.go must not exist (RULE-PROBE-10)")
		}
	})
}
