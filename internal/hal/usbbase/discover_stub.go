//go:build !linux || !cgo || !hidraw

package usbbase

import "context"

func platformEnumerate(_ []Matcher) ([]Device, error) {
	return nil, ErrUnsupported
}

func platformWatch(_ context.Context, _ []Matcher) (<-chan Event, error) {
	return nil, ErrUnsupported
}
