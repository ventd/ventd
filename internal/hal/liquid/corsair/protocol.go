package corsair

import (
	"encoding/binary"
	"fmt"
	"time"
)

// frameSize is the HID packet size for Commander Core v2 firmware.
// v1 firmware uses a smaller packet; devices responding with a shorter
// frame are probed as unknownFirmwareDevice.
// ref: commander_core.md §2.1 (packet structure)
const frameSize = 1024

// readTimeout is the per-read deadline for HID responses.
const readTimeout = 2 * time.Second

// maxStaleResponses is the maximum number of stale (mis-matched) responses
// the stale-response discard loop will consume before giving up.
// ref: commander_core.md §5 (stale-response handling)
const maxStaleResponses = 3

// HID frame header constants.
// ref: commander_core.md §2.2 (frame layout)
const (
	responseID = 0x00 // Byte 0: HID report ID / response-match cookie.
	writeCmd   = 0x08 // Byte 1: constant envelope byte for all outgoing frames.
)

// Opcodes — each carries a ref comment citing the protocol-doc section.
// Source: specs/spec-02-framing-review.md, cross-referenced against
// liquidctl/docs/developer/protocol/commander_core.md.
const (
	cmdWake          = 0x01 // ref: commander_core.md §3.1 (wake sequence: 01 03 00 02)
	cmdSleep         = 0x01 // ref: commander_core.md §3.1 (sleep sequence: 01 03 00 01)
	cmdGetFirmware   = 0x02 // ref: commander_core.md §3.2 (firmware version query: 02 13)
	cmdCloseEndpoint = 0x05 // ref: commander_core.md §4.1 (close endpoint)
	cmdWrite         = 0x06 // ref: commander_core.md §4.3 (write channel duty)
	cmdWriteMore     = 0x07 // ref: commander_core.md §4.4 (write additional data)
	cmdRead          = 0x08 // ref: commander_core.md §4.2 (read endpoint data)
	cmdOpenEndpoint  = 0x0d // ref: commander_core.md §4.1 (open endpoint with mode)
)

// Wake/Sleep payload bytes (appended after cmdWake/cmdSleep opcode).
// ref: commander_core.md §3.1
const (
	wakeSubA = 0x03
	wakeSubB = 0x00
	wakeOn   = 0x02
	wakeOff  = 0x01
)

// Endpoint modes for cmdOpenEndpoint.
// ref: commander_core.md §4.1 (mode table)
const (
	_modeGetTemps       = 0x10 // read coolant temperatures
	_modeGetSpeeds      = 0x17 // read fan/pump RPM speeds
	_modeLEDCount       = 0x20 // read LED count (not used in v0.4.0)
	_modeHWFixedPercent = 0x1a // set fixed duty-cycle per channel
	_modeHWCurve        = 0x1f // return channel to firmware curve mode
)

// fwQuerySub is the sub-byte for the firmware version query command.
// ref: commander_core.md §3.2
const fwQuerySub = 0x13

// cmdWakeFrame is the full 4-byte wake command payload.
// ref: commander_core.md §3.1
var cmdWakeFrame = []byte{cmdWake, wakeSubA, wakeSubB, wakeOn}

// cmdSleepFrame is the full 4-byte sleep command payload.
// ref: commander_core.md §3.1
var cmdSleepFrame = []byte{cmdSleep, wakeSubA, wakeSubB, wakeOff}

// cmdFirmwareFrame is the firmware version query payload.
// ref: commander_core.md §3.2
var cmdFirmwareFrame = []byte{cmdGetFirmware, fwQuerySub}

// hidIO is the minimal HID I/O surface required by the corsair protocol layer.
// In production, *hidraw.Device satisfies this interface.
// In tests, fakehid.CorsairPipe satisfies this interface.
type hidIO interface {
	Write([]byte) (int, error)
	Read([]byte) (int, error)
	SetReadDeadline(time.Time) error
	Close() error
}

// firmwareVersion represents a Commander Core firmware version.
type firmwareVersion struct {
	major, minor, patch uint8
}

// buildFrame constructs a 1024-byte HID output frame.
// outFrame layout: [responseID=0x00][writeCmd=0x08][cmd...][padding zeros]
// ref: commander_core.md §2.2
func buildFrame(cmd []byte) []byte {
	frame := make([]byte, frameSize)
	frame[0] = responseID
	frame[1] = writeCmd
	copy(frame[2:], cmd)
	return frame
}

// sendCommand sends cmd to hid and returns the 1024-byte response.
// Stale responses (response[0] != responseID) are discarded up to
// maxStaleResponses times.
//
// Callers MUST hold the per-device mutex before calling sendCommand.
// RULE-LIQUID-05: only one HID command transfer in flight per device.
func sendCommand(hid hidIO, cmd []byte) ([]byte, error) {
	frame := buildFrame(cmd)

	if _, err := hid.Write(frame); err != nil {
		return nil, fmt.Errorf("corsair: write command: %w", err)
	}

	resp := make([]byte, frameSize)
	for attempt := 0; attempt < maxStaleResponses; attempt++ {
		if err := hid.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
			return nil, fmt.Errorf("corsair: set deadline: %w", err)
		}
		n, err := hid.Read(resp)
		if err != nil {
			return nil, fmt.Errorf("corsair: read response: %w", err)
		}
		if n == 0 {
			continue
		}
		if resp[0] == responseID {
			_ = hid.SetReadDeadline(time.Time{})
			return resp[:n], nil
		}
		// Stale response — discard and retry.
	}
	return nil, fmt.Errorf("corsair: stale-response loop exceeded cap (%d)", maxStaleResponses)
}

// encodeDutyLE encodes a duty cycle (0-255) as a little-endian uint16.
// Commander Core firmware expects duty as a 16-bit little-endian value.
// ref: commander_core.md §4.3 (duty encoding, little-endian uint16)
func encodeDutyLE(duty uint8) [2]byte {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], uint16(duty))
	return b
}

// doWake sends the wake sequence. Caller must hold the per-device mutex.
func doWake(hid hidIO) error {
	_, err := sendCommand(hid, cmdWakeFrame)
	return err
}

// doGetFirmware queries the firmware version. Caller must hold the per-device mutex.
// Returns (major, minor, patch, error).
func doGetFirmware(hid hidIO) (uint8, uint8, uint8, error) {
	resp, err := sendCommand(hid, cmdFirmwareFrame)
	if err != nil {
		return 0, 0, 0, err
	}
	// Response: [responseID][...][major][minor][patch]
	// ref: commander_core.md §3.2 (firmware response layout)
	if len(resp) < 5 {
		return 0, 0, 0, fmt.Errorf("corsair: firmware response too short (%d bytes)", len(resp))
	}
	return resp[2], resp[3], resp[4], nil
}

// doOpenEndpoint sends cmd_open_ep with the given mode byte.
// Caller must hold the per-device mutex.
// ref: commander_core.md §4.1
func doOpenEndpoint(hid hidIO, mode byte) error {
	_, err := sendCommand(hid, []byte{cmdOpenEndpoint, mode})
	return err
}

// doCloseEndpoint sends cmd_close_ep. Caller must hold the per-device mutex.
// ref: commander_core.md §4.1
func doCloseEndpoint(hid hidIO) error {
	_, err := sendCommand(hid, []byte{cmdCloseEndpoint})
	return err
}

// doReadSpeeds reads fan RPM speeds. Caller must hold the per-device mutex.
// ref: commander_core.md §4.2 (speed read layout)
func doReadSpeeds(hid hidIO) ([]uint16, error) {
	if err := doOpenEndpoint(hid, _modeGetSpeeds); err != nil {
		return nil, fmt.Errorf("corsair: open speeds endpoint: %w", err)
	}
	defer func() { _ = doCloseEndpoint(hid) }()

	resp, err := sendCommand(hid, []byte{cmdRead})
	if err != nil {
		return nil, fmt.Errorf("corsair: read speeds: %w", err)
	}
	// Response: [responseID][count][speed0_lo][speed0_hi]...
	// ref: commander_core.md §4.2
	if len(resp) < 3 {
		return nil, fmt.Errorf("corsair: speed response too short (%d bytes)", len(resp))
	}
	count := int(resp[1])
	if count < 0 || 2+count*2 > len(resp) {
		return nil, fmt.Errorf("corsair: speed count %d out of range", count)
	}
	speeds := make([]uint16, count)
	for i := range speeds {
		speeds[i] = binary.LittleEndian.Uint16(resp[2+i*2:])
	}
	return speeds, nil
}

// doReadTemps reads coolant temperatures. Caller must hold the per-device mutex.
// ref: commander_core.md §4.2 (temperature read layout)
func doReadTemps(hid hidIO) ([]uint16, error) {
	if err := doOpenEndpoint(hid, _modeGetTemps); err != nil {
		return nil, fmt.Errorf("corsair: open temps endpoint: %w", err)
	}
	defer func() { _ = doCloseEndpoint(hid) }()

	resp, err := sendCommand(hid, []byte{cmdRead})
	if err != nil {
		return nil, fmt.Errorf("corsair: read temps: %w", err)
	}
	// Response: [responseID][count][temp0_lo][temp0_hi]...
	// ref: commander_core.md §4.2
	if len(resp) < 3 {
		return nil, fmt.Errorf("corsair: temp response too short (%d bytes)", len(resp))
	}
	count := int(resp[1])
	if count < 0 || 2+count*2 > len(resp) {
		return nil, fmt.Errorf("corsair: temp count %d out of range", count)
	}
	temps := make([]uint16, count)
	for i := range temps {
		temps[i] = binary.LittleEndian.Uint16(resp[2+i*2:])
	}
	return temps, nil
}

// doWriteDuty sends a fixed-percent duty cycle to a channel.
// duty is 0-255 (the HAL convention); encoded as LE uint16 per protocol.
// Caller must hold the per-device mutex.
// ref: commander_core.md §4.3
func doWriteDuty(hid hidIO, channel int, duty uint8) error {
	if err := doOpenEndpoint(hid, _modeHWFixedPercent); err != nil {
		return fmt.Errorf("corsair: open fixed-percent endpoint: %w", err)
	}
	defer func() { _ = doCloseEndpoint(hid) }()

	enc := encodeDutyLE(duty)
	cmd := []byte{cmdWrite, byte(channel), enc[0], enc[1]}
	if _, err := sendCommand(hid, cmd); err != nil {
		return fmt.Errorf("corsair: write duty ch%d: %w", channel, err)
	}
	return nil
}

// doRestoreChannel returns a channel to firmware curve mode.
// Caller must hold the per-device mutex.
// ref: commander_core.md §4.3 (hw-curve mode restore)
func doRestoreChannel(hid hidIO, channel int) error {
	if err := doOpenEndpoint(hid, _modeHWCurve); err != nil {
		return fmt.Errorf("corsair: open hw-curve endpoint: %w", err)
	}
	defer func() { _ = doCloseEndpoint(hid) }()

	cmd := []byte{cmdWrite, byte(channel)}
	if _, err := sendCommand(hid, cmd); err != nil {
		return fmt.Errorf("corsair: restore ch%d to hw-curve: %w", channel, err)
	}
	return nil
}
