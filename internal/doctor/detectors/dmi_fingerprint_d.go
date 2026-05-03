package detectors

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/recovery"
)

// DMIFingerprintDetector reports the live DMI fingerprint and whether
// the daemon has a catalog match for it. RULE-DOCTOR-05 says the
// detector MUST use the same hwdb.Fingerprint(dmi) code path the
// daemon uses; this is satisfied by calling into hwdb.ReadDMI +
// hwdb.Fingerprint directly.
//
// Match status comes from the wiring layer — the daemon resolves
// the CatalogMatch at startup and passes the result into the
// detector via Matched / MatchedBoardName. CLI invocations (which
// don't have the daemon's resolved match) construct the detector
// with Matched=false; the result is still informational.
//
// Severity policy:
//   - Matched=true                → OK (informational; show fingerprint + board)
//   - Matched=false + fingerprint → Warning ("not in catalog; generic mode")
//   - empty fingerprint          → no fact (graceful degrade for sandboxed runs)
type DMIFingerprintDetector struct {
	// FS is the root filesystem the fingerprint is read from.
	// Production passes os.DirFS("/"); tests inject testing/fstest.MapFS.
	FS fs.FS

	// Matched reports whether the daemon's catalog resolution found
	// a board profile for the running fingerprint.
	Matched bool

	// MatchedBoardName is the human-readable name from the catalog
	// match (e.g. "ASUS ROG STRIX Z790-E"). Empty when Matched=false.
	MatchedBoardName string
}

// NewDMIFingerprintDetector constructs a detector for the given
// match state. fsys nil → live filesystem.
func NewDMIFingerprintDetector(fsys fs.FS, matched bool, boardName string) *DMIFingerprintDetector {
	if fsys == nil {
		fsys = os.DirFS("/")
	}
	return &DMIFingerprintDetector{
		FS:               fsys,
		Matched:          matched,
		MatchedBoardName: boardName,
	}
}

// Name returns the stable detector ID.
func (d *DMIFingerprintDetector) Name() string { return "dmi_fingerprint" }

// Probe reads DMI via hwdb.ReadDMI, computes the fingerprint, and
// emits one Fact (OK or Warning depending on Matched).
func (d *DMIFingerprintDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dmi, err := hwdb.ReadDMI(d.FS)
	if err != nil {
		// ReadDMI returns nil for missing files; a non-nil error
		// here means a deeper /proc/cpuinfo parse problem. Surface
		// as Warning so the operator knows we couldn't fingerprint.
		now := timeNowFromDeps(deps)
		return []doctor.Fact{{
			Detector:   d.Name(),
			Severity:   doctor.SeverityWarning,
			Class:      recovery.ClassUnknown,
			Title:      "Cannot read DMI fingerprint inputs",
			Detail:     fmt.Sprintf("hwdb.ReadDMI: %v. Catalog match prediction will fall through to tier-3 generic mode.", err),
			EntityHash: doctor.HashEntity("dmi_unreadable"),
			Observed:   now,
		}}, nil
	}

	fp := hwdb.Fingerprint(dmi)
	if fp == "" {
		// Sandbox / container with no /sys/class/dmi/id — emit nothing.
		return nil, nil
	}

	now := timeNowFromDeps(deps)
	if d.Matched {
		return []doctor.Fact{{
			Detector: d.Name(),
			Severity: doctor.SeverityOK,
			Class:    recovery.ClassUnknown,
			Title:    fmt.Sprintf("Catalog match: %s", d.MatchedBoardName),
			Detail: fmt.Sprintf(
				"DMI fingerprint %s matched catalog board profile %q. The daemon runs with board-specific quirks + curve overrides applied.",
				fp, d.MatchedBoardName,
			),
			EntityHash: doctor.HashEntity("dmi_match", fp),
			Observed:   now,
		}}, nil
	}

	return []doctor.Fact{{
		Detector: d.Name(),
		Severity: doctor.SeverityWarning,
		Class:    recovery.ClassUnknown,
		Title:    "Board not in hwdb catalog — running in generic mode",
		Detail: fmt.Sprintf(
			"DMI fingerprint %s. The daemon falls back to tier-3 generic profiles; feature confidence is reduced. Contribute a board profile via the wizard's `Capture` flow if you'd like specific quirks/overrides — see https://github.com/ventd/ventd/wiki/contributing-board-profiles.",
			fp,
		),
		EntityHash: doctor.HashEntity("dmi_no_match", fp),
		Observed:   now,
	}}, nil
}
