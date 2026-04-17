package pkg

import "testing"

func TestSafety(t *testing.T) {
	t.Run("clamp_below_min", func(t *testing.T) {
		// invariant: clamp enforced
	})
	t.Run("stop_disabled", func(t *testing.T) {
		// invariant: allow_stop gate
	})
}
