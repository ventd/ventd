//go:build linux

package hidraw

// _ioc encodes a Linux ioctl number per asm-generic/ioctl.h.
// _IOC(dir, type, nr, size) = (dir<<30)|(size<<16)|(type<<8)|nr
// dir: 2=read, 1=write, 3=read|write.
func _ioc(dir, typ, nr, size uintptr) uintptr {
	return (dir << 30) | (size << 16) | (typ << 8) | nr
}

const (
	// HIDIOCGRDESCSIZE = _IOR('H', 0x01, int) → 0x80044801
	// sizeof(int)==4 on amd64/arm64; verified in TestIoctlNumbers_MatchKernelUAPI.
	HIDIOCGRDESCSIZE uintptr = (2 << 30) | (4 << 16) | (0x48 << 8) | 0x01

	// HIDIOCGRAWINFO = _IOR('H', 0x03, struct hidraw_devinfo) → 0x80084803
	// sizeof(hidrawDevinfo)==8; verified in TestIoctlNumbers_MatchKernelUAPI.
	HIDIOCGRAWINFO uintptr = (2 << 30) | (8 << 16) | (0x48 << 8) | 0x03
)

// hidiocsfeature encodes HIDIOCSFEATURE(n) = _IOC(_IOC_WRITE|_IOC_READ,'H',0x06,n)
func hidiocsfeature(n uintptr) uintptr { return _ioc(3, 0x48, 0x06, n) }

// hidiocgfeature encodes HIDIOCGFEATURE(n) = _IOC(_IOC_WRITE|_IOC_READ,'H',0x07,n)
func hidiocgfeature(n uintptr) uintptr { return _ioc(3, 0x48, 0x07, n) }

// hidrawDevinfo mirrors kernel struct hidraw_devinfo from include/uapi/linux/hidraw.h:
//
//	struct hidraw_devinfo { __u32 bustype; __s16 vendor; __s16 product; }
//
// 8 bytes, no padding on amd64/arm64. Verified by TestIoctlNumbers_MatchKernelUAPI.
type hidrawDevinfo struct {
	bustype uint32
	vendor  int16
	product int16
}
