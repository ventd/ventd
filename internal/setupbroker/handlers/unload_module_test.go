package handlers

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/setupbroker"
)

type unloadFakeDeps struct {
	mu            sync.Mutex
	dir           string
	modprobeCalls []string
	removeCalls   []string
	statCalls     []string
	modprobeOut   []byte
	modprobeErr   error
	removeErr     error
	statErr       error // overrides "file present" assumption when set
}

func newUnloadFake(t *testing.T) *unloadFakeDeps {
	return &unloadFakeDeps{dir: t.TempDir()}
}

func (f *unloadFakeDeps) deps() UnloadModuleDeps {
	return UnloadModuleDeps{
		Modprobe: func(module string) ([]byte, error) {
			f.mu.Lock()
			f.modprobeCalls = append(f.modprobeCalls, module)
			out, err := f.modprobeOut, f.modprobeErr
			f.mu.Unlock()
			return out, err
		},
		Remove: func(path string) error {
			f.mu.Lock()
			f.removeCalls = append(f.removeCalls, path)
			err := f.removeErr
			f.mu.Unlock()
			return err
		},
		Stat: func(path string) (os.FileInfo, error) {
			f.mu.Lock()
			f.statCalls = append(f.statCalls, path)
			err := f.statErr
			f.mu.Unlock()
			if err != nil {
				return nil, err
			}
			// "Present" path: synthesise a minimal os.FileInfo (we
			// don't read fields from it — handler only checks for
			// nil error).
			return fakeStatInfo{name: filepath.Base(path)}, nil
		},
		ModulesLoadDir: f.dir,
	}
}

type fakeStatInfo struct{ name string }

func (f fakeStatInfo) Name() string       { return f.name }
func (f fakeStatInfo) Size() int64        { return 0 }
func (f fakeStatInfo) Mode() os.FileMode  { return 0o644 }
func (f fakeStatInfo) ModTime() time.Time { return time.Time{} }
func (f fakeStatInfo) IsDir() bool        { return false }
func (f fakeStatInfo) Sys() any           { return nil }

func mustUnloadParams(t *testing.T, p UnloadModuleParams) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestUnloadModuleHandler_HappyPath_ModprobeOnly(t *testing.T) {
	f := newUnloadFake(t)
	h := UnloadModuleHandler(f.deps())
	req := &setupbroker.Request{
		Operation: setupbroker.OpUnloadModule,
		Params:    mustUnloadParams(t, UnloadModuleParams{Module: "nct6687"}),
	}
	res, err := h(req)
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if !res.OK {
		t.Errorf("Result.OK = false, want true; Error=%q", res.Error)
	}
	if len(f.modprobeCalls) != 1 || f.modprobeCalls[0] != "nct6687" {
		t.Errorf("modprobe calls = %v, want [nct6687]", f.modprobeCalls)
	}
	if len(f.removeCalls) != 0 {
		t.Errorf("Remove called %d times, want 0", len(f.removeCalls))
	}
}

func TestUnloadModuleHandler_HappyPath_WithRemovePersist(t *testing.T) {
	f := newUnloadFake(t)
	h := UnloadModuleHandler(f.deps())
	req := &setupbroker.Request{
		Operation: setupbroker.OpUnloadModule,
		Params: mustUnloadParams(t, UnloadModuleParams{
			Module: "nct6687", RemovePersist: true,
		}),
	}
	res, _ := h(req)
	if !res.OK {
		t.Fatalf("Result.OK = false; Error=%q", res.Error)
	}
	if len(f.removeCalls) != 1 {
		t.Fatalf("Remove called %d times, want 1", len(f.removeCalls))
	}
	wantPath := filepath.Join(f.dir, "ventd-nct6687.conf")
	if got := f.removeCalls[0]; got != wantPath {
		t.Errorf("Remove path = %q, want %q", got, wantPath)
	}
	if !strings.Contains(res.AuditSummary, "(persistence cleared)") {
		t.Errorf("AuditSummary = %q, want '(persistence cleared)' suffix", res.AuditSummary)
	}
}

// TestUnloadModuleHandler_PersistAlreadyAbsent_StillSucceeds — when
// the operator requested RemovePersist but the file is already gone,
// the handler reports success (idempotent intent is satisfied).
func TestUnloadModuleHandler_PersistAlreadyAbsent_StillSucceeds(t *testing.T) {
	f := newUnloadFake(t)
	f.statErr = fs.ErrNotExist
	h := UnloadModuleHandler(f.deps())
	req := &setupbroker.Request{
		Operation: setupbroker.OpUnloadModule,
		Params: mustUnloadParams(t, UnloadModuleParams{
			Module: "nct6687", RemovePersist: true,
		}),
	}
	res, _ := h(req)
	if !res.OK {
		t.Errorf("Result.OK = false (file already absent should be idempotent success); Error=%q", res.Error)
	}
	if len(f.removeCalls) != 0 {
		t.Errorf("Remove called %d times for already-absent file; want 0", len(f.removeCalls))
	}
}

func TestUnloadModuleHandler_RejectsInvalidModuleName(t *testing.T) {
	bad := []string{"", "../../etc/foo", "foo;bar", "foo bar"}
	for _, name := range bad {
		t.Run("name="+name, func(t *testing.T) {
			f := newUnloadFake(t)
			h := UnloadModuleHandler(f.deps())
			req := &setupbroker.Request{
				Operation: setupbroker.OpUnloadModule,
				Params:    mustUnloadParams(t, UnloadModuleParams{Module: name}),
			}
			res, _ := h(req)
			if res.OK {
				t.Errorf("Result.OK = true, want false for module=%q", name)
			}
			if !strings.Contains(res.Error, setupbroker.ErrInvalidParams.Error()) {
				t.Errorf("Error = %q, want wraps ErrInvalidParams", res.Error)
			}
			if len(f.modprobeCalls) != 0 {
				t.Errorf("modprobe was called for invalid name; want 0 calls")
			}
		})
	}
}

func TestUnloadModuleHandler_ModprobeFailureSurfaces(t *testing.T) {
	f := newUnloadFake(t)
	f.modprobeOut = []byte("modprobe: FATAL: Module nct6687 is in use\n")
	f.modprobeErr = errors.New("exit status 1")
	h := UnloadModuleHandler(f.deps())
	req := &setupbroker.Request{
		Operation: setupbroker.OpUnloadModule,
		Params:    mustUnloadParams(t, UnloadModuleParams{Module: "nct6687"}),
	}
	res, _ := h(req)
	if res.OK {
		t.Errorf("Result.OK = true, want false")
	}
	if !strings.Contains(res.Error, "is in use") {
		t.Errorf("Error = %q, want to contain modprobe stderr", res.Error)
	}
}

func TestUnloadModuleHandler_RemoveFailureWhenPresent_SurfacesAsPartial(t *testing.T) {
	f := newUnloadFake(t)
	f.removeErr = errors.New("permission denied")
	h := UnloadModuleHandler(f.deps())
	req := &setupbroker.Request{
		Operation: setupbroker.OpUnloadModule,
		Params: mustUnloadParams(t, UnloadModuleParams{
			Module: "nct6687", RemovePersist: true,
		}),
	}
	res, _ := h(req)
	if res.OK {
		t.Errorf("Result.OK = true, want false (remove failed)")
	}
	if !strings.Contains(res.AuditSummary, "removal failed") {
		t.Errorf("AuditSummary = %q, want partial-state mention", res.AuditSummary)
	}
}

func TestUnloadModuleHandler_RejectsUnknownParamFields(t *testing.T) {
	f := newUnloadFake(t)
	h := UnloadModuleHandler(f.deps())
	req := &setupbroker.Request{
		Operation: setupbroker.OpUnloadModule,
		Params:    json.RawMessage(`{"modul":"nct6687"}`),
	}
	res, _ := h(req)
	if res.OK {
		t.Errorf("Result.OK = true, want false")
	}
}
