package experimental

// DiagSnapshot is the JSON-serializable representation of the experimental
// feature state included in diagnostic bundles.
type DiagSnapshot struct {
	Active        []string                    `json:"active"`
	Preconditions map[string]PreconditionJSON `json:"preconditions"`
}

// PreconditionJSON is the wire-format view of a Precondition.
type PreconditionJSON struct {
	Met    bool   `json:"met"`
	Detail string `json:"detail"`
}

// Snapshot builds a DiagSnapshot for inclusion in the diagnostic bundle.
// Active contains the names of currently enabled flags; Preconditions contains
// the stub status for every known flag regardless of whether it is enabled.
func Snapshot(flags Flags) DiagSnapshot {
	prec := make(map[string]PreconditionJSON, len(all))
	for _, name := range all {
		p := Check(name)
		prec[name] = PreconditionJSON(p)
	}
	return DiagSnapshot{
		Active:        flags.Active(),
		Preconditions: prec,
	}
}
