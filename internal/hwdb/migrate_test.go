package hwdb

import "testing"

// TestMigrate_ChainIntegrity verifies that for every v > 1 in supportedVersions
// there is a registered migrators[v] entry. This is the load-bearing check
// that makes schema bumps mechanical: you cannot add a version to
// supportedVersions without providing the migration function.
//
// Bound: internal/hwdb/migrate_test.go:TestMigrate_ChainIntegrity
func TestMigrate_ChainIntegrity(t *testing.T) {
	for _, v := range supportedVersions {
		if v == 1 {
			continue
		}
		if _, ok := migrators[v]; !ok {
			t.Errorf("supportedVersions includes v%d but migrators[%d] is missing", v, v)
		}
	}
}
