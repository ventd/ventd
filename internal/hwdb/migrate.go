package hwdb

// supportedVersions enumerates the schema versions this binary can load.
// Adding a version REQUIRES adding the matching migrator below — RULE-HWDB-07
// enforces this at test time.
var supportedVersions = []int{1}

// CurrentVersion is the schema version this binary writes.
const CurrentVersion = 1

// migrators maps each schema version v to the function that upgrades a v-1
// document to v. Empty for v1 because there is no predecessor to migrate from.
// When spec-05 introduces v2, add: migrators[2] = migrate_1_to_2.
var migrators = map[int]func([]byte) ([]byte, error){
	// 2: migrate_1_to_2,
}
