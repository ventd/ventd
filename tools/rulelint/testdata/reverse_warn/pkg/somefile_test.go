package pkg

import "testing"

func TestSafety(t *testing.T) {
	t.Run("clamp_below_min", func(t *testing.T) {
		// claimed
	})
	t.Run("stop_disabled", func(t *testing.T) {
		// unclaimed — should trigger WARN but not ERROR
	})
}
