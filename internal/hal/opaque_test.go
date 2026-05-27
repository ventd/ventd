package hal

import (
	"errors"
	"strings"
	"testing"
)

type opaqueTestState struct{ Path string }

// TestStateFrom covers the generic Opaque extractor that replaced the nine
// hand-rolled per-backend stateFrom type switches. It pins the value/pointer
// acceptance, the nil-pointer and wrong-type refusals (including the channel
// ID in the message), and the optional validator running on both forms — the
// behaviours the backends relied on before the collapse.
func TestStateFrom(t *testing.T) {
	want := opaqueTestState{Path: "/sys/x"}

	// Value form round-trips.
	if got, err := StateFrom[opaqueTestState](Channel{Opaque: want}, "tb", nil); err != nil || got != want {
		t.Fatalf("value form: got %+v, err %v; want %+v, nil", got, err, want)
	}

	// Pointer form is dereferenced.
	if got, err := StateFrom[opaqueTestState](Channel{Opaque: &want}, "tb", nil); err != nil || got != want {
		t.Fatalf("pointer form: got %+v, err %v; want %+v, nil", got, err, want)
	}

	// Typed-nil pointer is refused with the backend-prefixed message.
	if _, err := StateFrom[opaqueTestState](Channel{Opaque: (*opaqueTestState)(nil)}, "tb", nil); err == nil ||
		!strings.Contains(err.Error(), "tb: nil opaque state") {
		t.Fatalf("nil pointer: err = %v; want contains %q", err, "tb: nil opaque state")
	}

	// Wrong type is refused and names the channel ID + concrete type.
	if _, err := StateFrom[opaqueTestState](Channel{ID: "ch9", Opaque: 42}, "tb", nil); err == nil ||
		!strings.Contains(err.Error(), `tb: channel "ch9" has wrong opaque type int`) {
		t.Fatalf("wrong type: err = %v; want contains channel id + type", err)
	}

	// Validator runs on the value form and its error is returned verbatim.
	validate := func(s opaqueTestState) error {
		if s.Path == "" {
			return errors.New("tb: empty path")
		}
		return nil
	}
	if _, err := StateFrom[opaqueTestState](Channel{Opaque: opaqueTestState{}}, "tb", validate); err == nil ||
		err.Error() != "tb: empty path" {
		t.Fatalf("validator (value): err = %v; want exactly %q", err, "tb: empty path")
	}

	// Validator also runs on the dereferenced pointer form.
	if _, err := StateFrom[opaqueTestState](Channel{Opaque: &opaqueTestState{}}, "tb", validate); err == nil ||
		err.Error() != "tb: empty path" {
		t.Fatalf("validator (pointer): err = %v; want exactly %q", err, "tb: empty path")
	}

	// Validator passes a well-formed state through.
	if _, err := StateFrom[opaqueTestState](Channel{Opaque: want}, "tb", validate); err != nil {
		t.Fatalf("validator pass: unexpected err %v", err)
	}
}
