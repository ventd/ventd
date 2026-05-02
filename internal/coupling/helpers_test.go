package coupling

import (
	"os"
	"testing"

	msgpack "github.com/vmihailenco/msgpack/v5"
)

// patchOnDiskBucket reads, mutates, re-writes a Bucket file.
// Used by tests that need to inject corrupt or future-version
// state without depending on the specific msgpack encoding.
func patchOnDiskBucket(t *testing.T, path string, mutate func(*Bucket)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var b Bucket
	if err := msgpack.Unmarshal(data, &b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mutate(&b)
	out, err := msgpack.Marshal(&b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}
