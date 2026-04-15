package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/hwdiag"
	setupmgr "github.com/ventd/ventd/internal/setup"
)

// postLoadModule issues a POST to /api/setup/load-module with the given
// session cookie and raw JSON body, and returns the recorder for assertions.
// Tokens may be empty when asserting the unauth'd path — in that case no
// cookie is attached.
func postLoadModule(t *testing.T, srv *Server, tok, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/setup/load-module", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	}
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	return rr
}

func TestHandleSetupLoadModule_RequiresAuth(t *testing.T) {
	srv, _ := newTestServer(t)
	// No cookie → requireAuth middleware rejects before the handler runs.
	rr := postLoadModule(t, srv, "", `{"module":"coretemp"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestHandleSetupLoadModule_MethodGet(t *testing.T) {
	srv, tok := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/setup/load-module", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

func TestHandleSetupLoadModule_BadJSON(t *testing.T) {
	srv, tok := newTestServer(t)
	rr := postLoadModule(t, srv, tok, `{not json`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHandleSetupLoadModule_MissingModule(t *testing.T) {
	srv, tok := newTestServer(t)
	rr := postLoadModule(t, srv, tok, `{}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHandleSetupLoadModule_NotAllowed(t *testing.T) {
	srv, tok := newTestServer(t)

	// Seed a non-allowlisted module in the hwdiag store; the handler must
	// NOT touch modprobe, NOT write a persistence file, and the seeded
	// entry must survive untouched. If hitting this path ever clears
	// arbitrary diag entries it's a serious information leak.
	srv.diag.Set(hwdiag.Entry{
		ID:        "test.seeded",
		Component: hwdiag.ComponentHwmon,
		Severity:  hwdiag.SeverityWarn,
		Summary:   "unrelated",
	})

	rr := postLoadModule(t, srv, tok, `{"module":"iwlwifi"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for non-allowlisted module", rr.Code)
	}
	if got := len(srv.diag.Snapshot(hwdiag.Filter{}).Entries); got != 1 {
		t.Fatalf("diag entries = %d, want 1 (seeded entry preserved)", got)
	}
}

func TestHandleSetupLoadModule_ShellMetacharRejected(t *testing.T) {
	srv, tok := newTestServer(t)
	rr := postLoadModule(t, srv, tok, `{"module":"coretemp; rm -rf /"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for shell metachar module name", rr.Code)
	}
}

func TestHandleSetupLoadModule_RequestTooLarge(t *testing.T) {
	srv, tok := newTestServer(t)
	// 1 KiB body trips the 256-byte limit well before the JSON decoder sees it.
	big := `{"module":"` + strings.Repeat("a", 1024) + `"}`
	rr := postLoadModule(t, srv, tok, big)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 for oversized body", rr.Code)
	}
}

func TestHandleSetupLoadModule_Roundtrip(t *testing.T) {
	// Stub modprobe + divert persistence writes to t.TempDir so the test
	// runs as non-root. A successful call must: return installLogResponse
	// with Success=true, write the persistence file, and clear the hwdiag
	// entry that advertised the module as missing.
	var calls atomic.Int32
	prevMod := setupmgr.SetModprobeCmd(func(ctx context.Context, mod string) ([]byte, error) {
		calls.Add(1)
		return []byte("module " + mod + " loaded\n"), nil
	})
	t.Cleanup(func() { setupmgr.SetModprobeCmd(prevMod) })

	dir := t.TempDir()
	prevDir := setupmgr.SetModulesLoadDir(dir)
	t.Cleanup(func() { setupmgr.SetModulesLoadDir(prevDir) })

	srv, tok := newTestServer(t)
	// Seed the hwdiag entry the UI would have been showing: a
	// hwmon.cpu_module_missing pointing at coretemp, plus an unrelated
	// entry that must survive.
	srv.diag.Set(hwdiag.Entry{
		ID:        hwdiag.IDHwmonCPUModuleMissing,
		Component: hwdiag.ComponentHwmon,
		Severity:  hwdiag.SeverityWarn,
		Summary:   "CPU temperature sensor needs the coretemp kernel module",
		Context:   map[string]any{"module": "coretemp"},
	})
	srv.diag.Set(hwdiag.Entry{
		ID:        "unrelated.entry",
		Component: hwdiag.ComponentCalibration,
		Severity:  hwdiag.SeverityInfo,
		Summary:   "unrelated",
	})

	rr := postLoadModule(t, srv, tok, `{"module":"coretemp"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp installLogResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Kind != "install_log" {
		t.Errorf("Kind = %q, want install_log", resp.Kind)
	}
	if !resp.Success {
		t.Errorf("Success = false, want true (log=%v error=%q)", resp.Log, resp.Error)
	}
	if resp.Error != "" {
		t.Errorf("Error = %q, want empty", resp.Error)
	}
	if calls.Load() != 1 {
		t.Errorf("modprobe called %d times, want 1", calls.Load())
	}

	// Persistence file under /etc/modules-load.d/ (swapped to tempdir).
	confPath := filepath.Join(dir, "ventd-coretemp.conf")
	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read persistence file: %v", err)
	}
	if string(data) != "coretemp\n" {
		t.Errorf("persistence content = %q, want \"coretemp\\n\"", string(data))
	}

	// Diag entry cleared; unrelated entry preserved.
	entries := srv.diag.Snapshot(hwdiag.Filter{}).Entries
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 (unrelated.entry preserved)", len(entries))
	}
	if entries[0].ID != "unrelated.entry" {
		t.Errorf("surviving entry = %q, want unrelated.entry", entries[0].ID)
	}
}

func TestHandleSetupLoadModule_ModprobeFailure_ReturnsErrorInBody(t *testing.T) {
	// Mirrors install-kernel-headers semantics: allowlisted module whose
	// modprobe call fails returns 200 with Success=false so the UI can
	// render the log + error inline without treating it as an HTTP error.
	prevMod := setupmgr.SetModprobeCmd(func(ctx context.Context, mod string) ([]byte, error) {
		return []byte("modprobe: FATAL: Module not found.\n"), errors.New("exit status 1")
	})
	t.Cleanup(func() { setupmgr.SetModprobeCmd(prevMod) })

	dir := t.TempDir()
	prevDir := setupmgr.SetModulesLoadDir(dir)
	t.Cleanup(func() { setupmgr.SetModulesLoadDir(prevDir) })

	srv, tok := newTestServer(t)
	// Seed the matching diag entry — it must remain since the fix didn't
	// actually land; clearing on failure would mislead the UI.
	srv.diag.Set(hwdiag.Entry{
		ID:        hwdiag.IDHwmonCPUModuleMissing,
		Component: hwdiag.ComponentHwmon,
		Severity:  hwdiag.SeverityWarn,
		Summary:   "CPU temperature sensor needs the coretemp kernel module",
		Context:   map[string]any{"module": "coretemp"},
	})

	rr := postLoadModule(t, srv, tok, `{"module":"coretemp"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with error body", rr.Code)
	}
	var resp installLogResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success {
		t.Errorf("Success = true, want false on modprobe failure")
	}
	if resp.Error == "" {
		t.Error("Error is empty on failure")
	}

	// Persistence file must NOT be written on failure.
	if _, err := os.Stat(filepath.Join(dir, "ventd-coretemp.conf")); !os.IsNotExist(err) {
		t.Errorf("persistence file exists after failed modprobe: stat err=%v", err)
	}

	// hwdiag entry preserved because the fix didn't actually work.
	entries := srv.diag.Snapshot(hwdiag.Filter{}).Entries
	found := false
	for _, e := range entries {
		if e.ID == hwdiag.IDHwmonCPUModuleMissing {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("diag entry cleared on failed modprobe; got entries=%v", entries)
	}
}
