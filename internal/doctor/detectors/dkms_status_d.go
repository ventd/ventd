package detectors

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// DKMSExec is the shell-out surface DKMSStatusDetector needs. The
// production wiring uses execDKMSStatus (default below). Tests inject
// a stub that returns canned `dkms status` output without touching
// the host.
type DKMSExec func(ctx context.Context, args ...string) (string, error)

// execDKMSStatus runs `dkms status <args...>` and returns stdout.
// Returns an error wrapping exec.ErrNotFound when dkms isn't on PATH
// — the detector reads that as "DKMS not installed", which is a
// distinct condition from "DKMS installed but a build failed".
func execDKMSStatus(ctx context.Context, args ...string) (string, error) {
	if _, err := exec.LookPath("dkms"); err != nil {
		return "", fmt.Errorf("dkms not on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, "dkms", append([]string{"status"}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("dkms status: %w", err)
	}
	return string(out), nil
}

// DKMSStatusDetector parses `dkms status` for entries marked
// "failed" / "broken" — typically the auto-rebuild on a kernel
// update couldn't compile the module against the new kernel
// headers. The detector surfaces these as Blocker Facts because the
// running kernel almost certainly doesn't have the module loaded;
// the operator's fans either run at firmware default or, on chips
// where firmware default is "off", don't run at all.
//
// "DKMS not installed" is NOT surfaced here — that's the preflight
// detector's job (ReasonDKMSMissing). This detector only runs when
// dkms is present.
type DKMSStatusDetector struct {
	// Exec is the shell-out surface; production uses execDKMSStatus.
	Exec DKMSExec
}

// NewDKMSStatusDetector constructs a detector with the production
// shell-out. Tests pass an explicit DKMSExec stub.
func NewDKMSStatusDetector(exec DKMSExec) *DKMSStatusDetector {
	if exec == nil {
		exec = execDKMSStatus
	}
	return &DKMSStatusDetector{Exec: exec}
}

// Name returns the stable detector ID.
func (d *DKMSStatusDetector) Name() string { return "dkms_status" }

// Probe runs `dkms status` and emits one Fact per failed entry.
// Returns no facts (and no error) when dkms is absent — that's the
// preflight detector's territory.
func (d *DKMSStatusDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := d.Exec(ctx)
	if err != nil {
		// dkms-not-on-PATH and exec failures both surface as "no
		// signal" — the preflight detector covers absence; surfacing
		// an exec error as a Fact would cross-class noise. Caller
		// gets a nil error so the runner doesn't log a DetectorError.
		return nil, nil
	}

	now := timeNowFromDeps(deps)
	var facts []doctor.Fact
	for _, line := range strings.Split(out, "\n") {
		mod, kver, status := parseDKMSStatusLine(line)
		if mod == "" {
			continue
		}
		if !isDKMSFailureStatus(status) {
			continue
		}
		facts = append(facts, doctor.Fact{
			Detector: d.Name(),
			Severity: doctor.SeverityBlocker,
			Class:    recovery.ClassDKMSBuildFailed,
			Title:    fmt.Sprintf("DKMS reports %s failed for kernel %s", mod, kver),
			Detail: fmt.Sprintf(
				"`dkms status` line: %q. The auto-rebuild on the most recent kernel update did not produce a loadable module. The running kernel is likely without %s loaded — fans may be running on firmware default (or off, on chips where default-off is the firmware policy). Rebuild via `sudo dkms install %s -k $(uname -r)`; check `dkms autoinstall --kernelver $(uname -r)` output for build errors.",
				strings.TrimSpace(line), mod, mod,
			),
			EntityHash: doctor.HashEntity("dkms_status", mod, kver),
			Observed:   now,
			Journal:    []string{strings.TrimSpace(line)},
		})
	}
	return facts, nil
}

// parseDKMSStatusLine extracts (module, kernel-version, status) from
// one line of `dkms status` output. Handles both the legacy comma-
// separated DKMS 2.x format and the newer DKMS 3.x format with
// slash-separated module/version. Returns ("", "", "") for any line
// that doesn't look like a status entry (blank, header, error).
//
// Tested-against forms:
//
//	"nct6687/0.5, 6.8.0-49-generic, x86_64: installed"
//	"nct6687/0.5: added"
//	"nct6687/0.5, 6.8.0-49-generic, x86_64: failed"
//	"corsair-cpro/1.0, 6.10.0-arch1-1, x86_64: built"
func parseDKMSStatusLine(line string) (mod, kver, status string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", ""
	}
	colon := strings.LastIndex(line, ":")
	if colon < 0 {
		return "", "", ""
	}
	head := strings.TrimSpace(line[:colon])
	status = strings.TrimSpace(line[colon+1:])

	// head is "<mod>/<ver>, <kver>, <arch>" or "<mod>/<ver>".
	parts := strings.SplitN(head, ",", 3)
	first := strings.TrimSpace(parts[0])
	slash := strings.Index(first, "/")
	if slash < 0 {
		mod = first
	} else {
		mod = first[:slash]
	}
	if len(parts) >= 2 {
		kver = strings.TrimSpace(parts[1])
	}
	return mod, kver, status
}

// isDKMSFailureStatus reports whether the parsed status word indicates
// a DKMS-side failure that doctor should surface. "failed" is the
// canonical DKMS 3.x failure word; "broken" appears on some forks; we
// match both. "added" / "built" / "installed" are non-failure states.
func isDKMSFailureStatus(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	return s == "failed" || s == "broken" || strings.HasPrefix(s, "failed ")
}
