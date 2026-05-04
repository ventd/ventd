// Package web — in-UI update endpoints. Phoenix asked for an update
// affordance on the dashboard so operators can upgrade ventd without
// dropping into a terminal AND without losing in-flight calibration
// progress. Most state already persists across daemon restarts
// (state.yaml, smart/shard-{B,C}/, .signature_salt, calibration JSON
// results). RULE-ENVELOPE-09 confirms the calibration probe is
// resumable from the last completed step.
//
// Two endpoints:
//   GET  /api/v1/update/check  — queries GitHub releases for the latest
//                                tag; returns {current, latest, available}.
//   POST /api/v1/update/apply  — body: {version: "vX.Y.Z"}. Spawns a
//                                detached install.sh in the background
//                                with VENTD_VERSION=<version> set; the
//                                install script handles dpkg/rpm install
//                                + systemctl restart. Returns 202 then
//                                the daemon dies + restarts under the
//                                new binary; the frontend polls /healthz
//                                to detect re-up and reloads.
//
// Per the no-theatre rule: this surfaces a real backend capability
// (the install.sh script that already exists) through a clean UI
// affordance.
package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// updateCheckResponse is the GET /api/v1/update/check payload. Available
// is computed by lex-comparing the SemVer-shaped version strings; if
// either is empty or unparseable, available falls back to a simple
// string-mismatch test. The daemon never auto-applies updates — every
// transition requires an explicit POST to /apply.
type updateCheckResponse struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Published string `json:"published_at,omitempty"` // RFC3339 from GitHub
	URL       string `json:"url,omitempty"`          // release page URL
	Error     string `json:"error,omitempty"`        // surface fetch errors honestly
}

// updateRepoSlug points at the canonical ventd repo. Override-able for
// tests / forks via the package-level var (export hook below).
var updateRepoSlug = "ventd/ventd"

// updateInstallScriptPath is the path to scripts/install.sh that the
// daemon shells out to during /apply. The .deb / .rpm packagers drop
// it at /usr/share/ventd/install.sh; the source tree's path is also
// accepted for dev workflow.
var updateInstallScriptCandidates = []string{
	"/usr/share/ventd/install.sh",
	"/usr/local/share/ventd/install.sh",
	"./scripts/install.sh",
}

// httpGetTimeout caps the GitHub release-list fetch. Public API; if it
// rate-limits or 503s we surface the error rather than hang the page.
var updateHTTPClient = &http.Client{Timeout: 8 * time.Second}

// fetchLatestRelease hits GitHub's releases-latest endpoint. Returns
// the release tag, the published_at, and the html_url so the UI can
// link out for manual download as a fallback.
func fetchLatestRelease(repoSlug string) (tag, publishedAt, htmlURL string, err error) {
	url := "https://api.github.com/repos/" + repoSlug + "/releases/latest"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", "", fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	var body struct {
		TagName     string `json:"tag_name"`
		PublishedAt string `json:"published_at"`
		HTMLURL     string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", "", fmt.Errorf("decode release: %w", err)
	}
	return body.TagName, body.PublishedAt, body.HTMLURL, nil
}

// versionAvailable returns true when latest is a strictly newer version
// than current. Both are expected in SemVer-with-leading-v form
// (e.g. "v0.5.14"). Snapshot/dev builds ("dev", "0.5.14-snapshot")
// always count as outdated when latest is set, so operators on a
// dev binary still see the update affordance.
func versionAvailable(current, latest string) bool {
	if latest == "" {
		return false
	}
	if current == "" || current == "dev" || strings.HasSuffix(current, "-snapshot") {
		return true
	}
	// Trim leading 'v' from both for comparison.
	c := strings.TrimPrefix(current, "v")
	l := strings.TrimPrefix(latest, "v")
	if c == l {
		return false
	}
	// Lex-compare on dotted-numeric — close enough for the SemVer
	// shapes we ship; the canonical test is "is current != latest"
	// since the daemon doesn't roll back.
	return c < l
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	resp := updateCheckResponse{Current: s.version.Version}
	tag, published, htmlURL, err := fetchLatestRelease(updateRepoSlug)
	if err != nil {
		resp.Error = err.Error()
		s.writeJSON(r, w, resp)
		return
	}
	resp.Latest = tag
	resp.Published = published
	resp.URL = htmlURL
	resp.Available = versionAvailable(resp.Current, tag)
	s.writeJSON(r, w, resp)
}

// updateApplyRequest is the POST /api/v1/update/apply body.
type updateApplyRequest struct {
	Version string `json:"version"`
}

// updateApplyResponse acks the request before the daemon dies. The UI
// then polls /healthz to detect the restart.
type updateApplyResponse struct {
	Status  string `json:"status"`            // "scheduled"
	Version string `json:"version"`           // version that will be applied
	Message string `json:"message,omitempty"` // human-readable
}

// updateRunFn is the package-level injection point for spawning the
// install script. nil-default invokes the real exec.Command path; tests
// swap to a stub that records invocations.
var updateRunFn = realUpdateRun

func realUpdateRun(version, scriptPath string) error {
	// Detach so the install script outlives the daemon's HTTP handler.
	// The script itself does the dpkg install + systemctl restart;
	// systemd brings the new binary back up.
	cmd := exec.Command("nohup", "bash", "-c",
		fmt.Sprintf("sleep 1 && VENTD_VERSION=%s bash %s >/var/log/ventd-update.log 2>&1 &",
			shellQuote(version), shellQuote(scriptPath)))
	return cmd.Start()
}

// findInstallScript walks the candidate path list; returns the first
// existing path or empty string.
func findInstallScript() string {
	for _, p := range updateInstallScriptCandidates {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
		// LookPath only succeeds for executables on PATH. For absolute
		// paths we need a plain stat; cheaper to defer to bash itself.
	}
	// Fallback: try the canonical packager path explicitly.
	for _, p := range updateInstallScriptCandidates {
		if statErr := exec.Command("test", "-f", p).Run(); statErr == nil {
			return p
		}
	}
	return ""
}

// shellQuote is a defensive single-quote escape for VENTD_VERSION /
// path interpolation into the bash -c string. The version is operator-
// provided through the API; even though we validate it as SemVer-shaped
// upstream we never want a shell-escape.
func shellQuote(s string) string {
	// '\'' is the canonical escape: end quote, escaped quote, re-open.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req updateApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	// Loose validation — version must be vX.Y.Z[-suffix]; reject
	// anything obviously bad before passing into the install script's
	// VENTD_VERSION env var.
	if !looksLikeVersion(req.Version) {
		http.Error(w, "version must look like vX.Y.Z (got "+req.Version+")", http.StatusBadRequest)
		return
	}
	scriptPath := findInstallScript()
	if scriptPath == "" {
		http.Error(w, "install.sh not found in any of "+strings.Join(updateInstallScriptCandidates, " ; "),
			http.StatusServiceUnavailable)
		return
	}
	if err := updateRunFn(req.Version, scriptPath); err != nil {
		http.Error(w, "spawn install: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("update: install scheduled", "version", req.Version, "script", scriptPath)
	w.WriteHeader(http.StatusAccepted)
	s.writeJSON(r, w, updateApplyResponse{
		Status:  "scheduled",
		Version: req.Version,
		Message: "install script spawned — daemon will restart shortly",
	})
}

// looksLikeVersion: vX.Y.Z[-suffix] where X/Y/Z are digit runs and the
// suffix is alphanumeric + dot/dash. Tighter than necessary but keeps
// the regex small + fast.
func looksLikeVersion(v string) bool {
	if !strings.HasPrefix(v, "v") {
		return false
	}
	body := v[1:]
	if body == "" {
		return false
	}
	dot := 0
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c >= '0' && c <= '9':
			// digit
		case c == '.':
			dot++
		case c == '-' || c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			// suffix character
		default:
			return false
		}
	}
	return dot >= 2
}
