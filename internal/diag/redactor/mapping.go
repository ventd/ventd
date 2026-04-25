// Package redactor implements the ventd diagnostic bundle privacy redactor.
// It strips identifying information from bundle content per the threat model
// in docs/research/2026-04-diag-privacy-threat-model.md.
package redactor

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

const mappingSchemaVersion = 1

// mappingFile is the on-disk format of the persistent mapping store.
type mappingFile struct {
	SchemaVersion int               `json:"schema_version"`
	Hostnames     map[string]string `json:"hostnames,omitempty"`
	MACs          map[string]string `json:"macs,omitempty"`
	IPs           map[string]string `json:"ips,omitempty"`
	Usernames     map[string]string `json:"usernames,omitempty"`
	Keywords      map[string]string `json:"keywords,omitempty"`
}

// MappingStore holds consistent-mapping tables for the primitives that
// need cross-file token consistency (P1, P3, P4, P5, P10).
// All methods are safe for concurrent use.
type MappingStore struct {
	mu        sync.Mutex
	path      string
	hostnames map[string]string
	macs      map[string]string
	ips       map[string]string
	usernames map[string]string
	keywords  map[string]string
}

// NewMappingStore creates an in-memory store without persistence.
func NewMappingStore() *MappingStore {
	return &MappingStore{
		hostnames: make(map[string]string),
		macs:      make(map[string]string),
		ips:       make(map[string]string),
		usernames: make(map[string]string),
		keywords:  make(map[string]string),
	}
}

// LoadOrCreate loads the mapping store from path (creating an empty one on
// error or schema mismatch per RULE-DIAG-PR2C-09). Mode 0600 enforced.
func LoadOrCreate(path string, log *slog.Logger) (*MappingStore, error) {
	s := &MappingStore{
		path:      path,
		hostnames: make(map[string]string),
		macs:      make(map[string]string),
		ips:       make(map[string]string),
		usernames: make(map[string]string),
		keywords:  make(map[string]string),
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil // no file yet, fresh store
	}
	if err != nil {
		if log != nil {
			log.Warn("redactor: mapping file unreadable, starting fresh", "path", path, "err", err)
		}
		return s, nil
	}
	var mf mappingFile
	if err := json.Unmarshal(data, &mf); err != nil || mf.SchemaVersion != mappingSchemaVersion {
		// RULE-DIAG-PR2C-09: graceful schema mismatch — don't crash.
		if log != nil {
			log.Warn("redactor: mapping file schema mismatch or corrupt, discarding", "path", path)
		}
		return s, nil
	}
	if mf.Hostnames != nil {
		s.hostnames = mf.Hostnames
	}
	if mf.MACs != nil {
		s.macs = mf.MACs
	}
	if mf.IPs != nil {
		s.ips = mf.IPs
	}
	if mf.Usernames != nil {
		s.usernames = mf.Usernames
	}
	if mf.Keywords != nil {
		s.keywords = mf.Keywords
	}
	return s, nil
}

// Save persists the store to its path with mode 0600.
// RULE-DIAG-PR2C-04: verified post-write.
func (s *MappingStore) Save(log *slog.Logger) error {
	s.mu.Lock()
	mf := mappingFile{
		SchemaVersion: mappingSchemaVersion,
		Hostnames:     copyMap(s.hostnames),
		MACs:          copyMap(s.macs),
		IPs:           copyMap(s.ips),
		Usernames:     copyMap(s.usernames),
		Keywords:      copyMap(s.keywords),
	}
	s.mu.Unlock()

	if s.path == "" {
		return nil // in-memory store, no persistence
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("redactor: mkdir mapping dir: %w", err)
	}
	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return fmt.Errorf("redactor: marshal mapping: %w", err)
	}
	// RULE-DIAG-PR2C-04: O_EXCL not used here because we allow updates;
	// mode 0600 from OpenFile is verified post-write.
	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("redactor: open mapping file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("redactor: write mapping file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("redactor: close mapping file: %w", err)
	}
	// Post-write stat verification per RULE-DIAG-PR2C-04.
	fi, err := os.Stat(s.path)
	if err != nil {
		return fmt.Errorf("redactor: stat mapping file: %w", err)
	}
	if fi.Mode().Perm() != 0o600 {
		return fmt.Errorf("redactor: mapping file mode %o, want 0600", fi.Mode().Perm())
	}
	return nil
}

// mapOrCreate returns the existing token for key, or allocates a new one
// using prefix and the map's current length.
func (s *MappingStore) mapOrCreate(m map[string]string, key, prefix string) string {
	if tok, ok := m[key]; ok {
		return tok
	}
	tok := fmt.Sprintf("%s_%d", prefix, len(m)+1)
	m[key] = tok
	return tok
}

func (s *MappingStore) hostname(h string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mapOrCreate(s.hostnames, h, "obf_host")
}

func (s *MappingStore) mac(m string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mapOrCreate(s.macs, m, "obf_mac")
}

func (s *MappingStore) ip(addr string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mapOrCreate(s.ips, addr, "obf_ip")
}

func (s *MappingStore) username(u string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mapOrCreate(s.usernames, u, "obf_user")
}

func (s *MappingStore) keyword(k string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mapOrCreate(s.keywords, k, "obf_keyword")
}

func copyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v // maps.Copy requires go1.21+; avoid new import for one line
	}
	return out
}
