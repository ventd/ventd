# RULE-ENVELOPE-05: Absolute temperature abort fires when any sensor exceeds Tjmax minus TAbsOffsetBelowTjmax.

`absoluteTempAbort(temps map[string]float64, tjmax float64, thr Thresholds) bool` returns true
when any sensor reading exceeds `tjmax - thr.TAbsOffsetBelowTjmax`. For ClassNASHDD the
threshold is `thr.TAbsAbsolute` rather than a Tjmax-relative value (NAS drives have a fixed
`TAbsAbsolute: 50.0` regardless of CPU Tjmax). At the boundary: a reading equal to
`tjmax - TAbsOffsetBelowTjmax` does NOT abort; a reading one ULP above it MUST abort. The
test uses a synthetic Tjmax of 100°C with offset 15°C (boundary at 85°C) and verifies that
84.9°C does not abort while 85.1°C does.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_05_TAbsTripBoundary
