package state

import (
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	logMaxRecordSize       = 64 * 1024 * 1024 // sanity cap: 64 MiB
	logRotationCheckEvery  = 100              // check rotation policy after N appends
	logCompressThresholdMB = 10               // compress rotated file if > 10 MiB
)

// RotationPolicy controls when and how a log file is rotated.
type RotationPolicy struct {
	MaxSizeMB   int  // rotate when file exceeds this size (0 = unlimited)
	MaxAgeDays  int  // rotate when file is older than this (0 = unlimited)
	KeepCount   int  // number of rotated files to retain (0 = use default 5)
	CompressOld bool // gzip-compress rotated files larger than logCompressThresholdMB
}

var defaultRotationPolicy = RotationPolicy{
	MaxSizeMB:   100,
	MaxAgeDays:  30,
	KeepCount:   5,
	CompressOld: true,
}

// LogDB is an append-only log store. Each named log is a separate file under
// the logs/ directory. Records are written with O_APPEND|O_DSYNC for crash
// safety (RULE-STATE-03). Torn and CRC-mismatched records are skipped on read
// (RULE-STATE-04). Rotation preserves all in-flight records (RULE-STATE-08).
type LogDB struct {
	dir      string
	logger   *slog.Logger
	mu       sync.Mutex
	files    map[string]*logHandle
	policies map[string]RotationPolicy
}

type logHandle struct {
	mu       sync.Mutex
	f        *os.File
	logPath  string // full path including .log extension
	size     int64
	openedAt time.Time
	appended int
	policy   RotationPolicy
	logger   *slog.Logger
}

func newLogDB(dir string, logger *slog.Logger) *LogDB {
	return &LogDB{
		dir:      dir,
		logger:   logger,
		files:    make(map[string]*logHandle),
		policies: make(map[string]RotationPolicy),
	}
}

func (db *LogDB) closeAll() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, h := range db.files {
		h.mu.Lock()
		if h.f != nil {
			_ = h.f.Close()
			h.f = nil
		}
		h.mu.Unlock()
	}
	return nil
}

// handle returns (creating if necessary) the logHandle for name.
func (db *LogDB) handle(name string) (*logHandle, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if h, ok := db.files[name]; ok {
		return h, nil
	}
	logPath := filepath.Join(db.dir, name+".log")
	if err := os.MkdirAll(db.dir, dirMode); err != nil {
		return nil, fmt.Errorf("log dir: %w", err)
	}
	// O_APPEND | O_DSYNC — crash-consistent append (RULE-STATE-03)
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_DSYNC, fileMode)
	if err != nil {
		return nil, fmt.Errorf("open log %s: %w", logPath, err)
	}
	var size int64
	if info, statErr := f.Stat(); statErr == nil {
		size = info.Size()
	}
	policy := defaultRotationPolicy
	if p, ok := db.policies[name]; ok {
		policy = p
	}
	h := &logHandle{
		f:        f,
		logPath:  logPath,
		size:     size,
		openedAt: time.Now(),
		policy:   policy,
		logger:   db.logger,
	}
	db.files[name] = h
	return h, nil
}

// Append writes payload as a length-prefixed CRC32-checksummed record.
// Format: [uint32 length][N bytes payload][uint32 CRC32-IEEE(length||payload)]
func (db *LogDB) Append(name string, payload []byte) error {
	h, err := db.handle(name)
	if err != nil {
		return err
	}
	return h.appendRecord(payload)
}

func (h *logHandle) appendRecord(payload []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.f == nil {
		return fmt.Errorf("log file not open")
	}

	length := uint32(len(payload))
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], length)

	crc := crc32.NewIEEE()
	crc.Write(lenBuf[:])
	crc.Write(payload)
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc.Sum32())

	rec := make([]byte, 4+len(payload)+4)
	copy(rec[0:4], lenBuf[:])
	copy(rec[4:], payload)
	copy(rec[4+len(payload):], crcBuf[:])

	n, err := h.f.Write(rec)
	if err != nil {
		return fmt.Errorf("append: %w", err)
	}
	h.size += int64(n)
	h.appended++

	if h.appended%logRotationCheckEvery == 0 && h.needsRotation() {
		if rotErr := h.rotateLocked(); rotErr != nil {
			h.logger.Warn("state: log: auto-rotation failed", "path", h.logPath, "err", rotErr)
		}
	}
	return nil
}

func (h *logHandle) needsRotation() bool {
	p := h.policy
	if p.MaxSizeMB > 0 && h.size > int64(p.MaxSizeMB)*1024*1024 {
		return true
	}
	if p.MaxAgeDays > 0 && time.Since(h.openedAt) > time.Duration(p.MaxAgeDays)*24*time.Hour {
		return true
	}
	return false
}

// Rotate explicitly rotates the named log (RULE-STATE-08).
func (db *LogDB) Rotate(name string) error {
	h, err := db.handle(name)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rotateLocked()
}

// rotateLocked performs the rotation while h.mu is already held.
// Sequence: shift old files → rename current→.1 → open new file.
// The lock prevents any append-after-rename window (RULE-STATE-08).
func (h *logHandle) rotateLocked() error {
	// Close current handle — no more writes to the old file.
	if h.f != nil {
		if err := h.f.Close(); err != nil {
			return fmt.Errorf("close for rotation: %w", err)
		}
		h.f = nil
	}

	if _, err := os.Stat(h.logPath); os.IsNotExist(err) {
		return h.openFileLocked()
	}

	keepCount := h.policy.KeepCount
	if keepCount <= 0 {
		keepCount = 5
	}

	// Delete the oldest that would exceed keepCount.
	oldest := h.logPath + "." + strconv.Itoa(keepCount)
	_ = os.Remove(oldest)
	_ = os.Remove(oldest + ".gz")

	// Shift existing rotated files: .N-1→.N, ..., .1→.2
	for i := keepCount - 1; i >= 1; i-- {
		src := h.logPath + "." + strconv.Itoa(i)
		dst := h.logPath + "." + strconv.Itoa(i+1)
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, dst)
		}
		if _, err := os.Stat(src + ".gz"); err == nil {
			_ = os.Rename(src+".gz", dst+".gz")
		}
	}

	// Atomic rename: current → .1 (no append-after-rename window)
	rotated := h.logPath + ".1"
	if err := os.Rename(h.logPath, rotated); err != nil {
		_ = h.openFileLocked() // best-effort re-open original
		return fmt.Errorf("rotate rename: %w", err)
	}

	// Open the new log file BEFORE releasing the lock.
	if err := h.openFileLocked(); err != nil {
		return err
	}

	// Optionally compress the rotated file in the background.
	if h.policy.CompressOld {
		if info, err := os.Stat(rotated); err == nil && info.Size() > logCompressThresholdMB*1024*1024 {
			go func(path string) {
				if gzErr := compressFile(path); gzErr != nil {
					h.logger.Warn("state: log: compress rotated file failed", "path", path, "err", gzErr)
				}
			}(rotated)
		}
	}

	h.size = 0
	h.openedAt = time.Now()
	return nil
}

func (h *logHandle) openFileLocked() error {
	f, err := os.OpenFile(h.logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_DSYNC, fileMode)
	if err != nil {
		return fmt.Errorf("open log %s: %w", h.logPath, err)
	}
	var size int64
	if info, err := f.Stat(); err == nil {
		size = info.Size()
	}
	h.f = f
	h.size = size
	return nil
}

// SetRotationPolicy configures the rotation policy for the named log.
// Takes effect immediately on any open handle; applied at open time for future handles.
func (db *LogDB) SetRotationPolicy(name string, policy RotationPolicy) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.policies[name] = policy
	if h, ok := db.files[name]; ok {
		h.mu.Lock()
		h.policy = policy
		h.mu.Unlock()
	}
	return nil
}

// Iterate calls fn for each valid record across all log files for name,
// ordered oldest-first. Torn records (length overrun) and CRC-mismatched
// records are skipped silently (RULE-STATE-04). since filters out log files
// whose modification time predates it; zero time means iterate all files.
func (db *LogDB) Iterate(name string, since time.Time, fn func(payload []byte) error) error {
	currentPath := filepath.Join(db.dir, name+".log")

	// Collect rotated files in reverse order (oldest first).
	var rotated []string
	for i := 1; ; i++ {
		plain := currentPath + "." + strconv.Itoa(i)
		gz := plain + ".gz"
		plainOK := fileExists(plain)
		gzOK := fileExists(gz)
		if !plainOK && !gzOK {
			break
		}
		if plainOK {
			rotated = append(rotated, plain)
		} else {
			rotated = append(rotated, gz)
		}
	}
	// Reverse to get oldest (.keepCount) first.
	for i, j := 0, len(rotated)-1; i < j; i, j = i+1, j-1 {
		rotated[i], rotated[j] = rotated[j], rotated[i]
	}
	all := append(rotated, currentPath)

	for _, path := range all {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if !since.IsZero() && info.ModTime().Before(since) {
			continue
		}
		if err := iteratePath(path, fn); err != nil {
			return err
		}
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func iteratePath(path string, fn func([]byte) error) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil // treat unreadable gzip as empty
		}
		defer func() { _ = gz.Close() }()
		return readRecords(gz, fn)
	}
	return readRecords(f, fn)
}

// readRecords reads length-prefixed CRC32 records from r (RULE-STATE-04).
// Torn records (length overrun) cause iteration to stop for this file.
// CRC-mismatched records are skipped and iteration continues.
func readRecords(r io.Reader, fn func([]byte) error) error {
	for {
		var lenBuf [4]byte
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return nil // EOF or torn length prefix — end of readable records
		}
		length := binary.BigEndian.Uint32(lenBuf[:])
		if length > logMaxRecordSize {
			// Implausibly large length → likely corrupt; stop reading this file.
			return nil
		}

		payload := make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			// Torn payload (RULE-STATE-04) — stop reading.
			return nil
		}

		var crcBuf [4]byte
		if _, err := io.ReadFull(r, crcBuf[:]); err != nil {
			// Torn CRC (RULE-STATE-04) — stop reading.
			return nil
		}

		expected := crc32.NewIEEE()
		expected.Write(lenBuf[:])
		expected.Write(payload)
		if binary.BigEndian.Uint32(crcBuf[:]) != expected.Sum32() {
			// CRC mismatch — skip this record and continue (RULE-STATE-04).
			continue
		}

		if err := fn(payload); err != nil {
			return err
		}
	}
}

func compressFile(path string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	dstPath := path + ".gz"
	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(dst)
	if _, err := io.Copy(gz, src); err != nil {
		_ = gz.Close()
		_ = dst.Close()
		_ = os.Remove(dstPath)
		return err
	}
	if err := gz.Close(); err != nil {
		_ = dst.Close()
		_ = os.Remove(dstPath)
		return err
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(dstPath)
		return err
	}
	return os.Remove(path)
}
