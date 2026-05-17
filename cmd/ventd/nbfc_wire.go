package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/ventd/ventd/internal/hal"
	halnbfc "github.com/ventd/ventd/internal/hal/nbfc"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/watchdog"
)

// registerNBFCBackend probes the live DMI against the upstream
// nbfc-linux catalogue and, on match, opens the EC transport (and
// optional ACPI bridge) before registering the backend with hal.Register.
//
// Every sentinel error from nbfc.Probe is benign in the sense that the
// daemon proceeds without NBFC fan control. The log severity differs by
// remediation:
//
//   - ErrNBFCNoMatch              → INFO  (host isn't a recognised laptop)
//   - ErrNBFCConfigNeedsLuaRuntime → INFO  (Lua deferred to v0.8+)
//   - ErrNBFCNoTransport          → WARN  (operator install: ec_sys modprobe)
//   - ErrNBFCConfigNeedsAcpiBridge → WARN  (operator install: acpi_call DKMS)
//   - any other error             → ERROR (unexpected; check journal)
//
// The doctor's RULE-NBFC-DOCTOR-01 detector surfaces the same match state
// to operators in the web UI regardless of whether the backend registered.
func registerNBFCBackend(logger *slog.Logger, sysRoot string) {
	dmi, err := hwdb.ReadDMI(os.DirFS(sysRoot))
	if err != nil {
		logger.Warn("nbfc: cannot read DMI; backend not registered", "err", err)
		return
	}
	be, err := halnbfc.Probe(dmi)
	switch {
	case err == nil:
		hal.Register(halnbfc.BackendName, be)
		logger.Info("nbfc: backend registered",
			"model", be.Config().NotebookModel,
			"file", be.Filename())
	case errors.Is(err, halnbfc.ErrNBFCNoMatch):
		logger.Info("nbfc: no catalogue match for this DMI; backend not registered")
	case errors.Is(err, halnbfc.ErrNBFCConfigNeedsLuaRuntime):
		logger.Info("nbfc: matched config requires Lua runtime (deferred); backend not registered", "err", err)
	case errors.Is(err, halnbfc.ErrNBFCNoTransport):
		logger.Warn("nbfc: matched a catalogue entry but no EC transport opened; install ec_sys or acpi_call DKMS", "err", err)
	case errors.Is(err, halnbfc.ErrNBFCConfigNeedsAcpiBridge):
		logger.Warn("nbfc: matched config requires acpi_call DKMS module; install via wizard", "err", err)
	default:
		logger.Error("nbfc: backend probe failed; backend not registered", "err", err)
	}
}

// registerNBFCWatchdogEntries enumerates NBFC channels via the HAL
// registry and routes each one through the watchdog so the
// cross-cutting RULE-WD-RESTORE-EXIT safety contract covers NBFC fans
// on every documented shutdown path. Without this wiring NBFC's
// Restore would never fire on graceful exit, leaving laptop fans at
// whatever PWM the daemon last wrote.
//
// Dispatches through wd.RegisterIPMI — the primitive is a generic
// closure-based restore-callback path (named IPMI for historical
// reasons; see RULE-WD-IPMI-ROUTING). NBFC and IPMI share the same
// backend-Restore-via-closure shape; the channel ID is prefixed with
// "nbfc:" so journal lines distinguish the two surfaces.
//
// No-op when the NBFC backend isn't registered (no catalogue match,
// or registerNBFCBackend hasn't been called on this code path —
// e.g. runsetup which only loads HAL for calibration).
func registerNBFCWatchdogEntries(wd *watchdog.Watchdog, logger *slog.Logger) {
	be, ok := hal.Backend(halnbfc.BackendName)
	if !ok {
		return
	}
	channels, err := be.Enumerate(context.Background())
	if err != nil {
		logger.Warn("watchdog: nbfc enumerate failed; NBFC fans will not be routed through watchdog exit",
			"err", err)
		return
	}
	for _, ch := range channels {
		ch := ch
		channelID := halnbfc.BackendName + ":" + ch.ID
		wd.RegisterIPMI(channelID, func() error {
			return be.Restore(ch)
		})
	}
	if len(channels) > 0 {
		logger.Info("watchdog: nbfc channels routed through watchdog exit",
			"count", len(channels))
	}
}
