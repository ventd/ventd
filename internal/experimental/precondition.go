package experimental

// Precondition describes whether an experimental feature's prerequisites are met.
type Precondition struct {
	Met    bool
	Detail string
}

// Check returns the precondition status for a named experimental flag.
// All preconditions are stubs in this release and return Met=false with a
// placeholder message. Real checks are wired in per-feature PRs.
func Check(flag string) Precondition {
	return Precondition{
		Met:    false,
		Detail: "precondition check not yet implemented",
	}
}
