//go:build !cgo || !hidraw

package usbbase

import "fmt"

// New returns a stub Bus that reports unavailability. USB HID access requires
// CGO (go-hid wraps the hidraw C kernel interface); build with CGO_ENABLED=1
// for production use.
func New() *Bus {
	return &Bus{layer: &stubLayer{}}
}

type stubLayer struct{}

func (s *stubLayer) Enumerate() ([]DeviceInfo, error) {
	return nil, fmt.Errorf("usbbase: not available: build with CGO_ENABLED=1")
}

func (s *stubLayer) OpenPath(_ string) (RawDevice, error) {
	return nil, fmt.Errorf("usbbase: not available: build with CGO_ENABLED=1")
}
