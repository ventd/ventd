// Package detection collects diagnostic items from the live system.
// Each collector returns a []Item; the bundle orchestrator writes them
// into the tarball and runs the redactor over each item's content.
//
// All collectors are best-effort: a missing binary or EPERM is recorded
// as a MissingTool entry in the manifest, never a fatal error.
package detection

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

// Item is one file to be written into the diagnostic bundle.
type Item struct {
	// Path is the relative path inside the bundle tarball.
	Path string
	// Content is the raw bytes to write (may be redacted later).
	Content []byte
	// Schema is the NDJSON schema version if applicable, else "".
	Schema string
	// IsSymlink indicates Path should be a symlink pointing to Target.
	IsSymlink bool
	// Target is the symlink target (relative path within the bundle).
	Target string
}

// MissingTool records a tool that was absent or returned EPERM.
type MissingTool struct {
	Name   string
	Reason string
}

// CollectResult is the output of a collector run.
type CollectResult struct {
	Items        []Item
	MissingTools []MissingTool
}

// runCmd executes a command with a 10-second deadline and returns stdout.
// Returns (nil, MissingTool) when the binary is not found or EPERM.
func runCmd(ctx context.Context, name string, args ...string) ([]byte, *MissingTool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err == nil {
		return out, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(out) > 0 {
		return out, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return nil, &MissingTool{Name: name, Reason: "not found"}
	}
	return nil, &MissingTool{Name: name, Reason: err.Error()}
}

// readFile reads a sysfs/proc file, returning nil on error.
func readFile(path string) []byte {
	data, _ := os.ReadFile(path)
	return data
}

// textItem builds a plain-text Item from content, ensuring trailing newline.
func textItem(path string, content []byte) Item {
	content = bytes.TrimRight(content, "\n")
	content = append(content, '\n')
	return Item{Path: path, Content: content}
}

// symlinkItem builds a symlink Item.
func symlinkItem(name, target string) Item {
	return Item{Path: name, IsSymlink: true, Target: target}
}
