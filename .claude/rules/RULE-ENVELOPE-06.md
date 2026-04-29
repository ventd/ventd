# RULE-ENVELOPE-06: Ambient headroom precondition refuses Envelope C when ambient ≥ (Tjmax − AmbientHeadroomMin).

`ambientHeadroomOK(ambient, tjmax float64, thr Thresholds) bool` returns true when
`ambient < tjmax - thr.AmbientHeadroomMin`. When the ambient sensor reading leaves fewer than
`AmbientHeadroomMin` degrees between ambient and Tjmax, the thermal headroom is insufficient
to safely run the Envelope C sweep without risking an absolute-temperature abort mid-step. The
gate is evaluated once before the first step write and cached for the probe run. The test verifies
the boundary: for Tjmax=100°C and AmbientHeadroomMin=60°C, ambient=39.9°C passes (100-39.9=60.1>0)
and ambient=40.0°C fails (100-40=60 ≤ 0).

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_06_AmbientHeadroomPrecondition
