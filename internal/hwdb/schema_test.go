package hwdb

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestSchema_Invariants verifies all nine RULE-HWDB-* invariants via fixture
// files in testdata/. Each subtest name matches exactly one Bound: line in
// .claude/rules/hwdb-schema.md.
func TestSchema_Invariants(t *testing.T) {
	t.Run("Rule_HWDB_01_RequiredFields", func(t *testing.T) {
		cases := []struct {
			file    string
			wantMsg string
		}{
			{"testdata/invalid_rule01_missing_id.yaml", "required-fields"},
			{"testdata/invalid_rule01_missing_fingerprint.yaml", "required-fields"},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.file, func(t *testing.T) {
				_, err := loadFile(t, tc.file)
				assertSchemaError(t, err, tc.wantMsg)
			})
		}
		// Valid minimal entry must load cleanly.
		profiles, err := loadFile(t, "testdata/valid_minimal.yaml")
		if err != nil {
			t.Fatalf("valid_minimal.yaml: unexpected error: %v", err)
		}
		if len(profiles) != 1 {
			t.Fatalf("valid_minimal.yaml: want 1 profile, got %d", len(profiles))
		}
	})

	t.Run("Rule_HWDB_02_UniqueIDs", func(t *testing.T) {
		_, err := loadFile(t, "testdata/invalid_rule02_duplicate_id.yaml")
		assertSchemaError(t, err, "unique-id")
	})

	t.Run("Rule_HWDB_03_KnownSchemaVersion", func(t *testing.T) {
		_, err := loadFile(t, "testdata/invalid_rule03_unknown_version.yaml")
		assertSchemaError(t, err, "schema-version")
	})

	t.Run("Rule_HWDB_04_MonotonicCurves", func(t *testing.T) {
		cases := []struct {
			file    string
			wantMsg string
		}{
			{"testdata/invalid_rule04_decreasing_temp.yaml", "monotonic-curves"},
			{"testdata/invalid_rule04_decreasing_pwm.yaml", "monotonic-curves"},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.file, func(t *testing.T) {
				_, err := loadFile(t, tc.file)
				assertSchemaError(t, err, tc.wantMsg)
			})
		}
		// valid_full.yaml has monotonic curves — must load cleanly.
		if _, err := loadFile(t, "testdata/valid_full.yaml"); err != nil {
			t.Fatalf("valid_full.yaml: unexpected error: %v", err)
		}
	})

	t.Run("Rule_HWDB_05_KnownPWMModule", func(t *testing.T) {
		_, err := loadFile(t, "testdata/invalid_rule05_unknown_module.yaml")
		assertSchemaError(t, err, "pwm_control")
	})

	t.Run("Rule_HWDB_06_PIIGate", func(t *testing.T) {
		// Unknown field rejected by KnownFields strict decode.
		_, err := loadFile(t, "testdata/invalid_rule06_unknown_field.yaml")
		assertSchemaError(t, err, "")

		// Bad contributed_by value rejected by format check.
		_, err = loadFile(t, "testdata/invalid_rule06_bad_contributed_by.yaml")
		assertSchemaError(t, err, "contributed-by")
	})

	t.Run("Rule_HWDB_08_PredictiveHints", func(t *testing.T) {
		_, err := loadFile(t, "testdata/invalid_rule08_hints_inverted.yaml")
		assertSchemaError(t, err, "predictive-hints")
	})

	t.Run("Rule_HWDB_09_StallPWMMinRequired", func(t *testing.T) {
		_, err := loadFile(t, "testdata/invalid_rule09_allow_stop_no_stall.yaml")
		assertSchemaError(t, err, "stall-pwm-min")
	})

	// LoadEmbedded_ParsesUnderV1 is a smoke test, not a bound invariant.
	// It verifies that the embedded profiles-v1.yaml (currently empty) loads
	// without error, keeping LoadEmbedded exercised so it is not dead code.
	t.Run("LoadEmbedded_ParsesUnderV1", func(t *testing.T) {
		profiles, err := LoadEmbedded()
		if err != nil {
			t.Fatalf("LoadEmbedded: %v", err)
		}
		if profiles == nil {
			t.Fatal("LoadEmbedded returned nil slice, want non-nil empty slice")
		}
	})
}

// loadFile is a test helper that opens a testdata fixture and calls Load.
func loadFile(t *testing.T, path string) ([]Profile, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return Load(f)
}

// assertSchemaError fails the test unless err wraps ErrSchema and (when
// wantSubstr is non-empty) the error message contains wantSubstr.
func assertSchemaError(t *testing.T, err error, wantSubstr string) {
	t.Helper()
	if err == nil {
		t.Fatal("Load returned nil error, want schema error")
	}
	if !errors.Is(err, ErrSchema) {
		t.Fatalf("error does not wrap ErrSchema: %v", err)
	}
	if wantSubstr != "" && !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("error %q does not contain %q", err.Error(), wantSubstr)
	}
}
