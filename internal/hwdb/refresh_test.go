package hwdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func resetRemote(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		remoteMu.Lock()
		remoteDB = nil
		remoteMu.Unlock()
	})
}

func TestRefreshFromURL_SHAMatch(t *testing.T) {
	resetRemote(t)

	payload := []byte("- board_vendor: TestVendor\n  board_name: TestBoard\n  modules: [testmod]\n")
	pinned := hexSHA256(payload)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	count, err := refreshFromURL(context.Background(), srv.Client(), srv.URL, pinned)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	remoteMu.RLock()
	n := len(remoteDB)
	remoteMu.RUnlock()
	if n != 1 {
		t.Errorf("remoteDB len = %d, want 1", n)
	}
}

func TestRefreshFromURL_SHAMismatch(t *testing.T) {
	resetRemote(t)

	payload := []byte("- board_vendor: V\n  modules: [m]\n")
	wrongPinned := hexSHA256([]byte("not the right content"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	_, err := refreshFromURL(context.Background(), srv.Client(), srv.URL, wrongPinned)
	if err == nil {
		t.Fatal("expected error on SHA mismatch, got nil")
	}

	remoteMu.RLock()
	n := len(remoteDB)
	remoteMu.RUnlock()
	if n != 0 {
		t.Errorf("remoteDB should be unchanged after SHA mismatch, got len=%d", n)
	}
}

func TestRefreshFromURL_ContextCancelled(t *testing.T) {
	resetRemote(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		http.Error(w, "cancelled", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := refreshFromURL(ctx, srv.Client(), srv.URL, "anyhash")
	if err == nil {
		t.Fatal("expected error when context is cancelled, got nil")
	}
}

func TestRefreshFromURL_FlagOff(t *testing.T) {
	resetRemote(t)

	// When AllowRemote=false callers never invoke RefreshFromRemote.
	// Verify the DB is untouched and mergedProfiles returns only the embedded set.
	remoteMu.RLock()
	n := len(remoteDB)
	remoteMu.RUnlock()
	if n != 0 {
		t.Errorf("remoteDB should be empty when refresh is never called, got len=%d", n)
	}

	profiles, err := mergedProfiles()
	if err != nil {
		t.Fatalf("mergedProfiles: %v", err)
	}
	embedded, err := LoadModules()
	if err != nil {
		t.Fatalf("LoadModules: %v", err)
	}
	if len(profiles) != len(embedded) {
		t.Errorf("merged len = %d, want %d (embedded only)", len(profiles), len(embedded))
	}
}

func TestMergedProfiles_LocalWinsOnConflict(t *testing.T) {
	resetRemote(t)

	embedded, err := LoadModules()
	if err != nil || len(embedded) == 0 {
		t.Skip("embedded DB empty or unreadable")
	}
	first := embedded[0]
	if first.Match.BoardVendor == "" || first.Match.BoardName == "" {
		t.Skip("first embedded profile has no vendor+name to test conflict on")
	}

	// Inject a remote profile with the same match fields but different modules.
	remoteMu.Lock()
	remoteDB = []ModuleProfile{{
		Match:   first.Match,
		Modules: []string{"__remote_should_not_win__"},
	}}
	remoteMu.Unlock()

	fp := HardwareFingerprint{
		BoardVendor: first.Match.BoardVendor,
		BoardName:   first.Match.BoardName,
	}
	got, err := Match(fp)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	for _, m := range got.Modules {
		if m == "__remote_should_not_win__" {
			t.Error("remote profile won over local — local-wins invariant violated")
		}
	}
}
