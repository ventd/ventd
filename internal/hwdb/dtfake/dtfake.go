// Package dtfake provides a synthetic /proc/device-tree filesystem for hwdb tests.
// It mirrors the dmifake pattern used for DMI: callers build a MapFS rooted at
// an in-process directory and pass it to hwdb.ReadDTData or hwdb.IsDMIPresent.
package dtfake

import (
	"strings"
	"testing/fstest"
)

// DTFake holds a synthetic /proc/device-tree filesystem.
type DTFake struct {
	fs fstest.MapFS
}

// New creates an empty DTFake.
func New() *DTFake {
	return &DTFake{fs: make(fstest.MapFS)}
}

// SetCompatible writes the null-separated, null-terminated compatible list to
// proc/device-tree/compatible. Each entry must not contain a null byte.
func (f *DTFake) SetCompatible(entries ...string) *DTFake {
	var buf strings.Builder
	for _, e := range entries {
		buf.WriteString(e)
		buf.WriteByte(0x00)
	}
	f.fs["proc/device-tree/compatible"] = &fstest.MapFile{Data: []byte(buf.String())}
	return f
}

// SetModel writes the null-terminated model string to proc/device-tree/model.
func (f *DTFake) SetModel(model string) *DTFake {
	f.fs["proc/device-tree/model"] = &fstest.MapFile{Data: append([]byte(model), 0x00)}
	return f
}

// FS returns the underlying MapFS for use with hwdb.ReadDTData or fs.FS arguments.
func (f *DTFake) FS() fstest.MapFS { return f.fs }
