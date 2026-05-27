// Package budget assembles the per-tick acoustic dBA-budget input the
// confidence-gated controller's cost gate consumes (controller.AcousticBudget):
// host loudness composed from every fan's measured RPM via the R33
// no-microphone psychoacoustic proxy, the candidate channel's marginal
// dBA-per-PWM, and the operator's dBA cap — plus the per-host R30 K_cal
// mic-calibration offset when present.
//
// It owns the fan-shape classification (curated hwdb FanProfile catalog
// with a name-hint heuristic fallback) and the mtime-gated K_cal cache.
// The board-DMI catalog match that selects the active FanProfile entry
// lives in the daemon composition root, which publishes the result here
// via SetFanProfileCatalog; the matching itself is an hwdb concern, not
// an acoustic one.
//
// This math previously lived in cmd/ventd, where it could be neither
// imported, reused, nor unit-tested (R6b of the architecture review).
package budget
