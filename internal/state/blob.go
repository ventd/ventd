package state

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

const (
	blobMagic      = "VBLB"
	blobHeaderSize = 16
	blobSHA256Size = 32
)

// BlobDB stores opaque binary blobs with a 16-byte header and SHA256 verification.
// Each blob is one file under the models/ directory.
// Reads verify magic, length, and checksum; mismatch returns found=false (RULE-STATE-02).
// Writes use tempfile+rename+fsync (RULE-STATE-01).
type BlobDB struct {
	dir    string
	logger *slog.Logger
}

func newBlobDB(dir string, logger *slog.Logger) *BlobDB {
	return &BlobDB{dir: dir, logger: logger}
}

// Read returns (payload, schemaVersion, found, error).
// found=false means the file is absent or the header/checksum is invalid.
// Consumers treat found=false as "missing state, re-initialise."
func (b *BlobDB) Read(name string) (payload []byte, schemaVersion uint16, found bool, err error) {
	path := filepath.Join(b.dir, name)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("open blob %s: %w", name, err)
	}
	defer f.Close()

	// Header: [0:4] magic  [4:6] schema_version  [6:8] reserved  [8:16] length
	var hdr [blobHeaderSize]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		b.logger.Warn("state: blob: short header, treating as corrupt", "name", name)
		return nil, 0, false, nil
	}
	if string(hdr[0:4]) != blobMagic {
		b.logger.Warn("state: blob: bad magic, treating as corrupt", "name", name,
			"magic", string(hdr[0:4]))
		return nil, 0, false, nil
	}
	sv := binary.BigEndian.Uint16(hdr[4:6])
	length := binary.BigEndian.Uint64(hdr[8:16])

	data := make([]byte, length)
	if _, err := io.ReadFull(f, data); err != nil {
		b.logger.Warn("state: blob: truncated payload, treating as corrupt", "name", name)
		return nil, 0, false, nil
	}

	var sum [blobSHA256Size]byte
	if _, err := io.ReadFull(f, sum[:]); err != nil {
		b.logger.Warn("state: blob: missing checksum, treating as corrupt", "name", name)
		return nil, 0, false, nil
	}
	if expected := sha256.Sum256(data); expected != sum {
		b.logger.Warn("state: blob: SHA256 mismatch, treating as corrupt", "name", name)
		return nil, 0, false, nil
	}

	return data, sv, true, nil
}

// Write atomically writes payload to name with magic+schemaVersion+length+sha256 framing.
func (b *BlobDB) Write(name string, schemaVersion uint16, payload []byte) error {
	if err := os.MkdirAll(b.dir, dirMode); err != nil {
		return fmt.Errorf("blob dir: %w", err)
	}
	sum := sha256.Sum256(payload)

	var hdr [blobHeaderSize]byte
	copy(hdr[0:4], blobMagic)
	binary.BigEndian.PutUint16(hdr[4:6], schemaVersion)
	// hdr[6:8] reserved — zeros
	binary.BigEndian.PutUint64(hdr[8:16], uint64(len(payload)))

	content := make([]byte, 0, blobHeaderSize+len(payload)+blobSHA256Size)
	content = append(content, hdr[:]...)
	content = append(content, payload...)
	content = append(content, sum[:]...)

	return atomicWrite(filepath.Join(b.dir, name), content, fileMode)
}

// Delete removes the named blob. A missing file is not an error.
func (b *BlobDB) Delete(name string) error {
	err := os.Remove(filepath.Join(b.dir, name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
