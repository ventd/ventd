package nbfc

import (
	"errors"
	"fmt"

	"github.com/ventd/ventd/internal/acpi"
	"github.com/ventd/ventd/internal/ec"
	"github.com/ventd/ventd/internal/hwdb"
	nbfcdb "github.com/ventd/ventd/internal/hwdb/nbfc"
)

// Probe is the construction entry point the daemon's main.go calls
// to wire the NBFC backend. It:
//
//  1. Matches the live DMI against the embedded nbfc catalogue. No
//     match → ErrNBFCNoMatch (clean exit, not an error).
//  2. Refuses Lua-driven configs (no runtime in v0.8.0).
//  3. For configs that touch EC registers, opens the EC transport
//     via ec.Available(). No transport → ErrNBFCNoTransport with the
//     wrapped cause from the underlying transports' failure chain.
//  4. For configs that invoke ACPI methods, opens the acpi_call
//     bridge via acpi.Available(). Bridge missing →
//     ErrNBFCConfigNeedsAcpiBridge so the doctor / install surface
//     points the operator at the acpi_call DKMS install path.
//  5. Constructs the Backend with the matched config + the EC
//     transport (allowlist-wrapped) + the ACPI bridge (allowlist-
//     scoped to AcpiMethodsUsed()).
//
// Every "graceful" outcome (no match, Lua, ACPI bridge missing,
// transport missing) returns a typed sentinel so the daemon's
// caller can branch via errors.Is to route the right doctor card
// + install pathway. Only an unexpected internal error (catalog
// failed to parse, transport opened then immediately failed) is a
// real error in the journal-trail sense.
func Probe(dmi hwdb.DMI) (*Backend, error) {
	cat, err := nbfcdb.LoadCatalog()
	if err != nil {
		return nil, fmt.Errorf("nbfc: load catalog: %w", err)
	}
	entry, tier := nbfcdb.Match(cat, dmi)
	if entry == nil {
		return nil, ErrNBFCNoMatch
	}
	_ = tier
	cfg := entry.Config
	if cfg.UsesLua() {
		return nil, ErrNBFCConfigNeedsLuaRuntime
	}

	// Open the EC transport when the config touches any register.
	var transport ec.Transport
	if len(cfg.RegistersUsed()) > 0 {
		t, err := ec.Available()
		if err != nil {
			if errors.Is(err, ec.ErrECNotAvailable) {
				return nil, fmt.Errorf("%w: %v", ErrNBFCNoTransport, err)
			}
			return nil, fmt.Errorf("nbfc: open EC: %w", err)
		}
		transport = t
	}

	// Open the ACPI bridge when the config invokes any method.
	var bridge *acpi.Bridge
	if cfg.UsesACPI() {
		if err := acpi.Available(); err != nil {
			if transport != nil {
				_ = transport.Close()
			}
			if errors.Is(err, acpi.ErrACPICallNotLoaded) {
				return nil, fmt.Errorf("%w: %v", ErrNBFCConfigNeedsAcpiBridge, err)
			}
			return nil, fmt.Errorf("nbfc: open ACPI bridge: %w", err)
		}
		bridge = acpi.New(cfg.AcpiMethodsUsed())
	}

	b, err := New(ProbeOpts{
		Config:    cfg,
		Filename:  entry.Filename,
		Transport: transport,
		ACPI:      bridge,
	})
	if err != nil {
		if transport != nil {
			_ = transport.Close()
		}
		return nil, err
	}
	return b, nil
}
