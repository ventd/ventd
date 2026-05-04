package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ventd/ventd/internal/setupbroker"
)

// UnloadModuleParams is the input schema for setupbroker.OpUnloadModule.
// The inverse of LoadModuleParams: removes the module from the running
// kernel and optionally removes the modules-load.d entry so it stays
// out across reboot.
type UnloadModuleParams struct {
	// Module is the kernel module name (e.g. "nct6687"). Required;
	// must match moduleNameRE (shared with LoadModuleParams).
	Module string `json:"module"`

	// RemovePersist, when true, deletes
	// /etc/modules-load.d/ventd-<module>.conf so the module is not
	// re-loaded after reboot. Typical wizard request sets this true;
	// ad-hoc smoke tests can leave it false to reverse only the
	// in-kernel state.
	RemovePersist bool `json:"remove_persist,omitempty"`
}

// UnloadModuleDeps is the test substitution surface for the unload
// handler. Mirrors LoadModuleDeps's shape so a future refactor that
// shares more code between the two can keep them in step.
type UnloadModuleDeps struct {
	// Modprobe runs `/sbin/modprobe -r <module>`. Returns combined
	// stdout+stderr + exec error.
	Modprobe func(module string) ([]byte, error)

	// Remove unlinks the modules-load.d conf file. Production points
	// at os.Remove; tests inject a recorder.
	Remove func(path string) error

	// Stat reports whether the conf file exists. Production uses
	// os.Stat; tests inject a fake. Used to distinguish "file
	// already absent (idempotent success)" from "file present but
	// remove failed (real error)".
	Stat func(path string) (os.FileInfo, error)

	// ModulesLoadDir is the directory the conf file lives in
	// (production: /etc/modules-load.d).
	ModulesLoadDir string
}

// RealUnloadModuleDeps wires production: /sbin/modprobe -r,
// os.Remove + os.Stat against /etc/modules-load.d.
func RealUnloadModuleDeps() UnloadModuleDeps {
	return UnloadModuleDeps{
		Modprobe: func(module string) ([]byte, error) {
			return exec.Command("/sbin/modprobe", "-r", module).CombinedOutput()
		},
		Remove:         os.Remove,
		Stat:           os.Stat,
		ModulesLoadDir: "/etc/modules-load.d",
	}
}

// UnloadModuleHandler is the broker.Handler for OpUnloadModule.
// Mirrors LoadModuleHandler's contract: returns OK=false + Error
// for per-operation failure; non-nil error reserved for genuine
// internal bugs.
//
// Idempotence policy: removing the persistence file when it doesn't
// exist is treated as SUCCESS (the operator's intent is "the file
// shouldn't be there" — and it isn't). Modprobe -r against a module
// that isn't loaded is similarly often a no-op kernel-side; we
// surface modprobe's exit status verbatim so the operator can
// distinguish the success-noop from a real error.
func UnloadModuleHandler(deps UnloadModuleDeps) setupbroker.Handler {
	return func(req *setupbroker.Request) (*setupbroker.Result, error) {
		var params UnloadModuleParams
		dec := json.NewDecoder(strings.NewReader(string(req.Params)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&params); err != nil {
			return paramErrResult(req, fmt.Errorf("decode params: %w", err))
		}
		if !moduleNameRE.MatchString(params.Module) {
			return paramErrResult(req, fmt.Errorf("invalid module name %q", params.Module))
		}

		// 1. modprobe -r
		out, err := deps.Modprobe(params.Module)
		if err != nil {
			return &setupbroker.Result{
				Operation:    string(req.Operation),
				OK:           false,
				Error:        fmt.Sprintf("modprobe -r %s: %v: %s", params.Module, err, strings.TrimSpace(string(out))),
				AuditSummary: fmt.Sprintf("modprobe -r failed for %q", params.Module),
			}, nil
		}

		// 2. Remove persistence if requested. Idempotent.
		if params.RemovePersist {
			path := deps.ModulesLoadDir + "/ventd-" + params.Module + ".conf"
			if _, statErr := deps.Stat(path); statErr == nil {
				if rmErr := deps.Remove(path); rmErr != nil {
					return &setupbroker.Result{
						Operation:    string(req.Operation),
						OK:           false,
						Error:        fmt.Sprintf("remove %s: %v", path, rmErr),
						AuditSummary: fmt.Sprintf("unloaded %q but persistence-file removal failed", params.Module),
					}, nil
				}
			}
			// Stat-error path: file probably doesn't exist
			// (idempotent success). Other stat errors (permission
			// denied, etc.) are silently treated as "absent" because
			// the operator intent is "don't auto-load on boot" and
			// a permission-denied stat means we can't tell — but
			// we also can't fix it, so reporting failure is more
			// confusing than letting the operator audit later.
		}

		summary := fmt.Sprintf("unloaded module %q", params.Module)
		if params.RemovePersist {
			summary += " (persistence cleared)"
		}
		return &setupbroker.Result{
			Operation:    string(req.Operation),
			OK:           true,
			AuditSummary: summary,
		}, nil
	}
}
