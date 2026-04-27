# RULE-STATE-02: Blob store reads MUST verify magic, length, and SHA256. Mismatch MUST result in found=false returned to consumer; consumer reinitialises.

`BlobDB.Read(name)` verifies: (1) the 4-byte magic header equals `"VBLB"`;
(2) the declared length (`uint64` at offset 8) matches the number of payload
bytes available; (3) the trailing SHA256 over the payload equals the computed
`sha256.Sum256(payload)`. On any mismatch — magic wrong, length overrun, or
checksum failure — `Read` returns `(nil, 0, false, nil)`. The consumer interprets
`found=false` as "state absent, re-initialise." This prevents silently propagating
a partially-written or disk-damaged blob into the thermal model or RLS state.

Bound: internal/state/state_test.go:TestRULE_STATE_02_BlobSHA256Verification
