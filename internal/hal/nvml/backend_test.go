package nvml

import (
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/hal"
)

// TestRegression_Issue380_RestoreParseIndexError verifies that Restore
// propagates a parseIndex failure instead of swallowing it with a nil return.
// regresses #380
func TestRegression_Issue380_RestoreParseIndexError(t *testing.T) {
	b := NewBackend(nil)
	ch := hal.Channel{
		ID:     "abc",
		Opaque: State{Index: "abc"},
	}

	err := b.Restore(ch)
	if err == nil {
		t.Fatal("Restore: want non-nil error for invalid gpu index, got nil")
	}

	// Error must use the same sentinel wrap style as Read() / Write().
	const sentinel = "hal/nvml: parse gpu index"
	if !strings.Contains(err.Error(), sentinel) {
		t.Fatalf("Restore: error %q does not contain sentinel %q", err.Error(), sentinel)
	}

	// Confirm Read returns the identical sentinel so both paths stay in sync.
	_, readErr := b.Read(ch)
	if readErr == nil {
		t.Fatal("Read: want non-nil error for invalid gpu index, got nil")
	}
	if !strings.Contains(readErr.Error(), sentinel) {
		t.Fatalf("Read: error %q does not contain sentinel %q", readErr.Error(), sentinel)
	}
}
