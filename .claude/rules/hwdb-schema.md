# HWDB schema invariants

These invariants govern the v1 hardware profile library schema loaded from
`internal/hwdb/profiles-v1.yaml` (embedded) and any user-supplied profile
documents. Violating them risks loading corrupt board metadata into the matcher,
the calibration pipeline, and the predictive thermal model.

Each rule below is bound 1:1 to a subtest in `internal/hwdb/schema_test.go`
or `internal/hwdb/migrate_test.go`. If a rule text is edited, update the
corresponding subtest in the same PR; if a new rule lands, it must ship with a
matching subtest or `tools/rulelint` blocks the merge.

## RULE-HWDB-01: required top-level fields present

Every entry in `profiles-v1.yaml` MUST have `id`, `schema_version`,
`fingerprint`, `hardware`, `contributed_by`, `captured_at`, and `verified`.
The `fingerprint` block MUST contain at least one of `dmi_board_vendor`,
`dmi_board_name`, `dmi_product_name`, or `superio_chip` — an entry with no
matchable fingerprint anchor is a schema error, not just a warning. The parser
rejects at load and names the failing field.

Bound: internal/hwdb/schema_test.go:Rule_HWDB_01_RequiredFields

## RULE-HWDB-02: unique IDs across file

The `id` field is the primary key. Duplicate `id` values within a single
`profiles-v1.yaml` reject at load. The parser names both the duplicate id and
the second-occurrence position.

Bound: internal/hwdb/schema_test.go:Rule_HWDB_02_UniqueIDs

## RULE-HWDB-03: schema_version known

The `schema_version` field MUST appear in the parser's `supportedVersions`
table. An unknown version (higher OR lower) rejects at load with a
human-readable migration hint that names the known versions.

Bound: internal/hwdb/schema_test.go:Rule_HWDB_03_KnownSchemaVersion

## RULE-HWDB-04: curve points monotonic non-decreasing

Every curve's `points` list is monotonic non-decreasing in both the temperature
axis and the PWM axis. A curve that decreases on either axis at any segment
rejects at load. The parser names the offending profile id, curve role, and
segment index.

Bound: internal/hwdb/schema_test.go:Rule_HWDB_04_MonotonicCurves

## RULE-HWDB-05: pwm_control must be a known kernel module name

The `hardware.pwm_control` value MUST be one of the names in the allowlist
constant `knownPWMModules` defined in `schema.go`. Unknown values reject at
load. The allowlist v1 covers the common Super I/O and embedded-controller
drivers: nct6775 through nct6799, the modern NCT668x family
(nct6686, nct6687, nct6687d), nct7802, nct7904(d), it87 family,
the IT86xx/IT8689E family (it8625, it8625e, it8628, it8628e, it8689e),
ITE laptop/NUC ECs (it5570, it5570-fan, it5571, it5572),
fintek f71xxx, winbond w83xxx,
asus-ec-sensors, asus-wmi-sensors, dell-smm-hwmon, hp-wmi-sensors,
thinkpad_acpi, applesmc, surface_fan,
msi-ec (mainline ≥6.10), legion-laptop, qnap8528, macsmc-hwmon,
gigabyte-waterforce, asus-rog-ryujin,
corsair-cpro, corsair-psu, nzxt-kraken2/3, nzxt-smart2,
aquacomputer-d5next, drivetemp, k10temp, coretemp, amdgpu, peci-cputemp,
sch5627, sch5636, f71882fg, fam15h_power, lm75, lm85, adt7475, adt7476,
max6645, max31790, emc2103, and pwm-fan.

The literal string `"unknown"` is also accepted as a sentinel for monitor-only
catalog entries whose hardware has no Linux fan-control driver path yet
(typical for laptop ECs behind NDA, ARM SBCs without a hwmon driver, and
BMC-managed servers). Catalog entries using `pwm_control: unknown` MUST also
declare a capability of `ro_unsupported`; downstream gates check capability,
not the pwm_control string.

Adding a name is a v1.x amendment, not a schema break.

Bound: internal/hwdb/schema_test.go:Rule_HWDB_05_KnownPWMModule

## RULE-HWDB-06: PII gate via strict field validation and contributor format

The YAML decoder is configured with `KnownFields(true)`. Any field in the
input that is not in the schema struct definition causes a load error. This is
the mechanical enforcement of the PII gate: fields like `smbios_uuid`,
`chassis_serial`, `mac_address`, and `hostname` are not in the struct, so a
contributor cannot smuggle them into a profile entry — the parser rejects
before any code touches the value.

Additionally, the `contributed_by` field's value is constrained to either the
literal string `"anonymous"` or a string matching the GitHub handle pattern
`^[a-zA-Z0-9-]{1,39}$`. Real names, emails, and free-form strings reject.

Bound: internal/hwdb/schema_test.go:Rule_HWDB_06_PIIGate

## RULE-HWDB-07: migration chain integrity

For every `v > 1` in `supportedVersions`, there MUST be a registered function
`migrators[v]` that takes a v-1 document and returns a v document. The test
walks the table and fails if any expected migrator is missing. This is the
contract that makes schema bumps mechanical: you cannot bump the version without
writing the migration.

Bound: internal/hwdb/migrate_test.go:TestMigrate_ChainIntegrity

## RULE-HWDB-08: predictive_hints validated when present

If the optional `predictive_hints` block is present, then:
`platform_heavy_threshold_watts` must be an integer > 0;
`thermal_critical_c` and `thermal_safe_ceiling_c` must be integers; and
`thermal_critical_c > thermal_safe_ceiling_c + 5`. Non-compliant entries
reject at load. The parser names the offending profile id and the failing
constraint. The block is optional in v1; spec-05 will consume it. Documenting
the field in v1 prevents a v2 schema migration when spec-05 lands.

Bound: internal/hwdb/schema_test.go:Rule_HWDB_08_PredictiveHints

## RULE-HWDB-09: stall_pwm_min required when allow_stop=true

A curve with `allow_stop: true` can drive PWM to 0 under the right thermal
conditions. This is only safe if every fan in the curve's `fan_ids` list has a
known `stall_pwm_min` — without it, ventd has no information about whether
driving PWM to 0 will leave the fan stopped (acceptable) or stalling
(mechanical wear). Validation: for every curve with `allow_stop: true`, every
`fan_id` in `fan_ids` MUST resolve to an entry in `hardware.fans` that has
`stall_pwm_min` set. Unset means reject. The parser names the offending profile
id, curve role, and fan id. Background: hwmon-research.md §17.22 — the
`pwm_enable=1, pwm=0` behaviour is chip-specific; ventd must not trust
`allow_stop` blindly without a board-specific anchor.

Bound: internal/hwdb/schema_test.go:Rule_HWDB_09_StallPWMMinRequired
