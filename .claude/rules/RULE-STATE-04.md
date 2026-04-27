# RULE-STATE-04: Log store iteration MUST tolerate torn records (length-prefix-overrun) and CRC-mismatched records (skip and continue).

`readRecords` processes a stream of length-prefix + payload + CRC32 records:

- If the length prefix indicates a payload larger than `logMaxRecordSize` (64 MiB),
  the record is treated as torn and iteration stops for that file (returning nil).
- If `io.ReadFull` for the payload or CRC bytes returns `io.ErrUnexpectedEOF`, the
  record is torn (truncated at a crash boundary); iteration stops for that file.
- If the CRC32-IEEE of `length||payload` does not match the stored CRC, the record
  is corrupt; it is **skipped** and iteration continues with the next record (the
  stream position is still valid because all bytes were consumed).

This ensures that a crash mid-append loses at most one record and does not prevent
access to the records that follow it.

Bound: internal/state/state_test.go:TestRULE_STATE_04_LogTornRecordSkip
