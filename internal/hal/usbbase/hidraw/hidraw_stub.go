//go:build !linux

package hidraw

import (
	"context"
	"time"
)

// Device is a placeholder on non-Linux platforms.
type Device struct{}

func (d *Device) Info() DeviceInfo                         { return DeviceInfo{} }
func (d *Device) SendFeature(_ []byte) error               { return ErrUnsupported }
func (d *Device) GetFeature(_ byte, _ []byte) (int, error) { return 0, ErrUnsupported }
func (d *Device) Write(_ []byte) (int, error)              { return 0, ErrUnsupported }
func (d *Device) Read(_ []byte) (int, error)               { return 0, ErrUnsupported }
func (d *Device) SetReadDeadline(_ time.Time) error        { return ErrUnsupported }
func (d *Device) Close() error                             { return ErrUnsupported }

func Enumerate(_ []Matcher) ([]DeviceInfo, error) { return nil, ErrUnsupported }
func Open(_ string) (*Device, error)              { return nil, ErrUnsupported }
func OpenReadOnly(_ string) (*Device, error)      { return nil, ErrUnsupported }
func Watch(_ context.Context, _ []Matcher) (<-chan Event, error) {
	return nil, ErrUnsupported
}
