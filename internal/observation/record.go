package observation

import (
	"fmt"
	"hash/fnv"

	msgpack "github.com/vmihailenco/msgpack/v5"
)

// Record is one controller tick on one channel, persisted as a msgpack payload
// framed by spec-16's length+CRC32 envelope.
//
// Field set and order are locked by schema doc §2.1 (RULE-OBS-SCHEMA-01).
// No map[string]interface{}, no user-controlled strings except signature_label
// which is an opaque SipHash-2-4 hex output (RULE-OBS-PRIVACY-02).
type Record struct {
	Ts                int64            `msgpack:"ts"`
	ChannelID         uint16           `msgpack:"channel_id"`
	PWMWritten        uint8            `msgpack:"pwm_written"`
	PWMEnable         uint8            `msgpack:"pwm_enable"`
	ControllerState   uint8            `msgpack:"controller_state"`
	RPM               int32            `msgpack:"rpm"`
	TachTier          uint8            `msgpack:"tach_tier"`
	SensorReadings    map[uint16]int16 `msgpack:"sensor_readings"`
	Polarity          uint8            `msgpack:"polarity"`
	SignatureLabel    string           `msgpack:"signature_label"`
	SignaturePromoted bool             `msgpack:"signature_promoted"`
	R12Residual       float32          `msgpack:"r12_residual"`
	EventFlags        uint32           `msgpack:"event_flags"`
}

// Header is the first record of each log file (RULE-OBS-SCHEMA-02).
// Consumers cache it per file to resolve channel_class_map without
// re-reading spec-16 KV on every record decode.
//
// Field set is locked by schema doc §2.4.
type Header struct {
	SchemaVersion   uint16           `msgpack:"schema_version"`
	DMIFingerprint  string           `msgpack:"dmi_fingerprint"`
	VentdVersion    string           `msgpack:"ventd_version"`
	RotationTs      int64            `msgpack:"rotation_ts"`
	ChannelClassMap map[uint16]uint8 `msgpack:"channel_class_map"`
}

// Frame bytes distinguish Header payloads from Record payloads in the log.
const (
	frameRecord byte = 0x01
	frameHeader byte = 0x02
)

// MarshalRecord encodes r as a framed msgpack payload ready for LogDB.Append.
func MarshalRecord(r *Record) ([]byte, error) {
	b, err := msgpack.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("observation: marshal record: %w", err)
	}
	out := make([]byte, 1+len(b))
	out[0] = frameRecord
	copy(out[1:], b)
	return out, nil
}

// MarshalHeader encodes h as a framed msgpack payload ready for LogDB.Append.
func MarshalHeader(h *Header) ([]byte, error) {
	b, err := msgpack.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("observation: marshal header: %w", err)
	}
	out := make([]byte, 1+len(b))
	out[0] = frameHeader
	copy(out[1:], b)
	return out, nil
}

// UnmarshalPayload decodes a raw payload from the log store.
// Returns (header, nil, nil) for header frames, (nil, record, nil) for
// record frames, and an error for unknown frame bytes or decode failures.
// Callers must check which pointer is non-nil.
func UnmarshalPayload(payload []byte) (*Header, *Record, error) {
	if len(payload) == 0 {
		return nil, nil, fmt.Errorf("observation: empty payload")
	}
	switch payload[0] {
	case frameHeader:
		var h Header
		if err := msgpack.Unmarshal(payload[1:], &h); err != nil {
			return nil, nil, fmt.Errorf("observation: decode header: %w", err)
		}
		return &h, nil, nil
	case frameRecord:
		var r Record
		if err := msgpack.Unmarshal(payload[1:], &r); err != nil {
			return nil, nil, fmt.Errorf("observation: decode record: %w", err)
		}
		return nil, &r, nil
	default:
		return nil, nil, fmt.Errorf("observation: unknown frame byte 0x%02x", payload[0])
	}
}

// ChannelID returns the stable uint16 identifier for a given PWM sysfs path.
// The identifier is derived deterministically via FNV-1a so that the same
// path always maps to the same ID across daemon restarts. Callers fill
// Record.ChannelID with this value.
func ChannelID(pwmPath string) uint16 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(pwmPath))
	return uint16(h.Sum32())
}

// SensorID returns the stable uint16 identifier for a sensor's
// configured name. Used as the key type in Record.SensorReadings
// (map[uint16]int16) so downstream consumers — Layer-A conf_A
// coverage, Layer-C marginal-benefit ΔT — can re-derive per-sensor
// values across daemon restarts. Same FNV-1a derivation as ChannelID;
// uniqueness expectations (worst-case ~10 sensors per host) match.
func SensorID(name string) uint16 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return uint16(h.Sum32())
}
