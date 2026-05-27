package web

import (
	"net/http"

	"github.com/ventd/ventd/internal/platformprofile"
)

// PlatformProfileResponse is the GET /api/v1/platform-profile payload.
//
// Snapshot: kernel-exposed current/available profiles + sysfs path.
// (No client-set knobs — ventd actively drives the profile based on
// hardware capabilities + live thermal/load inputs; manual sysfs writes
// are detected and respected via a 10-minute back-off but there is no
// REST surface for setting the profile. Per the zero-config-smart design
// philosophy, ventd figures out the right envelope on its own.)
type PlatformProfileResponse struct {
	*platformprofile.Snapshot
}

// handlePlatformProfile GET /api/v1/platform-profile — authenticated.
// Returns a JSON Snapshot describing the kernel-exposed platform_profile
// interface: available modes, current mode, sysfs path, and a Present flag
// indicating whether the interface exists on this hardware. On hardware
// without the interface, returns 200 with {"present": false} so the UI
// can hide the platform-profile widget cleanly.
//
// Read-only: no PUT/POST defined. ventd drives the profile selection;
// operators who want to override use the standard kernel interface
// (echo into /sys/class/platform-profile/.../profile), which ventd's
// controller detects and respects via back-off.
func (s *Server) handlePlatformProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	snap, err := platformprofile.Read()
	if err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "platform-profile read failed: "+err.Error())
		return
	}
	s.writeJSON(r, w, PlatformProfileResponse{Snapshot: snap})
}
