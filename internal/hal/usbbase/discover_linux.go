//go:build linux

package usbbase

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/hal/usbbase/hidraw"
)

func platformEnumerate(matchers []Matcher) ([]Device, error) {
	if len(matchers) == 0 {
		return nil, nil
	}
	hMatchers := toHidrawMatchers(matchers)
	infos, err := hidraw.Enumerate(hMatchers)
	if err != nil {
		return nil, fmt.Errorf("usbbase: enumerate: %w", err)
	}

	var out []Device
	for _, info := range infos {
		dev, err := hidraw.Open(info.Path)
		if err != nil {
			slog.Warn("usbbase: open failed", "path", info.Path, "err", err)
			continue
		}
		out = append(out, &hidrawAdapter{dev: dev, info: info})
	}
	return out, nil
}

func platformWatch(ctx context.Context, matchers []Matcher) (<-chan Event, error) {
	hMatchers := toHidrawMatchers(matchers)
	hCh, err := hidraw.Watch(ctx, hMatchers)
	if err != nil {
		return nil, fmt.Errorf("usbbase: watch: %w", err)
	}

	out := make(chan Event, 16)
	var mu sync.Mutex
	opened := make(map[string]*hidrawAdapter)

	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case hev, ok := <-hCh:
				if !ok {
					return
				}
				switch hev.Kind {
				case hidraw.Add:
					if !MatchesAny(matchers, hev.Info.VendorID, hev.Info.ProductID, hev.Info.InterfaceNumber) {
						continue
					}
					dev, err := hidraw.Open(hev.Info.Path)
					if err != nil {
						slog.Warn("usbbase: watch: open failed", "path", hev.Info.Path, "err", err)
						continue
					}
					adapter := &hidrawAdapter{dev: dev, info: hev.Info}
					mu.Lock()
					opened[hev.Info.Path] = adapter
					mu.Unlock()
					select {
					case out <- Event{Kind: Add, Device: adapter}:
					case <-ctx.Done():
						return
					}
				case hidraw.Remove:
					mu.Lock()
					adapter, exists := opened[hev.Info.Path]
					if exists {
						delete(opened, hev.Info.Path)
					}
					mu.Unlock()
					if !exists {
						continue
					}
					select {
					case out <- Event{Kind: Remove, Device: adapter}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return out, nil
}

// toHidrawMatchers converts usbbase.Matcher to hidraw.Matcher.
func toHidrawMatchers(ms []Matcher) []hidraw.Matcher {
	out := make([]hidraw.Matcher, len(ms))
	for i, m := range ms {
		out[i] = hidraw.Matcher{
			VendorID:   m.VendorID,
			ProductIDs: m.ProductIDs,
			Interface:  m.Interface,
		}
	}
	return out
}

// hidrawAdapter wraps *hidraw.Device and implements usbbase.Device.
type hidrawAdapter struct {
	dev  *hidraw.Device
	info hidraw.DeviceInfo
}

func (a *hidrawAdapter) VendorID() uint16     { return a.info.VendorID }
func (a *hidrawAdapter) ProductID() uint16    { return a.info.ProductID }
func (a *hidrawAdapter) SerialNumber() string { return a.info.SerialNumber }

func (a *hidrawAdapter) Read(buf []byte, timeout time.Duration) (int, error) {
	if err := a.dev.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return 0, fmt.Errorf("usbbase: set deadline: %w", err)
	}
	return a.dev.Read(buf)
}

func (a *hidrawAdapter) Write(buf []byte) (int, error) {
	return a.dev.Write(buf)
}

func (a *hidrawAdapter) Close() error {
	return a.dev.Close()
}
