package checks_test

import (
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/doctor/checks"
	"github.com/ventd/ventd/internal/experimental"
)

// TestDoctor_AMDOverdrive_ReportsActiveStateAndMask verifies
// RULE-EXPERIMENTAL-AMD-OVERDRIVE-03: doctor reports active state and
// ppfeaturemask value in its output.
func TestDoctor_AMDOverdrive_ReportsActiveStateAndMask(t *testing.T) {
	tests := []struct {
		name        string
		flags       experimental.Flags
		mask        uint32
		wantActive  bool
		wantSubstrs []string
	}{
		{
			name:       "active_with_mask",
			flags:      experimental.Flags{AMDOverdrive: true},
			mask:       0x00004000,
			wantActive: true,
			wantSubstrs: []string{
				"amd_overdrive",
				"active",
				"0x00004000",
			},
		},
		{
			name:       "active_mask_all_bits",
			flags:      experimental.Flags{AMDOverdrive: true},
			mask:       0xffffffff,
			wantActive: true,
			wantSubstrs: []string{
				"amd_overdrive",
				"active",
				"0xffffffff",
			},
		},
		{
			name:       "inactive_no_mask",
			flags:      experimental.Flags{AMDOverdrive: false},
			mask:       0,
			wantActive: false,
			wantSubstrs: []string{
				"amd_overdrive",
				"inactive",
			},
		},
		{
			name:       "inactive_mask_present",
			flags:      experimental.Flags{AMDOverdrive: false},
			mask:       0x4000,
			wantActive: false,
			wantSubstrs: []string{
				"amd_overdrive",
				"inactive",
				"0x00004000",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry := checks.CheckAMDOverdrive(tc.flags, tc.mask)

			if entry.Active != tc.wantActive {
				t.Errorf("Active=%v want %v", entry.Active, tc.wantActive)
			}
			if entry.Mask != tc.mask {
				t.Errorf("Mask=0x%08x want 0x%08x", entry.Mask, tc.mask)
			}
			for _, sub := range tc.wantSubstrs {
				if !strings.Contains(entry.StatusLine, sub) {
					t.Errorf("StatusLine %q does not contain %q", entry.StatusLine, sub)
				}
			}
		})
	}
}
