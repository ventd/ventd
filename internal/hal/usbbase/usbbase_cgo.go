//go:build cgo && hidraw

package usbbase

import (
	"fmt"

	hid "github.com/sstallion/go-hid"
)

// New returns a Bus backed by the hidraw kernel interface via go-hid.
// The daemon always uses this path; tests use NewWithLayer instead.
func New() *Bus {
	return &Bus{layer: &hidrawLayer{}}
}

// compile-time proof that *hid.Device satisfies RawDevice.
var _ RawDevice = (*hid.Device)(nil)

type hidrawLayer struct{}

func (l *hidrawLayer) Enumerate() ([]Device, error) {
	var out []Device
	err := hid.Enumerate(hid.VendorIDAny, hid.ProductIDAny, func(info *hid.DeviceInfo) error {
		out = append(out, Device{
			VendorID:     info.VendorID,
			ProductID:    info.ProductID,
			Path:         info.Path,
			Manufacturer: info.MfrStr,
			Product:      info.ProductStr,
			Serial:       info.SerialNbr,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("usbbase: enumerate: %w", err)
	}
	return out, nil
}

func (l *hidrawLayer) OpenPath(path string) (RawDevice, error) {
	dev, err := hid.OpenPath(path)
	if err != nil {
		return nil, fmt.Errorf("usbbase: open %q: %w", path, err)
	}
	return dev, nil
}
