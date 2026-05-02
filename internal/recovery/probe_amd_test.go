package recovery

import (
	"testing"
	"testing/fstest"
)

// RULE-WIZARD-RECOVERY-13: AMD OverDrive probe parses the kernel
// cmdline-derived ppfeaturemask sysfs file and reports whether bit
// 14 (0x4000) is set. ventd's amdgpu fan-write path requires this
// bit on every RDNA generation; the wizard preflight surfaces a
// recovery card when it's not set.
//
// Hex form: kernel emits "0xNNNNNNNN\n" when the user passed
// amdgpu.ppfeaturemask=0x... on the cmdline. Decimal form is also
// accepted for resilience.
func TestDetectAMDOverdrive(t *testing.T) {
	t.Parallel()
	t.Run("amdgpu not loaded — PpfeaturemaskFound=false", func(t *testing.T) {
		got := DetectAMDOverdrive(fstest.MapFS{}, fstest.MapFS{})
		if got.PpfeaturemaskFound {
			t.Fatalf("expected PpfeaturemaskFound=false on empty sysfs")
		}
	})
	t.Run("hex form with bit 14 set", func(t *testing.T) {
		// 0xffffffff is the canonical "everything on" value the
		// research agent reported as recommended in the wild.
		sys := fstest.MapFS{
			"module/amdgpu/parameters/ppfeaturemask": &fstest.MapFile{
				Data: []byte("0xffffffff\n"),
			},
		}
		got := DetectAMDOverdrive(sys, fstest.MapFS{})
		if !got.PpfeaturemaskFound {
			t.Fatalf("expected PpfeaturemaskFound=true")
		}
		if !got.OverdriveBitSet {
			t.Fatalf("0xffffffff has bit 14 set; got OverdriveBitSet=false")
		}
		if got.Mask != 0xffffffff {
			t.Errorf("Mask = 0x%x, want 0xffffffff", got.Mask)
		}
	})
	t.Run("hex form without bit 14 — default mask shape", func(t *testing.T) {
		// 0x3fff has bits 0..13 set but NOT bit 14 — common default
		// shape on stock kernels where the user hasn't enabled OD.
		sys := fstest.MapFS{
			"module/amdgpu/parameters/ppfeaturemask": &fstest.MapFile{
				Data: []byte("0x3fff\n"),
			},
		}
		got := DetectAMDOverdrive(sys, fstest.MapFS{})
		if !got.PpfeaturemaskFound {
			t.Fatalf("expected PpfeaturemaskFound=true")
		}
		if got.OverdriveBitSet {
			t.Fatalf("0x3fff lacks bit 14; got OverdriveBitSet=true")
		}
	})
	t.Run("decimal form is accepted", func(t *testing.T) {
		// 16384 = 0x4000 — exactly the OverDrive bit, nothing else.
		sys := fstest.MapFS{
			"module/amdgpu/parameters/ppfeaturemask": &fstest.MapFile{
				Data: []byte("16384\n"),
			},
		}
		got := DetectAMDOverdrive(sys, fstest.MapFS{})
		if !got.OverdriveBitSet {
			t.Fatalf("decimal 16384 should set OverdriveBitSet")
		}
	})
}

// RULE-WIZARD-RECOVERY-13b: The taint-warning flag fires when the
// running kernel is ≥ 6.14 — confirmed via commit b472b8d829c1
// ("drm/amd: Taint the kernel when enabling overdrive") landed in
// 6.14. The wizard surfaces this so the operator can opt-in
// knowingly rather than discover the taint after enabling.
func TestDetectAMDOverdrive_TaintWarning(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		release string
		want    bool
	}{
		{"6.13.0-generic — no taint", "6.13.0-generic", false},
		{"6.14.0 exactly — taint", "6.14.0", true},
		{"6.14.0-1-pve — taint", "6.14.0-1-pve", true},
		{"6.15.0 — taint", "6.15.0", true},
		{"6.20.0 — taint", "6.20.0", true},
		{"5.15.0-LTS — no taint", "5.15.0-generic", false},
		{"6.6.0-LTS — no taint", "6.6.0-generic", false},
		{"7.0.0 — taint (future)", "7.0.0", true},
		{"unparseable — no taint (conservative)", "garbage", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sys := fstest.MapFS{
				"module/amdgpu/parameters/ppfeaturemask": &fstest.MapFile{
					Data: []byte("0xffffffff\n"),
				},
			}
			proc := fstest.MapFS{
				"sys/kernel/osrelease": &fstest.MapFile{
					Data: []byte(tc.release + "\n"),
				},
			}
			got := DetectAMDOverdrive(sys, proc)
			if got.TaintsKernel != tc.want {
				t.Errorf("release=%q: TaintsKernel=%v, want %v",
					tc.release, got.TaintsKernel, tc.want)
			}
		})
	}
}
