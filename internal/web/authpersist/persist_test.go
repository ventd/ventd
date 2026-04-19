package authpersist

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_MissingFile(t *testing.T) {
	a, err := Load(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("missing file: want nil err, got %v", err)
	}
	if a != nil {
		t.Fatal("missing file: want nil Auth, got non-nil")
	}
}

func TestLoad_EmptyHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"admin":{"username":"admin","bcrypt_hash":""}}`), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("empty bcrypt_hash: want error, got nil")
	}
}

func TestLoad_BadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`not json`), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("bad JSON: want error, got nil")
	}
}

func TestSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	a := &Auth{
		Admin: AdminCreds{
			Username:   "admin",
			BcryptHash: "$2a$12$fakeHashForTestingPurposesOnly123456",
			CreatedAt:  time.Date(2026, 4, 19, 17, 11, 55, 0, time.UTC),
		},
	}
	if err := Save(path, a); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if got.Version != fileVersion {
		t.Errorf("Version = %d, want %d", got.Version, fileVersion)
	}
	if got.Admin.BcryptHash != a.Admin.BcryptHash {
		t.Errorf("BcryptHash = %q, want %q", got.Admin.BcryptHash, a.Admin.BcryptHash)
	}
	if got.Admin.Username != "admin" {
		t.Errorf("Username = %q, want admin", got.Admin.Username)
	}
}

func TestSave_AtomicNoBakOnFirstWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := Save(path, &Auth{Admin: AdminCreds{Username: "admin", BcryptHash: "$2a$12$aaa"}}); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	// No .bak should exist on first write (there was nothing to back up).
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Error(".bak should not exist after first write")
	}
	// .tmp should be cleaned up.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error(".tmp file leaked after Save")
	}
}

func TestSave_BackupCreatedOnOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	first := &Auth{Admin: AdminCreds{Username: "admin", BcryptHash: "$2a$12$first"}}
	if err := Save(path, first); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	second := &Auth{Admin: AdminCreds{Username: "admin", BcryptHash: "$2a$12$second"}}
	if err := Save(path, second); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	// auth.json.bak must exist and hold the first hash.
	bak, err := Load(path + ".bak")
	if err != nil {
		t.Fatalf("Load .bak: %v", err)
	}
	if bak.Admin.BcryptHash != "$2a$12$first" {
		t.Errorf(".bak hash = %q, want $2a$12$first", bak.Admin.BcryptHash)
	}
}

func TestDefaultPath(t *testing.T) {
	got := DefaultPath("/etc/ventd")
	want := "/etc/ventd/auth.json"
	if got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
}
