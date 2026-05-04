package setupbroker

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestSchemaVersion_PinnedToOne pins SchemaVersion=1 — bumping it
// is a breaking change to the wire format that needs a coordinated
// wizard-side bump + migration. The test fails on any change so
// such a bump is forced through code review.
func TestSchemaVersion_PinnedToOne(t *testing.T) {
	if SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1 (bumping is a wire-format break)", SchemaVersion)
	}
}

// TestAllOperations_StableOrder — the dispatcher's exhaustive
// "operation not implemented in this build" stubs and rulelint
// bindings depend on AllOperations() returning operations in
// a stable known order. Reordering breaks both.
func TestAllOperations_StableOrder(t *testing.T) {
	want := []Operation{
		OpInstallOOTDriver, OpInstallDependency, OpLoadModule,
		OpUnloadModule, OpPatchKernelParam, OpNVMLWrite, OpRunSensorsDetect,
	}
	got := AllOperations()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AllOperations()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDecodeRequest_HappyPath round-trips the canonical envelope
// from spec-v0_6_0-split-daemon §"ventd-setup.service request format".
func TestDecodeRequest_HappyPath(t *testing.T) {
	body := `{
		"schema_version": 1,
		"operation": "install_oot_driver",
		"params": {"chip_key": "nct6687d", "kernel_version": "6.8.0-111-generic", "ack_secure_boot": false},
		"audit": {"wizard_session_id": "abc123", "requested_by": "phoenix@desktop", "client_ip": "192.168.7.10"}
	}`
	req, err := DecodeRequest(strings.NewReader(body))
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if req.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", req.SchemaVersion)
	}
	if req.Operation != OpInstallOOTDriver {
		t.Errorf("Operation = %q, want %q", req.Operation, OpInstallOOTDriver)
	}
	if req.Audit.WizardSessionID != "abc123" {
		t.Errorf("Audit.WizardSessionID = %q, want abc123", req.Audit.WizardSessionID)
	}
}

// TestDecodeRequest_RejectsUnknownEnvelopeFields — DisallowUnknownFields
// is the load-bearing strict-mode setting. A typo in the wizard's
// envelope (e.g. "audi" instead of "audit") rejects rather than
// silently dropping the field.
func TestDecodeRequest_RejectsUnknownEnvelopeFields(t *testing.T) {
	body := `{"schema_version":1,"operation":"load_module","params":{},"audi":{}}`
	_, err := DecodeRequest(strings.NewReader(body))
	if err == nil {
		t.Fatal("expected error for unknown envelope field, got nil")
	}
	if !strings.Contains(err.Error(), "audi") {
		t.Errorf("err missing offending field name: %v", err)
	}
}

// TestDecodeRequest_RejectsSchemaMismatch — a wizard at a future
// schema version talking to an older ventd-setup must surface a
// clean error, not fall through with default values.
func TestDecodeRequest_RejectsSchemaMismatch(t *testing.T) {
	body := `{"schema_version":99,"operation":"load_module","params":{}}`
	_, err := DecodeRequest(strings.NewReader(body))
	if err == nil {
		t.Fatal("expected error for schema mismatch, got nil")
	}
	if !errors.Is(err, ErrSchemaMismatch) {
		t.Errorf("err = %v, want wraps ErrSchemaMismatch", err)
	}
}

// TestDecodeRequest_RejectsUnknownOperation — operations not in
// AllOperations reject. Adding a new operation requires a constant +
// dispatch entry; this test catches the case where a wizard tries
// to call an operation the broker doesn't know about.
func TestDecodeRequest_RejectsUnknownOperation(t *testing.T) {
	body := `{"schema_version":1,"operation":"format_disk","params":{}}`
	_, err := DecodeRequest(strings.NewReader(body))
	if err == nil {
		t.Fatal("expected error for unknown operation, got nil")
	}
	if !errors.Is(err, ErrUnknownOperation) {
		t.Errorf("err = %v, want wraps ErrUnknownOperation", err)
	}
}

// TestDispatcher_NoHandlerReturnsNotImpl — Phase A ships every
// operation as not-implemented. The broker MUST return a populated
// Result (not a panic, not a nil) with Error wrapping
// ErrOperationNotImpl so the wizard surfaces a clean "operation
// stubbed in this build" message.
func TestDispatcher_NoHandlerReturnsNotImpl(t *testing.T) {
	d := NewDispatcher()
	req := &Request{Operation: OpLoadModule}
	res, err := d.Dispatch(req)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v (want nil; not-impl surfaces in Result)", err)
	}
	if res == nil {
		t.Fatal("Dispatch returned nil Result")
	}
	if res.OK {
		t.Errorf("Result.OK = true, want false for not-implemented")
	}
	if !strings.Contains(res.Error, ErrOperationNotImpl.Error()) {
		t.Errorf("Result.Error = %q, want containing ErrOperationNotImpl text", res.Error)
	}
	if res.Operation != string(OpLoadModule) {
		t.Errorf("Result.Operation = %q, want %q (echo)", res.Operation, OpLoadModule)
	}
}

// TestDispatcher_RegisteredHandlerInvoked — Register + Dispatch
// happy path. Future PRs that fill in real handlers will rely on
// this contract.
func TestDispatcher_RegisteredHandlerInvoked(t *testing.T) {
	called := false
	d := NewDispatcher()
	d.Register(OpLoadModule, func(req *Request) (*Result, error) {
		called = true
		return &Result{OK: true, Operation: string(req.Operation)}, nil
	})
	res, err := d.Dispatch(&Request{Operation: OpLoadModule})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !called {
		t.Error("Handler was not invoked")
	}
	if !res.OK {
		t.Errorf("Result.OK = false, want true")
	}
}

// TestEncodeResult_PopulatesSchemaVersion — EncodeResult MUST
// stamp SchemaVersion regardless of caller input so the wizard's
// reader can validate the wire shape. Catches the case where a
// Handler forgot to set it.
func TestEncodeResult_PopulatesSchemaVersion(t *testing.T) {
	var buf bytes.Buffer
	res := &Result{Operation: "load_module", OK: true}
	if err := EncodeResult(&buf, res); err != nil {
		t.Fatalf("EncodeResult: %v", err)
	}
	var decoded Result
	if err := json.NewDecoder(&buf).Decode(&decoded); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if decoded.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", decoded.SchemaVersion, SchemaVersion)
	}
}

// TestEncodeResult_OneLineNewlineTerminated — `journalctl -u
// ventd-setup --output=json` parses one JSON object per log line,
// so EncodeResult MUST emit compact single-line + newline.
// Multi-line pretty-printed output would corrupt journald parsing.
func TestEncodeResult_OneLineNewlineTerminated(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeResult(&buf, &Result{OK: true, Operation: "load_module"}); err != nil {
		t.Fatalf("EncodeResult: %v", err)
	}
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output missing trailing newline: %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("output not single-line (newline count = %d): %q", strings.Count(out, "\n"), out)
	}
}

// TestDecodeRequest_RejectsTruncatedInput — a partial / truncated
// request body must reject cleanly. Catches the case where the
// wizard's writer was killed mid-write and ventd-setup tries to
// parse the partial file.
func TestDecodeRequest_RejectsTruncatedInput(t *testing.T) {
	_, err := DecodeRequest(strings.NewReader(`{"schema_version":1,"oper`))
	if err == nil {
		t.Fatal("expected error for truncated input, got nil")
	}
	if !strings.Contains(err.Error(), "decode request") {
		t.Errorf("err = %v, want wrapped 'decode request'", err)
	}
}

// TestDecodeRequest_NilReaderRejectsCleanly — defensive: never
// panic on a nil reader, always return a clean error.
func TestDecodeRequest_NilReaderRejectsCleanly(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("DecodeRequest panicked on nil reader: %v", r)
		}
	}()
	_, err := DecodeRequest(io.LimitReader(strings.NewReader(""), 0))
	if err == nil {
		t.Fatal("expected error for empty/nil reader, got nil")
	}
}
