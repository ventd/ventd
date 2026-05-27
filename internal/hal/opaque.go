package hal

import "fmt"

// StateFrom extracts a backend's private Opaque payload from a Channel,
// accepting both the value (S) and pointer (*S) forms backends have
// historically stored (Enumerate may hand out either, and Restore re-reads a
// channel it cached). It collapses nine byte-identical-modulo-prefix type
// switches the backends each hand-rolled.
//
// backend is the error-message prefix the backend used before (e.g.
// "hal/hwmon", "legion") so the surfaced errors are unchanged. validate is an
// optional hook for backends that reject a structurally-present but
// semantically-empty state (e.g. a missing sysfs path); it runs against both
// the value and dereferenced-pointer forms and MUST return a fully-formed,
// backend-prefixed error because StateFrom returns it verbatim. Pass nil when
// any well-typed payload is acceptable.
//
// Callers keep a one-line package-local stateFrom wrapper so existing call
// sites and rule-bound tests are untouched.
func StateFrom[S any](ch Channel, backend string, validate func(S) error) (S, error) {
	var zero S
	switch v := ch.Opaque.(type) {
	case S:
		if validate != nil {
			if err := validate(v); err != nil {
				return zero, err
			}
		}
		return v, nil
	case *S:
		if v == nil {
			return zero, fmt.Errorf("%s: nil opaque state", backend)
		}
		if validate != nil {
			if err := validate(*v); err != nil {
				return zero, err
			}
		}
		return *v, nil
	default:
		return zero, fmt.Errorf("%s: channel %q has wrong opaque type %T", backend, ch.ID, ch.Opaque)
	}
}
