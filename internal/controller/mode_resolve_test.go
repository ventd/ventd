package controller

import (
	"testing"

	"github.com/ventd/ventd/internal/hal"
)

// TestResolvedMode pins the config.Fan.PWMMode → hwmon State.ResolvedMode
// mapping the controller uses when building a channel (#759). Empty must
// stay nil so the backend never touches pwm*_mode for the vast majority
// of fans the wizard never healed.
func TestResolvedMode(t *testing.T) {
	if got := resolvedMode(""); got != nil {
		t.Fatalf("resolvedMode(\"\") = %v, want nil", *got)
	}
	if got := resolvedMode("bogus"); got != nil {
		t.Fatalf("resolvedMode(\"bogus\") = %v, want nil", *got)
	}
	if got := resolvedMode("dc"); got == nil || *got != hal.ModeDC {
		t.Fatalf("resolvedMode(\"dc\") = %v, want %d", got, hal.ModeDC)
	}
	if got := resolvedMode("pwm"); got == nil || *got != hal.ModePWM {
		t.Fatalf("resolvedMode(\"pwm\") = %v, want %d", got, hal.ModePWM)
	}
}
