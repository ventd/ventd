package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
)

// VersionInfo is the shape emitted by GET /api/version (and the CLI's
// --version --json flag). Fields are stable API: downstream packagers parse
// this to stamp their own package version into the binary, and operators
// shell-script against it. Add fields conservatively.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Go        string `json:"go"`
}

// NewVersionInfo returns a VersionInfo filled with runtime.Version(). Caller
// supplies the three ldflag-populated fields from main().
func NewVersionInfo(version, commit, buildDate string) VersionInfo {
	return VersionInfo{
		Version:   version,
		Commit:    commit,
		BuildDate: buildDate,
		Go:        runtime.Version(),
	}
}

// Print writes either plain text ("ventd <v> (<c>) <d>\n") or single-line
// JSON. Shared between cmd/ventd's --version flag and /api/version handler
// so the two emit identical payloads.
func (v VersionInfo) Print(w io.Writer, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(v)
	}
	_, err := fmt.Fprintf(w, "ventd %s (%s) %s\n", v.Version, v.Commit, v.BuildDate)
	return err
}

// handleVersion GET /api/version — unauthenticated. Returns the same JSON
// payload as `ventd --version --json` so tooling can query a running
// daemon's build without shell access to its host.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, s.version)
}
