// Package setupbroker is the request schema + dispatch table for
// the v0.6.0 split-daemon setup-side service (ventd-setup.service).
// See specs/spec-v0_6_0-split-daemon.md for the full architecture.
//
// In Phase A (this PR) the broker is shipped but not used:
// the wizard still routes privileged operations through the existing
// root ventd path. The broker package + the cmd/ventd-setup binary
// + the deploy/ventd-setup.service unit land together so HIL
// operators can smoke-test the setup binary in isolation. Phase B
// (a future PR) wires the wizard's privileged steps to actually
// dispatch through this broker.
//
// The wire format is JSON with strict field validation:
//
//	wizard writes  /run/ventd/setup-request.json
//	wizard runs    systemctl start ventd-setup.service
//	ventd-setup    reads the request, dispatches, writes
//	               /run/ventd/setup-result.json, exits
//	wizard reads   /run/ventd/setup-result.json
//
// Strict validation = json.Decoder.DisallowUnknownFields, closed-set
// operation enum, per-operation params type. A typo in the request
// surfaces as a 400-equivalent rejection rather than silently doing
// the wrong thing.
package setupbroker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// SchemaVersion pins the request/result wire shape. Any breaking
// change to the request envelope or per-operation params bumps this
// + ships a migration in the wizard's request writer + the broker's
// reader. Mid-version rolling-upgrade compatibility (wizard at
// version N, ventd-setup at N+1) is achieved via additive
// per-operation Params extensions, never via envelope churn.
const SchemaVersion = 1

// Operation is the closed set of privileged operations the broker
// can dispatch. New operations are added by:
//  1. Adding a constant here.
//  2. Adding the corresponding Params struct to params.go.
//  3. Registering a Handler in the dispatch table.
//
// Operations not in this list are rejected at decode time with a
// schema error.
type Operation string

const (
	OpInstallOOTDriver  Operation = "install_oot_driver"
	OpInstallDependency Operation = "install_dependency"
	OpLoadModule        Operation = "load_module"
	OpUnloadModule      Operation = "unload_module"
	OpPatchKernelParam  Operation = "patch_kernel_param"
	OpNVMLWrite         Operation = "nvml_write"
	OpRunSensorsDetect  Operation = "run_sensors_detect"
)

// AllOperations returns every supported operation in stable order.
// Used by the dispatcher's "operation not supported" error path
// + by tests that exhaustively assert the dispatch table.
func AllOperations() []Operation {
	return []Operation{
		OpInstallOOTDriver,
		OpInstallDependency,
		OpLoadModule,
		OpUnloadModule,
		OpPatchKernelParam,
		OpNVMLWrite,
		OpRunSensorsDetect,
	}
}

// Audit captures the wizard-side context the operator should be
// able to trace back through `journalctl -u ventd-setup`. None of
// these fields gate dispatch; they're emitted to the journal when
// the broker dispatches the operation.
type Audit struct {
	WizardSessionID string `json:"wizard_session_id,omitempty"`
	RequestedBy     string `json:"requested_by,omitempty"`
	ClientIP        string `json:"client_ip,omitempty"`
}

// Request is the on-wire envelope the wizard writes to
// /run/ventd/setup-request.json.
type Request struct {
	SchemaVersion int             `json:"schema_version"`
	Operation     Operation       `json:"operation"`
	Params        json.RawMessage `json:"params"`
	Audit         Audit           `json:"audit,omitempty"`
}

// Result is the on-wire response written to
// /run/ventd/setup-result.json by ventd-setup. The wizard polls for
// the file's existence after `systemctl start ventd-setup` returns,
// then unmarshals + acts on the result.
type Result struct {
	SchemaVersion int    `json:"schema_version"`
	Operation     string `json:"operation"`
	OK            bool   `json:"ok"`
	Error         string `json:"error,omitempty"`
	AuditSummary  string `json:"audit_summary,omitempty"`
}

// Sentinel errors for the request decoder. Callers route on these
// to surface operator-actionable messages vs internal failures.
var (
	ErrSchemaMismatch   = errors.New("setupbroker: schema_version mismatch")
	ErrUnknownOperation = errors.New("setupbroker: unknown operation")
	ErrInvalidParams    = errors.New("setupbroker: invalid params for operation")
	ErrOperationNotImpl = errors.New("setupbroker: operation not yet implemented in this build")
)

// DecodeRequest reads a Request from r with strict field validation
// (DisallowUnknownFields). Unknown envelope fields, schema-version
// mismatches, and unknown operation names all reject; the caller is
// expected to surface the error to the wizard as the result struct.
func DecodeRequest(r io.Reader) (*Request, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var req Request
	if err := dec.Decode(&req); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	if req.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: got %d, want %d",
			ErrSchemaMismatch, req.SchemaVersion, SchemaVersion)
	}
	if !knownOperation(req.Operation) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownOperation, req.Operation)
	}
	return &req, nil
}

// EncodeResult writes a Result to w as a single line of compact JSON
// followed by a newline. The single-line format is friendly to
// `journalctl -u ventd-setup --output=json` post-mortems.
func EncodeResult(w io.Writer, res *Result) error {
	res.SchemaVersion = SchemaVersion
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(res)
}

// knownOperation reports whether op is in the closed set returned
// by AllOperations.
func knownOperation(op Operation) bool {
	for _, candidate := range AllOperations() {
		if op == candidate {
			return true
		}
	}
	return false
}

// Handler dispatches a single privileged operation. Implementations
// receive the unmarshalled params struct (cast via the per-operation
// Params* type) and return either a populated Result on success or
// an error. The dispatcher wraps either path back into a Result for
// the wire.
type Handler func(req *Request) (*Result, error)

// Dispatcher routes a Request to the registered Handler for its
// Operation. Operations without a registered Handler fall through to
// ErrOperationNotImpl — Phase A ships every operation as
// not-yet-implemented to keep the wire shape stable while the per-
// operation handlers are filled in across follow-up PRs.
type Dispatcher struct {
	handlers map[Operation]Handler
}

// NewDispatcher returns a Dispatcher with no registered handlers.
// Phase A's main wires every operation to a not-implemented stub so
// a prematurely-routed wizard request fails with a clean error
// rather than panicking.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[Operation]Handler)}
}

// Register attaches a Handler to an Operation. Duplicate registration
// for the same Operation overwrites the prior handler. Returns the
// Dispatcher for fluent chaining.
func (d *Dispatcher) Register(op Operation, h Handler) *Dispatcher {
	d.handlers[op] = h
	return d
}

// Dispatch routes req to its registered Handler. If no Handler is
// registered for the Operation, the dispatcher returns a Result
// whose Error wraps ErrOperationNotImpl + a nil error (the caller
// distinguishes "operation explicitly stubbed" from "broker bug").
func (d *Dispatcher) Dispatch(req *Request) (*Result, error) {
	h, ok := d.handlers[req.Operation]
	if !ok {
		return &Result{
			Operation: string(req.Operation),
			OK:        false,
			Error:     fmt.Errorf("%w: %q", ErrOperationNotImpl, req.Operation).Error(),
		}, nil
	}
	return h(req)
}
