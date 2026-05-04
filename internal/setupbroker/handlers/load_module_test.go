package handlers

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ventd/ventd/internal/setupbroker"
)

// fakeDeps is the test recorder for LoadModuleDeps. Captures every
// modprobe + WriteFile call so assertions can pin the exact wire
// the handler issued.
type fakeDeps struct {
	mu             sync.Mutex
	dir            string
	modprobeCalls  []modprobeCall
	writeFileCalls []writeFileCall
	modprobeOut    []byte
	modprobeErr    error
	writeFileErr   error
}

type modprobeCall struct {
	module string
	args   []string
}

type writeFileCall struct {
	path string
	data []byte
	mode os.FileMode
}

func newFakeDeps(t *testing.T) *fakeDeps {
	return &fakeDeps{dir: t.TempDir()}
}

func (f *fakeDeps) deps() LoadModuleDeps {
	return LoadModuleDeps{
		Modprobe: func(module string, args ...string) ([]byte, error) {
			f.mu.Lock()
			f.modprobeCalls = append(f.modprobeCalls, modprobeCall{module, append([]string(nil), args...)})
			out, err := f.modprobeOut, f.modprobeErr
			f.mu.Unlock()
			return out, err
		},
		WriteFile: func(path string, data []byte, mode os.FileMode) error {
			f.mu.Lock()
			f.writeFileCalls = append(f.writeFileCalls, writeFileCall{path, append([]byte(nil), data...), mode})
			err := f.writeFileErr
			f.mu.Unlock()
			return err
		},
		ModulesLoadDir: f.dir,
	}
}

func mustParams(t *testing.T, p LoadModuleParams) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return b
}

// TestLoadModuleHandler_HappyPath_ModprobeOnly — a minimal request
// (module name only, no persist, no args) should call modprobe once
// with that name + zero args, NOT touch WriteFile, return OK=true,
// and emit a one-line audit summary.
func TestLoadModuleHandler_HappyPath_ModprobeOnly(t *testing.T) {
	f := newFakeDeps(t)
	h := LoadModuleHandler(f.deps())
	req := &setupbroker.Request{
		Operation: setupbroker.OpLoadModule,
		Params:    mustParams(t, LoadModuleParams{Module: "nct6687"}),
	}
	res, err := h(req)
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if !res.OK {
		t.Errorf("Result.OK = false, want true; Error=%q", res.Error)
	}
	if len(f.modprobeCalls) != 1 {
		t.Fatalf("modprobe calls = %d, want 1", len(f.modprobeCalls))
	}
	if got := f.modprobeCalls[0].module; got != "nct6687" {
		t.Errorf("modprobe module = %q, want %q", got, "nct6687")
	}
	if len(f.modprobeCalls[0].args) != 0 {
		t.Errorf("modprobe args = %v, want []", f.modprobeCalls[0].args)
	}
	if len(f.writeFileCalls) != 0 {
		t.Errorf("WriteFile called %d times, want 0 (no persist)", len(f.writeFileCalls))
	}
	if !strings.Contains(res.AuditSummary, "nct6687") {
		t.Errorf("AuditSummary = %q, want to contain module name", res.AuditSummary)
	}
}

// TestLoadModuleHandler_HappyPath_WithPersist — persist_at_boot:true
// must write /etc/modules-load.d/ventd-<module>.conf with the
// module name + newline.
func TestLoadModuleHandler_HappyPath_WithPersist(t *testing.T) {
	f := newFakeDeps(t)
	h := LoadModuleHandler(f.deps())
	req := &setupbroker.Request{
		Operation: setupbroker.OpLoadModule,
		Params:    mustParams(t, LoadModuleParams{Module: "nct6687", PersistAtBoot: true}),
	}
	res, err := h(req)
	if err != nil || !res.OK {
		t.Fatalf("expected success; err=%v ok=%v error=%q", err, res.OK, res.Error)
	}
	if len(f.writeFileCalls) != 1 {
		t.Fatalf("WriteFile calls = %d, want 1", len(f.writeFileCalls))
	}
	wantPath := filepath.Join(f.dir, "ventd-nct6687.conf")
	if got := f.writeFileCalls[0].path; got != wantPath {
		t.Errorf("WriteFile path = %q, want %q", got, wantPath)
	}
	if got := string(f.writeFileCalls[0].data); got != "nct6687\n" {
		t.Errorf("WriteFile body = %q, want %q", got, "nct6687\n")
	}
	if got := f.writeFileCalls[0].mode; got != 0o644 {
		t.Errorf("WriteFile mode = %#o, want 0644", got)
	}
	if !strings.Contains(res.AuditSummary, "(persisted)") {
		t.Errorf("AuditSummary = %q, want '(persisted)' suffix", res.AuditSummary)
	}
}

// TestLoadModuleHandler_HappyPath_WithArgs — arg list passes through
// to modprobe verbatim.
func TestLoadModuleHandler_HappyPath_WithArgs(t *testing.T) {
	f := newFakeDeps(t)
	h := LoadModuleHandler(f.deps())
	req := &setupbroker.Request{
		Operation: setupbroker.OpLoadModule,
		Params: mustParams(t, LoadModuleParams{
			Module: "it87",
			Args:   []string{"force_id=0x8688", "ignore_resource_conflict=1"},
		}),
	}
	res, err := h(req)
	if err != nil || !res.OK {
		t.Fatalf("expected success; err=%v ok=%v error=%q", err, res.OK, res.Error)
	}
	if len(f.modprobeCalls) != 1 {
		t.Fatalf("modprobe calls = %d, want 1", len(f.modprobeCalls))
	}
	got := f.modprobeCalls[0].args
	want := []string{"force_id=0x8688", "ignore_resource_conflict=1"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestLoadModuleHandler_RejectsInvalidModuleName — anything outside
// [A-Za-z0-9_-]{1,64} rejects with OK=false + ErrInvalidParams.
// Catches path-traversal + shell-metacharacter inputs at the param
// layer.
func TestLoadModuleHandler_RejectsInvalidModuleName(t *testing.T) {
	bad := []string{
		"",                      // empty
		"../../etc/passwd",      // path traversal
		"foo;reboot",            // shell metachar
		"foo bar",               // space
		"foo$bar",               // shell var
		strings.Repeat("a", 65), // too long (>64)
	}
	for _, name := range bad {
		t.Run("name="+name, func(t *testing.T) {
			f := newFakeDeps(t)
			h := LoadModuleHandler(f.deps())
			req := &setupbroker.Request{
				Operation: setupbroker.OpLoadModule,
				Params:    mustParams(t, LoadModuleParams{Module: name}),
			}
			res, err := h(req)
			if err != nil {
				t.Fatalf("handler err: %v", err)
			}
			if res.OK {
				t.Errorf("Result.OK = true, want false for module=%q", name)
			}
			if !strings.Contains(res.Error, setupbroker.ErrInvalidParams.Error()) {
				t.Errorf("Error = %q, want wraps ErrInvalidParams", res.Error)
			}
			if len(f.modprobeCalls) != 0 {
				t.Errorf("modprobe was called %d times for invalid name; want 0", len(f.modprobeCalls))
			}
		})
	}
}

// TestLoadModuleHandler_RejectsShellMetacharsInArgs — even though
// modprobe is exec'd directly (no shell), defensive arg validation
// keeps a future shell-mode dispatch refactor from accidentally
// opening an injection window.
func TestLoadModuleHandler_RejectsShellMetacharsInArgs(t *testing.T) {
	bad := []string{
		"force_id=0x86;reboot",
		"force_id=$(id)",
		"force_id=`id`",
		"option=foo|bar",
	}
	for _, arg := range bad {
		t.Run("arg="+arg, func(t *testing.T) {
			f := newFakeDeps(t)
			h := LoadModuleHandler(f.deps())
			req := &setupbroker.Request{
				Operation: setupbroker.OpLoadModule,
				Params:    mustParams(t, LoadModuleParams{Module: "nct6687", Args: []string{arg}}),
			}
			res, _ := h(req)
			if res.OK {
				t.Errorf("Result.OK = true, want false for arg=%q", arg)
			}
			if !strings.Contains(res.Error, "shell metacharacter") {
				t.Errorf("Error = %q, want 'shell metacharacter'", res.Error)
			}
			if len(f.modprobeCalls) != 0 {
				t.Errorf("modprobe was called %d times for bad arg; want 0", len(f.modprobeCalls))
			}
		})
	}
}

// TestLoadModuleHandler_ModprobeFailureSurfaces — modprobe returning
// a non-nil error must produce OK=false with the trimmed stderr in
// the Error field. Catches the case where the wizard needs to surface
// the real "module not found" / "key rejected" message to the operator.
func TestLoadModuleHandler_ModprobeFailureSurfaces(t *testing.T) {
	f := newFakeDeps(t)
	f.modprobeOut = []byte("modprobe: FATAL: Module nct6687 not found in directory /lib/modules/6.8.0-111-generic\n")
	f.modprobeErr = errors.New("exit status 1")
	h := LoadModuleHandler(f.deps())
	req := &setupbroker.Request{
		Operation: setupbroker.OpLoadModule,
		Params:    mustParams(t, LoadModuleParams{Module: "nct6687"}),
	}
	res, err := h(req)
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if res.OK {
		t.Errorf("Result.OK = true, want false")
	}
	if !strings.Contains(res.Error, "Module nct6687 not found") {
		t.Errorf("Error = %q, want to contain modprobe stderr", res.Error)
	}
	if len(f.writeFileCalls) != 0 {
		t.Errorf("WriteFile was called after modprobe failure; want 0 calls")
	}
}

// TestLoadModuleHandler_PersistFailureSurfacesAsPartial — modprobe
// succeeded (module is in the running kernel) but writing the
// modules-load.d file failed. The handler must report OK=false so
// the wizard knows persistence is missing — the next reboot would
// drop the module. The audit summary names the partial state.
func TestLoadModuleHandler_PersistFailureSurfacesAsPartial(t *testing.T) {
	f := newFakeDeps(t)
	f.writeFileErr = errors.New("disk full")
	h := LoadModuleHandler(f.deps())
	req := &setupbroker.Request{
		Operation: setupbroker.OpLoadModule,
		Params:    mustParams(t, LoadModuleParams{Module: "nct6687", PersistAtBoot: true}),
	}
	res, _ := h(req)
	if res.OK {
		t.Errorf("Result.OK = true, want false (persist failed)")
	}
	if !strings.Contains(res.Error, "disk full") {
		t.Errorf("Error = %q, want to contain underlying err", res.Error)
	}
	if !strings.Contains(res.AuditSummary, "persistence write failed") {
		t.Errorf("AuditSummary = %q, want to mention partial-success state", res.AuditSummary)
	}
}

// TestLoadModuleHandler_RejectsUnknownParamFields — DisallowUnknownFields
// in the per-handler decoder catches typos (e.g. "modul" instead of
// "module") that the envelope-level validation can't see (envelope
// validation only validates the envelope's own fields, not the
// per-operation params).
func TestLoadModuleHandler_RejectsUnknownParamFields(t *testing.T) {
	f := newFakeDeps(t)
	h := LoadModuleHandler(f.deps())
	req := &setupbroker.Request{
		Operation: setupbroker.OpLoadModule,
		Params:    json.RawMessage(`{"modul":"nct6687","persit_at_boot":true}`),
	}
	res, _ := h(req)
	if res.OK {
		t.Errorf("Result.OK = true, want false")
	}
	if !strings.Contains(res.Error, setupbroker.ErrInvalidParams.Error()) {
		t.Errorf("Error = %q, want wraps ErrInvalidParams", res.Error)
	}
}
