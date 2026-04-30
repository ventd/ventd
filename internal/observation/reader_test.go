package observation

import (
	"testing"
)

// TestReader_Stream_TraversesActiveAndRotated_InOrder verifies that Stream
// returns records from all files in append order (RULE-OBS-READ-01).
func TestReader_Stream_TraversesActiveAndRotated_InOrder(t *testing.T) {
	// Build 3 mock files with 3 records each (Ts 1-9).
	makeFile := func(start, count int) [][]byte {
		var payloads [][]byte
		for i := range count {
			p, err := MarshalRecord(&Record{Ts: int64(start + i)})
			if err != nil {
				t.Fatalf("MarshalRecord: %v", err)
			}
			payloads = append(payloads, p)
		}
		return payloads
	}

	ml := &mockLogStore{
		files: [][][]byte{
			makeFile(1, 3),
			makeFile(4, 3),
			makeFile(7, 3),
		},
	}

	rd := NewReader(ml)
	var got []int64
	err := rd.Stream(noSince, func(r *Record) bool {
		got = append(got, r.Ts)
		return true
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(got) != 9 {
		t.Fatalf("record count: got %d, want 9", len(got))
	}
	for i, ts := range got {
		if ts != int64(i+1) {
			t.Errorf("record[%d].Ts: got %d, want %d", i, ts, int64(i+1))
		}
	}
}

// TestReader_Latest_BoundedRing_NotFullLoad verifies that Latest returns at
// most n records without loading the full history into memory (RULE-OBS-READ-02).
func TestReader_Latest_BoundedRing_NotFullLoad(t *testing.T) {
	var payloads [][]byte
	for i := range 10 {
		p, err := MarshalRecord(&Record{Ts: int64(i + 1)})
		if err != nil {
			t.Fatalf("MarshalRecord: %v", err)
		}
		payloads = append(payloads, p)
	}
	ml := &mockLogStore{files: [][][]byte{payloads}}

	rd := NewReader(ml)
	recs, err := rd.Latest(noSince, nil, 3)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("record count: got %d, want 3", len(recs))
	}
	wantTs := []int64{8, 9, 10}
	for i, r := range recs {
		if r.Ts != wantTs[i] {
			t.Errorf("recs[%d].Ts: got %d, want %d", i, r.Ts, wantTs[i])
		}
	}
}

// TestReader_TornRecord_SkippedSilently_IterationContinues verifies that a
// corrupt payload (valid frame byte, invalid msgpack body) is silently skipped
// and iteration continues to the next valid record (RULE-OBS-CRASH-01).
func TestReader_TornRecord_SkippedSilently_IterationContinues(t *testing.T) {
	// A payload with a valid record frame byte but truncated/invalid msgpack body.
	corrupt := []byte{frameRecord, 0xFF, 0xFF, 0xFF}

	valid, err := MarshalRecord(&Record{Ts: 42})
	if err != nil {
		t.Fatalf("MarshalRecord: %v", err)
	}

	ml := &mockLogStore{
		files: [][][]byte{{corrupt, valid}},
	}

	rd := NewReader(ml)
	var got []*Record
	err = rd.Stream(noSince, func(r *Record) bool {
		got = append(got, r)
		return true
	})
	if err != nil {
		t.Fatalf("Stream: unexpected error %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("record count: got %d, want 1 (corrupt skipped, valid delivered)", len(got))
	}
	if got[0].Ts != 42 {
		t.Errorf("record Ts: got %d, want 42", got[0].Ts)
	}
}
