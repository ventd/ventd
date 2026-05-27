package state

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/iox"
)

// discardLogger returns a no-op slog.Logger for test use.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestKV_SchemaVersionLoaded binds RULE-STATE-11: SchemaVersionLoaded()
// reports true only after a clean openKV load; nil receivers report false.
func TestKV_SchemaVersionLoaded(t *testing.T) {
	dir := t.TempDir()
	db, err := openKV(filepath.Join(dir, "state.yaml"), discardLogger())
	if err != nil {
		t.Fatalf("openKV: %v", err)
	}
	if !db.SchemaVersionLoaded() {
		t.Fatalf("clean open must report schema loaded")
	}

	// nil-safety at both levels (monitor-only / pre-open daemon).
	var nilKV *KVDB
	if nilKV.SchemaVersionLoaded() {
		t.Fatalf("nil KVDB must report schema not loaded")
	}
	var nilState *State
	if nilState.SchemaVersionLoaded() {
		t.Fatalf("nil State must report schema not loaded")
	}

	if st := (&State{KV: db}); !st.SchemaVersionLoaded() {
		t.Fatalf("State passthrough must report schema loaded")
	}
}

// TestRULE_STATE_01_AtomicWrite verifies that KV writes use tempfile+rename
// semantics: the final state.yaml is correct and no leftover .tmp files exist.
// A pre-existing partial .tmp file (simulating a prior crash mid-write) must
// not corrupt the store (RULE-STATE-01).
func TestRULE_STATE_01_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	db, err := openKV(filepath.Join(dir, "state.yaml"), discardLogger())
	if err != nil {
		t.Fatalf("openKV: %v", err)
	}

	// Leave a stale .tmp file to simulate a prior crashed write.
	stale := filepath.Join(dir, "state.yaml.tmp.deadbeef")
	if err := os.WriteFile(stale, []byte("corrupted"), 0o640); err != nil {
		t.Fatalf("write stale tmp: %v", err)
	}

	if err := db.Set("ns", "key", "value"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// state.yaml must exist with the correct value.
	if _, err := os.Stat(filepath.Join(dir, "state.yaml")); err != nil {
		t.Fatalf("state.yaml missing after Set: %v", err)
	}
	v, ok, _ := db.Get("ns", "key")
	if !ok || v != "value" {
		t.Errorf("Get after Set: got %v ok=%v, want value ok=true", v, ok)
	}

	// The stale tmp file must still exist (atomicWrite only removes its OWN tmp).
	if _, err := os.Stat(stale); err != nil {
		t.Error("stale .tmp from prior crash was unexpectedly removed")
	}

	// No additional .tmp files must remain after a successful write.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") && e.Name() != filepath.Base(stale) {
			t.Errorf("leftover tmp file after write: %s", e.Name())
		}
	}

	// Verify file mode is 0640.
	info, err := os.Stat(filepath.Join(dir, "state.yaml"))
	if err != nil {
		t.Fatalf("stat state.yaml: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("state.yaml mode = %o, want 0640", info.Mode().Perm())
	}

	// Re-open the KV store from disk and verify persistence.
	db2, err := openKV(filepath.Join(dir, "state.yaml"), discardLogger())
	if err != nil {
		t.Fatalf("re-open KV: %v", err)
	}
	v2, ok2, _ := db2.Get("ns", "key")
	if !ok2 || v2 != "value" {
		t.Errorf("persisted Get: got %v ok=%v, want value ok=true", v2, ok2)
	}
}

// TestRULE_STATE_02_BlobSHA256Verification verifies that a corrupted blob
// payload causes Read to return found=false rather than the corrupted bytes,
// and that valid blobs round-trip correctly (RULE-STATE-02).
func TestRULE_STATE_02_BlobSHA256Verification(t *testing.T) {
	dir := t.TempDir()
	b := newBlobDB(filepath.Join(dir, "models"), discardLogger())

	payload := []byte("thermal model coefficients v1")
	if err := b.Write("thermal.dat", 1, payload); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Happy path: read back intact.
	got, sv, found, err := b.Read("thermal.dat")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !found {
		t.Fatal("Read: expected found=true for valid blob")
	}
	if string(got) != string(payload) {
		t.Errorf("Read payload mismatch: got %q, want %q", got, payload)
	}
	if sv != 1 {
		t.Errorf("Read schemaVersion: got %d, want 1", sv)
	}

	// Corrupt one byte in the payload region (offset 16 = start of payload).
	blobPath := filepath.Join(dir, "models", "thermal.dat")
	f, err := os.OpenFile(blobPath, os.O_RDWR, 0o640)
	if err != nil {
		t.Fatalf("open blob for corruption: %v", err)
	}
	corruptOffset := int64(blobHeaderSize + 5) // byte 5 of the payload
	orig := make([]byte, 1)
	if _, err := f.ReadAt(orig, corruptOffset); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	corrupted := []byte{orig[0] ^ 0xFF}
	if _, err := f.WriteAt(corrupted, corruptOffset); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close corrupted blob: %v", err)
	}

	// Read after corruption: must return found=false.
	_, _, found, err = b.Read("thermal.dat")
	if err != nil {
		t.Fatalf("Read after corruption: %v", err)
	}
	if found {
		t.Error("Read after corruption: expected found=false (SHA256 mismatch)")
	}

	// Missing file: must return found=false without error.
	_, _, found, err = b.Read("nonexistent.dat")
	if err != nil {
		t.Fatalf("Read missing: %v", err)
	}
	if found {
		t.Error("Read missing file: expected found=false")
	}
}

// TestRULE_STATE_03_LogOAppendODsync is a static-analysis test: verifies that
// log.go contains the O_APPEND and O_DSYNC flags, proving that the
// implementation uses crash-consistent append semantics (RULE-STATE-03).
func TestRULE_STATE_03_LogOAppendODsync(t *testing.T) {
	data, err := os.ReadFile("log.go")
	if err != nil {
		t.Fatalf("read log.go: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "O_APPEND") {
		t.Error("log.go must use O_APPEND (RULE-STATE-03)")
	}
	if !strings.Contains(content, "O_DSYNC") {
		t.Error("log.go must use O_DSYNC (RULE-STATE-03)")
	}
}

// TestRULE_STATE_04_LogTornRecordSkip verifies that the log reader tolerates
// both torn records (truncation mid-record) and CRC-mismatched records, and
// that valid records surrounding the corrupt one are still delivered
// (RULE-STATE-04).
func TestRULE_STATE_04_LogTornRecordSkip(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	// Build a raw log file with known records, bypassing LogDB to avoid
	// O_DSYNC on a temp filesystem and to precisely control corruption.
	writeRawRecord := func(w io.Writer, payload []byte) {
		length := uint32(len(payload))
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], length)
		crc := crc32.NewIEEE()
		crc.Write(lenBuf[:])
		crc.Write(payload)
		var crcBuf [4]byte
		binary.BigEndian.PutUint32(crcBuf[:], crc.Sum32())
		_, _ = w.Write(lenBuf[:])
		_, _ = w.Write(payload)
		_, _ = w.Write(crcBuf[:])
	}

	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	// Records 0-4: valid before corrupt.
	for i := range 5 {
		writeRawRecord(f, []byte(fmt.Sprintf("record-%02d", i)))
	}
	// Record 5: write with a flipped CRC byte (complete record, bad checksum).
	corruptPayload := []byte("record-05-corrupt")
	length := uint32(len(corruptPayload))
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], length)
	crc := crc32.NewIEEE()
	crc.Write(lenBuf[:])
	crc.Write(corruptPayload)
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc.Sum32()^0xDEADBEEF) // flip CRC
	_, _ = f.Write(lenBuf[:])
	_, _ = f.Write(corruptPayload)
	_, _ = f.Write(crcBuf[:])
	// Records 6-9: valid after corrupt.
	for i := 6; i < 10; i++ {
		writeRawRecord(f, []byte(fmt.Sprintf("record-%02d", i)))
	}
	// Torn record: write only the length prefix for record 10 (no payload/CRC).
	var tornLen [4]byte
	binary.BigEndian.PutUint32(tornLen[:], 20) // claims 20-byte payload that never arrives
	_, _ = f.Write(tornLen[:])
	// Records 11-14 after the torn record — these will NOT be returned because
	// the torn length prefix consumed the byte stream.
	for i := 11; i < 15; i++ {
		writeRawRecord(f, []byte(fmt.Sprintf("record-%02d", i)))
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close log file: %v", err)
	}

	// Collect records via readRecords.
	rf, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer func() { _ = rf.Close() }()

	var got []string
	err = readRecords(rf, func(p []byte) error {
		got = append(got, string(p))
		return nil
	})
	if err != nil {
		t.Fatalf("readRecords: %v", err)
	}

	// Expect records 0-4 and 6-9 (record-05 CRC skipped; record-10 torn stops iteration).
	want := []string{
		"record-00", "record-01", "record-02", "record-03", "record-04",
		// record-05 skipped (bad CRC)
		"record-06", "record-07", "record-08", "record-09",
		// torn record-10 stops further iteration
	}
	if len(got) != len(want) {
		t.Fatalf("readRecords returned %d records, want %d\ngot: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("record[%d]: got %q, want %q", i, got[i], w)
		}
	}
}

// TestRULE_STATE_05_SchemaVersionCheck verifies that a downgrade (on-disk
// version newer than binary) is refused, and that an upgrade (on-disk version
// older than binary) is handled gracefully (RULE-STATE-05).
func TestRULE_STATE_05_SchemaVersionCheck(t *testing.T) {
	t.Run("downgrade_refused", func(t *testing.T) {
		dir := t.TempDir()
		// Write a future version to the sentinel.
		if err := os.WriteFile(filepath.Join(dir, versionFileName), []byte("99\n"), 0o640); err != nil {
			t.Fatalf("write version: %v", err)
		}
		err := CheckVersion(dir)
		if err == nil {
			t.Fatal("CheckVersion: expected error for version 99 > currentVersion, got nil")
		}
		if !errors.Is(err, ErrDowngrade) {
			t.Errorf("CheckVersion error does not wrap ErrDowngrade: %v", err)
		}
		if !strings.Contains(err.Error(), "99") {
			t.Errorf("error message should mention on-disk version 99: %v", err)
		}
	})

	t.Run("upgrade_no_migration_treats_as_missing", func(t *testing.T) {
		dir := t.TempDir()
		// Write a past version (0 < currentVersion=1) with no migration registered.
		if err := os.WriteFile(filepath.Join(dir, versionFileName), []byte("0\n"), 0o640); err != nil {
			t.Fatalf("write version: %v", err)
		}
		// No migration registered for 0→1, so it should succeed (treat as missing).
		if err := CheckVersion(dir); err != nil {
			t.Fatalf("CheckVersion upgrade without migration: %v", err)
		}
		// Version sentinel must now reflect currentVersion.
		raw, err := os.ReadFile(filepath.Join(dir, versionFileName))
		if err != nil {
			t.Fatalf("read version after upgrade: %v", err)
		}
		v, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		if v != currentVersion {
			t.Errorf("version after upgrade: got %d, want %d", v, currentVersion)
		}
	})

	t.Run("same_version_ok", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, versionFileName),
			[]byte(strconv.Itoa(currentVersion)+"\n"), 0o640); err != nil {
			t.Fatalf("write version: %v", err)
		}
		if err := CheckVersion(dir); err != nil {
			t.Fatalf("CheckVersion same version: %v", err)
		}
	})

	t.Run("first_run_creates_sentinel", func(t *testing.T) {
		dir := t.TempDir()
		if err := CheckVersion(dir); err != nil {
			t.Fatalf("CheckVersion first run: %v", err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, versionFileName))
		if err != nil {
			t.Fatalf("version file not created: %v", err)
		}
		v, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		if v != currentVersion {
			t.Errorf("created version: got %d, want %d", v, currentVersion)
		}
	})
}

// TestRULE_STATE_06_PIDFileMultiProcess verifies that AcquirePID detects a
// running process via the PID file and returns ErrAlreadyRunning, and that a
// stale PID file (dead process) is replaced successfully (RULE-STATE-06).
func TestRULE_STATE_06_PIDFileMultiProcess(t *testing.T) {
	dir := t.TempDir()

	// Write the test process's own PID to simulate a running daemon.
	pidPath := filepath.Join(dir, pidFileName)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o640); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	// AcquirePID must return ErrAlreadyRunning.
	_, err := AcquirePID(dir)
	if err == nil {
		t.Fatal("AcquirePID: expected ErrAlreadyRunning when PID file holds live PID, got nil")
	}
	var already *ErrAlreadyRunning
	if !errors.As(err, &already) {
		t.Fatalf("AcquirePID: expected *ErrAlreadyRunning, got %T: %v", err, err)
	}
	if already.PID != os.Getpid() {
		t.Errorf("ErrAlreadyRunning.PID: got %d, want %d", already.PID, os.Getpid())
	}

	// Simulate stale PID (dead process): write a PID that cannot be alive.
	// PID 2^22 is above Linux max_pid (default 4194304 = 2^22); kernel wraps
	// before that, so a ridiculously large value is guaranteed non-existent.
	if err := os.WriteFile(pidPath, []byte("9999999\n"), 0o640); err != nil {
		t.Fatalf("write stale pid: %v", err)
	}

	release, err := AcquirePID(dir)
	if err != nil {
		t.Fatalf("AcquirePID with stale pid: %v", err)
	}
	defer release()

	// PID file should now contain our PID.
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read pid after acquire: %v", err)
	}
	got, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if got != os.Getpid() {
		t.Errorf("pid file after acquire: got %d, want %d", got, os.Getpid())
	}
}

// TestRULE_STATE_07_TransactionAtomicCommit verifies that WithTransaction
// commits all changes in a single write and that a failed transaction leaves
// the store unchanged (RULE-STATE-07).
func TestRULE_STATE_07_TransactionAtomicCommit(t *testing.T) {
	dir := t.TempDir()
	db, err := openKV(filepath.Join(dir, "state.yaml"), discardLogger())
	if err != nil {
		t.Fatalf("openKV: %v", err)
	}

	// Seed one key so there is pre-existing state.
	if err := db.Set("pre", "exists", "yes"); err != nil {
		t.Fatalf("Set seed: %v", err)
	}

	// Successful transaction: 3 sets across 2 namespaces.
	if err := db.WithTransaction(func(tx *KVTx) error {
		tx.Set("ns1", "k1", "v1")
		tx.Set("ns1", "k2", "v2")
		tx.Set("ns2", "k3", "v3")
		return nil
	}); err != nil {
		t.Fatalf("WithTransaction: %v", err)
	}

	for _, tc := range []struct{ ns, k, want string }{
		{"ns1", "k1", "v1"}, {"ns1", "k2", "v2"}, {"ns2", "k3", "v3"},
		{"pre", "exists", "yes"}, // pre-existing key preserved
	} {
		v, ok, _ := db.Get(tc.ns, tc.k)
		if !ok || v != tc.want {
			t.Errorf("Get(%s,%s)=%v ok=%v; want %q ok=true", tc.ns, tc.k, v, ok, tc.want)
		}
	}

	// No leftover .tmp files.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Errorf("leftover tmp after transaction: %s", e.Name())
		}
	}

	// Failed transaction: error from fn must leave store unchanged.
	txErr := db.WithTransaction(func(tx *KVTx) error {
		tx.Set("ns1", "k_rollback", "should_not_appear")
		return fmt.Errorf("simulated failure")
	})
	if txErr == nil {
		t.Fatal("WithTransaction: expected error from failing fn, got nil")
	}
	_, ok, _ := db.Get("ns1", "k_rollback")
	if ok {
		t.Error("k_rollback must not be set after failed transaction")
	}
}

// TestRULE_STATE_08_LogRotationNoRecordLoss verifies that log rotation does
// not lose records that were written before or after the rotation
// (RULE-STATE-08).
func TestRULE_STATE_08_LogRotationNoRecordLoss(t *testing.T) {
	dir := t.TempDir()
	db := newLogDB(dir, discardLogger())
	defer func() { _ = db.closeAll() }()

	// Use a policy with no auto-rotation and no compression to keep the test
	// deterministic and fast.
	if err := db.SetRotationPolicy("test", RotationPolicy{
		MaxSizeMB:   0,
		MaxAgeDays:  0,
		KeepCount:   5,
		CompressOld: false,
	}); err != nil {
		t.Fatalf("SetRotationPolicy: %v", err)
	}

	// Append 50 records.
	for i := range 50 {
		payload := fmt.Sprintf("pre-rotate-%02d", i)
		if err := db.Append("test", []byte(payload)); err != nil {
			t.Fatalf("Append pre-rotate[%d]: %v", i, err)
		}
	}

	// Rotate.
	if err := db.Rotate("test"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// Append 50 more records after rotation.
	for i := 50; i < 100; i++ {
		payload := fmt.Sprintf("post-rotate-%02d", i)
		if err := db.Append("test", []byte(payload)); err != nil {
			t.Fatalf("Append post-rotate[%d]: %v", i, err)
		}
	}

	// Iterate and collect all records.
	var records []string
	if err := db.Iterate("test", time.Time{}, func(p []byte) error {
		records = append(records, string(p))
		return nil
	}); err != nil {
		t.Fatalf("Iterate: %v", err)
	}

	if len(records) != 100 {
		t.Fatalf("Iterate returned %d records, want 100\nrecords: %v", len(records), records)
	}

	// Spot-check boundary records.
	found := func(needle string) bool {
		for _, r := range records {
			if r == needle {
				return true
			}
		}
		return false
	}
	for _, needle := range []string{"pre-rotate-00", "pre-rotate-49", "post-rotate-50", "post-rotate-99"} {
		if !found(needle) {
			t.Errorf("record %q missing from iteration", needle)
		}
	}
}

// TestRULE_STATE_09_FileModeRepair verifies that openKV repairs a state.yaml
// that has the wrong file permission (e.g. 0600 from a umask quirk) to 0640,
// and that the daemon continues operating normally rather than refusing
// (RULE-STATE-09).
func TestRULE_STATE_09_FileModeRepair(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "state.yaml")

	// Create state.yaml with the wrong mode (0600 instead of 0640).
	if err := os.WriteFile(yamlPath, []byte("schema_version: 1\n"), 0o600); err != nil {
		t.Fatalf("write state.yaml: %v", err)
	}
	info, _ := os.Stat(yamlPath)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("pre-condition: expected mode 0600, got %o", info.Mode().Perm())
	}

	// openKV must repair the mode and succeed.
	db, err := openKV(yamlPath, discardLogger())
	if err != nil {
		t.Fatalf("openKV: %v", err)
	}
	_ = db

	info, err = os.Stat(yamlPath)
	if err != nil {
		t.Fatalf("stat after repair: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode after repair: got %o, want 0640", info.Mode().Perm())
	}
}

// TestRULE_STATE_10_DirectoryBootstrap verifies that Open creates the state
// directory hierarchy when it does not exist, returns no error, and leaves the
// stores functional (RULE-STATE-10).
func TestRULE_STATE_10_DirectoryBootstrap(t *testing.T) {
	// Point at a subdirectory that does not yet exist.
	base := t.TempDir()
	stateDir := filepath.Join(base, "ventd", "state")

	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatal("pre-condition: state dir must not exist")
	}

	st, err := Open(stateDir, discardLogger())
	if err != nil {
		t.Fatalf("Open on missing dir: %v", err)
	}
	defer func() { _ = st.Close() }()

	// All three subdirectories must have been created.
	for _, sub := range []string{stateDir, filepath.Join(stateDir, "models"), filepath.Join(stateDir, "logs")} {
		if _, err := os.Stat(sub); err != nil {
			t.Errorf("directory not created: %s: %v", sub, err)
		}
	}

	// KV store must be usable.
	if err := st.KV.Set("test", "bootstrap", true); err != nil {
		t.Fatalf("KV.Set after bootstrap: %v", err)
	}
	v, ok, _ := st.KV.Get("test", "bootstrap")
	if !ok || v != true {
		t.Errorf("KV.Get after bootstrap: got %v ok=%v, want true ok=true", v, ok)
	}

	// Blob store must be usable.
	if err := st.Blob.Write("probe.dat", 1, []byte("hello")); err != nil {
		t.Fatalf("Blob.Write after bootstrap: %v", err)
	}
	payload, _, found, _ := st.Blob.Read("probe.dat")
	if !found || string(payload) != "hello" {
		t.Errorf("Blob.Read after bootstrap: got %q found=%v", payload, found)
	}

	// Log store must be usable.
	if err := st.Log.Append("boot", []byte("first entry")); err != nil {
		t.Fatalf("Log.Append after bootstrap: %v", err)
	}
	var logRecords []string
	if err := st.Log.Iterate("boot", time.Time{}, func(p []byte) error {
		logRecords = append(logRecords, string(p))
		return nil
	}); err != nil {
		t.Fatalf("Log.Iterate after bootstrap: %v", err)
	}
	if len(logRecords) != 1 || logRecords[0] != "first entry" {
		t.Errorf("Log.Iterate after bootstrap: got %v, want [first entry]", logRecords)
	}
}

// TestRULE_STATE_12_FreeSpaceGuard verifies that KV writes refuse
// before mutating in-memory state when the state directory has less
// than iox.MinFreeBytesForState bytes free. This prevents the
// silent in-memory/on-disk divergence that the senior-review's C7
// finding identified: prior to this guard, Set() would mutate the
// in-memory map first (line 100 of kv.go) and call persist() second
// (line 101); a persist failure on ENOSPC returned the error to the
// caller but left the in-memory map advanced — on restart the daemon
// loaded the OLD on-disk value while the runtime had been quietly
// running on the NEW value (RULE-STATE-12).
//
// Tests inject via the ensureFreeSpaceFn package-level seam rather
// than mounting a tmpfs, so the refusal path is reproducible on any
// CI runner regardless of the test root's actual free space.
func TestRULE_STATE_12_FreeSpaceGuard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")

	t.Run("RULE-STATE-12_set_refuses_before_in_memory_mutation", func(t *testing.T) {
		db, err := openKV(path, discardLogger())
		if err != nil {
			t.Fatalf("openKV: %v", err)
		}
		// Seed an initial value so we can prove the refused Set did
		// not overwrite it.
		if err := db.Set("ns", "key", "original"); err != nil {
			t.Fatalf("Set seed: %v", err)
		}

		// Inject a stub that always refuses.
		orig := ensureFreeSpaceFn
		ensureFreeSpaceFn = func(_ string) error {
			return fmt.Errorf("test stub: %w", iox.ErrInsufficientFreeSpace)
		}
		t.Cleanup(func() { ensureFreeSpaceFn = orig })

		err = db.Set("ns", "key", "newvalue")
		if err == nil {
			t.Fatal("expected refusal on stubbed-low-space, got nil")
		}
		if !errors.Is(err, iox.ErrInsufficientFreeSpace) {
			t.Errorf("expected wrapped iox.ErrInsufficientFreeSpace, got %v", err)
		}

		// Critical assertion: in-memory state was NOT mutated by the
		// refused Set. A naïve "pre-flight in persist()" implementation
		// would have already mutated the map before persist refused;
		// this test would then read "newvalue" and fail.
		v, ok, err := db.Get("ns", "key")
		if err != nil {
			t.Fatalf("Get after refused Set: %v", err)
		}
		if !ok {
			t.Fatal("expected key to remain after refused Set")
		}
		if v != "original" {
			t.Errorf("in-memory mutated despite refused Set: Get returned %q, want %q", v, "original")
		}

		// And on-disk state must also still be the original — a
		// healthy persist following a low-space refusal doesn't
		// happen, so the disk file is unchanged.
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read state.yaml: %v", readErr)
		}
		if !strings.Contains(string(raw), "original") {
			t.Errorf("on-disk state.yaml lost the original value; content:\n%s", raw)
		}
		if strings.Contains(string(raw), "newvalue") {
			t.Errorf("on-disk state.yaml acquired the refused newvalue; content:\n%s", raw)
		}
	})

	t.Run("RULE-STATE-12_delete_refuses_before_in_memory_mutation", func(t *testing.T) {
		dir := t.TempDir()
		db, err := openKV(filepath.Join(dir, "state.yaml"), discardLogger())
		if err != nil {
			t.Fatalf("openKV: %v", err)
		}
		if err := db.Set("ns", "key", "original"); err != nil {
			t.Fatalf("Set seed: %v", err)
		}

		orig := ensureFreeSpaceFn
		ensureFreeSpaceFn = func(_ string) error {
			return fmt.Errorf("test stub: %w", iox.ErrInsufficientFreeSpace)
		}
		t.Cleanup(func() { ensureFreeSpaceFn = orig })

		err = db.Delete("ns", "key")
		if err == nil {
			t.Fatal("expected Delete refusal on stubbed-low-space, got nil")
		}
		if !errors.Is(err, iox.ErrInsufficientFreeSpace) {
			t.Errorf("expected wrapped iox.ErrInsufficientFreeSpace, got %v", err)
		}

		// Key must still be present in-memory — Delete refused before
		// the delete-from-map step.
		_, ok, _ := db.Get("ns", "key")
		if !ok {
			t.Error("Delete refused but key was still removed from in-memory map")
		}
	})

	t.Run("RULE-STATE-12_with_transaction_refuses_before_calling_fn", func(t *testing.T) {
		dir := t.TempDir()
		db, err := openKV(filepath.Join(dir, "state.yaml"), discardLogger())
		if err != nil {
			t.Fatalf("openKV: %v", err)
		}

		orig := ensureFreeSpaceFn
		ensureFreeSpaceFn = func(_ string) error {
			return fmt.Errorf("test stub: %w", iox.ErrInsufficientFreeSpace)
		}
		t.Cleanup(func() { ensureFreeSpaceFn = orig })

		fnCalled := false
		err = db.WithTransaction(func(tx *KVTx) error {
			fnCalled = true
			tx.Set("ns", "key", "v")
			return nil
		})
		if err == nil {
			t.Fatal("expected WithTransaction refusal on stubbed-low-space, got nil")
		}
		if !errors.Is(err, iox.ErrInsufficientFreeSpace) {
			t.Errorf("expected wrapped iox.ErrInsufficientFreeSpace, got %v", err)
		}
		if fnCalled {
			t.Error("WithTransaction invoked fn despite low-space refusal; expected pre-flight to short-circuit")
		}
	})

	t.Run("RULE-STATE-12_seam_restored_after_test_lets_subsequent_writes_pass", func(t *testing.T) {
		// Sanity: after the cleanup-restored seam, a fresh KV write
		// against the test root (which has gigabytes free) succeeds.
		// Catches any test that forgets to restore the seam and
		// poisons subsequent tests in the same package.
		dir := t.TempDir()
		db, err := openKV(filepath.Join(dir, "state.yaml"), discardLogger())
		if err != nil {
			t.Fatalf("openKV: %v", err)
		}
		if err := db.Set("ns", "key", "post-cleanup"); err != nil {
			t.Errorf("Set on healthy fs after seam cleanup failed: %v", err)
		}
	})
}

// TestRULE_STATE_MIGRATION_V1_V2_NOOP pins the registered no-op v1→v2
// migrator. A registered no-op is structurally distinct from a missing
// migrator: missing causes RULE-STATE-05's upgrade loop to break and
// the caller's state is effectively wiped on next access. Registered
// no-op keeps existing calibration / polarity / smart-mode shards
// intact across the version bump while exercising the migration
// mechanism end-to-end so any future real migration drops in cleanly.
//
// The bound rule lives in docs/rules/RULE-STATE-MIGRATION-V1-V2-NOOP.md.
func TestRULE_STATE_MIGRATION_V1_V2_NOOP(t *testing.T) {
	t.Run("v1_to_v2_migrator_is_registered", func(t *testing.T) {
		// Pin that the v1→v2 entry exists in the migrations map.
		// A regression that drops the entry — or registers it under
		// a different key — silently re-introduces the "treat as
		// missing" path the next time someone bumps currentVersion
		// without re-registering, wiping calibration / polarity /
		// smart-mode shards on first access.
		fn, ok := migrations[[2]int{1, 2}]
		if !ok {
			t.Fatal("migrations[[2]int{1,2}] not registered; v1→v2 must be a registered no-op")
		}
		if fn == nil {
			t.Fatal("migrations[[2]int{1,2}] is nil; want a callable no-op")
		}
		// And it must succeed on a tempdir without touching anything.
		if err := fn(t.TempDir()); err != nil {
			t.Errorf("registered no-op migrator returned error: %v", err)
		}
	})

	t.Run("upgrade_v1_to_currentVersion_runs_migrator_and_bumps_sentinel", func(t *testing.T) {
		// Set up a state dir as if a v1-vintage daemon wrote it.
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, versionFileName),
			[]byte("1\n"), 0o640); err != nil {
			t.Fatalf("write version: %v", err)
		}
		// CheckVersion must run the registered no-op AND bump the
		// sentinel to currentVersion. Neither alone is sufficient.
		if err := CheckVersion(dir); err != nil {
			t.Fatalf("CheckVersion v1→%d: %v", currentVersion, err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, versionFileName))
		if err != nil {
			t.Fatalf("read version after migrate: %v", err)
		}
		v, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		if v != currentVersion {
			t.Errorf("version after migrate: got %d, want %d (currentVersion)", v, currentVersion)
		}
	})

	t.Run("noop_migrator_does_not_mutate_sibling_files_in_state_dir", func(t *testing.T) {
		// The no-op migration MUST NOT touch any file other than
		// what CheckVersion's writeVersion bumps at the end. Seed
		// a sibling file representing existing calibration data,
		// run the migration, verify the sibling is byte-identical.
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, versionFileName),
			[]byte("1\n"), 0o640); err != nil {
			t.Fatalf("write version: %v", err)
		}
		sibling := filepath.Join(dir, "calibration-data.bin")
		want := "calibration data that must survive the no-op migration intact"
		if err := os.WriteFile(sibling, []byte(want), 0o640); err != nil {
			t.Fatalf("write sibling: %v", err)
		}
		if err := CheckVersion(dir); err != nil {
			t.Fatalf("CheckVersion: %v", err)
		}
		got, err := os.ReadFile(sibling)
		if err != nil {
			t.Fatalf("sibling disappeared after no-op migration: %v", err)
		}
		if string(got) != want {
			t.Errorf("sibling mutated by no-op migration: got %q, want %q", got, want)
		}
	})

	t.Run("currentVersion_is_at_least_2", func(t *testing.T) {
		// Pin that currentVersion was bumped. A regression that
		// reverts currentVersion to 1 makes the v1→v2 migrator
		// dead code and undoes the v0.6 broker-namespace reservation.
		if currentVersion < 2 {
			t.Errorf("currentVersion = %d; want >= 2 (v0.5.30 reserved v2 for v0.6 broker-namespace migration)", currentVersion)
		}
	})
}

// TestDurability_WriteVersion_UsesIOXWriteFile is a static-analysis
// regression test for audit pass-6 finding C1 (#1050). The pre-fix
// version.go used `os.WriteFile` directly, skipping iox.WriteFile's
// tempfile + fsync + rename + dir-fsync chain. A regression to
// `os.WriteFile` here re-opens RULE-IOX-01 violation on the version
// sentinel.
func TestDurability_WriteVersion_UsesIOXWriteFile(t *testing.T) {
	data, err := os.ReadFile("version.go")
	if err != nil {
		t.Fatalf("read version.go: %v", err)
	}
	src := string(data)
	if strings.Contains(src, "os.WriteFile(path") {
		t.Error("version.go must NOT call os.WriteFile directly on the version sentinel; use iox.WriteFile (#1050)")
	}
	if !strings.Contains(src, "iox.WriteFile(path") {
		t.Error("version.go must call iox.WriteFile on the version sentinel (#1050, RULE-IOX-01)")
	}

	// Behavioural check: writeVersion creates the sentinel atomically
	// and leaves no `.tmp.` siblings. Mirrors the leak-and-overwrite
	// assertions in TestRULE_STATE_01_AtomicWrite.
	dir := t.TempDir()
	path := filepath.Join(dir, versionFileName)
	if err := writeVersion(path, 2); err != nil {
		t.Fatalf("writeVersion: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(body) != "2\n" {
		t.Errorf("sentinel content = %q; want %q", body, "2\n")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Errorf("writeVersion left a tempfile sibling: %s", e.Name())
		}
	}
}

// TestDurability_AcquirePID_UsesIOXWriteFile is a regression test
// for audit pass-6 finding C2 (#1051). The pre-fix pidfile.go used
// `os.WriteFile` directly, so a concurrent reader could see a
// half-written PID file and treat it as stale, racing a still-alive
// daemon.
func TestDurability_AcquirePID_UsesIOXWriteFile(t *testing.T) {
	data, err := os.ReadFile("pidfile.go")
	if err != nil {
		t.Fatalf("read pidfile.go: %v", err)
	}
	src := string(data)
	if strings.Contains(src, "os.WriteFile(path") {
		t.Error("pidfile.go must NOT call os.WriteFile directly on the PID file; use iox.WriteFile (#1051)")
	}
	if !strings.Contains(src, "iox.WriteFile(path") {
		t.Error("pidfile.go must call iox.WriteFile on the PID file (#1051, RULE-IOX-01)")
	}

	// Behavioural check: AcquirePID writes a valid PID and leaves no
	// `.tmp.` siblings under the state directory.
	dir := t.TempDir()
	release, err := AcquirePID(dir)
	if err != nil {
		t.Fatalf("AcquirePID: %v", err)
	}
	t.Cleanup(release)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Errorf("AcquirePID left a tempfile sibling: %s", e.Name())
		}
	}
}

// TestDurability_LogOpen_DirFsyncOnCreate is a regression test for
// audit pass-6 finding C3 (#1052). Fresh log-file creation goes
// through iox.SyncDir to make the directory entry durable on power
// loss. The behaviour is opaque to userspace; assertions pin (a) the
// source references iox.SyncDir, and (b) the create-then-iterate
// round-trip works in the presence of the syncs.
func TestDurability_LogOpen_DirFsyncOnCreate(t *testing.T) {
	data, err := os.ReadFile("log.go")
	if err != nil {
		t.Fatalf("read log.go: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, "iox.SyncDir") {
		t.Error("log.go must call iox.SyncDir after fresh log file creation (#1052)")
	}

	dir := t.TempDir()
	db := newLogDB(dir, discardLogger())
	defer func() { _ = db.closeAll() }()

	if err := db.Append("test", []byte("first record")); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	if err := db.Append("test", []byte("second record")); err != nil {
		t.Fatalf("second Append: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "test.log")); err != nil {
		t.Fatalf("log file missing after Append: %v", err)
	}
	var got []string
	if err := db.Iterate("test", time.Time{}, func(p []byte) error {
		got = append(got, string(p))
		return nil
	}); err != nil {
		t.Fatalf("Iterate: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Iterate: got %d records, want 2: %v", len(got), got)
	}
}

// TestDurability_LogRotation_DirFsyncOnRotate is a regression test
// for audit pass-6 finding H1 (#1053). After the rename chain in
// rotateLocked, the parent directory MUST be fsync'd once so the
// .N-1 → .N shifts + current → .1 rename are durable as a batch on
// power loss.
func TestDurability_LogRotation_DirFsyncOnRotate(t *testing.T) {
	data, err := os.ReadFile("log.go")
	if err != nil {
		t.Fatalf("read log.go: %v", err)
	}
	src := string(data)
	// The rotation-time sync is identified by the WARN line text;
	// pin the message so a regression that drops the call (or the
	// WARN) fails CI.
	if !strings.Contains(src, "dir-fsync after rotation failed") {
		t.Error("log.go's rotateLocked must dir-fsync after the rename chain (#1053)")
	}

	// Behavioural smoke: rotate succeeds end-to-end with the sync
	// hooked in.
	dir := t.TempDir()
	db := newLogDB(dir, discardLogger())
	defer func() { _ = db.closeAll() }()
	if err := db.SetRotationPolicy("test", RotationPolicy{KeepCount: 3, CompressOld: false}); err != nil {
		t.Fatalf("SetRotationPolicy: %v", err)
	}
	for i := range 5 {
		if err := db.Append("test", []byte(fmt.Sprintf("pre-rot-%d", i))); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := db.Rotate("test"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "test.log.1")); err != nil {
		t.Errorf("rotated file .1 missing: %v", err)
	}
	if err := db.Append("test", []byte("post-rot")); err != nil {
		t.Fatalf("post-rot Append: %v", err)
	}
}

// TestRULE_STATE_07_TransactionPersistFailureSurfacesAndRollsBack
// extends RULE-STATE-07's atomic-commit pinning with the failed-
// commit case from audit pass-6 finding H3 (#1054). When fn returns
// nil but persist fails, the caller MUST receive an error that
// errors.Is matches ErrTransactionPersistFailed, AND the in-memory
// state MUST be rolled back so subsequent Get returns the
// pre-transaction value rather than the orphaned in-memory mutation.
func TestRULE_STATE_07_TransactionPersistFailureSurfacesAndRollsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	db, err := openKV(path, discardLogger())
	if err != nil {
		t.Fatalf("openKV: %v", err)
	}
	// Seed the pre-transaction value.
	if err := db.Set("ns", "k", "before"); err != nil {
		t.Fatalf("Set seed: %v", err)
	}

	// Force persist failure by replacing state.yaml with a
	// directory of the same name. atomicWrite's final
	// `os.Rename(tmp, path)` then fails with EISDIR-equivalent
	// (target is a directory; rename refuses to clobber a non-empty
	// dir with a regular file). The failure exercises the exact
	// "fn-returned-nil-but-persist-failed" branch RULE-STATE-07
	// extends.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(path, "wedge"), 0o755); err != nil {
		t.Fatalf("mkdir path-as-dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })

	txErr := db.WithTransaction(func(tx *KVTx) error {
		tx.Set("ns", "k", "would-have-committed")
		tx.Set("ns", "new", "also-would-have-committed")
		return nil
	})
	if txErr == nil {
		t.Fatal("WithTransaction: expected persist failure, got nil")
	}
	if !errors.Is(txErr, ErrTransactionPersistFailed) {
		t.Errorf("WithTransaction error: got %v; want errors.Is ErrTransactionPersistFailed", txErr)
	}

	// In-memory rollback: the failed-commit attempt must not have
	// advanced db.data ahead of the on-disk file.
	v, ok, _ := db.Get("ns", "k")
	if !ok || v != "before" {
		t.Errorf("Get after failed commit: got %v ok=%v; want \"before\" ok=true", v, ok)
	}
	if _, ok, _ := db.Get("ns", "new"); ok {
		t.Error("Get \"ns/new\" after failed commit: expected ok=false (rolled back)")
	}

	// Restore: remove the directory-shaped placeholder and persist
	// from the rolled-back in-memory state so we can re-open and
	// confirm the on-disk view matches what callers now see.
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("cleanup dir-shaped path: %v", err)
	}
	if err := db.Set("ns", "k", "before"); err != nil {
		t.Fatalf("re-persist after rollback: %v", err)
	}

	db2, err := openKV(path, discardLogger())
	if err != nil {
		t.Fatalf("re-openKV: %v", err)
	}
	v2, ok2, _ := db2.Get("ns", "k")
	if !ok2 || v2 != "before" {
		t.Errorf("Re-openKV Get: got %v ok=%v; want \"before\" ok=true", v2, ok2)
	}
}

// TestKV_Load_CorruptYAMLReturnsErrCorruptState is the regression
// test for audit pass-6 finding H4 (#1055). A corrupt state.yaml
// MUST surface as a typed error (ErrCorruptState) rather than be
// silently downgraded to "empty state, first-boot semantics" — the
// pre-fix behaviour silently destroyed wizard / polarity / smart-
// mode records that drive operator-visible decisions.
//
// The companion "missing file = empty state is fine" case is
// preserved per RULE-STATE-10.
func TestKV_Load_CorruptYAMLReturnsErrCorruptState(t *testing.T) {
	t.Run("corrupt_yaml_returns_ErrCorruptState", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "state.yaml")
		// Write a payload that's syntactically invalid YAML.
		corrupt := []byte("not: valid: yaml: [unbalanced")
		if err := os.WriteFile(path, corrupt, 0o640); err != nil {
			t.Fatalf("write corrupt: %v", err)
		}
		_, err := openKV(path, discardLogger())
		if err == nil {
			t.Fatal("openKV on corrupt YAML: expected error, got nil")
		}
		if !errors.Is(err, ErrCorruptState) {
			t.Errorf("openKV on corrupt YAML: got %v; want errors.Is ErrCorruptState", err)
		}
	})

	t.Run("missing_file_returns_empty_state", func(t *testing.T) {
		// Preservation of RULE-STATE-10's first-boot semantics:
		// a missing state.yaml is a normal first-boot condition,
		// not corruption.
		dir := t.TempDir()
		path := filepath.Join(dir, "state.yaml")
		db, err := openKV(path, discardLogger())
		if err != nil {
			t.Fatalf("openKV on missing file: %v", err)
		}
		_, ok, _ := db.Get("ns", "k")
		if ok {
			t.Error("Get on empty KV: expected ok=false")
		}
	})

	t.Run("valid_yaml_loads_cleanly", func(t *testing.T) {
		// Pin that the change doesn't break the happy path:
		// a valid YAML payload loads without error.
		dir := t.TempDir()
		path := filepath.Join(dir, "state.yaml")
		db, err := openKV(path, discardLogger())
		if err != nil {
			t.Fatalf("openKV fresh: %v", err)
		}
		if err := db.Set("ns", "k", "v"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		// Re-open from disk.
		db2, err := openKV(path, discardLogger())
		if err != nil {
			t.Fatalf("re-openKV on valid YAML: %v", err)
		}
		v, ok, _ := db2.Get("ns", "k")
		if !ok || v != "v" {
			t.Errorf("Get after re-open: got %v ok=%v; want \"v\" ok=true", v, ok)
		}
	})
}
