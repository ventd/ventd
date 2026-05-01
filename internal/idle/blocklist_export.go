package idle

// MaintenanceBlocklistNames returns the canonical R5 maintenance-class
// process names that idle uses to refuse the calibration gate. v0.5.6's
// signature library re-exports this list as a positive-label dictionary
// (a process whose comm is in this list and dominates the K=4 set
// produces a "maint/<name>" reserved signature label rather than a
// hash-tuple).
//
// The names are returned in arbitrary order and the slice is owned by
// the caller — modifying it does not affect idle's internal map.
func MaintenanceBlocklistNames() []string {
	out := make([]string, 0, len(processBlocklist))
	for name := range processBlocklist {
		out = append(out, name)
	}
	return out
}

// IsMaintenanceProcess reports whether the given comm matches an R5
// blocklist entry. Returns the canonical name (currently identity)
// when matched. Used by the signature library for the maintenance-
// class label override (R7 §Q2).
func IsMaintenanceProcess(comm string) (canonical string, ok bool) {
	if _, exists := processBlocklist[comm]; exists {
		return comm, true
	}
	return "", false
}
