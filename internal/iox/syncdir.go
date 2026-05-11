package iox

import "os"

// SyncDir fsyncs the given directory so that recent metadata-only
// mutations (file create, rename, unlink) are durable on power loss.
// Consumer SSDs on filesystems mounted without explicit barriers can
// batch directory entries; a fsync of the directory itself forces the
// batched metadata writes through.
//
// This is the "step 5" of the canonical WriteFile sequence
// (tempfile + write + fsync + rename + dir-fsync) exposed as a
// standalone primitive for callers that need durability on operations
// that aren't a simple file-replace — log-file creation, log
// rotation's rename chain, PID-file writes etc.
//
// Errors are returned to the caller but are usually safe to log-and-
// continue: the underlying metadata mutation has already committed in
// the page cache; a fsync failure is a latent durability cost, not a
// correctness regression. Callers that need stricter semantics can
// inspect the returned error.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
