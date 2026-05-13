package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/ventd/ventd/internal/setupbroker"
)

// silentLogger swallows slog output so test runs don't print noise.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestRun_UnregisteredOperation_WritesNotImplResult — operations
// without a registered handler fall through to ErrOperationNotImpl.
// The binary should still exit 0 (request was syntactically valid
// and dispatched cleanly) and the result file should carry
// OK=false + the not-impl error.
//
// The test picks OpPatchKernelParam because it has no handler today
// (only OpLoadModule and OpUnloadModule are registered in main.go's
// dispatcher build). The LoadModule / UnloadModule happy-paths are
// exercised hermetically in internal/setupbroker/handlers/
// {load,unload}_module_test.go; this test guards the dispatcher's
// fallthrough contract, not any handler's happy path.
//
// Pre-#1095 this test sent OpLoadModule and asserted OK=false on
// the grounds that "Phase A has no handlers" — true when the test
// was written but invalidated when LoadModuleHandler shipped. On
// dev hosts that happen to have the requested module already
// loadable (e.g. nct6687 on a board that supports it) the real
// /sbin/modprobe call inside RealLoadModuleDeps would succeed and
// flip OK to true. Switching to an un-registered op restores the
// fallthrough contract without needing a deps-injection refactor.
func TestRun_UnregisteredOperation_WritesNotImplResult(t *testing.T) {
	dir := t.TempDir()
	reqPath := filepath.Join(dir, "request.json")
	resPath := filepath.Join(dir, "result.json")

	req := setupbroker.Request{
		SchemaVersion: setupbroker.SchemaVersion,
		Operation:     setupbroker.OpPatchKernelParam,
		Params:        json.RawMessage(`{}`),
		Audit: setupbroker.Audit{
			WizardSessionID: "test-session",
			RequestedBy:     "test@harness",
		},
	}
	body, _ := json.Marshal(req)
	if err := os.WriteFile(reqPath, body, 0o600); err != nil {
		t.Fatalf("write request: %v", err)
	}

	if code := run(silentLogger(), reqPath, resPath); code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}

	resBytes, err := os.ReadFile(resPath)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	var res setupbroker.Result
	if err := json.NewDecoder(bytes.NewReader(resBytes)).Decode(&res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.OK {
		t.Errorf("Result.OK = true, want false (op has no registered handler)")
	}
	if res.SchemaVersion != setupbroker.SchemaVersion {
		t.Errorf("Result.SchemaVersion = %d, want %d", res.SchemaVersion, setupbroker.SchemaVersion)
	}
	if res.Operation != string(setupbroker.OpPatchKernelParam) {
		t.Errorf("Result.Operation = %q, want %q", res.Operation, setupbroker.OpPatchKernelParam)
	}
}

// TestRun_MissingRequestFile — exit code 1, no result file created.
func TestRun_MissingRequestFile(t *testing.T) {
	dir := t.TempDir()
	resPath := filepath.Join(dir, "result.json")
	if code := run(silentLogger(), filepath.Join(dir, "absent.json"), resPath); code != 1 {
		t.Errorf("run with missing request returned %d, want 1", code)
	}
	if _, err := os.Stat(resPath); !os.IsNotExist(err) {
		t.Errorf("result file should not exist, got err=%v", err)
	}
}

// TestRun_MalformedRequestRejects — invalid JSON exits 1, no result.
func TestRun_MalformedRequestRejects(t *testing.T) {
	dir := t.TempDir()
	reqPath := filepath.Join(dir, "request.json")
	resPath := filepath.Join(dir, "result.json")
	if err := os.WriteFile(reqPath, []byte(`not json at all`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if code := run(silentLogger(), reqPath, resPath); code != 1 {
		t.Errorf("run with malformed request returned %d, want 1", code)
	}
}

// TestRun_ResultFileMode — result file MUST be 0o600 (the request /
// response can carry session metadata; default umask 022 would
// publish it world-readable).
func TestRun_ResultFileMode(t *testing.T) {
	dir := t.TempDir()
	reqPath := filepath.Join(dir, "request.json")
	resPath := filepath.Join(dir, "result.json")
	body, _ := json.Marshal(setupbroker.Request{
		SchemaVersion: setupbroker.SchemaVersion,
		Operation:     setupbroker.OpLoadModule,
		Params:        json.RawMessage(`{}`),
	})
	if err := os.WriteFile(reqPath, body, 0o600); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if code := run(silentLogger(), reqPath, resPath); code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	st, err := os.Stat(resPath)
	if err != nil {
		t.Fatalf("stat result: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Errorf("result file mode = %v, want 0600", mode)
	}
}
