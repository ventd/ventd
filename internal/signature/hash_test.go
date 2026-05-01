package signature

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestHasher_Determinism asserts the same input + key produces the
// same output across calls. RULE-SIG-HASH-01.
func TestHasher_Determinism(t *testing.T) {
	salt := bytes.Repeat([]byte{0x42}, 32)
	h, err := NewHasher(salt)
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	got1 := h.HashComm("chrome")
	got2 := h.HashComm("chrome")
	if got1 != got2 {
		t.Errorf("non-deterministic hash: %x vs %x", got1, got2)
	}
}

// TestHasher_OutputIs64BitHex asserts the hex form is exactly 16
// lowercase characters. RULE-SIG-HASH-01.
func TestHasher_OutputIs64BitHex(t *testing.T) {
	h := makeHasher(t)
	hex := h.HashCommHex("Game.exe")
	if len(hex) != 16 {
		t.Errorf("hex length: got %d, want 16", len(hex))
	}
	for _, c := range hex {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("hex contains non-lowercase-hex char: %q", c)
		}
	}
}

// TestHasher_DeterministicAcrossRestarts asserts that two Hasher
// instances built from the same salt produce identical outputs —
// the property that lets the persisted KV labels remain valid
// across daemon restart. RULE-SIG-HASH-03.
func TestHasher_DeterministicAcrossRestarts(t *testing.T) {
	salt := bytes.Repeat([]byte{0xAB}, 32)
	h1, err := NewHasher(salt)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := NewHasher(salt)
	if err != nil {
		t.Fatal(err)
	}
	if h1.HashComm("cc1") != h2.HashComm("cc1") {
		t.Errorf("hash differs across Hasher instances")
	}
}

// TestSalt_FilePermissionsAre0600 asserts the freshly-generated
// salt file has mode 0600. RULE-SIG-SALT-01.
func TestSalt_FilePermissionsAre0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".signature_salt")
	if _, err := LoadOrCreateSalt(path); err != nil {
		t.Fatalf("LoadOrCreateSalt: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != SaltFileMode {
		t.Errorf("mode: got %o, want %o", info.Mode().Perm(), SaltFileMode)
	}
}

// TestSalt_LengthIs32Bytes asserts the salt file holds 32 bytes
// (16 used by SipHash, 16 reserved for forward compat).
// RULE-SIG-SALT-01.
func TestSalt_LengthIs32Bytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".signature_salt")
	salt, err := LoadOrCreateSalt(path)
	if err != nil {
		t.Fatalf("LoadOrCreateSalt: %v", err)
	}
	if len(salt) != SaltLen {
		t.Errorf("salt length: got %d, want %d", len(salt), SaltLen)
	}
}

// TestSalt_RegenerationOnMissingFile asserts that LoadOrCreateSalt
// regenerates a fresh salt when the file is absent.
// RULE-SIG-SALT-03.
func TestSalt_RegenerationOnMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".signature_salt")
	first, err := LoadOrCreateSalt(path)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Delete and re-call.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	second, err := LoadOrCreateSalt(path)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Errorf("regenerated salt matches old salt; rand.Reader broken?")
	}
}

// TestSalt_RejectsLooseFilePermissions asserts that an existing
// salt file with mode > 0600 is refused (operator must chmod
// before daemon start). RULE-SIG-SALT-01.
func TestSalt_RejectsLooseFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".signature_salt")
	// Create with a too-loose mode.
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x01}, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOrCreateSalt(path)
	if err == nil {
		t.Fatal("expected error for mode 0644 salt file")
	}
}

// TestSaltKey_DifferentSaltDifferentLabels asserts the privacy
// invariant: two installs with different salts produce different
// labels for the same comm. RULE-SIG-SALT-03.
func TestSaltKey_DifferentSaltDifferentLabels(t *testing.T) {
	saltA := bytes.Repeat([]byte{0x10}, 32)
	saltB := bytes.Repeat([]byte{0x20}, 32)
	hA, _ := NewHasher(saltA)
	hB, _ := NewHasher(saltB)
	if hA.HashComm("chrome") == hB.HashComm("chrome") {
		t.Errorf("different salts produced identical hashes for the same comm")
	}
}
