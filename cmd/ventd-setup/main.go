// ventd-setup is the v0.6.0 split-daemon setup-side service.
// systemd starts it on demand from the wizard; ventd-setup reads a
// request from /run/ventd/setup-request.json (override via
// --request), dispatches it through internal/setupbroker, writes the
// result to /run/ventd/setup-result.json (override via --result),
// and exits.
//
// Phase A (this binary's first ship): every operation handler is
// stubbed via the broker's not-implemented fall-through. The binary
// builds + installs + can be invoked from systemd; the actual
// privileged operations land in follow-up PRs as the wizard's
// per-step routing through this broker is wired (Phase B).
//
// Logging is stderr → systemd journal (Type=oneshot inherits stderr).
// Operator-facing diagnostics live there; the wire-shape result
// file at --result is the wizard's canonical input.
//
// See specs/spec-v0_6_0-split-daemon.md for the architecture.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/ventd/ventd/internal/setupbroker"
)

const (
	defaultRequestPath = "/run/ventd/setup-request.json"
	defaultResultPath  = "/run/ventd/setup-result.json"
)

func main() {
	var (
		requestPath = flag.String("request", defaultRequestPath,
			"path to the wizard's request JSON")
		resultPath = flag.String("result", defaultResultPath,
			"path to write the result JSON")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if exitCode := run(logger, *requestPath, *resultPath); exitCode != 0 {
		os.Exit(exitCode)
	}
}

// run is the testable entry point. Returns a process exit code.
//
//	0 — request decoded + dispatched (regardless of operation outcome;
//	    the result file carries the per-operation OK / error)
//	1 — request file unreadable or schema-invalid; no result file written
//	2 — result file unwritable (rare; fs full / permission denied)
func run(logger *slog.Logger, requestPath, resultPath string) int {
	requestBytes, err := os.ReadFile(requestPath)
	if err != nil {
		logger.Error("read request file", "path", requestPath, "err", err)
		return 1
	}

	req, err := setupbroker.DecodeRequest(bytes.NewReader(requestBytes))
	if err != nil {
		logger.Error("decode request", "err", err)
		return 1
	}

	logger.Info("dispatch start",
		"operation", req.Operation,
		"wizard_session", req.Audit.WizardSessionID,
		"requested_by", req.Audit.RequestedBy,
		"client_ip", req.Audit.ClientIP)

	disp := setupbroker.NewDispatcher()
	// Phase A: no operations implemented yet; every Dispatch falls
	// through to ErrOperationNotImpl. Phase B fills these in via
	// disp.Register(setupbroker.OpInstallOOTDriver, handler), etc.

	result, dispatchErr := disp.Dispatch(req)
	if dispatchErr != nil {
		// The dispatcher returns errors only for genuine broker bugs
		// (panic recovery, internal state corruption). Per-operation
		// failures surface in result.Error with result.OK=false.
		logger.Error("dispatch error", "operation", req.Operation, "err", dispatchErr)
		result = &setupbroker.Result{
			Operation: string(req.Operation),
			OK:        false,
			Error:     fmt.Sprintf("dispatcher error: %v", dispatchErr),
		}
	}

	out, err := os.OpenFile(resultPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		logger.Error("write result file", "path", resultPath, "err", err)
		return 2
	}
	defer func() { _ = out.Close() }()
	if err := setupbroker.EncodeResult(out, result); err != nil {
		logger.Error("encode result", "err", err)
		return 2
	}

	logger.Info("dispatch complete",
		"operation", req.Operation,
		"ok", result.OK,
		"audit_summary", result.AuditSummary)
	return 0
}
