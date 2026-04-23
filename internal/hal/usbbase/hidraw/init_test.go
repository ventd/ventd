//go:build linux

package hidraw

// TestIoctlNumbers_MatchKernelUAPI verifies the hidrawDevinfo struct layout
// and the HIDIOCGRAWINFO / HIDIOCGRDESCSIZE constants against the kernel uapi
// values derived from include/uapi/linux/hidraw.h.
//
// RULE-HIDRAW-06: ioctl numbers MUST match kernel uapi layout.
import (
	"testing"
	"unsafe"
)

func TestIoctlNumbers_MatchKernelUAPI(t *testing.T) {
	if sz := unsafe.Sizeof(hidrawDevinfo{}); sz != 8 {
		t.Errorf("unsafe.Sizeof(hidrawDevinfo{}) = %d, want 8 (u32+s16+s16, no padding)", sz)
	}
	if v := HIDIOCGRAWINFO; v != 0x80084803 {
		t.Errorf("HIDIOCGRAWINFO = %#x, want 0x80084803", v)
	}
	if v := uintptr(HIDIOCGRDESCSIZE); v != 0x80044801 {
		t.Errorf("HIDIOCGRDESCSIZE = %#x, want 0x80044801", v)
	}
}
