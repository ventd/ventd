package state

import (
	"fmt"
	"log/slog"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

const kvSchemaVersion = 1

// KVDB is a YAML-backed key-value store.
// Writes use tempfile+rename+fsync for crash safety (RULE-STATE-01).
// A single RWMutex serialises concurrent access within the process (RULE-STATE-06).
type KVDB struct {
	path   string
	logger *slog.Logger
	mu     sync.RWMutex
	data   map[string]map[string]any // namespace → key → value
}

func openKV(path string, logger *slog.Logger) (*KVDB, error) {
	db := &KVDB{
		path:   path,
		logger: logger,
		data:   make(map[string]map[string]any),
	}
	// Repair mode before reading so the repaired file is what we parse.
	if err := db.repairMode(); err != nil {
		logger.Warn("state: kv: mode repair failed", "path", path, "err", err)
	}
	if err := db.load(); err != nil {
		return nil, err
	}
	return db, nil
}

// repairMode corrects the file permission if it differs from 0640 (RULE-STATE-09).
func (db *KVDB) repairMode() error {
	info, err := os.Stat(db.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode().Perm() != fileMode {
		db.logger.Info("state: kv: repairing file mode", "path", db.path,
			"current", info.Mode().Perm(), "target", fileMode)
		return os.Chmod(db.path, fileMode)
	}
	return nil
}

func (db *KVDB) load() error {
	raw, err := os.ReadFile(db.path)
	if os.IsNotExist(err) {
		return nil // first boot — empty state is valid (RULE-STATE-10)
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", db.path, err)
	}
	var top map[string]any
	if err := yaml.Unmarshal(raw, &top); err != nil {
		db.logger.Warn("state: kv: corrupt state.yaml, treating as empty", "path", db.path, "err", err)
		return nil
	}
	for k, v := range top {
		if k == "schema_version" {
			continue
		}
		if ns, ok := v.(map[string]any); ok {
			db.data[k] = ns
		}
	}
	return nil
}

// Get retrieves namespace/key. ok=false when the key is absent.
func (db *KVDB) Get(namespace, key string) (any, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	ns, ok := db.data[namespace]
	if !ok {
		return nil, false, nil
	}
	v, ok := ns[key]
	return v, ok, nil
}

// Set stores namespace/key=value and persists atomically.
func (db *KVDB) Set(namespace, key string, value any) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.data[namespace] == nil {
		db.data[namespace] = make(map[string]any)
	}
	db.data[namespace][key] = value
	return db.persist()
}

// Delete removes namespace/key and persists atomically.
func (db *KVDB) Delete(namespace, key string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if ns, ok := db.data[namespace]; ok {
		delete(ns, key)
		if len(ns) == 0 {
			delete(db.data, namespace)
		}
	}
	return db.persist()
}

// List returns a snapshot of all key/value pairs in namespace.
func (db *KVDB) List(namespace string) (map[string]any, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	ns, ok := db.data[namespace]
	if !ok {
		return map[string]any{}, nil
	}
	out := make(map[string]any, len(ns))
	for k, v := range ns {
		out[k] = v
	}
	return out, nil
}

// KVTx is a transaction context for batched KV operations.
type KVTx struct {
	data map[string]map[string]any
}

// Get retrieves a value within the transaction.
func (tx *KVTx) Get(namespace, key string) (any, bool) {
	ns, ok := tx.data[namespace]
	if !ok {
		return nil, false
	}
	v, ok := ns[key]
	return v, ok
}

// Set records a value within the transaction (not written until commit).
func (tx *KVTx) Set(namespace, key string, value any) {
	if tx.data[namespace] == nil {
		tx.data[namespace] = make(map[string]any)
	}
	tx.data[namespace][key] = value
}

// Delete records a deletion within the transaction.
func (tx *KVTx) Delete(namespace, key string) {
	if ns, ok := tx.data[namespace]; ok {
		delete(ns, key)
		if len(ns) == 0 {
			delete(tx.data, namespace)
		}
	}
}

// WithTransaction executes fn in a transaction and commits all changes with a
// single atomic write (RULE-STATE-07). If fn returns an error no changes are
// committed — the in-memory state and the on-disk file are unchanged.
func (db *KVDB) WithTransaction(fn func(tx *KVTx) error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	snap := kvDeepCopy(db.data)
	tx := &KVTx{data: snap}
	if err := fn(tx); err != nil {
		return err
	}
	db.data = tx.data
	return db.persist()
}

// persist serialises in-memory state to state.yaml via atomicWrite.
// Must be called with db.mu held (write or from WithTransaction).
func (db *KVDB) persist() error {
	out := make(map[string]any, len(db.data)+1)
	out["schema_version"] = kvSchemaVersion
	for ns, kv := range db.data {
		out[ns] = kv
	}
	data, err := yaml.Marshal(out)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return atomicWrite(db.path, data, fileMode)
}

func kvDeepCopy(src map[string]map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(src))
	for ns, kv := range src {
		cp := make(map[string]any, len(kv))
		for k, v := range kv {
			cp[k] = v
		}
		out[ns] = cp
	}
	return out
}
