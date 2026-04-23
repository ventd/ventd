package usbbase

import "context"

// Enumerate returns all USB HID devices matching any Matcher.
// Returns ErrUnsupported on platforms or build configurations without
// hidraw support (non-Linux, or Linux built without CGO and the hidraw tag).
// An empty matchers slice returns nothing even on supported platforms.
func Enumerate(matchers []Matcher) ([]Device, error) {
	return platformEnumerate(matchers)
}

// Watch streams USB hotplug Events for devices matching any Matcher.
// The returned channel is closed when ctx is cancelled.
// Returns ErrUnsupported on unsupported platforms.
func Watch(ctx context.Context, matchers []Matcher) (<-chan Event, error) {
	return platformWatch(ctx, matchers)
}
