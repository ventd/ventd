// Package handlers contains the per-Operation Handlers the
// cmd/ventd-setup binary registers with its dispatcher. Each handler
// is a tightly-scoped privileged operation; the package as a whole
// is the realisation of spec-v0_6_0-split-daemon's
// "all-privileged-operations-here" surface.
//
// Handlers MUST:
//   - Validate every input field; reject unknown / out-of-range.
//   - Inject filesystem + exec deps so tests can run hermetically.
//   - Write a populated *setupbroker.Result with OK=true on success
//     OR OK=false + Error on per-operation failure. Errors returned
//     from the handler are reserved for genuine internal failures
//     (panic recovery, dispatcher state corruption).
//   - Populate AuditSummary with a one-line operator-readable sentence.
package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/ventd/ventd/internal/iox"
	"github.com/ventd/ventd/internal/setupbroker"
)

// LoadModuleParams is the input schema for setupbroker.OpLoadModule.
// Strict-decoded via json.Decoder.DisallowUnknownFields in the
// handler; unknown fields reject before any side effect runs.
type LoadModuleParams struct {
	// Module is the kernel module name (e.g. "nct6687"). Required;
	// must match moduleNameRE.
	Module string `json:"module"`

	// Args is the optional modprobe argument list (e.g.
	// ["force_id=0x8688"]). Each entry is passed verbatim to
	// modprobe; the broker's allowlist validation is upstream
	// (RULE-MODPROBE-OPTIONS-01) — at this layer we just pass
	// through what the dispatcher's gating already approved.
	Args []string `json:"args,omitempty"`

	// PersistAtBoot, when true, also writes
	// /etc/modules-load.d/ventd-<module>.conf so the module is
	// re-loaded after reboot. The wizard's typical request sets
	// this to true; ad-hoc smoke tests can leave it false.
	PersistAtBoot bool `json:"persist_at_boot,omitempty"`
}

// moduleNameRE locks the operator-supplied module string to the
// kernel-side legal character set: [A-Za-z0-9_-] up to 64 chars.
// Rejects path traversal, shell metacharacters, and the spurious
// dot-leading inputs that have caused historical modprobe foot-guns.
var moduleNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// LoadModuleDeps lets tests substitute the modprobe + filesystem
// surface. Production wiring uses RealLoadModuleDeps().
type LoadModuleDeps struct {
	// Modprobe runs `/sbin/modprobe <module> <args...>`. Returns the
	// combined stdout+stderr + the exec error (if any).
	Modprobe func(module string, args ...string) ([]byte, error)

	// WriteFile is the modules-load.d sink. Production points at
	// iox.WriteFile (atomic + parent-dir creation). Tests inject
	// an in-memory recorder.
	WriteFile func(path string, data []byte, mode os.FileMode) error

	// ModulesLoadDir is the directory the persist-at-boot file
	// lands in (production: /etc/modules-load.d). Tests override
	// with a tempdir.
	ModulesLoadDir string
}

// RealLoadModuleDeps wires the production modprobe + iox.WriteFile.
// /etc/modules-load.d is the canonical systemd path consulted by
// systemd-modules-load.service at boot.
func RealLoadModuleDeps() LoadModuleDeps {
	return LoadModuleDeps{
		Modprobe: func(module string, args ...string) ([]byte, error) {
			cmdArgs := append([]string{module}, args...)
			cmd := exec.Command("/sbin/modprobe", cmdArgs...)
			return cmd.CombinedOutput()
		},
		WriteFile:      iox.WriteFile,
		ModulesLoadDir: "/etc/modules-load.d",
	}
}

// LoadModuleHandler is the broker.Handler for OpLoadModule. Returns
// a populated *Result with OK=true on success, OK=false on validation
// or modprobe failure. Internal errors (panic recovery) propagate as
// a non-nil error.
func LoadModuleHandler(deps LoadModuleDeps) setupbroker.Handler {
	return func(req *setupbroker.Request) (*setupbroker.Result, error) {
		var params LoadModuleParams
		// DisallowUnknownFields rejects typos / extra keys with a
		// clean error rather than silently dropping them. The
		// envelope-level decoder already did the same; doing it
		// again here makes per-operation params self-validating
		// without depending on caller setup.
		dec := json.NewDecoder(strings.NewReader(string(req.Params)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&params); err != nil {
			return paramErrResult(req, fmt.Errorf("decode params: %w", err))
		}
		if !moduleNameRE.MatchString(params.Module) {
			return paramErrResult(req, fmt.Errorf("invalid module name %q", params.Module))
		}
		// Defensive arg validation — refuse any arg containing a
		// shell metacharacter even though we exec without a shell.
		// Future shell-mode dispatch refactors won't accidentally
		// open a shell-injection window for these inputs.
		for i, a := range params.Args {
			if strings.ContainsAny(a, ";|&`$()<>\n\r") {
				return paramErrResult(req,
					fmt.Errorf("args[%d] contains shell metacharacter", i))
			}
		}

		// 1. modprobe the module.
		out, err := deps.Modprobe(params.Module, params.Args...)
		if err != nil {
			return &setupbroker.Result{
				Operation:    string(req.Operation),
				OK:           false,
				Error:        fmt.Sprintf("modprobe %s: %v: %s", params.Module, err, strings.TrimSpace(string(out))),
				AuditSummary: fmt.Sprintf("modprobe failed for %q", params.Module),
			}, nil
		}

		// 2. Persist for boot if requested.
		if params.PersistAtBoot {
			path := deps.ModulesLoadDir + "/ventd-" + params.Module + ".conf"
			body := []byte(params.Module + "\n")
			if err := deps.WriteFile(path, body, 0o644); err != nil {
				// Module is loaded in this kernel; persistence
				// failure is partial success worth surfacing
				// as not-OK so the wizard knows a reboot will
				// drop the module again.
				return &setupbroker.Result{
					Operation:    string(req.Operation),
					OK:           false,
					Error:        fmt.Sprintf("write %s: %v", path, err),
					AuditSummary: fmt.Sprintf("loaded %q but persistence write failed", params.Module),
				}, nil
			}
		}

		summary := fmt.Sprintf("loaded module %q", params.Module)
		if params.PersistAtBoot {
			summary += " (persisted)"
		}
		return &setupbroker.Result{
			Operation:    string(req.Operation),
			OK:           true,
			AuditSummary: summary,
		}, nil
	}
}

// paramErrResult is the canonical "input rejected" Result shape.
// The handler returns these as (result, nil) so the dispatcher
// surfaces the operator-readable error to the wizard instead of
// short-circuiting to a generic broker-internal-error template.
func paramErrResult(req *setupbroker.Request, err error) (*setupbroker.Result, error) {
	return &setupbroker.Result{
		Operation:    string(req.Operation),
		OK:           false,
		Error:        fmt.Sprintf("%s: %v", setupbroker.ErrInvalidParams, err),
		AuditSummary: "params validation failed",
	}, nil
}
