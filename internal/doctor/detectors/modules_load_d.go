package detectors

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// ModulesLoadFS is the read-only filesystem surface
// ModulesLoadDetector needs. Production wires the live filesystem;
// tests inject testing/fstest.MapFS.
type ModulesLoadFS interface {
	// ReadFile returns the bytes of name. Implementations should
	// return os.ErrNotExist (or a wrapper) when the file is absent
	// so the detector can distinguish "removed" from "I/O error".
	ReadFile(name string) ([]byte, error)
}

// osFS adapts the real filesystem to ModulesLoadFS so production
// code uses the same interface tests stub.
type osFS struct{}

func (osFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

// modulesLoadConfPath is the canonical drop-in path written by
// PR-D's persistModuleLoad step. Module-load.d entries are stable
// across distro family (Debian/Ubuntu/Fedora/Arch/openSUSE).
const modulesLoadDir = "/etc/modules-load.d"

func modulesLoadConfPath(module string) string {
	return filepath.Join(modulesLoadDir, "ventd-"+module+".conf")
}

// ModulesLoadDetector verifies /etc/modules-load.d/ventd-<mod>.conf
// still exists with the expected content. The wizard writes this
// file at install time so the OOT module auto-loads on every boot;
// a sysadmin removing or editing it would silently break the next
// reboot's fan control.
//
// "File missing" surfaces as a Warning (the running daemon still
// has the module loaded; the issue is for next boot). "Content
// drifted" also Warning — could be intentional operator edit, but
// worth flagging because the wizard's idempotent re-run would
// overwrite it.
type ModulesLoadDetector struct {
	// Modules are the OOT modules whose drop-ins should exist.
	// Production wires from the wizard's resolved DriverNeed list.
	Modules []string

	// FS is the read-only filesystem surface. Defaults to osFS{}.
	FS ModulesLoadFS
}

// NewModulesLoadDetector constructs a detector for the given modules.
// fs nil means "use the real filesystem".
func NewModulesLoadDetector(modules []string, mfs ModulesLoadFS) *ModulesLoadDetector {
	if mfs == nil {
		mfs = osFS{}
	}
	return &ModulesLoadDetector{Modules: modules, FS: mfs}
}

// Name returns the stable detector ID.
func (d *ModulesLoadDetector) Name() string { return "modules_load" }

// Probe checks every module's drop-in file. Emits a Warning Fact for
// each missing or content-drifted entry.
func (d *ModulesLoadDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := timeNowFromDeps(deps)

	var facts []doctor.Fact
	for _, mod := range d.Modules {
		path := modulesLoadConfPath(mod)
		raw, err := d.FS.ReadFile(path)
		if err != nil {
			if isNotExistErr(err) {
				facts = append(facts, doctor.Fact{
					Detector: d.Name(),
					Severity: doctor.SeverityWarning,
					Class:    recovery.ClassUnknown,
					Title:    fmt.Sprintf("Module-load drop-in for %s is missing", mod),
					Detail: fmt.Sprintf(
						"%s is absent. The module is currently loaded so live operation continues, but the next reboot won't auto-load %s — fans will fall back to firmware default. Re-run the wizard or `sudo tee %s <<<%s` to restore.",
						path, mod, path, mod,
					),
					EntityHash: doctor.HashEntity("modules_load_missing", mod),
					Observed:   now,
				})
				continue
			}
			// Other I/O errors (e.g. permission denied during
			// unprivileged doctor run) — surface as a Warning so the
			// operator knows we couldn't verify, not silently pass.
			facts = append(facts, doctor.Fact{
				Detector: d.Name(),
				Severity: doctor.SeverityWarning,
				Class:    recovery.ClassUnknown,
				Title:    fmt.Sprintf("Cannot verify module-load drop-in for %s", mod),
				Detail: fmt.Sprintf(
					"Reading %s failed: %v. If you're running `ventd doctor` as a non-root user this is expected — RULE-DOCTOR-04 graceful degrade. Re-run as root for the full check.",
					path, err,
				),
				EntityHash: doctor.HashEntity("modules_load_unreadable", mod),
				Observed:   now,
			})
			continue
		}

		// Content check: the wizard writes the bare module name on
		// its own line (with a trailing newline). Allow leading/
		// trailing whitespace + optional comment lines starting with
		// '#' so an operator's annotation isn't treated as drift.
		if !contentMentionsModule(raw, mod) {
			facts = append(facts, doctor.Fact{
				Detector: d.Name(),
				Severity: doctor.SeverityWarning,
				Class:    recovery.ClassUnknown,
				Title:    fmt.Sprintf("Module-load drop-in for %s no longer references the module", mod),
				Detail: fmt.Sprintf(
					"%s exists but does not contain a non-comment line naming %s. Auto-load on next reboot is not guaranteed. The wizard's idempotent re-run will overwrite this file with %s on its own line.",
					path, mod, mod,
				),
				EntityHash: doctor.HashEntity("modules_load_drifted", mod),
				Observed:   now,
			})
		}
	}
	return facts, nil
}

// isNotExistErr returns true for "file does not exist" errors from
// either os.ReadFile or fstest.MapFS (the latter wraps with
// fs.ErrNotExist). errors.Is would also work; we're explicit because
// the detector wants to distinguish missing-file from other I/O
// failures.
func isNotExistErr(err error) bool {
	return os.IsNotExist(err) ||
		(err != nil && (strings.Contains(err.Error(), "file does not exist") ||
			strings.Contains(err.Error(), "no such file")))
}

// contentMentionsModule reports whether the drop-in's content has
// at least one non-comment, non-empty line that contains the module
// name as a whitespace-bounded token.
func contentMentionsModule(raw []byte, module string) bool {
	if module == "" {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Token-bounded match — a substring match would mistake
		// "nct6687d" for "nct6687". Split on whitespace + commas
		// (the latter for distros that allow comma-separated lists).
		fields := strings.FieldsFunc(trimmed, func(r rune) bool {
			return r == ' ' || r == '\t' || r == ','
		})
		for _, f := range fields {
			if f == module {
				return true
			}
		}
	}
	return false
}

// Compile-time check that osFS satisfies the interface. Pin so a
// future signature change to ModulesLoadFS forces the adapter to
// update too.
var _ ModulesLoadFS = osFS{}
var _ fs.ReadFileFS = (osFSReadFileFS{})

// osFSReadFileFS is the io/fs.ReadFileFS adapter ModulesLoadDetector
// could optionally consume — kept here as a type-level reminder that
// the detector's narrow ReadFile-only interface is by design (fs.FS
// would force callers to thread a root path that doesn't apply to
// /etc/modules-load.d/ absolute paths).
type osFSReadFileFS struct{}

func (osFSReadFileFS) Open(name string) (fs.File, error) { return os.Open(name) }
func (osFSReadFileFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}
