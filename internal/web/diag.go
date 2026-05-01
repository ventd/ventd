package web

import (
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ventd/ventd/internal/diag"
	"github.com/ventd/ventd/internal/diag/redactor"
)

// bundleNameRe constrains the filename component of /api/diag/download/<name>
// to the goreleaser-style pattern produced by diag.Generate. This rejects
// path-traversal attempts (../, /, leading dots) at the gate so the file
// server never sees an attacker-supplied path component.
var bundleNameRe = regexp.MustCompile(`^ventd-diag-[A-Za-z0-9_.-]+\.tar\.gz$`)

// diagBundleResponse is the JSON shape returned by POST /api/diag/bundle.
type diagBundleResponse struct {
	Filename    string `json:"filename"`
	DownloadURL string `json:"download_url"`
	Profile     string `json:"redaction_profile"`
}

// handleDiagBundle generates a redacted diagnostic bundle synchronously and
// returns its filename plus a download URL. The wizard's recovery surface
// (calibration error banner) calls this and follows the URL to deliver the
// bundle to the operator without requiring shell access.
func (s *Server) handleDiagBundle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	opts := diag.Options{
		RedactorCfg:  redactor.DefaultConfig(),
		VentdVersion: s.version.Version,
	}
	path, err := diag.Generate(r.Context(), opts)
	if err != nil {
		s.logger.Warn("web: diag.Generate failed", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		s.writeJSON(r, w, map[string]string{"error": err.Error()})
		return
	}
	name := filepath.Base(path)
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, diagBundleResponse{
		Filename:    name,
		DownloadURL: "/api/v1/diag/download/" + name,
		Profile:     opts.RedactorCfg.Profile,
	})
}

// handleDiagDownload streams a previously-generated bundle file. The path's
// trailing component is matched against bundleNameRe so callers cannot escape
// the bundle output directory.
func (s *Server) handleDiagDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Path is one of:
	//   /api/diag/download/<name>
	//   /api/v1/diag/download/<name>
	idx := strings.LastIndex(r.URL.Path, "/diag/download/")
	if idx < 0 {
		http.NotFound(w, r)
		return
	}
	name := r.URL.Path[idx+len("/diag/download/"):]
	if !bundleNameRe.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	dir := diag.ResolveOutputDir("")
	full := filepath.Join(dir, name)
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, full)
}
