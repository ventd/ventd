package usbbase

import "fmt"

// New returns a stub Bus. The Bus API (low-level HIDLayer surface) is used
// only in tests via NewWithLayer; production code uses Enumerate and Watch.
func New() *Bus {
	return &Bus{layer: &stubLayer{}}
}

type stubLayer struct{}

func (s *stubLayer) Enumerate() ([]DeviceInfo, error) {
	return nil, fmt.Errorf("usbbase: Bus.Enumerate not available; use usbbase.Enumerate")
}

func (s *stubLayer) OpenPath(_ string) (RawDevice, error) {
	return nil, fmt.Errorf("usbbase: Bus.Open not available; use usbbase.Enumerate")
}
