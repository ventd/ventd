# RULE-ENVELOPE-04: dT/dt thermal abort fires when temperature rise rate exceeds DTDtAbortCPerSec for the class.

`thermalAbort(temps map[string]float64, prev map[string]float64, dt time.Duration, thr Thresholds) bool`
returns true when any sensor's temperature delta (current − previous) divided by `dt.Seconds()`
exceeds `thr.DTDtAbortCPerSec`. The check is skipped for NAS (ClassNASHDD) systems which use
`DTDtAbortCPerMin` over a longer window instead. At the abort boundary: a delta of exactly
`DTDtAbortCPerSec * dt.Seconds()` must NOT abort (boundary is exclusive); a delta strictly
above it MUST abort. The test injects synthetic temperature maps with precisely the boundary
value and one step above to verify the exclusive/inclusive behaviour.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_04_DTDtTripBoundary
