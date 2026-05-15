package ec

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// ecSysPath is the canonical debugfs path the `ec_sys` kernel module
// exposes. The file is exactly 256 bytes — the full EC register
// space — and supports positional ReadAt/WriteAt. Indirected for
// test injection via openECSysFn.
const ecSysPath = "/sys/kernel/debug/ec/ec0/io"

// openECSysFn is the seam tests use to inject a fake EC backing
// store. Production: openECSysReal.
var openECSysFn = openECSysReal

// ecSysParamPath is the sysfs path for the kernel module's
// write_support parameter. When the module is loaded with
// `ec_sys.write_support=1` this file contains "Y"; otherwise "N".
const ecSysParamPath = "/sys/module/ec_sys/parameters/write_support"

// readECSysWriteSupport returns true when the `ec_sys` kernel module
// was loaded with `write_support=1`. The kernel exposes the live
// parameter at `/sys/module/ec_sys/parameters/write_support` ("Y"
// or "N"). Indirected so the preflight check + transport open path
// both consult the same code.
var readECSysWriteSupport = func() (bool, error) {
	data, err := os.ReadFile(ecSysParamPath)
	if err != nil {
		return false, fmt.Errorf("ec_sys: read %s: %w", ecSysParamPath, err)
	}
	s := strings.TrimSpace(string(data))
	return s == "Y" || s == "y" || s == "1", nil
}

// ecSysFile abstracts the positional-IO operations we need from the
// underlying *os.File. Lets tests substitute an in-memory buffer.
type ecSysFile interface {
	io.ReaderAt
	io.WriterAt
	io.Closer
}

// openECSysReal opens the production /sys/kernel/debug/ec/ec0/io
// file with RDWR access. Returns ErrECSysWriteSupportDisabled when
// write_support=0; in that case the caller should attempt
// /dev/port instead or surface the preflight remediation.
func openECSysReal() (ecSysFile, error) {
	ok, err := readECSysWriteSupport()
	if err != nil {
		return nil, fmt.Errorf("ec_sys: probe write_support: %w", err)
	}
	if !ok {
		return nil, ErrECSysWriteSupportDisabled
	}
	f, err := os.OpenFile(ecSysPath, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("ec_sys: open %s: %w", ecSysPath, err)
	}
	return f, nil
}

// ecSysTransport implements Transport against the ec_sys debugfs
// flat-file interface. The kernel module handles the OBF/IBF
// handshake internally; userspace just reads/writes bytes at the
// register offset.
type ecSysTransport struct {
	f ecSysFile
}

// openECSys opens the ec_sys transport via the injectable seam.
func openECSys() (Transport, error) {
	f, err := openECSysFn()
	if err != nil {
		return nil, err
	}
	return &ecSysTransport{f: f}, nil
}

func (t *ecSysTransport) Name() string { return "ec_sys" }

func (t *ecSysTransport) Close() error {
	if t.f == nil {
		return nil
	}
	err := t.f.Close()
	t.f = nil
	return err
}

func (t *ecSysTransport) Read(reg uint8) (uint8, error) {
	var buf [1]byte
	if _, err := t.f.ReadAt(buf[:], int64(reg)); err != nil {
		return 0, fmt.Errorf("ec_sys: read reg %#x: %w", reg, err)
	}
	return buf[0], nil
}

func (t *ecSysTransport) Write(reg uint8, val uint8) error {
	buf := [1]byte{val}
	if _, err := t.f.WriteAt(buf[:], int64(reg)); err != nil {
		return fmt.Errorf("ec_sys: write reg %#x = %#x: %w", reg, val, err)
	}
	return nil
}

func (t *ecSysTransport) Read16(reg uint8) (uint16, error) {
	var buf [2]byte
	if _, err := t.f.ReadAt(buf[:], int64(reg)); err != nil {
		return 0, fmt.Errorf("ec_sys: read16 reg %#x: %w", reg, err)
	}
	// nbfc-linux's ec_read_word is little-endian: low byte at reg,
	// high byte at reg+1.
	return uint16(buf[0]) | uint16(buf[1])<<8, nil
}

func (t *ecSysTransport) Write16(reg uint8, val uint16) error {
	buf := [2]byte{byte(val), byte(val >> 8)}
	if _, err := t.f.WriteAt(buf[:], int64(reg)); err != nil {
		return fmt.Errorf("ec_sys: write16 reg %#x = %#x: %w", reg, val, err)
	}
	return nil
}
