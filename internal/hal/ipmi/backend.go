// Package ipmi is the IPMI implementation of hal.FanBackend. It communicates
// with the BMC via /dev/ipmi0 using raw ioctls (IPMICTL_SEND_COMMAND and
// IPMICTL_RECEIVE_MSG_TRUNC) — no shell-out to ipmitool, no external library.
//
// DMI gating: Enumerate returns empty on non-server chassis to prevent sending
// BMC commands to random /dev/ipmi* devices on desktops. A machine qualifies
// as a server if chassis_type == 23 (rack-mount) OR the system vendor string
// matches a known server OEM (Supermicro, Dell, HP/HPE, Lenovo).
//
// Vendor-specific write paths are implemented for Supermicro and Dell.
// HPE returns a clear error (iLO Advanced required). Unknown vendors support
// read-only enumeration via Get Sensor Reading.
//
// Struct layout notes: the ioctl structs below match the 64-bit Linux
// (amd64 and arm64) kernel ABI for linux/ipmi.h.  Compile-time size
// assertions at the end of the var block guard against accidental misalignment.
package ipmi

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/ventd/ventd/internal/hal"
)

// BackendName is the registry tag applied to channels produced by this backend.
const BackendName = "ipmi"

// Vendor strings returned by detectVendorFromString.
const (
	vendorSupermicro = "supermicro"
	vendorDell       = "dell"
	vendorHPE        = "hpe"
	vendorUnknown    = "unknown"
)

// knownServerVendors are the sys_vendor substrings that classify a machine
// as a server even when chassis_type != 23. Matches are case-insensitive.
var knownServerVendors = []string{
	"supermicro",
	"dell",
	"hp",
	"lenovo",
}

// ipmiRackMountChassis is the SMBIOS chassis type for rack-mount servers.
const ipmiRackMountChassis = 23

// IPMI ioctl numbers for 64-bit Linux (amd64 and arm64).
// Computed as:
//
//	IPMICTL_SEND_COMMAND      = _IOR('i', 13, sizeof(ipmiReq{}))   = 0x8028690d
//	IPMICTL_RECEIVE_MSG_TRUNC = _IOWR('i', 11, sizeof(ipmiRecv{})) = 0xc030690b
const (
	ioctlSendCommand     uintptr = 0x8028690d
	ioctlReceiveMsgTrunc uintptr = 0xc030690b
)

// IPMI system interface address type and BMC channel.
const (
	ipmiSysIfaceAddrType = int32(0x0c)
	ipmiBMCChannel       = int16(0x0f)
)

// IPMI network function codes used by this backend.
const (
	netFnSensor  = uint8(0x04)
	netFnStorage = uint8(0x0a)
	netFnOEM     = uint8(0x30)
)

// IPMI command codes.
const (
	cmdGetSensorReading     = uint8(0x2d)
	cmdReserveSDRRepository = uint8(0x22)
	cmdGetSDR               = uint8(0x23)

	// Supermicro OEM fan commands (NetFn=0x30)
	cmdSMFanWrite = uint8(0x70) // SET_FAN_SPEED — payload: [0x66, 0x01, zone, pct]
	cmdSMSetMode  = uint8(0x45) // SET_FAN_MODE  — payload: [mode]; 0x00 = Standard/auto

	// Dell OEM fan command (NetFn=0x30); sub-function in first data byte
	cmdDellFan = uint8(0x30)
)

// sensorTypeFan is the IPMI sensor type for fan sensors.
const sensorTypeFan = uint8(0x04)

// ipmiResponseTimeoutMs is the maximum time to wait for a BMC response.
const ipmiResponseTimeoutMs = 5000

// ── ioctl struct definitions (64-bit Linux) ──────────────────────────────────
//
// These must match the C layout exactly. Padding fields are made explicit so
// the compiler does not insert implicit padding in a different location.

// ipmiSysIfaceAddr matches struct ipmi_system_interface_addr (8 bytes).
type ipmiSysIfaceAddr struct {
	addrType int32
	channel  int16
	lun      uint8
	_        [1]byte
}

// ipmiMsg matches struct ipmi_msg (16 bytes on 64-bit):
// netfn(1) + cmd(1) + datalen(2) + pad(4) + data_ptr(8).
type ipmiMsg struct {
	netfn   uint8
	cmd     uint8
	datalen uint16
	_       [4]byte // pad data pointer to 8-byte boundary
	data    uintptr
}

// ipmiReq matches struct ipmi_req (40 bytes on 64-bit):
// addr_ptr(8) + addr_len(4) + pad(4) + msgid(8) + msg(16).
type ipmiReq struct {
	addr    uintptr
	addrLen uint32
	_       [4]byte
	msgID   int64
	msg     ipmiMsg
}

// ipmiRecv matches struct ipmi_recv (48 bytes on 64-bit):
// recv_type(4) + pad(4) + addr_ptr(8) + addr_len(4) + pad(4) + msgid(8) + msg(16).
type ipmiRecv struct {
	recvType int32
	_pad1    [4]byte
	addr     uintptr
	addrLen  uint32
	_pad2    [4]byte
	msgID    int64
	msg      ipmiMsg
}

// Compile-time size assertions.  These fail to compile if the struct layouts
// do not match the kernel ABI, preventing silent mis-communication to the BMC.
var _ [40]byte = [unsafe.Sizeof(ipmiReq{})]byte{}
var _ [48]byte = [unsafe.Sizeof(ipmiRecv{})]byte{}

// ── per-channel state ────────────────────────────────────────────────────────

// State is the per-channel payload carried in hal.Channel.Opaque.
type State struct {
	SensorNumber uint8
	SensorName   string
	// SDR conversion coefficients for raw reading → RPM.
	M    int16 // 10-bit signed resolution multiplier
	B    int16 // 10-bit signed offset
	RExp int8  // 4-bit signed result exponent (K2)
	BExp int8  // 4-bit signed B exponent (K1)
	// Zone is the Supermicro fan zone derived from sensor number.
	Zone uint8
}

// ── DMI helpers ──────────────────────────────────────────────────────────────

// dmiInfo holds the DMI fields used for IPMI server gating and vendor detection.
type dmiInfo struct {
	chassisType int
	sysVendor   string
}

// readDMIFromSysfs reads chassis_type and sys_vendor from /sys/class/dmi/id.
func readDMIFromSysfs() dmiInfo {
	var info dmiInfo
	if raw, err := os.ReadFile("/sys/class/dmi/id/chassis_type"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			info.chassisType = n
		}
	}
	if raw, err := os.ReadFile("/sys/class/dmi/id/sys_vendor"); err == nil {
		info.sysVendor = strings.TrimSpace(string(raw))
	}
	return info
}

// isServerChassis returns true when the DMI indicates a server system.
func isServerChassis(info dmiInfo) bool {
	if info.chassisType == ipmiRackMountChassis {
		return true
	}
	lower := strings.ToLower(info.sysVendor)
	for _, v := range knownServerVendors {
		if strings.Contains(lower, v) {
			return true
		}
	}
	return false
}

// detectVendorFromString classifies a sys_vendor string into a vendor constant.
// Matching is case-insensitive substring.
func detectVendorFromString(vendor string) string {
	lower := strings.ToLower(vendor)
	switch {
	case strings.Contains(lower, "supermicro"),
		strings.Contains(lower, "super micro"):
		return vendorSupermicro
	case strings.Contains(lower, "dell"):
		return vendorDell
	case strings.Contains(lower, "hp"), // "hp", "hpe"
		strings.Contains(lower, "hewlett"): // "hewlett-packard", "hewlett packard enterprise"
		return vendorHPE
	default:
		return vendorUnknown
	}
}

// ── Option ───────────────────────────────────────────────────────────────────

// Option configures a Backend at construction time. Production callers use
// zero options; test harnesses pass WithDevicePath/WithSendRecv to redirect
// I/O to a fixture.
type Option func(*Backend)

// WithDevicePath overrides the default device path ("/dev/ipmi0").
// Intended for test fixtures that open a tmpfile or socket-pair.
func WithDevicePath(p string) Option {
	return func(b *Backend) { b.device = p }
}

// WithSendRecv replaces the default ioctl-based transport with a
// fixture-provided function. When set, the backend calls fn(req, resp)
// instead of issuing IPMICTL_SEND_COMMAND / IPMICTL_RECEIVE_MSG_TRUNC.
// req encodes [netfn, cmd, data...]; resp is a 128-byte buffer the fixture
// fills with [completionCode, payload...].
func WithSendRecv(fn func(req, resp []byte) error) Option {
	return func(b *Backend) { b.sendRecv = fn }
}

// ── Backend ──────────────────────────────────────────────────────────────────

// Backend is the IPMI implementation of hal.FanBackend.
type Backend struct {
	device  string
	logger  *slog.Logger
	vendor  string
	dmi     dmiInfo
	readDMI func() dmiInfo

	mu sync.Mutex // serialises the full send-poll-receive cycle
	fd int        // -1 = closed; guarded by mu for open/close

	msgSeq atomic.Int64 // per-request sequence number

	sendRecv func(req, resp []byte) error // nil = ioctl path; non-nil = test override
}

// NewBackend constructs an IPMI backend. The device defaults to /dev/ipmi0.
// DMI is read at construction; /dev/ipmi0 is not opened until Enumerate
// confirms a server chassis.
func NewBackend(logger *slog.Logger, opts ...Option) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	b := &Backend{
		device:  "/dev/ipmi0",
		logger:  logger,
		readDMI: readDMIFromSysfs,
		fd:      -1,
	}
	for _, opt := range opts {
		opt(b)
	}
	b.dmi = b.readDMI()
	b.vendor = detectVendorFromString(b.dmi.sysVendor)
	return b
}

// withDMI returns a copy of b with the DMI info replaced; used by tests via
// NewBackendForTest in export_test.go.
func (b *Backend) withDMI(info dmiInfo) *Backend {
	return &Backend{
		device:   b.device,
		logger:   b.logger,
		readDMI:  b.readDMI,
		dmi:      info,
		vendor:   detectVendorFromString(info.sysVendor),
		fd:       -1,
		sendRecv: b.sendRecv,
	}
}

// withVendor returns a copy of b with the vendor overridden; used by tests.
func (b *Backend) withVendor(vendor string) *Backend {
	return &Backend{
		device:   b.device,
		logger:   b.logger,
		readDMI:  b.readDMI,
		dmi:      b.dmi,
		vendor:   vendor,
		fd:       -1,
		sendRecv: b.sendRecv,
	}
}

// Name returns the stable backend identifier.
func (b *Backend) Name() string { return BackendName }

// Close releases the /dev/ipmi0 file descriptor. Safe to call twice.
func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fd < 0 {
		return nil
	}
	err := unix.Close(b.fd)
	b.fd = -1
	return err
}

// Enumerate confirms server chassis via DMI, then walks the BMC's SDR to
// return one Channel per fan sensor. Returns empty (no error) on non-server
// systems or when /dev/ipmi0 is unavailable.
func (b *Backend) Enumerate(ctx context.Context) ([]hal.Channel, error) {
	if !isServerChassis(b.dmi) {
		b.logger.Info("ipmi: not a server chassis, skipping")
		return nil, nil
	}

	if err := b.openOnce(); err != nil {
		b.logger.Info("ipmi: device unavailable", "err", err)
		return nil, nil
	}

	channels, err := b.enumerateSDR()
	if err != nil {
		b.logger.Warn("ipmi: SDR enumeration failed", "err", err)
		return nil, nil
	}
	return channels, nil
}

// Read samples the current fan RPM via Get Sensor Reading.
func (b *Backend) Read(ch hal.Channel) (hal.Reading, error) {
	st, err := stateFrom(ch)
	if err != nil {
		return hal.Reading{}, err
	}

	cc, resp, err := b.ioctlSendRecv(netFnSensor, cmdGetSensorReading, []byte{st.SensorNumber})
	if err != nil {
		return hal.Reading{OK: false}, nil
	}
	if cc != 0 || len(resp) < 1 {
		return hal.Reading{OK: false}, nil
	}

	// Byte 1 (resp[0] is the reading): byte 2 validity bit 6 = scanning enabled.
	if len(resp) >= 2 && resp[1]&0x40 == 0 {
		return hal.Reading{OK: false}, nil
	}

	rpm := sdrToRPM(resp[0], st)
	return hal.Reading{RPM: rpm, OK: true}, nil
}

// Write sends a vendor-specific fan-speed command to the BMC.
// PWM 0-255 is converted to 0-100 percent before dispatch.
func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	st, err := stateFrom(ch)
	if err != nil {
		return err
	}

	pct := pwmToPercent(pwm)

	switch b.vendor {
	case vendorSupermicro:
		cc, _, err := b.ioctlSendRecv(netFnOEM, cmdSMFanWrite, []byte{0x66, 0x01, st.Zone, pct})
		if err != nil {
			return fmt.Errorf("ipmi: supermicro fan write: %w", err)
		}
		if cc != 0 {
			return fmt.Errorf("ipmi: supermicro fan write completion code 0x%02x", cc)
		}
		return nil

	case vendorDell:
		cc, _, err := b.ioctlSendRecv(netFnOEM, cmdDellFan, []byte{0x02, 0xff, pct})
		if err != nil {
			return fmt.Errorf("ipmi: dell fan write: %w", err)
		}
		if cc != 0 {
			return fmt.Errorf("ipmi: dell fan write completion code 0x%02x", cc)
		}
		return nil

	case vendorHPE:
		return errors.New("ipmi: HPE fan control requires iLO Advanced; not supported")

	default:
		return errors.New("ipmi: unsupported vendor for fan control")
	}
}

// Restore returns fan control to firmware-auto mode.
//
//   - Supermicro: SET_FAN_MODE=0x00 (Standard / firmware auto)
//   - Dell: enable automatic fan control sub-command [0x01, 0x01]
//   - HPE / unknown: no-op (control was never taken)
func (b *Backend) Restore(ch hal.Channel) error {
	switch b.vendor {
	case vendorSupermicro:
		cc, _, err := b.ioctlSendRecv(netFnOEM, cmdSMSetMode, []byte{0x00})
		if err != nil {
			b.logger.Error("ipmi: supermicro fan mode restore failed", "err", err)
			return err
		}
		if cc != 0 {
			return fmt.Errorf("ipmi: supermicro restore completion code 0x%02x", cc)
		}
		b.logger.Info("ipmi: fans restored to standard auto mode", "vendor", vendorSupermicro)
		return nil

	case vendorDell:
		cc, _, err := b.ioctlSendRecv(netFnOEM, cmdDellFan, []byte{0x01, 0x01})
		if err != nil {
			b.logger.Error("ipmi: dell fan control restore failed", "err", err)
			return err
		}
		if cc != 0 {
			return fmt.Errorf("ipmi: dell restore completion code 0x%02x", cc)
		}
		b.logger.Info("ipmi: fans restored to standard auto mode", "vendor", vendorDell)
		return nil

	default:
		return nil
	}
}

// ── device open ──────────────────────────────────────────────────────────────

func (b *Backend) openOnce() error {
	if b.sendRecv != nil {
		return nil // fixture mode: no real device needed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fd >= 0 {
		return nil
	}
	fd, err := unix.Open(b.device, unix.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("ipmi: open %s: %w", b.device, err)
	}
	b.fd = fd
	return nil
}

// ── ioctl send / receive ─────────────────────────────────────────────────────

// ioctlSendRecv sends one IPMI request and receives the response.
// The entire send-poll-receive sequence is serialised under b.mu so that
// concurrent callers (e.g. two goroutines calling Enumerate) do not interleave
// requests and responses.
//
// When b.sendRecv is non-nil (test fixture mode), the ioctl path is skipped
// and the fixture function is called instead.
//
// Returns (completionCode, responsePayload, error).
// completionCode 0x00 means success; the caller is responsible for checking it.
func (b *Backend) ioctlSendRecv(netfn, cmd uint8, reqData []byte) (byte, []byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Test-injected hook: bypasses device fd and ioctl entirely.
	if b.sendRecv != nil {
		req := make([]byte, 2+len(reqData))
		req[0] = netfn
		req[1] = cmd
		copy(req[2:], reqData)
		resp := make([]byte, 128)
		if err := b.sendRecv(req, resp); err != nil {
			return 0, nil, err
		}
		return resp[0], resp[1:], nil
	}

	fd := b.fd
	if fd < 0 {
		return 0, nil, errors.New("ipmi: device not open")
	}

	addr := ipmiSysIfaceAddr{
		addrType: ipmiSysIfaceAddrType,
		channel:  ipmiBMCChannel,
		lun:      0,
	}

	var dataPtr uintptr
	if len(reqData) > 0 {
		dataPtr = uintptr(unsafe.Pointer(&reqData[0]))
	}

	msgID := b.msgSeq.Add(1)

	req := ipmiReq{
		addr:    uintptr(unsafe.Pointer(&addr)),
		addrLen: uint32(unsafe.Sizeof(addr)),
		msgID:   msgID,
		msg: ipmiMsg{
			netfn:   netfn,
			cmd:     cmd,
			datalen: uint16(len(reqData)),
			data:    dataPtr,
		},
	}

	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		ioctlSendCommand,
		uintptr(unsafe.Pointer(&req)),
	)
	runtime.KeepAlive(addr)
	runtime.KeepAlive(reqData)
	if errno != 0 {
		return 0, nil, fmt.Errorf("ipmi: IPMICTL_SEND_COMMAND: %w", errno)
	}

	// Poll until the BMC has a response ready.
	pollfds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, err := unix.Poll(pollfds, ipmiResponseTimeoutMs)
	if err != nil {
		return 0, nil, fmt.Errorf("ipmi: poll: %w", err)
	}
	if n == 0 {
		return 0, nil, errors.New("ipmi: BMC response timeout")
	}

	respBuf := make([]byte, 128)
	recvAddr := ipmiSysIfaceAddr{}
	recv := ipmiRecv{
		addr:    uintptr(unsafe.Pointer(&recvAddr)),
		addrLen: uint32(unsafe.Sizeof(recvAddr)),
		msg: ipmiMsg{
			data:    uintptr(unsafe.Pointer(&respBuf[0])),
			datalen: uint16(len(respBuf)),
		},
	}

	_, _, errno = unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		ioctlReceiveMsgTrunc,
		uintptr(unsafe.Pointer(&recv)),
	)
	runtime.KeepAlive(recvAddr)
	runtime.KeepAlive(respBuf)
	if errno != 0 {
		return 0, nil, fmt.Errorf("ipmi: IPMICTL_RECEIVE_MSG_TRUNC: %w", errno)
	}

	dataLen := recv.msg.datalen
	if dataLen == 0 {
		return 0, nil, errors.New("ipmi: empty response from BMC")
	}
	if int(dataLen) > len(respBuf) {
		return 0, nil, fmt.Errorf("ipmi: response truncated (%d > %d)", dataLen, len(respBuf))
	}

	completionCode := respBuf[0]
	var responsePayload []byte
	if dataLen > 1 {
		responsePayload = make([]byte, dataLen-1)
		copy(responsePayload, respBuf[1:dataLen])
	}
	return completionCode, responsePayload, nil
}

// ── SDR enumeration ──────────────────────────────────────────────────────────

// enumerateSDR walks the BMC SDR repository and returns a Channel for each
// Full Sensor Record (type 0x01) whose sensor type is 0x04 (fan).
// Called with b.mu NOT held; each sendRecv call acquires it individually.
func (b *Backend) enumerateSDR() ([]hal.Channel, error) {
	// Reserve SDR repository for a consistent enumeration snapshot.
	cc, resp, err := b.ioctlSendRecv(netFnStorage, cmdReserveSDRRepository, nil)
	if err != nil {
		return nil, fmt.Errorf("ipmi: reserve SDR: %w", err)
	}
	if cc != 0 {
		return nil, fmt.Errorf("ipmi: reserve SDR completion code 0x%02x", cc)
	}
	if len(resp) < 2 {
		return nil, errors.New("ipmi: reserve SDR: short response")
	}
	reservationID := binary.LittleEndian.Uint16(resp[0:2])

	var channels []hal.Channel
	recordID := uint16(0x0000)

	for {
		req := []byte{
			byte(reservationID),
			byte(reservationID >> 8),
			byte(recordID),
			byte(recordID >> 8),
			0x00, // offset into record
			0xFF, // read all bytes
		}

		cc, resp, err := b.ioctlSendRecv(netFnStorage, cmdGetSDR, req)
		if err != nil {
			b.logger.Warn("ipmi: get SDR error", "record_id", fmt.Sprintf("0x%04x", recordID), "err", err)
			break
		}
		if cc != 0 {
			break
		}
		if len(resp) < 3 {
			break
		}

		nextRecordID := binary.LittleEndian.Uint16(resp[0:2])
		sdrData := resp[2:]

		if ch, ok := b.parseSDRRecord(sdrData); ok {
			channels = append(channels, ch)
		}

		if nextRecordID == 0xFFFF {
			break
		}
		recordID = nextRecordID
	}

	return channels, nil
}

// parseSDRRecord extracts a hal.Channel from a Full Sensor Record (0x01) with
// sensor type 0x04 (fan). Returns (channel, true) on success.
func (b *Backend) parseSDRRecord(data []byte) (hal.Channel, bool) {
	if len(data) < 16 {
		return hal.Channel{}, false
	}
	// Byte 3: record type; only Full Sensor Records (0x01) have coefficients.
	if data[3] != 0x01 {
		return hal.Channel{}, false
	}
	// Byte 12: sensor type.
	if data[12] != sensorTypeFan {
		return hal.Channel{}, false
	}

	sensorNumber := data[7]

	// Extract 10-bit signed M and B, and 4-bit signed R_exp / B_exp.
	var m, bCoef int16
	var rExp, bExp int8
	if len(data) >= 32 {
		mLo := int16(data[24])
		mHi := int16(data[25] & 0x03)
		m = (mHi << 8) | mLo
		if m >= 512 {
			m -= 1024
		}

		bLo := int16(data[27])
		bHi := int16(data[28] & 0x03)
		bCoef = (bHi << 8) | bLo
		if bCoef >= 512 {
			bCoef -= 1024
		}

		rExpRaw := int8(data[31] >> 4)
		if rExpRaw >= 8 {
			rExpRaw -= 16
		}
		rExp = rExpRaw

		bExpRaw := int8(data[31] & 0x0F)
		if bExpRaw >= 8 {
			bExpRaw -= 16
		}
		bExp = bExpRaw
	}

	// Extract ASCII sensor name from the ID String field (byte 48 onward).
	name := fmt.Sprintf("Fan %d", sensorNumber)
	if len(data) >= 49 {
		typeLenByte := data[48]
		nameType := (typeLenByte >> 6) & 0x03
		nameLen := int(typeLenByte & 0x3F)
		if nameType == 3 && 49+nameLen <= len(data) {
			s := strings.TrimRight(string(data[49:49+nameLen]), "\x00 ")
			if s != "" {
				name = s
			}
		}
	}

	caps := hal.CapRead | hal.CapRestore
	if b.vendor == vendorSupermicro || b.vendor == vendorDell {
		caps |= hal.CapWritePWM
	}

	st := State{
		SensorNumber: sensorNumber,
		SensorName:   name,
		M:            m,
		B:            bCoef,
		RExp:         rExp,
		BExp:         bExp,
		Zone:         0, // Hard-coded 0 for Supermicro: CPU-zone fans are always zone 0
		// on X11/X12/H13 boards. Dynamic per-board zone discovery is
		// deferred to a future probe (netFn=0x30 cmd=0x66).
	}

	return hal.Channel{
		ID:     fmt.Sprintf("sensor%d", sensorNumber),
		Role:   hal.RoleAIOFan,
		Caps:   caps,
		Opaque: st,
	}, true
}

// ── helpers ──────────────────────────────────────────────────────────────────

// stateFrom coerces a Channel's Opaque to the IPMI State type.
func stateFrom(ch hal.Channel) (State, error) {
	switch v := ch.Opaque.(type) {
	case State:
		return v, nil
	case *State:
		if v == nil {
			return State{}, errors.New("hal/ipmi: nil opaque state")
		}
		return *v, nil
	default:
		return State{}, fmt.Errorf("hal/ipmi: channel %q has wrong opaque type %T", ch.ID, ch.Opaque)
	}
}

// pwmToPercent converts a 0-255 duty cycle to a 0-100 percent value,
// rounding to the nearest integer.
func pwmToPercent(pwm uint8) uint8 {
	pct := int(math.Round(float64(pwm) * 100.0 / 255.0))
	if pct > 100 {
		pct = 100
	}
	return uint8(pct)
}

// sdrToRPM converts a raw Get Sensor Reading byte to RPM using the SDR
// coefficients: RPM = (M * raw + B * 10^BExp) * 10^RExp
func sdrToRPM(raw uint8, st State) uint16 {
	if st.M == 0 && st.B == 0 && st.RExp == 0 && st.BExp == 0 {
		return uint16(raw)
	}
	y := (float64(st.M)*float64(raw) + float64(st.B)*math.Pow10(int(st.BExp))) * math.Pow10(int(st.RExp))
	if y < 0 {
		y = 0
	}
	if y > 65535 {
		y = 65535
	}
	return uint16(math.Round(y))
}
