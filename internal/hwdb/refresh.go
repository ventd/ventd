package hwdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"gopkg.in/yaml.v3"
)

// upstreamURL is the canonical location of the community hardware profile DB.
const upstreamURL = "https://raw.githubusercontent.com/ventd/hardware-profiles/main/profiles.yaml"

var (
	remoteMu sync.RWMutex
	remoteDB []Profile // nil until a successful RefreshFromRemote call
)

// RefreshFromRemote fetches profiles.yaml from upstreamURL, verifies that its
// SHA-256 matches pinnedSHA256 (hex-encoded, caller-supplied), then merges the
// parsed profiles into the in-memory DB. Local (embedded) entries win on
// conflict: they always appear first in the list that Match traverses, so a
// local exact/prefix/wildcard match is found before any remote entry for the
// same fingerprint.
//
// Returns the number of remote profiles parsed. On SHA mismatch the DB is not
// mutated. Context cancellation propagates through the HTTP round-trip.
func RefreshFromRemote(ctx context.Context, client *http.Client, pinnedSHA256 string) (int, error) {
	return refreshFromURL(ctx, client, upstreamURL, pinnedSHA256)
}

// refreshFromURL is the testable core of RefreshFromRemote. Tests inject an
// httptest.Server URL instead of upstreamURL.
func refreshFromURL(ctx context.Context, client *http.Client, url, pinnedSHA256 string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("hwdb refresh: build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("hwdb refresh: fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("hwdb refresh: read body: %w", err)
	}

	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if got != pinnedSHA256 {
		return 0, fmt.Errorf("hwdb refresh: SHA-256 mismatch: got %s want %s", got, pinnedSHA256)
	}

	var profiles []Profile
	dec := yaml.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&profiles); err != nil {
		return 0, fmt.Errorf("hwdb refresh: parse yaml: %w", err)
	}

	remoteMu.Lock()
	remoteDB = profiles
	remoteMu.Unlock()

	slog.Info("hwdb-refresh", "kind", "hwdb-refresh", "count", len(profiles), "sha", got)
	return len(profiles), nil
}

// mergedProfiles returns the embedded profiles followed by any remotely
// refreshed profiles. Embedded entries come first, preserving the invariant
// that local profiles win within each Match resolution stage.
func mergedProfiles() ([]Profile, error) {
	embedded, err := Load()
	if err != nil {
		return nil, err
	}
	remoteMu.RLock()
	remote := remoteDB
	remoteMu.RUnlock()
	if len(remote) == 0 {
		return embedded, nil
	}
	merged := make([]Profile, 0, len(embedded)+len(remote))
	merged = append(merged, embedded...)
	merged = append(merged, remote...)
	return merged, nil
}
