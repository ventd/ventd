package hwmon

import "testing"

func TestPreflightOOT(t *testing.T) {
	nd := DriverNeed{
		Key:                "nct6687d",
		ChipName:           "NCT6687D",
		Module:             "nct6687",
		MaxSupportedKernel: "6.10.0",
	}

	cases := []struct {
		name   string
		probes Probes
		want   Reason
	}{
		{
			name: "ok_all_present",
			probes: Probes{
				KernelRelease:     func() string { return "6.8.0-1-generic" },
				BuildDirExists:    func(string) bool { return true },
				HasBinary:         func(string) bool { return true },
				SecureBootEnabled: func() (bool, bool) { return false, true },
			},
			want: ReasonOK,
		},
		{
			name: "secure_boot_blocks",
			probes: Probes{
				KernelRelease:     func() string { return "6.8.0" },
				BuildDirExists:    func(string) bool { return true },
				HasBinary:         func(string) bool { return true },
				SecureBootEnabled: func() (bool, bool) { return true, true },
			},
			want: ReasonSecureBootBlocks,
		},
		{
			name: "secure_boot_unknown_is_not_blocker",
			probes: Probes{
				KernelRelease:     func() string { return "6.8.0" },
				BuildDirExists:    func(string) bool { return true },
				HasBinary:         func(string) bool { return true },
				SecureBootEnabled: func() (bool, bool) { return false, false },
			},
			want: ReasonOK,
		},
		{
			name: "kernel_too_new",
			probes: Probes{
				KernelRelease:     func() string { return "6.12.0-generic" },
				BuildDirExists:    func(string) bool { return true },
				HasBinary:         func(string) bool { return true },
				SecureBootEnabled: func() (bool, bool) { return false, true },
			},
			want: ReasonKernelTooNew,
		},
		{
			name: "headers_missing",
			probes: Probes{
				KernelRelease:     func() string { return "6.8.0" },
				BuildDirExists:    func(string) bool { return false },
				HasBinary:         func(string) bool { return true },
				SecureBootEnabled: func() (bool, bool) { return false, true },
			},
			want: ReasonKernelHeadersMissing,
		},
		{
			name: "dkms_missing",
			probes: Probes{
				KernelRelease:     func() string { return "6.8.0" },
				BuildDirExists:    func(string) bool { return true },
				HasBinary:         func(name string) bool { return name != "dkms" },
				SecureBootEnabled: func() (bool, bool) { return false, true },
			},
			want: ReasonDKMSMissing,
		},
		{
			name: "secure_boot_wins_over_headers",
			probes: Probes{
				KernelRelease:     func() string { return "6.8.0" },
				BuildDirExists:    func(string) bool { return false },
				HasBinary:         func(string) bool { return false },
				SecureBootEnabled: func() (bool, bool) { return true, true },
			},
			want: ReasonSecureBootBlocks,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PreflightOOT(nd, tc.probes)
			if got.Reason != tc.want {
				t.Errorf("reason=%d want %d detail=%q", got.Reason, tc.want, got.Detail)
			}
			if tc.want != ReasonOK && got.Detail == "" {
				t.Errorf("expected non-empty detail for reason %d", got.Reason)
			}
		})
	}
}

func TestKernelAbove(t *testing.T) {
	cases := []struct {
		running, ceiling string
		want             bool
	}{
		{"6.12.0", "6.10.0", true},
		{"6.10.0", "6.10.0", false},
		{"6.8.0-1-generic", "6.10.0", false},
		{"6.10.1", "6.10.0", true},
		{"7.0.0", "6.99.99", true},
		{"garbage", "6.10.0", false},
	}
	for _, tc := range cases {
		if got := kernelAbove(tc.running, tc.ceiling); got != tc.want {
			t.Errorf("kernelAbove(%q, %q) = %v, want %v", tc.running, tc.ceiling, got, tc.want)
		}
	}
}
