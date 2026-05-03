package web

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ventd/ventd/internal/config"
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

// ─── v0.5.12 #64 / spec issue #809: outbound diag ingest ──────────────

// diagSendResponse is the JSON shape returned by POST /api/v1/diag/send.
// reference is the maintainer-side ID the operator quotes when filing
// an issue; bytes is the post-redaction tarball size; redactor_profile
// confirms the bundle was redacted before transit.
type diagSendResponse struct {
	Reference       string `json:"reference"`
	Bytes           int    `json:"bytes"`
	RedactorProfile string `json:"redactor_profile"`
	URL             string `json:"url"`
}

// uploadHTTPClient is the http.Client used for outbound POSTs to the
// maintainer ingest. Overridable in tests via SetUploadHTTPClient.
// 60s timeout — bundles are typically <2 MB but slow links exist.
var uploadHTTPClient = &http.Client{Timeout: 60 * time.Second}

// SetUploadHTTPClient replaces the package-level uploadHTTPClient.
// Tests use this to inject an httptest.Server-backed client.
func SetUploadHTTPClient(c *http.Client) {
	if c != nil {
		uploadHTTPClient = c
	}
}

// errIngestDisabled is returned when the operator hasn't enabled the
// upstream ingest in config.yaml. The handler maps it to HTTP 412
// Precondition Failed so the UI can show a "enable in Settings"
// message rather than a generic "send failed".
var errIngestDisabled = errors.New("upstream ingest disabled in config")

// handleDiagSend generates a redacted diagnostic bundle and POSTs it
// to the configured maintainer-side ingest endpoint. Per #809 the
// flow is:
//
//  1. Refuse if Diag.UpstreamIngest.Enabled == false (412).
//  2. Refuse if URL is empty or non-https (412).
//  3. Generate the bundle via the existing diag.Generate path
//     (same redactor profile as /api/diag/bundle — never sends an
//     un-redacted tarball).
//  4. Auto-generate a per-installation bearer token if absent;
//     persist it via Save so subsequent POSTs reuse the same key.
//  5. POST the bundle bytes to URL with Authorization: Bearer <token>
//     and a 60s timeout.
//  6. On success, return the maintainer's reference id so the
//     operator can quote it when filing the issue.
//
// Phoenix's HIL feedback context: "diag bundle download today is a
// friction wall — most operators won't email the file". This is the
// daemon-side opt-in alternative; the maintainer-side receiving
// service is a separate spec.
func (s *Server) handleDiagSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	live := s.cfg.Load()
	if live == nil {
		http.Error(w, "no live config", http.StatusInternalServerError)
		return
	}

	if !live.Diag.UpstreamIngest.Enabled || strings.TrimSpace(live.Diag.UpstreamIngest.URL) == "" {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusPreconditionFailed)
		s.writeJSON(r, w, map[string]string{
			"error": errIngestDisabled.Error(),
			"hint":  "enable diag.upstream_ingest.enabled and set diag.upstream_ingest.url in config (or via Settings → Diagnostics)",
		})
		return
	}
	parsed, err := url.Parse(live.Diag.UpstreamIngest.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		http.Error(w, "diag.upstream_ingest.url must be a valid https:// URL", http.StatusPreconditionFailed)
		return
	}

	// Auto-generate token on first send if absent. Persist via Save
	// so subsequent sends reuse it (the maintainer endpoint dedupes
	// on (token, content-hash) so reusing is the correct behaviour).
	token := strings.TrimSpace(live.Diag.UpstreamIngest.Token)
	if token == "" {
		newTok, err := generateIngestToken()
		if err != nil {
			http.Error(w, "generate token: "+err.Error(), http.StatusInternalServerError)
			return
		}
		token = newTok
		// Mutate the live config + persist. Mirrors the
		// confidence/preset PUT pattern so the change survives
		// daemon restart.
		updated := *live
		updated.Diag.UpstreamIngest.Token = token
		if saved, err := config.Save(&updated, s.configPath); err != nil {
			s.logger.Warn("diag/send: token persist failed (continuing with in-memory token)", "err", err)
			s.cfg.Store(&updated)
		} else {
			s.cfg.Store(saved)
		}
	}

	// Generate the bundle. Same redactor profile as /api/diag/bundle
	// — the bundle is identical whether downloaded or sent.
	opts := diag.Options{
		RedactorCfg:  redactor.DefaultConfig(),
		VentdVersion: s.version.Version,
	}
	bundlePath, err := diag.Generate(r.Context(), opts)
	if err != nil {
		s.logger.Warn("diag/send: bundle generate failed", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		s.writeJSON(r, w, map[string]string{"error": err.Error()})
		return
	}

	// Read the bundle bytes (bounded at 50 MB — bundles are typically
	// <2 MB; anything larger is suspicious and would hammer the
	// maintainer endpoint). 50 MB matches diag.OutputDirSpaceCap.
	const maxBundleBytes = 50 * 1024 * 1024
	body, err := readBundleBounded(bundlePath, maxBundleBytes)
	if err != nil {
		s.logger.Warn("diag/send: bundle read failed", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		s.writeJSON(r, w, map[string]string{"error": err.Error()})
		return
	}

	// POST to the ingest. The ingest URL is fixed-in-config; we
	// don't follow redirects (a redirect to http:// would leak the
	// bundle in cleartext).
	postReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, live.Diag.UpstreamIngest.URL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "build request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	postReq.Header.Set("Authorization", "Bearer "+token)
	postReq.Header.Set("Content-Type", "application/gzip")
	postReq.Header.Set("X-Ventd-Version", s.version.Version)
	postReq.Header.Set("X-Ventd-Filename", filepath.Base(bundlePath))

	noFollow := *uploadHTTPClient
	noFollow.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := noFollow.Do(postReq)
	if err != nil {
		s.logger.Warn("diag/send: upload failed", "err", err, "url_host", parsed.Host)
		w.WriteHeader(http.StatusBadGateway)
		s.writeJSON(r, w, map[string]string{"error": "upload failed: " + err.Error()})
		return
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.logger.Warn("diag/send: ingest rejected", "status", resp.StatusCode, "body_preview", truncatePreview(respBody, 200))
		w.WriteHeader(http.StatusBadGateway)
		s.writeJSON(r, w, map[string]string{
			"error":   fmt.Sprintf("ingest returned HTTP %d", resp.StatusCode),
			"preview": truncatePreview(respBody, 200),
		})
		return
	}

	reference := parseIngestReference(respBody)

	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, diagSendResponse{
		Reference:       reference,
		Bytes:           len(body),
		RedactorProfile: opts.RedactorCfg.Profile,
		URL:             live.Diag.UpstreamIngest.URL,
	})
}

// generateIngestToken returns a 32-byte random hex string for the
// per-installation bearer token. The maintainer endpoint dedupes on
// it so reusing across sessions is correct; rotating requires the
// operator to clear it manually.
func generateIngestToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// readBundleBounded reads up to limit bytes from the bundle file.
// Returns ErrTooLarge if the file exceeds limit (the daemon should
// never produce one that big; this is a defence-in-depth check
// against pathological cases).
func readBundleBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("bundle exceeds %d byte cap", limit)
	}
	return body, nil
}

// parseIngestReference accepts either a JSON body of shape
// {"reference":"abc123"} or a plain text reference. Returns "" if
// neither shape matches; the UI handles the absence gracefully
// ("uploaded; no reference returned").
func parseIngestReference(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return ""
	}
	if body[0] == '{' {
		// Cheap JSON probe: look for "reference" key. Avoid pulling
		// encoding/json's overhead since we only need one field.
		idx := bytes.Index(body, []byte(`"reference"`))
		if idx < 0 {
			return ""
		}
		// Find the next quoted value after the colon.
		rest := body[idx:]
		colon := bytes.IndexByte(rest, ':')
		if colon < 0 {
			return ""
		}
		rest = rest[colon+1:]
		// Skip whitespace then expect a quote.
		rest = bytes.TrimLeft(rest, " \t\n\r")
		if len(rest) == 0 || rest[0] != '"' {
			return ""
		}
		end := bytes.IndexByte(rest[1:], '"')
		if end < 0 {
			return ""
		}
		return string(rest[1 : 1+end])
	}
	// Plain text — bound to 64 chars (typical reference length).
	if len(body) > 64 {
		body = body[:64]
	}
	return strings.TrimSpace(string(body))
}

func truncatePreview(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
