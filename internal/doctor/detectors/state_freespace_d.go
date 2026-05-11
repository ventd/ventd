package detectors

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/iox"
	"github.com/ventd/ventd/internal/recovery"
)

// FreeSpaceCheck is the read-only state-space probe used by
// StateFreeSpaceDetector. Production points at iox.EnsureFreeSpace;
// tests inject wrapped errors to exercise errors.Is behaviour.
type FreeSpaceCheck func(path string, minBytes uint64) error

// StateFreeSpaceDetector surfaces low free space on ventd's state
// filesystem before runtime writes hit the KV gate. It consumes
// iox.ErrInsufficientFreeSpace through errors.Is so the sentinel is a
// live production contract, not only a state-package test hook.
type StateFreeSpaceDetector struct {
	Path     string
	MinBytes uint64
	Check    FreeSpaceCheck
}

// NewStateFreeSpaceDetector constructs a detector. Empty path uses
// /var/lib/ventd. minBytes==0 uses iox.MinFreeBytesForState. nil check
// uses iox.EnsureFreeSpace.
func NewStateFreeSpaceDetector(path string, minBytes uint64, check FreeSpaceCheck) *StateFreeSpaceDetector {
	if path == "" {
		path = "/var/lib/ventd"
	}
	if minBytes == 0 {
		minBytes = iox.MinFreeBytesForState
	}
	if check == nil {
		check = iox.EnsureFreeSpace
	}
	return &StateFreeSpaceDetector{Path: path, MinBytes: minBytes, Check: check}
}

func (d *StateFreeSpaceDetector) Name() string { return "state_free_space" }

func (d *StateFreeSpaceDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	err := d.Check(d.Path, d.MinBytes)
	if err == nil {
		return nil, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		// First boot: state.Open creates the directory before writes.
		return nil, nil
	}

	now := timeNowFromDeps(deps)
	if errors.Is(err, iox.ErrInsufficientFreeSpace) {
		return []doctor.Fact{{
			Detector:   d.Name(),
			Severity:   doctor.SeverityBlocker,
			Class:      recovery.ClassUnknown,
			Title:      "State filesystem is below the free-space floor",
			Detail:     fmt.Sprintf("%v. KV writes refuse before mutating in-memory state when less than %d bytes are available.", err, d.MinBytes),
			EntityHash: doctor.HashEntity("state_free_space", d.Path),
			Observed:   now,
			Journal:    []string{err.Error()},
		}}, nil
	}

	return []doctor.Fact{{
		Detector:   d.Name(),
		Severity:   doctor.SeverityWarning,
		Class:      recovery.ClassUnknown,
		Title:      "Cannot measure free space for state directory",
		Detail:     fmt.Sprintf("%v. Rerun `sudo ventd doctor` if this is a permissions issue.", err),
		EntityHash: doctor.HashEntity("state_free_space_measure", d.Path),
		Observed:   now,
		Journal:    []string{err.Error()},
	}}, nil
}
