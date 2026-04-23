// Package proto defines the length-prefixed JSON wire protocol used between
// the ventd main daemon and the ventd-ipmi privilege-separated sidecar.
//
// Framing: 4-byte big-endian length header followed by a JSON body.
// One request/response per frame. No streaming, no multiplexing.
// Frame size is capped at MaxFrameBytes (64 KiB) to bound allocations.
package proto

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// MaxFrameBytes is the largest allowed frame body. A frame header claiming
// more than this many bytes is rejected immediately.
const MaxFrameBytes = 64 * 1024

// Op names the operation a Request carries.
type Op string

const (
	OpEnumerate     Op = "ENUMERATE"
	OpReadSensors   Op = "READ_SENSORS"
	OpSetManualMode Op = "SET_MANUAL_MODE"
	OpWriteDuty     Op = "WRITE_DUTY"
	OpRestore       Op = "RESTORE"
)

// Request is sent by the main daemon to the sidecar.
type Request struct {
	ReqID      int64           `json:"req_id"`
	Op         Op              `json:"op"`
	Channel    json.RawMessage `json:"channel,omitempty"`
	Duty       uint8           `json:"duty,omitempty"`
	VendorHint string          `json:"vendor_hint,omitempty"`
}

// Response is sent by the sidecar back to the main daemon.
type Response struct {
	ReqID int64           `json:"req_id"`
	OK    bool            `json:"ok"`
	Err   string          `json:"err,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// ChannelWire is a serializable hal.Channel for use in proto messages.
// Opaque carries backend-specific JSON (e.g. the IPMI State struct).
type ChannelWire struct {
	ID     string          `json:"id"`
	Role   string          `json:"role"`
	Caps   uint32          `json:"caps"`
	Opaque json.RawMessage `json:"opaque,omitempty"`
}

// ReadingWire is a serializable hal.Reading for use in proto responses.
type ReadingWire struct {
	PWM  uint8   `json:"pwm"`
	RPM  uint16  `json:"rpm"`
	Temp float64 `json:"temp,omitempty"`
	OK   bool    `json:"ok"`
}

// Codec reads and writes framed messages over a pair of streams.
// r and w may be the same object (e.g. a net.Conn).
type Codec struct {
	r io.Reader
	w io.Writer
}

// NewCodec constructs a Codec that reads from r and writes to w.
func NewCodec(r io.Reader, w io.Writer) *Codec {
	return &Codec{r: r, w: w}
}

// WriteRequest encodes req as a length-prefixed JSON frame.
func (c *Codec) WriteRequest(req *Request) error {
	return writeFrame(c.w, req)
}

// ReadRequest decodes a length-prefixed JSON frame into a Request.
func (c *Codec) ReadRequest() (*Request, error) {
	var req Request
	if err := readFrame(c.r, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// WriteResponse encodes resp as a length-prefixed JSON frame.
func (c *Codec) WriteResponse(resp *Response) error {
	return writeFrame(c.w, resp)
}

// ReadResponse decodes a length-prefixed JSON frame into a Response.
func (c *Codec) ReadResponse() (*Response, error) {
	var resp Response
	if err := readFrame(c.r, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func writeFrame(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("proto: marshal: %w", err)
	}
	if len(data) > MaxFrameBytes {
		return fmt.Errorf("proto: frame too large: %d > %d", len(data), MaxFrameBytes)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("proto: write header: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("proto: write body: %w", err)
	}
	return nil
}

func readFrame(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return fmt.Errorf("proto: read header: %w", err)
	}
	size := binary.BigEndian.Uint32(hdr[:])
	if size > MaxFrameBytes {
		return fmt.Errorf("proto: frame too large: %d > %d", size, MaxFrameBytes)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("proto: read body: %w", err)
	}
	if err := json.Unmarshal(buf, v); err != nil {
		return fmt.Errorf("proto: unmarshal: %w", err)
	}
	return nil
}
