package ec

import "testing"

// TestLockdownActive covers the three states the kernel exposes via
// /sys/kernel/security/lockdown plus the file-absent case.
func TestLockdownActive(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		readErr  error
		expected bool
	}{
		{"none active", "[none] integrity confidentiality\n", nil, false},
		{"integrity active", "none [integrity] confidentiality\n", nil, true},
		{"confidentiality active", "none integrity [confidentiality]\n", nil, true},
		{"file absent (no LSM)", "", errFileNotFound, false},
		{"empty content", "", nil, false},
	}
	saved := readLockdownFile
	t.Cleanup(func() { readLockdownFile = saved })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			readLockdownFile = func() (string, error) {
				if tc.readErr != nil {
					return "", tc.readErr
				}
				return tc.content, nil
			}
			got := LockdownActive()
			if got != tc.expected {
				t.Errorf("LockdownActive() = %v, want %v", got, tc.expected)
			}
		})
	}
}

// errFileNotFound stands in for any read error — the function should
// treat all read failures as "lockdown not active".
type fileNotFoundErr struct{}

func (fileNotFoundErr) Error() string { return "no such file or directory" }

var errFileNotFound = fileNotFoundErr{}
