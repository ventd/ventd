package detection

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

const corsairVID = "1b1c"

// CollectCorsairAIO gathers HID device info for Corsair devices (§12.5).
// Only runs when a VID 0x1b1c hidraw device is present.
func CollectCorsairAIO(ctx context.Context) CollectResult {
	var res CollectResult

	devs := findCorsairHidraw()
	if len(devs) == 0 {
		return res // no Corsair device present
	}

	for _, devPath := range devs {
		name := filepath.Base(devPath)
		// uevent for VID/PID/physical path.
		ueventPath := filepath.Join("/sys/class/hidraw", name, "device", "uevent")
		if data := readFile(ueventPath); len(data) > 0 {
			res.Items = append(res.Items, textItem(
				"commands/corsair_aio/"+name+"_uevent", data,
			))
		}
		// hidraw devinfo via ioctl would require cgo; capture uevent instead.
	}
	_ = ctx
	return res
}

// findCorsairHidraw returns /dev/hidrawN paths for Corsair VID 0x1b1c devices.
func findCorsairHidraw() []string {
	entries, err := os.ReadDir("/sys/class/hidraw")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		uevent := readFile(filepath.Join("/sys/class/hidraw", e.Name(), "device", "uevent"))
		if strings.Contains(strings.ToLower(string(uevent)), corsairVID) {
			out = append(out, "/dev/"+e.Name())
		}
	}
	return out
}
