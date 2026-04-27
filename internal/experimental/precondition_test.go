package experimental_test

import (
	"testing"

	"github.com/ventd/ventd/internal/experimental"
)

func TestCheck_ReturnsNotMetForAllKnownFlags(t *testing.T) {
	for _, name := range experimental.All() {
		t.Run(name, func(t *testing.T) {
			p := experimental.Check(name)
			if p.Met {
				t.Errorf("Check(%q).Met = true, want false (precondition should not be met in test env)", name)
			}
			if p.Detail == "" {
				t.Errorf("Check(%q).Detail is empty", name)
			}
		})
	}
}

func TestCheck_UnknownFlagNotMet(t *testing.T) {
	p := experimental.Check("bogus_flag")
	if p.Met {
		t.Error("Check(unknown).Met = true, want false")
	}
}
