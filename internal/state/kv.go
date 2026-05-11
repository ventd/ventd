package state

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/ventd/ventd/internal/iox"
)

// ErrCorruptState is returned by load() when state.yaml's YAML
// payload fails to unmarshal. The pre-fix behaviour silently
// downgraded "corrupt state" to "empty state, first-boot semantics"
// — silently destroying wizard / polarity / smart-mode records that
// drive operator-visible decisions. Returning a sentinel error lets
// the daemon's startup path surface the corruption to the operator
// rather than re-running the setup wizard with no warning. Audit
// pass-6 finding H4 (#1055).
//
// The blob store's "found=false → re-initialise" pattern
// (RULE-STATE-02) is intentionally tolerant of partial writes
// because every blob is checksummed and small. The KV store has no
// per-key checksum and lives behind the YAML format's grammar, so
// the conservative behaviour is to refuse rather than silently lose.
var ErrCorruptState = errors.New("state: kv: state.yaml failed to parse")

// ErrTransactionPersistFailed wraps the persist-time error
// returned by WithTransaction so callers can distinguish a failed
// commit (caller's mutation reached fn but the disk write failed)
// from a failed fn (the caller's closure returned an error and no
// commit was attempted). On a failed commit the in-memory KV state
// is rolled back to its pre-transaction snapshot so the in-memory
// view and the on-disk file stay consistent. Audit pass-6 finding
// H3 (#1054).
var ErrTransactionPersistFailed = errors.New("state: kv: transaction persist failed; in-memory state rolled back")

// ensureFreeSpaceFn is the swappable seam KV writes consult before
// mutating in-memory state. Production points at iox.EnsureFreeSpace
// with iox.MinFreeBytesForState as the threshold. Tests swap this
// to a stub returning iox.ErrInsufficientFreeSpace to exercise the
// refusal path without needing an actually-full filesystem. Per
// RULE-STATE-12.
var ensureFreeSpaceFn = func(dir string) error {
	return iox.EnsureFreeSpace(dir, iox.MinFreeBytesForState)
}

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
		// Surface corruption explicitly so the daemon's startup
		// path can refuse-start rather than silently re-initialise
		// the wizard / polarity / smart-mode records that drive
		// operator-visible decisions. Pre-fix behaviour: WARN +
		// return nil. Audit pass-6 finding H4 (#1055).
		db.logger.Warn("state: kv: corrupt state.yaml",
			"path", db.path, "err", err)
		return fmt.Errorf("%w: %v", ErrCorruptState, err)
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

// Set stores namespace/key=value and persists atomically. Refuses
// before mutating in-memory state when the state directory has less
// than iox.MinFreeBytesForState bytes free, so a doomed disk-write
// cannot cause the daemon's in-memory view to drift ahead of the
// on-disk file. Per RULE-STATE-12.
func (db *KVDB) Set(namespace, key string, value any) error {
	if err := ensureFreeSpaceFn(filepath.Dir(db.path)); err != nil {
		return err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.data[namespace] == nil {
		db.data[namespace] = make(map[string]any)
	}
	db.data[namespace][key] = value
	return db.persist()
}

// Delete removes namespace/key and persists atomically. Same
// pre-flight free-space gate as Set so the refusal happens before
// in-memory mutation. Per RULE-STATE-12.
func (db *KVDB) Delete(namespace, key string) error {
	if err := ensureFreeSpaceFn(filepath.Dir(db.path)); err != nil {
		return err
	}
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
//
// The free-space pre-flight (RULE-STATE-12) fires before fn is even
// called: if the state directory is critically low, the whole
// transaction is refused without invoking the caller's mutation
// closure. This preserves WithTransaction's contract that fn-returns-
// error leaves the world untouched, AND extends it with "low-disk
// returns iox.ErrInsufficientFreeSpace before fn ever runs", so a
// caller that does expensive work inside fn doesn't burn the cycles
// only to discover the commit can't land.
func (db *KVDB) WithTransaction(fn func(tx *KVTx) error) error {
	if err := ensureFreeSpaceFn(filepath.Dir(db.path)); err != nil {
		return err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	// Snapshot for fn to mutate.
	snap := kvDeepCopy(db.data)
	tx := &KVTx{data: snap}
	if err := fn(tx); err != nil {
		// fn-returns-error: snap is discarded; db.data untouched.
		return err
	}
	// Preserve the pre-transaction state in case persist fails — the
	// pre-fix code mutated db.data BEFORE attempting persist, so a
	// failed persist left the in-memory view advanced while the on-
	// disk file was stale. On daemon restart the runtime quietly
	// loaded the OLD on-disk value, having spent the prior lifetime
	// reading and writing against the NEW (now-orphaned) in-memory
	// state. Audit pass-6 finding H3 (#1054).
	prev := db.data
	db.data = tx.data
	if err := db.persist(); err != nil {
		// Roll back so the in-memory and on-disk views stay
		// consistent, and wrap so the caller can distinguish a
		// "fn-failed" rollback (no commit attempted) from a
		// "persist-failed" rollback (commit attempted, write
		// failed) via errors.Is(err, ErrTransactionPersistFailed).
		db.data = prev
		return fmt.Errorf("%w: %v", ErrTransactionPersistFailed, err)
	}
	return nil
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
