package signature

import "github.com/ventd/ventd/internal/idle"

// MaintenanceBlocklist re-exports R5's maintenance-class process list
// as a positive-label dictionary. R7 §Q2 (B): when an R5 blocklist
// process dominates the K=4 set, the signature label is overridden
// to "maint/<canonical-name>" rather than the hash-tuple.
//
// The single source of truth is internal/idle/blocklist_export.go;
// this thin shim re-exports the read API for the signature package
// to avoid an import cycle in the other direction. RULE-SIG-LIB-06.
type MaintenanceBlocklist struct{}

// NewMaintenanceBlocklist returns a blocklist instance. The
// underlying R5 list is package-global; this type is for
// dependency-injection symmetry, not state isolation.
func NewMaintenanceBlocklist() *MaintenanceBlocklist {
	return &MaintenanceBlocklist{}
}

// IsMaintenance reports whether comm is one of the R5 maintenance-
// class names. Returns the canonical name (currently identity)
// when matched.
func (b *MaintenanceBlocklist) IsMaintenance(comm string) (canonical string, ok bool) {
	return idle.IsMaintenanceProcess(comm)
}

// MaintLabel renders the reserved label for a canonical name —
// "maint/rsync", "maint/plex-transcoder", etc.
func MaintLabel(canonical string) string {
	return MaintLabelPrefix + canonical
}
