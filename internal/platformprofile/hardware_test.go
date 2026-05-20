package platformprofile

import "testing"

// TestParseDMIProcessorVersion exercises a real SMBIOS 3.0 type-4
// Processor structure captured from the Latitude 7280 — the daemon-side
// motivating case for moving CPU-model detection off /proc/cpuinfo.
func TestParseDMIProcessorVersion(t *testing.T) {
	// Bytes from `od -c /sys/firmware/dmi/entries/4-0/raw` on the 7280.
	// Offsets:
	//   0:  type=0x04
	//   1:  length=0x30 (48)
	//   2-3: handle 0x4C 0x00
	//   4:  socket designation string index = 1 (→ "U3E1")
	//   5:  processor type = 3
	//   6:  processor family = 0xC6
	//   7:  manufacturer string index = 2 (→ "Intel(R) Corporation")
	//   8-15: processor ID
	//   16: processor version string index = 3 (→ model name)
	//   48+: null-terminated strings table
	raw := []byte{
		0x04, 0x30, 0x4C, 0x00, 0x01, 0x03, 0xC6, 0x02,
		0xE3, 0x06, 0x04, 0x00, 0xFF, 0xFB, 0xEB, 0xBF,
		0x03, 0x88, 0x64, 0x00, 0x48, 0x0D, 0xFC, 0x08,
		0x41, 0x01, 0x49, 0x00, 0x4A, 0x00, 0x4B, 0x00,
		0x04, 0x05, 0x06, 0x02, 0x02, 0x04, 0xFC, 0x00,
		0xC6, 0x00, 0x02, 0x00, 0x02, 0x00, 0x04, 0x00,
		// Strings table:
	}
	strs := []byte{}
	strs = append(strs, []byte("U3E1\x00")...)
	strs = append(strs, []byte("Intel(R) Corporation\x00")...)
	strs = append(strs, []byte("Intel(R) Core(TM) i7-6600U CPU @ 2.60GHz\x00")...)
	strs = append(strs, []byte("To Be Filled By O.E.M.\x00")...)
	strs = append(strs, 0x00) // double-null terminator
	raw = append(raw, strs...)

	got := parseDMIProcessorVersion(raw)
	want := "Intel(R) Core(TM) i7-6600U CPU @ 2.60GHz"
	if got != want {
		t.Errorf("parseDMIProcessorVersion:\n got %q\nwant %q", got, want)
	}
}

func TestParseDMIProcessorVersion_NotType4(t *testing.T) {
	raw := []byte{0x01, 0x30, 0x00, 0x00, 0x01}
	if got := parseDMIProcessorVersion(raw); got != "" {
		t.Errorf("non-type-4: want empty, got %q", got)
	}
}

func TestParseDMIProcessorVersion_TooShort(t *testing.T) {
	raw := []byte{0x04, 0x30, 0x4C, 0x00}
	if got := parseDMIProcessorVersion(raw); got != "" {
		t.Errorf("truncated: want empty, got %q", got)
	}
}

func TestParseDMIProcessorVersion_ZeroVersionIdx(t *testing.T) {
	// Length=17, formatted bytes up to offset 16 = 0x00 (no version
	// string assigned).
	raw := make([]byte, 17)
	raw[0] = 0x04
	raw[1] = 0x11 // length = 17
	if got := parseDMIProcessorVersion(raw); got != "" {
		t.Errorf("zero version idx: want empty, got %q", got)
	}
}

func TestParseDMIProcessorVersion_VersionIdxOutOfRange(t *testing.T) {
	raw := []byte{
		0x04, 0x11, 0x00, 0x00, 0x01, 0x03, 0xC6, 0x02,
		0xE3, 0x06, 0x04, 0x00, 0xFF, 0xFB, 0xEB, 0xBF,
		0x09, // version index 9, but only 1 string follows
	}
	raw = append(raw, []byte("just one\x00\x00")...)
	if got := parseDMIProcessorVersion(raw); got != "" {
		t.Errorf("out-of-range version idx: want empty, got %q", got)
	}
}
