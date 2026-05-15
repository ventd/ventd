package ec

import (
	"fmt"
	"io"
	"os"
	"time"
)

// /dev/port-based EC transport. ACPI 4.0 §12.3 specifies a two-port
// interface at 0x66 (command/status) and 0x62 (data). Linux exposes
// raw port I/O via `/dev/port` — a positional read at offset N is
// equivalent to an `inb` from port N; positional write is `outb`.
// Requires CAP_SYS_RAWIO (ventd runs as root).
//
// Status byte bits (read from 0x66):
//
//	bit 0 — OBF (output buffer full)  : 1 → data is ready in 0x62
//	bit 1 — IBF (input buffer full)   : 1 → wait, EC hasn't consumed last command/data
//
// Read protocol (8-bit):
//
//	wait IBF clear
//	outb 0x66, 0x80    (READ_EC command)
//	wait IBF clear
//	outb 0x62, reg
//	wait OBF set
//	inb 0x62 -> value
//
// Write protocol (8-bit):
//
//	wait IBF clear
//	outb 0x66, 0x81    (WRITE_EC command)
//	wait IBF clear
//	outb 0x62, reg
//	wait IBF clear
//	outb 0x62, value

const (
	portCmdStatus uint8 = 0x66
	portData      uint8 = 0x62

	cmdReadEC  uint8 = 0x80
	cmdWriteEC uint8 = 0x81

	statusOBF = 0x01
	statusIBF = 0x02
)

// devPortPath is /dev/port. Indirected for test seam.
const devPortPath = "/dev/port"

// openDevPortFn is the test injection seam. Production: openDevPortReal.
var openDevPortFn = openDevPortReal

// pollInterval is the busy-wait sleep step inside the OBF/IBF wait
// loops. ACPI doesn't mandate a value; 10 µs is the
// `nbfc-linux/src/ec_linux.c` choice.
var pollInterval = 10 * time.Microsecond

// pollTimeout is the absolute deadline for a single handshake wait.
// `nbfc-linux` uses 1 ms; we match. A handshake that takes longer
// is a wedged EC and we surface ErrECBusy.
var pollTimeout = 1 * time.Millisecond

// devPortFile abstracts the positional-IO ops we need from *os.File.
type devPortFile interface {
	io.ReaderAt
	io.WriterAt
	io.Closer
}

func openDevPortReal() (devPortFile, error) {
	f, err := os.OpenFile(devPortPath, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("dev_port: open %s: %w", devPortPath, err)
	}
	return f, nil
}

// devPortTransport implements Transport via /dev/port port I/O.
type devPortTransport struct {
	f devPortFile
}

// openDevPort opens the /dev/port transport.
func openDevPort() (Transport, error) {
	f, err := openDevPortFn()
	if err != nil {
		return nil, err
	}
	return &devPortTransport{f: f}, nil
}

func (t *devPortTransport) Name() string { return "dev_port" }

func (t *devPortTransport) Close() error {
	if t.f == nil {
		return nil
	}
	err := t.f.Close()
	t.f = nil
	return err
}

// outb writes one byte to the named port.
func (t *devPortTransport) outb(port uint8, val uint8) error {
	buf := [1]byte{val}
	if _, err := t.f.WriteAt(buf[:], int64(port)); err != nil {
		return fmt.Errorf("dev_port: outb %#x <- %#x: %w", port, val, err)
	}
	return nil
}

// inb reads one byte from the named port.
func (t *devPortTransport) inb(port uint8) (uint8, error) {
	var buf [1]byte
	if _, err := t.f.ReadAt(buf[:], int64(port)); err != nil {
		return 0, fmt.Errorf("dev_port: inb %#x: %w", port, err)
	}
	return buf[0], nil
}

// waitStatus polls the command/status port until the named bit
// matches `want` (true → bit set, false → bit clear). Returns
// ErrECBusy on timeout per RULE-NBFC-EC-03.
func (t *devPortTransport) waitStatus(bit uint8, want bool) error {
	deadline := time.Now().Add(pollTimeout)
	for {
		s, err := t.inb(portCmdStatus)
		if err != nil {
			return err
		}
		isSet := s&bit != 0
		if isSet == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: bit=%#x want=%v status=%#x", ErrECBusy, bit, want, s)
		}
		time.Sleep(pollInterval)
	}
}

// Read implements Transport.Read using the ACPI READ_EC protocol.
func (t *devPortTransport) Read(reg uint8) (uint8, error) {
	if err := t.waitStatus(statusIBF, false); err != nil {
		return 0, err
	}
	if err := t.outb(portCmdStatus, cmdReadEC); err != nil {
		return 0, err
	}
	if err := t.waitStatus(statusIBF, false); err != nil {
		return 0, err
	}
	if err := t.outb(portData, reg); err != nil {
		return 0, err
	}
	if err := t.waitStatus(statusOBF, true); err != nil {
		return 0, err
	}
	return t.inb(portData)
}

// Write implements Transport.Write using the ACPI WRITE_EC protocol.
func (t *devPortTransport) Write(reg uint8, val uint8) error {
	if err := t.waitStatus(statusIBF, false); err != nil {
		return err
	}
	if err := t.outb(portCmdStatus, cmdWriteEC); err != nil {
		return err
	}
	if err := t.waitStatus(statusIBF, false); err != nil {
		return err
	}
	if err := t.outb(portData, reg); err != nil {
		return err
	}
	if err := t.waitStatus(statusIBF, false); err != nil {
		return err
	}
	return t.outb(portData, val)
}

// Read16 reads two consecutive 8-bit registers and assembles them
// little-endian. nbfc-linux's ec_read_word does the same.
func (t *devPortTransport) Read16(reg uint8) (uint16, error) {
	lo, err := t.Read(reg)
	if err != nil {
		return 0, err
	}
	hi, err := t.Read(reg + 1)
	if err != nil {
		return 0, err
	}
	return uint16(lo) | uint16(hi)<<8, nil
}

// Write16 writes a 16-bit little-endian value across two registers.
func (t *devPortTransport) Write16(reg uint8, val uint16) error {
	if err := t.Write(reg, byte(val)); err != nil {
		return err
	}
	return t.Write(reg+1, byte(val>>8))
}
