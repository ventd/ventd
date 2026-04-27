package checks_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/experimental/checks"
)

func TestDetectAMDOverdrive(t *testing.T) {
	tests := []struct {
		name        string
		cmdline     string // written to fake /proc/cmdline; empty means no file
		wantEnabled bool
		wantMask    uint32
		wantErr     bool
	}{
		{
			name:        "no_ppfeaturemask",
			cmdline:     "BOOT_IMAGE=/vmlinuz-6.12 root=/dev/sda1 quiet splash",
			wantEnabled: false,
			wantMask:    0,
		},
		{
			name:        "ppfeaturemask_0x4000_only",
			cmdline:     "BOOT_IMAGE=/vmlinuz amdgpu.ppfeaturemask=0x4000",
			wantEnabled: true,
			wantMask:    0x4000,
		},
		{
			name:        "ppfeaturemask_all_bits",
			cmdline:     "BOOT_IMAGE=/vmlinuz amdgpu.ppfeaturemask=0xffffffff",
			wantEnabled: true,
			wantMask:    0xffffffff,
		},
		{
			name:        "ppfeaturemask_bit14_unset",
			cmdline:     "BOOT_IMAGE=/vmlinuz amdgpu.ppfeaturemask=0x3fff",
			wantEnabled: false,
			wantMask:    0x3fff,
		},
		{
			name:        "ppfeaturemask_decimal",
			cmdline:     "BOOT_IMAGE=/vmlinuz amdgpu.ppfeaturemask=16384",
			wantEnabled: true,
			wantMask:    16384,
		},
		{
			name:        "ppfeaturemask_mid_line",
			cmdline:     "quiet amdgpu.ppfeaturemask=0x4000 splash",
			wantEnabled: true,
			wantMask:    0x4000,
		},
		{
			name:        "ppfeaturemask_uppercase_hex",
			cmdline:     "amdgpu.ppfeaturemask=0xFFFF4000",
			wantEnabled: true,
			wantMask:    0xFFFF4000,
		},
		{
			name:        "ppfeaturemask_zero",
			cmdline:     "amdgpu.ppfeaturemask=0x0",
			wantEnabled: false,
			wantMask:    0,
		},
		{
			name:    "missing_file",
			cmdline: "",
			wantErr: true,
		},
		{
			name:    "malformed_value",
			cmdline: "amdgpu.ppfeaturemask=0xGGGG",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			cmdlinePath := filepath.Join(tmp, "cmdline")
			if tc.cmdline != "" {
				if err := os.WriteFile(cmdlinePath, []byte(tc.cmdline), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.cmdline == "" {
				// Use a path that doesn't exist.
				cmdlinePath = filepath.Join(tmp, "no-such-file")
			}

			enabled, mask, err := checks.DetectAMDOverdrive(cmdlinePath)
			if (err != nil) != tc.wantErr {
				t.Fatalf("DetectAMDOverdrive() err=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if enabled != tc.wantEnabled {
				t.Errorf("enabled=%v want %v", enabled, tc.wantEnabled)
			}
			if mask != tc.wantMask {
				t.Errorf("mask=0x%08x want 0x%08x", mask, tc.wantMask)
			}
		})
	}
}

// TestAMDOverdrive_PreconditionFailsActionableWhenBitUnset verifies
// RULE-EXPERIMENTAL-AMD-OVERDRIVE-02: precondition check fails with an actionable
// message when amdgpu.ppfeaturemask is set but bit 0x4000 is absent.
func TestAMDOverdrive_PreconditionFailsActionableWhenBitUnset(t *testing.T) {
	tests := []struct {
		name           string
		cmdline        string
		wantMet        bool
		wantDetailSubs []string
	}{
		{
			name:    "bit_unset_mask_present",
			cmdline: "amdgpu.ppfeaturemask=0x3fff",
			wantMet: false,
			wantDetailSubs: []string{
				"0x4000",
				"reboot",
				"0x00003fff",
			},
		},
		{
			name:    "ppfeaturemask_absent",
			cmdline: "BOOT_IMAGE=/vmlinuz quiet",
			wantMet: false,
			wantDetailSubs: []string{
				"amdgpu.ppfeaturemask not set",
				"reboot",
			},
		},
		{
			name:    "bit_set",
			cmdline: "amdgpu.ppfeaturemask=0x4000",
			wantMet: true,
			wantDetailSubs: []string{
				"0x00004000",
				"active",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			cmdlinePath := filepath.Join(tmp, "cmdline")
			if err := os.WriteFile(cmdlinePath, []byte(tc.cmdline), 0o644); err != nil {
				t.Fatal(err)
			}

			met, detail := checks.CheckAMDOverdrivePrecondition(cmdlinePath)
			if met != tc.wantMet {
				t.Errorf("met=%v want %v; detail=%q", met, tc.wantMet, detail)
			}
			for _, sub := range tc.wantDetailSubs {
				if !strings.Contains(detail, sub) {
					t.Errorf("detail %q does not contain %q", detail, sub)
				}
			}
			if detail == "" {
				t.Error("detail must not be empty")
			}
		})
	}
}
