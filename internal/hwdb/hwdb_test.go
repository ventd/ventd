package hwdb

import "testing"

// regresses #308
func TestMatch_VerifiedBeatsUnverifiedShadow(t *testing.T) {
	resetRemote(t)

	// Place the unverified vendor-only profile first so it would win under
	// the old file-order logic (board_name="" is an implicit wildcard that
	// satisfies even the "exact" stage for any board_name).
	remoteMu.Lock()
	remoteDB = []Profile{
		{
			Match:      HardwareFingerprint{BoardVendor: "TESTREGRESS308"},
			Modules:    []string{"unverified_mod"},
			Unverified: true,
		},
		{
			Match:   HardwareFingerprint{BoardVendor: "TESTREGRESS308", BoardName: "TESTREGRESS308 MAG Z790"},
			Modules: []string{"verified_mod"},
		},
	}
	remoteMu.Unlock()

	got, err := Match(HardwareFingerprint{
		BoardVendor: "TESTREGRESS308",
		BoardName:   "TESTREGRESS308 MAG Z790",
	})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if got.Unverified {
		t.Error("unverified profile shadowed verified one — two-pass fix not working")
	}
	if got.Match.BoardName == "" {
		t.Error("got vendor-only profile, want board-specific verified profile")
	}
}
