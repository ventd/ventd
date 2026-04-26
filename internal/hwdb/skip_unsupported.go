package hwdb

import (
	"log/slog"
	"sync"
)

var unsupportedLogged sync.Map

// LogUnsupportedOnce emits one INFO log for an unsupported board, keyed by
// boardID. Subsequent calls with the same boardID are silent. Returns true
// the first time the log fires. RULE-OVERRIDE-UNSUPPORTED-01.
func LogUnsupportedOnce(boardID string, log *slog.Logger) bool {
	_, loaded := unsupportedLogged.LoadOrStore(boardID, struct{}{})
	if loaded {
		return false
	}
	log.Info("This hardware has no Linux fan-control driver. ventd will report sensors only.",
		slog.String("board_id", boardID))
	return true
}

// ShouldSkipCalibration returns true when the resolved profile has
// overrides.unsupported: true, meaning calibration and autocurve
// generation must be skipped. RULE-OVERRIDE-UNSUPPORTED-02.
func ShouldSkipCalibration(ecp *EffectiveControllerProfile) bool {
	return ecp != nil && ecp.Unsupported
}
