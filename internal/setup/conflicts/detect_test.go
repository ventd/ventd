package conflicts

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// fakeSystemctl is the test-side stub for SystemctlRunner. activeUnits +
// enabledUnits drive the return values; everything else returns false/nil.
type fakeSystemctl struct {
	activeUnits  map[string]struct{}
	enabledUnits map[string]struct{}
}

func (f *fakeSystemctl) IsActive(_ context.Context, unit string) (bool, error) {
	_, ok := f.activeUnits[unit]
	return ok, nil
}

func (f *fakeSystemctl) IsEnabled(_ context.Context, unit string) (bool, error) {
	_, ok := f.enabledUnits[unit]
	return ok, nil
}

func newFakeSystemctl(active, enabled []string) *fakeSystemctl {
	a := make(map[string]struct{}, len(active))
	for _, u := range active {
		a[u] = struct{}{}
	}
	e := make(map[string]struct{}, len(enabled))
	for _, u := range enabled {
		e[u] = struct{}{}
	}
	return &fakeSystemctl{activeUnits: a, enabledUnits: e}
}

// stageProc builds a fake /proc tree with the given (comm, cmdline)
// processes. Returns the root path.
type fakeProc struct {
	comm    string
	cmdline string
}

func stageProc(t *testing.T, procs []fakeProc) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proc")
	for i, p := range procs {
		pid := strconv.Itoa(1000 + i)
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(p.comm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(p.cmdline), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDetect_SystemdActiveUnit(t *testing.T) {
	entries := []Entry{
		{
			Name:           "fancontrol",
			ConflictReason: "test",
			Units:          []string{"fancontrol.service"},
			Intrusiveness:  IntrusivenessLow,
		},
	}
	hits := Detect(context.Background(), DetectOptions{
		Systemctl:              newFakeSystemctl([]string{"fancontrol.service"}, nil),
		Entries:                entries,
		DisableConfigPathCheck: true,
	})
	if len(hits) != 1 {
		t.Fatalf("expected 1 conflict, got %d: %+v", len(hits), hits)
	}
	if hits[0].Entry.Name != "fancontrol" {
		t.Errorf("Conflict.Entry.Name = %q, want fancontrol", hits[0].Entry.Name)
	}
	if len(hits[0].UnitsActive) != 1 || hits[0].UnitsActive[0] != "fancontrol.service" {
		t.Errorf("UnitsActive = %v, want [fancontrol.service]", hits[0].UnitsActive)
	}
}

func TestDetect_SystemdEnabledOnlyStillReports(t *testing.T) {
	// "Installed and configured to start on boot but not currently
	// running" is the wizard's "did you mean to enable fancontrol
	// instead of ventd?" UX trigger.
	entries := []Entry{
		{
			Name: "fancontrol", ConflictReason: "test", Intrusiveness: IntrusivenessLow,
			Units: []string{"fancontrol.service"},
		},
	}
	hits := Detect(context.Background(), DetectOptions{
		Systemctl:              newFakeSystemctl(nil, []string{"fancontrol.service"}),
		Entries:                entries,
		DisableConfigPathCheck: true,
	})
	if len(hits) != 1 {
		t.Fatalf("enabled-but-inactive should still report; got %d", len(hits))
	}
	if len(hits[0].UnitsEnabled) != 1 {
		t.Errorf("UnitsEnabled missing on enabled-only conflict")
	}
}

func TestDetect_ProcMatchByCommExact(t *testing.T) {
	entries := []Entry{
		{
			Name: "thinkfan", ConflictReason: "test", Intrusiveness: IntrusivenessLow,
			ProcPatterns: []*regexp.Regexp{regexp.MustCompile(`^thinkfan$`)},
		},
	}
	procRoot := stageProc(t, []fakeProc{
		{comm: "thinkfan", cmdline: "/usr/sbin/thinkfan"},
		{comm: "bash", cmdline: "/bin/bash"},
	})
	hits := Detect(context.Background(), DetectOptions{
		ProcRoot:               procRoot,
		Entries:                entries,
		DisableConfigPathCheck: true,
	})
	if len(hits) != 1 {
		t.Fatalf("expected 1 conflict via proc scan, got %d", len(hits))
	}
	if len(hits[0].ProcessesFound) != 1 {
		t.Errorf("ProcessesFound = %v", hits[0].ProcessesFound)
	}
}

func TestDetect_ProcMatchByCmdlineWhenCommIsGeneric(t *testing.T) {
	// liquidctl is invoked via python wrapper → comm is "python3", only
	// cmdline carries the daemon identity.
	entries := []Entry{
		{
			Name: "liquidctl", ConflictReason: "test", Intrusiveness: IntrusivenessLow,
			ProcPatterns: []*regexp.Regexp{regexp.MustCompile(`liquidctl`)},
		},
	}
	procRoot := stageProc(t, []fakeProc{
		{comm: "python3", cmdline: "/usr/bin/python3 -m liquidctl set fan speed 50"},
	})
	hits := Detect(context.Background(), DetectOptions{
		ProcRoot:               procRoot,
		Entries:                entries,
		DisableConfigPathCheck: true,
	})
	if len(hits) != 1 {
		t.Fatalf("expected cmdline-match, got %d conflicts: %+v", len(hits), hits)
	}
}

func TestDetect_ConfigPathPresenceReports(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "fancontrol")
	if err := os.WriteFile(cfg, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{
		{
			Name: "fancontrol", ConflictReason: "test", Intrusiveness: IntrusivenessLow,
			ConfigPaths: []string{cfg},
		},
	}
	hits := Detect(context.Background(), DetectOptions{
		Entries: entries,
	})
	if len(hits) != 1 {
		t.Fatalf("config-path presence should report; got %d", len(hits))
	}
	if len(hits[0].ConfigsFound) != 1 {
		t.Errorf("ConfigsFound missing")
	}
}

func TestDetect_ModprobeDropInReports(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fancontrol.conf"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{
		{
			Name: "fancontrol_modprobe", ConflictReason: "test", Intrusiveness: IntrusivenessMedium,
			ModprobeDropIns: []string{"fancontrol.conf"},
		},
	}
	hits := Detect(context.Background(), DetectOptions{
		Entries:                entries,
		ModprobeDirs:           []string{dir},
		DisableConfigPathCheck: true,
	})
	if len(hits) != 1 {
		t.Fatalf("modprobe drop-in should report; got %d", len(hits))
	}
}

func TestDetect_NoSignalsNoConflict(t *testing.T) {
	entries := []Entry{
		{
			Name: "fancontrol", ConflictReason: "test", Intrusiveness: IntrusivenessLow,
			Units: []string{"fancontrol.service"},
		},
	}
	hits := Detect(context.Background(), DetectOptions{
		Systemctl:              newFakeSystemctl(nil, nil),
		Entries:                entries,
		DisableConfigPathCheck: true,
	})
	if len(hits) != 0 {
		t.Errorf("happy path should yield no conflicts; got %+v", hits)
	}
}

func TestDetect_SortedByIntrusivenessThenName(t *testing.T) {
	entries := []Entry{
		{
			Name: "vendor_a", ConflictReason: "x", Intrusiveness: IntrusivenessHigh, Vendor: true,
			Units: []string{"vendor_a.service"},
		},
		{
			Name: "low_b", ConflictReason: "x", Intrusiveness: IntrusivenessLow,
			Units: []string{"low_b.service"},
		},
		{
			Name: "low_a", ConflictReason: "x", Intrusiveness: IntrusivenessLow,
			Units: []string{"low_a.service"},
		},
	}
	hits := Detect(context.Background(), DetectOptions{
		Systemctl: newFakeSystemctl(
			[]string{"vendor_a.service", "low_b.service", "low_a.service"}, nil,
		),
		Entries:                entries,
		DisableConfigPathCheck: true,
	})
	if len(hits) != 3 {
		t.Fatalf("expected 3 conflicts, got %d", len(hits))
	}
	got := []string{hits[0].Entry.Name, hits[1].Entry.Name, hits[2].Entry.Name}
	want := []string{"low_a", "low_b", "vendor_a"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sort order [%d]: got %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestDetect_MultipleSignalsForSameEntryMerge(t *testing.T) {
	// An entry with active unit + matching proc + config-path present
	// should produce one conflict with all three populated, not three
	// duplicate conflicts.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "fancontrol")
	if err := os.WriteFile(cfg, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{
		{
			Name: "fancontrol", ConflictReason: "test", Intrusiveness: IntrusivenessLow,
			Units:        []string{"fancontrol.service"},
			ProcPatterns: []*regexp.Regexp{regexp.MustCompile(`^fancontrol$`)},
			ConfigPaths:  []string{cfg},
		},
	}
	procRoot := stageProc(t, []fakeProc{{comm: "fancontrol", cmdline: "/usr/sbin/fancontrol"}})
	hits := Detect(context.Background(), DetectOptions{
		Systemctl: newFakeSystemctl([]string{"fancontrol.service"}, nil),
		ProcRoot:  procRoot,
		Entries:   entries,
	})
	if len(hits) != 1 {
		t.Fatalf("expected 1 merged conflict, got %d: %+v", len(hits), hits)
	}
	c := hits[0]
	if len(c.UnitsActive) == 0 || len(c.ProcessesFound) == 0 || len(c.ConfigsFound) == 0 {
		t.Errorf("merged conflict missing fields: units=%v procs=%v cfgs=%v",
			c.UnitsActive, c.ProcessesFound, c.ConfigsFound)
	}
}

func TestDetect_NilSystemctlSkipsUnitChecksGracefully(t *testing.T) {
	// Running inside a non-systemd container: systemctl is missing,
	// runner is nil. Detection of unit-only entries returns empty,
	// other signals still work.
	entries := []Entry{
		{
			Name: "fancontrol", ConflictReason: "test", Intrusiveness: IntrusivenessLow,
			Units: []string{"fancontrol.service"},
		},
	}
	hits := Detect(context.Background(), DetectOptions{
		Systemctl:              nil,
		Entries:                entries,
		DisableConfigPathCheck: true,
	})
	if len(hits) != 0 {
		t.Errorf("nil runner with no other signals should produce no conflicts; got %+v", hits)
	}
}
