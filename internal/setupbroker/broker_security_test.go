package setupbroker

import (
	"bytes"
	"strings"
	"testing"
)

// TestDecodeRequest_RejectsOversizedRequest pins the bug-hunt fix
// (Agent 2 #7): a request body larger than MaxRequestBytes must
// reject with a clean error rather than OOMing the broker. Shape
// the synthetic body as a JSON-valid envelope with a giant audit
// field so the JSON parser actually has to allocate during Decode.
func TestDecodeRequest_RejectsOversizedRequest(t *testing.T) {
	// 80 KiB of "A" inside the audit.requested_by field — JSON-valid,
	// well over the 64 KiB cap. The unbounded path would let the
	// decoder allocate the full string into the Request struct.
	pad := strings.Repeat("A", 80*1024)
	body := `{"schema_version":1,"operation":"load_module","params":{},"audit":{"requested_by":"` + pad + `"}}`

	if len(body) <= MaxRequestBytes {
		t.Fatalf("test setup: synthetic body length %d ≤ MaxRequestBytes %d; bump the pad", len(body), MaxRequestBytes)
	}
	_, err := DecodeRequest(strings.NewReader(body))
	if err == nil {
		t.Fatal("DecodeRequest accepted oversized request; expected error")
	}
	if !strings.Contains(err.Error(), "oversized") && !strings.Contains(err.Error(), "decode request") {
		t.Errorf("err = %v, want 'oversized' or 'decode request' marker", err)
	}
}

// TestDecodeRequest_AcceptsRequestAtCap verifies a body exactly at
// the cap parses cleanly. Catches an off-by-one where the +1
// sentinel byte erroneously rejects valid input.
func TestDecodeRequest_AcceptsRequestAtCap(t *testing.T) {
	// Build a body whose length is JUST under MaxRequestBytes.
	// A JSON envelope with an audit pad sized to fill the
	// remaining budget.
	const envelopeOverhead = 100 // rough; doesn't need to be exact
	padLen := MaxRequestBytes - envelopeOverhead
	if padLen < 1 {
		t.Skip("MaxRequestBytes too small for this test")
	}
	pad := strings.Repeat("A", padLen)
	body := `{"schema_version":1,"operation":"load_module","params":{},"audit":{"requested_by":"` + pad + `"}}`

	// Trim the body if it exceeds the cap due to JSON overhead
	// estimation. We just need a valid envelope ≤ cap.
	if len(body) > MaxRequestBytes {
		// Re-pad smaller.
		pad = strings.Repeat("A", padLen-(len(body)-MaxRequestBytes))
		body = `{"schema_version":1,"operation":"load_module","params":{},"audit":{"requested_by":"` + pad + `"}}`
	}
	if len(body) > MaxRequestBytes {
		t.Fatalf("test setup: body length %d > cap %d after trim", len(body), MaxRequestBytes)
	}

	req, err := DecodeRequest(strings.NewReader(body))
	if err != nil {
		t.Fatalf("DecodeRequest rejected body of length %d (cap %d): %v",
			len(body), MaxRequestBytes, err)
	}
	if req.Operation != OpLoadModule {
		t.Errorf("Operation = %q, want %q", req.Operation, OpLoadModule)
	}
}

// TestDecodeRequest_OversizedNonJSONStillRejects — even raw garbage
// must not OOM the broker. A 1 GiB stream of zeroes would be the
// pathological worst case; the cap at 64 KiB+1 limits the read.
func TestDecodeRequest_OversizedNonJSONStillRejects(t *testing.T) {
	// 200 KiB of zero bytes — definitely exceeds cap, definitely
	// not JSON. We're not asserting any specific error message,
	// just that the read terminates and an error returns.
	junk := bytes.Repeat([]byte{0x00}, 200*1024)
	_, err := DecodeRequest(bytes.NewReader(junk))
	if err == nil {
		t.Fatal("DecodeRequest accepted 200 KiB of zero bytes; expected error")
	}
}
