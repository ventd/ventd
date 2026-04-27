# RULE-PROBE-05: No downstream code branches on CatalogMatch==nil vs non-nil — channels are enumerated the same way regardless.

`ClassifyOutcome` MUST return the same `Outcome` for two `ProbeResult` values that have
identical `ThermalSources` and `ControllableChannels` but differ only in whether
`CatalogMatch` is nil. The catalog overlay adds `CapabilityHint` annotations to existing
channels and may set `OverlayApplied` on `CatalogMatch`, but it MUST NOT create or remove
`ControllableChannel` entries. Downstream code that reads `ControllableChannels` must work
identically whether the catalog matched or not — a channel's presence in the slice is
determined solely by hwmon sysfs enumeration, not by catalog knowledge.

Bound: internal/probe/probe_test.go:TestProbe_Rules/RULE-PROBE-05_channels_uniform_regardless_of_catalog_match
