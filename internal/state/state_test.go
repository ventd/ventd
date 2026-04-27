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
)

// discardLogger returns a no-op slog.Logger for test use.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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
