package web

import (
	"net/http"
)

// handleHwmonDebug GET /api/debug/hwmon
//
// Read-only telemetry that returns the before/after snapshot of the
// most recent rescan plus the current view. Until the first rescan
// fires, before/after are null and current is populated lazily from a
// fresh enumeration so an operator inspecting the endpoint on a
// healthy daemon still sees useful data.
//
// The response shape is a debug surface — keep it stable enough for
// the UI rescan toast and hwdiag scripts to depend on, but expect it
// to grow fields over time.
func (s *Server) handleHwmonDebug(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.rescan.mu.Lock()
	lastAt := s.rescan.lastAt
	trigger := s.rescan.trigger
	before := s.rescan.before
	after := s.rescan.after
	current := s.rescan.current
	s.rescan.mu.Unlock()

	if current == nil {
		// Lazy-populate so the first GET (before any rescan has fired)
		// still shows the live topology instead of a misleading empty
		// list. before/after stay nil so consumers can tell rescan has
		// never run.
		current = toDebugDevices(enumerateForRescan())
		s.rescan.mu.Lock()
		if s.rescan.current == nil {
			s.rescan.current = current
		}
		s.rescan.mu.Unlock()
	}

	resp := map[string]interface{}{
		"last_rescan_at": nil,
		"rescan_trigger": trigger,
		"before":         before,
		"after":          after,
		"current":        current,
	}
	if !lastAt.IsZero() {
		resp["last_rescan_at"] = lastAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	s.writeJSON(r, w, resp)
}
