---
name: RULE-HWDB-PR2-10
description: Layer precedence (board > chip > driver, calibration > all for runtime fields) MUST be enforced by the resolver
type: project
---

# RULE-HWDB-PR2-10: Layer precedence (board > chip > driver, calibration > all for runtime fields) MUST be enforced by the resolver. Invariant-test asserts on synthetic 3-layer fixture.

The `ResolveEffectiveProfile(driver, chip, board, cal)` function MUST apply layer
precedence: board overrides chip, chip overrides driver; calibration fields override
all three for the fields they populate (PWMPolarity, MinResponsivePWM, StallPWM,
PhantomChannel). The test fixture uses a synthetic triple: driver=nct6775 (defaults),
chip=nct6798 (overrides OffBehaviour to bios_dependent), board=asus_z790_a (overrides
PollingLatencyHint to 75ms and CPUTINFloats=true). The resolved profile must reflect
all three overrides: chip beats driver on OffBehaviour; board beats chip on latency and
quirk. A calibration result with PWMPolarity=inverted must further override the resolved
profile's calibration fields. Each assertion is labeled with the layer that should win.

Bound: internal/hwdb/effective_profile_test.go:TestRuleHwdbPR2_10
<!-- rulelint:allow-orphan -->
