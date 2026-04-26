# RULE-GPU-PR2D-04: Schema v1.0 unchanged — new GPU driver YAMLs validate against existing profile_v1.go schema with no new fields.

All GPU driver catalog entries added in spec-03 PR 2d MUST validate against the existing
`internal/hwdb/profile_v1.go` schema (schema_version "1.0") without requiring any new
struct fields, new YAML keys, or changes to `validateDriverProfile`. Every GPU driver
profile (`nvidia`, `amdgpu`, `amdgpu_rdna3`, `i915`, `xe`, `nouveau`, `radeon`) must pass
RULE-HWDB-PR2-01..05 validation. The subtest `TestHWDB_GPUEntriesV1Compatible` calls
`LoadCatalog()` on the embedded filesystem and asserts that all GPU driver module names
are present in `cat.Drivers` with non-nil profiles, and that the catalog load returns
nil error. A GPU profile that introduces a new required field would silently break all
existing board profiles that do not set the new field.

Bound: internal/hwdb/profile_v1_test.go:TestHWDB_GPUEntriesV1Compatible
