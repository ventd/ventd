package setup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/hwdiag"
)

// stubModprobeOK returns a modprobeCmd stub that records how many times it
// was invoked. Tests wire it via SetModprobeCmd + t.Cleanup.
func stubModprobeOK(t *testing.T) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	prev := SetModprobeCmd(func(ctx context.Context, mod string) ([]byte, error) {
		calls.Add(1)
		return []byte("modprobe succeeded\n"), nil
	})
	t.Cleanup(func() { SetModprobeCmd(prev) })
	return &calls
}

// stubModprobeFail returns a stub that fails every call with err. Useful
// for asserting the persistence file is NOT written on modprobe failure.
func stubModprobeFail(t *testing.T, err error) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	prev := SetModprobeCmd(func(ctx context.Context, mod string) ([]byte, error) {
		calls.Add(1)
		return []byte("modprobe: FATAL: Module " + mod + " not found.\n"), err
	})
	t.Cleanup(func() { SetModprobeCmd(prev) })
	return &calls
}

// withTempModulesLoadDir swaps modulesLoadDir to a t.TempDir() and restores
// it on cleanup. Returns the tempdir path for file-content assertions. The
// default modulesLoadWrite (os.WriteFile) is left in place so the tests
// actually exercise the real write path — just into a writable tempdir
// rather than /etc.
func withTempModulesLoadDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := SetModulesLoadDir(dir)
	t.Cleanup(func() { SetModulesLoadDir(prev) })
	return dir
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestLoadModule_AllowlistRejects(t *testing.T) {
	cases := []string{
		"evilmod",
		"iwlwifi",
		"COPY_OF_CORETEMP", // uppercase fails the regex even though allowlist lookup would miss
	}
	for _, mod := range cases {
		t.Run(mod, func(t *testing.T) {
			calls := stubModprobeOK(t)
			withTempModulesLoadDir(t)
			m := newTestManager(t)

			_, err := m.LoadModule(context.Background(), mod)
			if err == nil {
				t.Fatalf("LoadModule(%q) succeeded, want rejection", mod)
			}
			if calls.Load() != 0 {
				t.Fatalf("modprobe was invoked %d times for rejected module", calls.Load())
			}
		})
	}
}

func TestLoadModule_ShellMetacharRejection(t *testing.T) {
	cases := []string{
		"coretemp; rm -rf /",
		"coretemp\nkloadme",
		"../../etc/passwd",
		"core temp",
		"coretemp$(whoami)",
		"",
	}
	for _, mod := range cases {
		t.Run(mod, func(t *testing.T) {
			calls := stubModprobeOK(t)
			withTempModulesLoadDir(t)
			m := newTestManager(t)

			_, err := m.LoadModule(context.Background(), mod)
			if err == nil {
				t.Fatalf("LoadModule(%q) succeeded, want rejection", mod)
			}
			if calls.Load() != 0 {
				t.Fatalf("modprobe invoked with metachar input %q", mod)
			}
		})
	}
}

func TestLoadModule_Success_WritesPersistenceFile(t *testing.T) {
	calls := stubModprobeOK(t)
	dir := withTempModulesLoadDir(t)
	m := newTestManager(t)

	log, err := m.LoadModule(context.Background(), "coretemp")
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("modprobe called %d times, want 1", calls.Load())
	}

	want := filepath.Join(dir, "ventd-coretemp.conf")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read persistence file: %v", err)
	}
	if got := string(data); got != "coretemp\n" {
		t.Errorf("persistence file = %q, want \"coretemp\\n\"", got)
	}
	fi, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat persistence file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0644 {
		t.Errorf("persistence file perm = %o, want 0644", perm)
	}

	// Log should mention both the load and the persistence file path.
	joined := strings.Join(log, "\n")
	if !strings.Contains(joined, "coretemp") {
		t.Errorf("log missing module name: %q", joined)
	}
	if !strings.Contains(joined, want) {
		t.Errorf("log missing persistence path: %q", joined)
	}
}

func TestLoadModule_ModprobeFailure_NoPersistence(t *testing.T) {
	stubModprobeFail(t, errors.New("exit status 1"))
	dir := withTempModulesLoadDir(t)
	m := newTestManager(t)

	_, err := m.LoadModule(context.Background(), "nct6687")
	if err == nil {
		t.Fatal("LoadModule succeeded, want modprobe error")
	}
	if !strings.Contains(err.Error(), "nct6687") {
		t.Errorf("error %q missing module name", err.Error())
	}

	// Critical: the persistence file must NOT exist when modprobe failed.
	// Otherwise an unloadable module becomes a boot-time loop of failures.
	want := filepath.Join(dir, "ventd-nct6687.conf")
	if _, statErr := os.Stat(want); statErr == nil {
		t.Errorf("persistence file %s exists after modprobe failure", want)
	} else if !os.IsNotExist(statErr) {
		t.Errorf("unexpected stat error: %v", statErr)
	}
}

func TestLoadModule_IdempotentPersistence(t *testing.T) {
	stubModprobeOK(t)
	dir := withTempModulesLoadDir(t)
	m := newTestManager(t)

	// Pre-seed a stale value; the write should overwrite it rather than
	// appending or erroring.
	confPath := filepath.Join(dir, "ventd-coretemp.conf")
	if err := os.WriteFile(confPath, []byte("stale-content\n"), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := m.LoadModule(context.Background(), "coretemp"); err != nil {
			t.Fatalf("LoadModule iter=%d: %v", i, err)
		}
	}
	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(data); got != "coretemp\n" {
		t.Errorf("persistence file = %q, want \"coretemp\\n\" (idempotent overwrite)", got)
	}
}

func TestLoadModule_ClearsMatchingDiagEntries(t *testing.T) {
	stubModprobeOK(t)
	withTempModulesLoadDir(t)
	store := hwdiag.NewStore()
	m := newTestManager(t)
	m.SetDiagnosticStore(store)

	// Seed: one CPU-module-missing entry (ID-based clear) and one DMI
	// candidate entry (Context["module"]-based clear).
	store.Set(hwdiag.Entry{
		ID:        hwdiag.IDHwmonCPUModuleMissing,
		Component: hwdiag.ComponentHwmon,
		Severity:  hwdiag.SeverityWarn,
		Summary:   "CPU temperature sensor needs the coretemp kernel module",
		Context:   map[string]any{"module": "coretemp"},
	})
	store.Set(hwdiag.Entry{
		ID:        hwdiag.IDDMICandidatePrefix + "coretemp",
		Component: hwdiag.ComponentDMI,
		Severity:  hwdiag.SeverityWarn,
		Summary:   "Try loading coretemp",
		Context:   map[string]any{"module": "coretemp"},
	})
	// Unrelated entry — must be preserved after coretemp is loaded.
	store.Set(hwdiag.Entry{
		ID:        hwdiag.IDDMICandidatePrefix + "nct6687d",
		Component: hwdiag.ComponentDMI,
		Severity:  hwdiag.SeverityWarn,
		Summary:   "Try loading nct6687",
		Context:   map[string]any{"module": "nct6687"},
	})

	if _, err := m.LoadModule(context.Background(), "coretemp"); err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	snap := store.Snapshot(hwdiag.Filter{})
	if len(snap.Entries) != 1 {
		t.Fatalf("entries after LoadModule = %d, want 1 (only nct6687 preserved)", len(snap.Entries))
	}
	if got := snap.Entries[0].ID; got != hwdiag.IDDMICandidatePrefix+"nct6687d" {
		t.Errorf("surviving entry = %q, want nct6687d", got)
	}
}

func TestLoadModule_NoStoreIsOK(t *testing.T) {
	// LoadModule is sometimes invoked by the CLI setup flow without a
	// diagnostic store wired. The clear-entries step must tolerate that.
	stubModprobeOK(t)
	withTempModulesLoadDir(t)
	m := newTestManager(t)

	if _, err := m.LoadModule(context.Background(), "coretemp"); err != nil {
		t.Fatalf("LoadModule without diag store: %v", err)
	}
}

func TestAllowedModule(t *testing.T) {
	// Pin the allowlist so an accidental addition without a doc update
	// fails loudly in review.
	want := map[string]bool{
		"coretemp": true, "k10temp": true,
		"nct6683": true, "nct6687": true,
		"it87": true, "drivetemp": true,
	}
	for mod, ok := range want {
		if got := AllowedModule(mod); got != ok {
			t.Errorf("AllowedModule(%q) = %v, want %v", mod, got, ok)
		}
	}
	for _, bad := range []string{"iwlwifi", "snd_hda_intel", "", "coretemp "} {
		if AllowedModule(bad) {
			t.Errorf("AllowedModule(%q) = true, want false", bad)
		}
	}
}

func TestEmitCPUSensorModuleMissingDiag(t *testing.T) {
	tests := []struct {
		name     string
		vendor   string
		wantMod  string // "" means no emit
		wantChip string
	}{
		{name: "intel", vendor: "GenuineIntel", wantMod: "coretemp", wantChip: "coretemp"},
		{name: "amd", vendor: "AuthenticAMD", wantMod: "k10temp", wantChip: "k10temp"},
		{name: "hygon", vendor: "HygonGenuine", wantMod: "k10temp", wantChip: "k10temp"},
		{name: "arm_no_vendor", vendor: "", wantMod: ""},
		{name: "unknown", vendor: "SomeFutureCPU", wantMod: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mod, chip := cpuSensorModuleForVendor(tc.vendor)
			if mod != tc.wantMod || chip != tc.wantChip {
				t.Errorf("cpuSensorModuleForVendor(%q) = (%q,%q), want (%q,%q)",
					tc.vendor, mod, chip, tc.wantMod, tc.wantChip)
			}
		})
	}
}

func TestEmitCPUSensorModuleMissingDiag_SuppressedByDMI(t *testing.T) {
	// When the DMI pass has already proposed the same module (e.g. on a
	// board whose DMI entry matches coretemp), the hwmon emitter must stay
	// silent to avoid rendering two remediation cards for the same fix.
	store := hwdiag.NewStore()
	m := newTestManager(t)
	m.SetDiagnosticStore(store)

	// Force vendor detection to return coretemp by hijacking the same
	// store entry shape the DMI path would create.
	store.Set(hwdiag.Entry{
		ID:        hwdiag.IDDMICandidatePrefix + "coretemp",
		Component: hwdiag.ComponentDMI,
		Severity:  hwdiag.SeverityWarn,
		Summary:   "Try loading coretemp for your board",
		Context:   map[string]any{"module": "coretemp"},
	})

	// readCPUVendor reads /proc/cpuinfo; this test uses the live vendor
	// (newTestManager wires the production /proc root). Skip on non-Intel
	// hosts — the direct cpuSensorModuleForVendor coverage in
	// TestEmitCPUSensorModuleMissingDiag pins the mapping, and the
	// fixture-rooted manager tests in manager_fixtures_test.go exercise
	// the vendor read against synthetic cpuinfo.
	vendor := m.readCPUVendor()
	if vendor != "GenuineIntel" {
		t.Skipf("skip on non-Intel host (vendor=%q); DMI suppression is host-independent logic", vendor)
	}

	m.emitCPUSensorModuleMissingDiag()

	snap := store.Snapshot(hwdiag.Filter{})
	for _, e := range snap.Entries {
		if e.ID == hwdiag.IDHwmonCPUModuleMissing {
			t.Fatalf("hwmon CPU-missing emitted despite DMI duplicate: %+v", e)
		}
	}
}
