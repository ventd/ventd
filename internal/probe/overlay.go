package probe

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ventd/ventd/internal/hwdb"
)

// applyOverlay attempts to match the current hardware fingerprint against the
// catalog and annotate ControllableChannels with capability hints (§4.4, §6.1).
//
// A catalog miss is not an error — downstream code reads channels uniformly
// regardless of CatalogMatch == nil (RULE-PROBE-05).
func (p *prober) applyOverlay(_ context.Context, r *ProbeResult) (*CatalogMatch, []Diagnostic) {
	var diags []Diagnostic

	root := p.cfg.RootFS
	if root == nil {
		root = os.DirFS("/")
	}

	dmi, err := hwdb.ReadDMI(root)
	if err != nil {
		diags = append(diags, Diagnostic{
			Severity: "warning",
			Code:     "PROBE-OVERLAY-DMI-FAIL",
			Message:  "DMI read failed; catalog overlay skipped: " + err.Error(),
		})
		return nil, diags
	}

	cat, err := hwdb.LoadCatalog()
	if err != nil {
		diags = append(diags, Diagnostic{
			Severity: "warning",
			Code:     "PROBE-OVERLAY-CATALOG-FAIL",
			Message:  "catalog load failed; overlay skipped: " + err.Error(),
		})
		return nil, diags
	}

	// Build DMIFingerprint for the matcher. bios_version is not in hwdb.DMI so
	// we read it separately.
	biosVersion, _ := readTrimmed(root, "sys/class/dmi/id/bios_version")
	fp := hwdb.DMIFingerprint{
		SysVendor:    dmi.SysVendor,
		ProductName:  dmi.ProductName,
		BoardVendor:  dmi.BoardVendor,
		BoardName:    dmi.BoardName,
		BoardVersion: dmi.BoardVersion,
		BiosVersion:  biosVersion,
	}

	ecp, matchErr := hwdb.MatchV1(cat, "", fp)
	if matchErr != nil || ecp == nil {
		// No match — not an error (RULE-PROBE-05).
		fingerprint := hwdb.Fingerprint(dmi)
		diags = append(diags, Diagnostic{
			Severity: "info",
			Code:     "PROBE-OVERLAY-NO-MATCH",
			Message:  fmt.Sprintf("no catalog match for fingerprint %q; using default-paranoid defaults", fingerprint),
		})
		return nil, diags
	}

	fingerprint := hwdb.Fingerprint(dmi)
	match := &CatalogMatch{
		Matched:     true,
		Fingerprint: fingerprint,
	}

	// Annotate channels: set CapabilityHint from the matched driver profile.
	capHint := string(ecp.Capability)
	profileName := ecp.Module
	match.OverlayApplied = append(match.OverlayApplied, profileName)
	for i := range r.ControllableChannels {
		if r.ControllableChannels[i].CapabilityHint == "" {
			r.ControllableChannels[i].CapabilityHint = capHint
		}
	}

	diags = append(diags, Diagnostic{
		Severity: "info",
		Code:     "PROBE-OVERLAY-MATCH",
		Message:  "catalog match: " + strings.Join(match.OverlayApplied, ","),
		Context:  map[string]string{"fingerprint": match.Fingerprint},
	})
	return match, diags
}
