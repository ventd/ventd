# RULE-DIAG-PR2C-09: Reading a mapping file from another machine must not crash (graceful schema-mismatch).

When the mapping store attempts to load an existing `redactor-mapping.json` and the file
contains an unrecognised schema version or malformed JSON, the load MUST succeed with an
empty mapping (discarding the file's contents) and log a warning. The bundle generation
continues with a fresh mapping for this run. A crash (panic or returned fatal error) on
a foreign mapping file would prevent the daemon from generating any bundle on machines
where a mapping file was copied from another host or corrupted by a partial write. The
test fixture constructs a mapping file with `"schema_version": 99` and an unknown field
and verifies that `MappingStore.Load` returns a non-nil `*MappingStore` with empty maps
and a logged warning, not an error.

Bound: internal/diag/redactor/redactor_test.go:TestRuleDiagPR2C_09/foreign_mapping_file_graceful
<!-- rulelint:allow-orphan -->
