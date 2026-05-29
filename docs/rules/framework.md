# Framework / fw-fanctrl corpus rules

Framework laptops run ChromeOS-derived EC firmware driven by the mainline
`cros_ec_hwmon` driver (writable `pwm1` since kernel 6.18 — see the
`cros_ec_hwmon` driver catalog row), which ventd's hwmon backend drives
directly. ventd vendors the curve presets from `fw-fanctrl`
(github.com/TamtamHero/fw-fanctrl, BSD-3-Clause) as a Mode-C corpus under
`internal/hwdb/framework/` so ventd can recognise a Framework host and surface
proven fan curves. These are reference curves only — ventd never shells out to
`ectool`/`fw-fanctrl`, and the corpus carries no EC-register or ACPI-method map
(so, unlike the nbfc corpus, there is no register allowlist to gate). spec-17 PR-2.

## RULE-FRAMEWORK-CATALOG-01: a malformed or invalid vendored preset aborts the load with the offending file named.

`framework.LoadCatalogFS` MUST fail closed: a config file that is not valid
JSON, or that fails the corpus invariants, aborts the entire load with an error
naming the offending file — never a silently half-loaded catalogue. The
embedded corpus (`internal/hwdb/framework/configs/*.json`) MUST therefore parse
and validate cleanly, so a daemon that loads it sees either the full set or a
clear diagnostic.

Bound: internal/hwdb/framework/embed_test.go:TestLoadCatalogFS_RejectsMalformedJSON

## RULE-FRAMEWORK-CATALOG-02: every vendored preset satisfies the curve invariants.

`framework.validate` MUST reject a config that defines no strategies, whose
`defaultStrategy` (or non-empty `strategyOnDischarging`) does not resolve to a
defined strategy, or any of whose `speedCurve` anchors carry a percentage
outside `[0,100]` or a temperature below the previous anchor (curves are
ascending by temperature). A preset naming a missing strategy or an inverted
curve is a sync error, not a curve to offer an operator.

Bound: internal/hwdb/framework/embed_test.go:TestValidate_RejectsBadConfigs

## RULE-FRAMEWORK-CATALOG-03: the Framework matcher is deterministic and keyed on the Framework sys_vendor.

`framework.Match` MUST be a pure function of the catalog + DMI: a Framework host
(DMI `sys_vendor` == "Framework", case-folded) resolves to the canonical
mainline `fw-fanctrl` preset entry, and a non-Framework host resolves to no
match. Repeated calls with the same input MUST return the same result (no map
iteration order leaking into the choice). The mainboard-specific `board_name`
is not consulted — the presets are not mainboard-specific.

Bound: internal/hwdb/framework/match_test.go:TestMatch_FrameworkReturnsMainline

## RULE-FRAMEWORK-DOCTOR-01: the Framework doctor card is corpus-backed and fires only on Framework hosts.

The `framework_strategies` detector MUST emit exactly one card on a Framework
host that names the available fw-fanctrl strategies and the upstream default
curve (read from the vendored corpus, not hard-coded), and MUST stay silent on
a non-Framework host. The card carries the correct `cros_ec_hwmon` kernel facts
(writable `pwm1` since 6.18); it supersedes the generic Framework card the
`vendor_remediation` detector previously emitted.

Bound: internal/doctor/detectors/framework_strategies_d_test.go:TestFrameworkStrategies_FrameworkHostEmitsCard
