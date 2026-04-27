package amdgpu

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsRDNA4(t *testing.T) {
	tests := []struct {
		name     string
		deviceID string
		want     bool
		wantErr  bool
	}{
		{
			name:     "navi48_rdna4",
			deviceID: "0x7550\n",
			want:     true,
		},
		{
			name:     "navi31_rdna3_not_rdna4",
			deviceID: "0x744c\n",
			want:     false,
		},
		{
			name:     "navi21_rdna2_not_rdna4",
			deviceID: "0x73bf\n",
			want:     false,
		},
		{
			name:     "unknown_id",
			deviceID: "0x1234\n",
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			cardPath := filepath.Join(tmp, "card0")
			deviceDir := filepath.Join(cardPath, "device")
			if err := os.MkdirAll(deviceDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(deviceDir, "device"), []byte(tc.deviceID), 0o644); err != nil {
				t.Fatal(err)
			}

			got, err := IsRDNA4(cardPath)
			if (err != nil) != tc.wantErr {
				t.Fatalf("IsRDNA4() err=%v wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("IsRDNA4()=%v want %v", got, tc.want)
			}
		})
	}
}

func TestKernelAtLeast(t *testing.T) {
	tests := []struct {
		name      string
		osrelease string
		major     int
		minor     int
		want      bool
		wantErr   bool
	}{
		{"6.15_vs_6.15", "6.15.0-gentoo\n", 6, 15, true, false},
		{"6.16_vs_6.15", "6.16.3-arch1\n", 6, 15, true, false},
		{"6.14_vs_6.15", "6.14.8\n", 6, 15, false, false},
		{"7.0_vs_6.15", "7.0.0\n", 6, 15, true, false},
		{"5.15_vs_6.15", "5.15.0\n", 6, 15, false, false},
		{"6.15_vs_6.14", "6.15.0\n", 6, 14, true, false},
		{"6.14_vs_6.14", "6.14.0\n", 6, 14, true, false},
		{"minor_suffix", "6.15-lts\n", 6, 15, true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			osrPath := filepath.Join(tmp, "osrelease")
			if err := os.WriteFile(osrPath, []byte(tc.osrelease), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := kernelAtLeast(tc.major, tc.minor, osrPath)
			if (err != nil) != tc.wantErr {
				t.Fatalf("kernelAtLeast() err=%v wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("kernelAtLeast(%d,%d) on %q = %v want %v", tc.major, tc.minor, tc.osrelease, got, tc.want)
			}
		})
	}
}

// TestAMDGPU_RDNA4RefusesOnKernelBelow615 verifies
// RULE-EXPERIMENTAL-AMD-OVERDRIVE-04: RDNA4 WriteFanCurveGated returns
// ErrRDNA4NeedsKernel615 when the running kernel is < 6.15.
func TestAMDGPU_RDNA4RefusesOnKernelBelow615(t *testing.T) {
	tests := []struct {
		name      string
		deviceID  string
		osrelease string
		wantErr   error
	}{
		{
			name:      "rdna4_kernel_6.14_refused",
			deviceID:  "0x7550\n",
			osrelease: "6.14.8\n",
			wantErr:   ErrRDNA4NeedsKernel615,
		},
		{
			name:      "rdna4_kernel_6.15_permitted",
			deviceID:  "0x7550\n",
			osrelease: "6.15.0\n",
			wantErr:   nil,
		},
		{
			name:      "rdna4_kernel_6.16_permitted",
			deviceID:  "0x7550\n",
			osrelease: "6.16.1-arch1\n",
			wantErr:   nil,
		},
		{
			name:      "rdna3_kernel_6.14_permitted",
			deviceID:  "0x744c\n", // Navi 31 — RDNA3, not RDNA4
			osrelease: "6.14.0\n",
			wantErr:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			cardPath := buildFakeCard(t, tmp, tc.deviceID)
			osrPath := filepath.Join(tmp, "osrelease")
			if err := os.WriteFile(osrPath, []byte(tc.osrelease), 0o644); err != nil {
				t.Fatal(err)
			}

			card := &CardInfo{
				CardPath:     cardPath,
				HwmonPath:    filepath.Join(cardPath, "device", "hwmon", "hwmonX"),
				HasFanCurve:  true,
				AMDOverdrive: true,
			}

			// Redirect osReleasePath for this test.
			prev := osReleasePath
			osReleasePath = osrPath
			t.Cleanup(func() { osReleasePath = prev })

			// Build a fake fan_curve file so WriteFanCurve can open it.
			fanCurveDir := filepath.Join(cardPath, "device", "gpu_od", "fan_ctrl")
			if err := os.MkdirAll(fanCurveDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(fanCurveDir, "fan_curve"), []byte(""), 0o644); err != nil {
				t.Fatal(err)
			}

			points := []FanCurvePoint{{0, 50, 30}, {1, 70, 50}, {2, 85, 70}, {3, 95, 90}, {4, 100, 100}}
			err := card.WriteFanCurveGated(points)

			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Errorf("WriteFanCurveGated() err=%v want %v", err, tc.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("WriteFanCurveGated() unexpected err: %v", err)
				}
			}
		})
	}
}

// buildFakeCard creates a minimal fake card directory with the given device ID.
func buildFakeCard(t *testing.T, base, deviceID string) string {
	t.Helper()
	cardPath := filepath.Join(base, "card0")
	deviceDir := filepath.Join(cardPath, "device")
	hwmonDir := filepath.Join(cardPath, "device", "hwmon", "hwmonX")
	for _, dir := range []string{deviceDir, hwmonDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(deviceDir, "device"), []byte(deviceID), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hwmonDir, "name"), []byte("amdgpu\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return cardPath
}
