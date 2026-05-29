# HAL CurveSink rules

The CurveSink seam (spec-17 PR-1b) lets a fan backend declare that its hardware
follows a programmed fan curve on its own — the firmware reads its own
temperature sensor and interpolates between anchor points — rather than
accepting a per-tick duty byte. The first consumer is the AMD amdgpu backend's
RDNA3/4 `gpu_od/fan_ctrl/fan_curve` interface; future vendor-EC backends
(Legion debugfs fancurve, HP Omen, Razer, Alienware) are designed to plug into
the same seam. See `internal/hal/curve_sink.go`.

## RULE-HAL-CURVE-SINK-01: A CurveSink channel is programmed once and re-programmed only on curve change, never via per-tick PWM.

A channel that advertises `hal.CapWriteCurve` is driven through
`hal.CurveSink.WriteCurve`, not through per-tick `FanBackend.Write`. The
controller programs the hardware curve at startup and re-programs it ONLY when
the computed anchor set changes (a hot-reload that alters the bound curve, its
points, the fan's PWM bounds, or a manual override). An unchanged curve MUST
NOT be re-written, and the hardware firmware — not a ventd per-tick loop — owns
the control loop for that channel. Change detection compares the OUTPUT anchor
set, so it is correct regardless of which config field moved (the bound curve's
`points` slice is not captured by the per-tick `curveSig` fingerprint). A failed
`WriteCurve` leaves the channel unmarked so the next tick retries rather than
wedging.

Bound: internal/controller/curvesink_test.go:TestProgramCurveSink_ProgramsOnceAndOnChange

## RULE-HAL-CURVE-SINK-02: Curve-sink detection keys on the channel's CapWriteCurve bit, not the backend type.

The controller takes the curve-sink path for a channel only when its backend
satisfies `hal.CurveSink` AND the enumerated channel advertises
`hal.CapWriteCurve`. A backend that implements `CurveSink` for some cards but
exposes a particular channel as per-tick PWM (`CapWritePWM`, not
`CapWriteCurve`) — the AMD amdgpu backend, where RDNA3/4 is a curve sink and
RDNA1/2 is per-tick duty — MUST fall through to the unchanged per-tick `tick()`
path for that channel. This keeps RDNA1/2 control and every non-curve backend
byte-for-byte unchanged.

Bound: internal/controller/curvesink_test.go:TestCurveSinkBackend_Detection

## RULE-HAL-CURVE-SINK-03: amdgpu RDNA3/4 maps the bound curve to exactly five ascending anchors through the gated fan_curve write.

The amdgpu backend's `WriteCurve` normalises any caller-supplied
`[]hal.CurvePoint` to exactly five `FanCurvePoint`s with strictly-increasing
temperatures and non-decreasing, `[0,100]`-clamped percentages — the shape the
`gpu_od/fan_ctrl/fan_curve` hardware accepts — and the write goes through
`CardInfo.WriteFanCurveGated`, so the `--enable-amd-overdrive` gate
(RULE-EXPERIMENTAL-AMD-OVERDRIVE-01) and the RDNA4 kernel-6.15 gate
(RULE-EXPERIMENTAL-AMD-OVERDRIVE-04) are re-checked before any sysfs byte is
written. RDNA3/4 channels advertise `CapWriteCurve` (not `CapWritePWM`) only
when `--enable-amd-overdrive` is set; without it they stay monitor-only and
`WriteCurve` is refused without touching the node.

This write path is NOT validated on real RDNA3/4 silicon (the dev box is
NVIDIA-only); it is exercised against a fake sysfs tree only, the same precedent
as the un-HIL'd RDNA1/2 control path.

Bound: internal/hal/gpu/amdgpu/curve_sink_test.go:TestBackend_RDNA3CurveSink
