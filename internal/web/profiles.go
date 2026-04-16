package web

import (
	"encoding/json"
	"net/http"

	"github.com/ventd/ventd/internal/config"
)

// Profile endpoints — part of the Session C 2e scope. These expose the
// named fan→curve binding sets that land under config.Profiles and let
// the UI switch between Silent / Balanced / Performance (or custom
// sets) without editing every Control row one by one.
//
// The server mutates the in-memory config pointer only. Persisting
// the switch to disk is the operator's explicit Apply flow — same
// discipline used for every other dashboard-level config change, so a
// browsing operator can compare two profiles live and discard the
// switch by reloading without committing to disk.

// profileResponse is the shape returned by GET /api/profile. The
// bindings are nested inside Profile objects so the client can render
// a per-profile preview without a second round-trip.
type profileResponse struct {
	Active   string                    `json:"active"`
	Profiles map[string]config.Profile `json:"profiles"`
}

// handleProfile GET /api/profile returns the active profile name and
// the full map of configured profiles. When no profiles are configured
// (v0.2.x config, or a v0.3 config that hasn't defined any yet), the
// response carries an empty map and an empty active string — the UI
// renders that as "no profile dropdown" so operators unaware of the
// feature see no change.
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	live := s.cfg.Load()
	resp := profileResponse{
		Active:   live.ActiveProfile,
		Profiles: live.Profiles,
	}
	if resp.Profiles == nil {
		resp.Profiles = map[string]config.Profile{}
	}
	s.writeJSON(r, w, resp)
}

// handleProfileActive POST /api/profile/active switches the active
// profile. The handler:
//
//   - Reads {"name": "<profile>"} from the body.
//   - Verifies the profile exists.
//   - Copies the live config (so concurrent readers through cfg.Load
//     keep seeing a coherent snapshot), rewrites Controls[i].Curve for
//     every Control whose Fan appears in the profile's Bindings map,
//     sets ActiveProfile, and atomically stores the new pointer.
//   - Returns the resulting effective bindings so the UI can update its
//     dashboard without a follow-up GET.
//
// Unknown profile → 400. Empty/malformed body → 400. This is the only
// handler that mutates Controls in place outside the Apply flow;
// callers rely on it to be atomic from the reader's perspective, which
// the atomic.Pointer swap gives us.
func (s *Server) handleProfileActive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	live := s.cfg.Load()
	profile, ok := live.Profiles[req.Name]
	if !ok {
		http.Error(w, "unknown profile: "+req.Name, http.StatusBadRequest)
		return
	}

	// Copy the live config so we don't mutate the snapshot other
	// goroutines may be reading. Shallow copy is safe for every field
	// except the slices we're about to rewrite; we deep-copy Controls
	// so the atomic swap hands out the new binding set without anyone
	// observing a half-updated Control{}.
	next := *live
	next.ActiveProfile = req.Name
	next.Controls = make([]config.Control, len(live.Controls))
	copy(next.Controls, live.Controls)
	for i := range next.Controls {
		if curve, hit := profile.Bindings[next.Controls[i].Fan]; hit {
			next.Controls[i].Curve = curve
		}
	}
	s.cfg.Store(&next)

	s.writeJSON(r, w, profileResponse{
		Active:   next.ActiveProfile,
		Profiles: next.Profiles,
	})
}
