---
name: RULE-HWDB-PR2-08
description: Calibration result bios_overridden true MUST cause the controller apply path to refuse PWM writes for that channel
type: project
---

# RULE-HWDB-PR2-08: Calibration result bios_overridden: true MUST cause apply path to refuse curve writes for that channel.

When a `ChannelCalibration` loaded from disk has `BIOSOverridden: true`, the controller
apply path MUST return `hwdb.ErrBIOSOverridden` and skip writing any PWM value to the
associated channel. The test fixture verifies that `writeWithRetry` returns
`hwdb.ErrBIOSOverridden` and that `backend.Write` is never called when the controller's
`calCh` has `BIOSOverridden: true`. This prevents silent no-op writes to channels where
the BIOS firmware actively overrides ventd's PWM values — the correct response is to
surface the issue in the diagnostic bundle and mark the channel as monitor-only.

Bound: internal/controller/controller_test.go:TestWriteWithRetry_RefusesBIOSOverridden
