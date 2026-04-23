package proto_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/hal/ipmi/proto"
)

// TestCodec_Request_Roundtrip verifies that each Op type survives a
// WriteRequest → ReadRequest cycle with all fields intact.
func TestCodec_Request_Roundtrip(t *testing.T) {
	tests := []struct {
		name string
		req  proto.Request
	}{
		{
			name: "enumerate",
			req:  proto.Request{ReqID: 1, Op: proto.OpEnumerate},
		},
		{
			name: "read_sensors",
			req: proto.Request{
				ReqID:   2,
				Op:      proto.OpReadSensors,
				Channel: json.RawMessage(`{"SensorNumber":5,"SensorName":"Fan A"}`),
			},
		},
		{
			name: "set_manual_mode",
			req: proto.Request{
				ReqID:      3,
				Op:         proto.OpSetManualMode,
				VendorHint: "supermicro",
			},
		},
		{
			name: "write_duty",
			req: proto.Request{
				ReqID:   4,
				Op:      proto.OpWriteDuty,
				Channel: json.RawMessage(`{"SensorNumber":1}`),
				Duty:    128,
			},
		},
		{
			name: "restore",
			req: proto.Request{
				ReqID:   5,
				Op:      proto.OpRestore,
				Channel: json.RawMessage(`{"SensorNumber":1}`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			c := proto.NewCodec(&buf, &buf)

			if err := c.WriteRequest(&tt.req); err != nil {
				t.Fatalf("WriteRequest: %v", err)
			}

			got, err := c.ReadRequest()
			if err != nil {
				t.Fatalf("ReadRequest: %v", err)
			}

			if got.ReqID != tt.req.ReqID {
				t.Errorf("ReqID: got %d, want %d", got.ReqID, tt.req.ReqID)
			}
			if got.Op != tt.req.Op {
				t.Errorf("Op: got %q, want %q", got.Op, tt.req.Op)
			}
			if got.Duty != tt.req.Duty {
				t.Errorf("Duty: got %d, want %d", got.Duty, tt.req.Duty)
			}
			if got.VendorHint != tt.req.VendorHint {
				t.Errorf("VendorHint: got %q, want %q", got.VendorHint, tt.req.VendorHint)
			}
			if tt.req.Channel != nil && string(got.Channel) != string(tt.req.Channel) {
				t.Errorf("Channel: got %s, want %s", got.Channel, tt.req.Channel)
			}
		})
	}
}

// TestCodec_Response_Roundtrip verifies that ok, error, and data responses
// round-trip through WriteResponse → ReadResponse.
func TestCodec_Response_Roundtrip(t *testing.T) {
	tests := []struct {
		name string
		resp proto.Response
	}{
		{
			name: "ok_with_data",
			resp: proto.Response{
				ReqID: 1,
				OK:    true,
				Data:  json.RawMessage(`[{"id":"sensor1"}]`),
			},
		},
		{
			name: "error",
			resp: proto.Response{
				ReqID: 2,
				OK:    false,
				Err:   "ipmi: unsupported vendor",
			},
		},
		{
			name: "ok_no_data",
			resp: proto.Response{ReqID: 3, OK: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			c := proto.NewCodec(&buf, &buf)

			if err := c.WriteResponse(&tt.resp); err != nil {
				t.Fatalf("WriteResponse: %v", err)
			}

			got, err := c.ReadResponse()
			if err != nil {
				t.Fatalf("ReadResponse: %v", err)
			}

			if got.ReqID != tt.resp.ReqID {
				t.Errorf("ReqID: got %d, want %d", got.ReqID, tt.resp.ReqID)
			}
			if got.OK != tt.resp.OK {
				t.Errorf("OK: got %v, want %v", got.OK, tt.resp.OK)
			}
			if got.Err != tt.resp.Err {
				t.Errorf("Err: got %q, want %q", got.Err, tt.resp.Err)
			}
			if tt.resp.Data != nil && string(got.Data) != string(tt.resp.Data) {
				t.Errorf("Data: got %s, want %s", got.Data, tt.resp.Data)
			}
		})
	}
}

// TestCodec_FrameTooLarge_ReadRequest_Errors verifies that a header claiming
// MaxFrameBytes+1 bytes is rejected without reading body bytes.
func TestCodec_FrameTooLarge_ReadRequest_Errors(t *testing.T) {
	var buf bytes.Buffer
	// Write a 4-byte length header claiming MaxFrameBytes+1 bytes.
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(proto.MaxFrameBytes+1))
	buf.Write(hdr[:])

	c := proto.NewCodec(&buf, &buf)
	_, err := c.ReadRequest()
	if err == nil {
		t.Fatal("expected error for oversized frame, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error %q does not mention 'too large'", err.Error())
	}
}

// TestCodec_FrameTooLarge_WriteRequest_Errors verifies that marshaling a
// payload that exceeds MaxFrameBytes is rejected before any bytes are written.
func TestCodec_FrameTooLarge_WriteRequest_Errors(t *testing.T) {
	var buf bytes.Buffer
	c := proto.NewCodec(&buf, &buf)

	// Build a channel payload just over 64 KB.
	bigChannel := make([]byte, proto.MaxFrameBytes+1)
	for i := range bigChannel {
		bigChannel[i] = 'x'
	}
	req := proto.Request{
		ReqID:   99,
		Op:      proto.OpWriteDuty,
		Channel: json.RawMessage(`"` + string(bigChannel) + `"`),
	}
	err := c.WriteRequest(&req)
	if err == nil {
		t.Fatal("expected error for oversized frame, got nil")
	}
}

// TestCodec_ShortHeader_Errors verifies that an incomplete length header
// returns an error.
func TestCodec_ShortHeader_Errors(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0x00, 0x00}) // only 2 of 4 header bytes

	c := proto.NewCodec(&buf, &buf)
	_, err := c.ReadRequest()
	if err == nil {
		t.Fatal("expected error for short header, got nil")
	}
}

// TestCodec_ShortBody_Errors verifies that a correct header followed by a
// truncated body returns an error.
func TestCodec_ShortBody_Errors(t *testing.T) {
	var buf bytes.Buffer
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 100) // claim 100-byte body
	buf.Write(hdr[:])
	buf.Write([]byte(`{"req_id":1}`)) // only 13 bytes — truncated

	c := proto.NewCodec(&buf, &buf)
	_, err := c.ReadRequest()
	if err == nil {
		t.Fatal("expected error for truncated body, got nil")
	}
}

// TestCodec_MultipleFrames verifies that sequential write/read pairs work
// correctly when multiple frames are queued in the same buffer.
func TestCodec_MultipleFrames(t *testing.T) {
	var buf bytes.Buffer
	c := proto.NewCodec(&buf, &buf)

	reqs := []proto.Request{
		{ReqID: 10, Op: proto.OpEnumerate},
		{ReqID: 11, Op: proto.OpWriteDuty, Duty: 200},
		{ReqID: 12, Op: proto.OpRestore},
	}

	for i := range reqs {
		if err := c.WriteRequest(&reqs[i]); err != nil {
			t.Fatalf("WriteRequest[%d]: %v", i, err)
		}
	}

	for i := range reqs {
		got, err := c.ReadRequest()
		if err != nil {
			t.Fatalf("ReadRequest[%d]: %v", i, err)
		}
		if got.ReqID != reqs[i].ReqID {
			t.Errorf("frame %d: ReqID got %d, want %d", i, got.ReqID, reqs[i].ReqID)
		}
		if got.Op != reqs[i].Op {
			t.Errorf("frame %d: Op got %q, want %q", i, got.Op, reqs[i].Op)
		}
	}
}

// TestCodec_EmptyReader_EOFError verifies that ReadRequest on an empty reader
// returns io.ErrUnexpectedEOF or io.EOF wrapped in a proto error.
func TestCodec_EmptyReader_EOFError(t *testing.T) {
	c := proto.NewCodec(strings.NewReader(""), io.Discard)
	_, err := c.ReadRequest()
	if err == nil {
		t.Fatal("expected error reading from empty reader, got nil")
	}
}
