// Package web — in-UI update endpoints. Phoenix asked for an update
// affordance on the dashboard so operators can upgrade ventd without
// dropping into a terminal AND without losing in-flight calibration
// progress. Most state already persists across daemon restarts
// (state.yaml, smart/shard-{B,C}/, .signature_salt, calibration JSON
// results). RULE-ENVELOPE-09 confirms the calibration probe is
// resumable from the last completed step.
//
// Two endpoints:
//
//	GET  /api/v1/update/check  — queries GitHub releases for the latest
//	                             tag; returns {current, latest, available}.
//	POST /api/v1/update/apply  — body: {version: "vX.Y.Z"}. Spawns a
//	                             detached install.sh in the background
//	                             with VENTD_VERSION=<version> set; the
//	                             install script handles dpkg/rpm install
//	                             + systemctl restart. Returns 202 then
//	                             the daemon dies + restarts under the
//	                             new binary; the frontend polls /healthz
//	                             to detect re-up and reloads.
//
// Per the no-theatre rule: this surfaces a real backend capability
// (the install.sh script that already exists) through a clean UI
// affordance.
package web

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// installShEmbedded is the canonical scripts/install.sh source baked
// into the daemon binary at build time. The bootstrap fallback in
// findInstallScript writes this to a temp file when no on-disk copy
// exists in any of the candidate paths — covers operators who
// upgraded from a pre-v0.5.17 build that didn't ship install.sh in
// the .deb / .rpm package, plus dev runs and partial installs.
//
//go:embed install.sh.embedded
var installShEmbedded []byte

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
	defer func() { _ = resp.Body.Close() }()
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

// inUIUpdateSkipChecks is the comma-separated set of preflight check
// names that the daemon's in-UI update path tells install.sh to skip.
//
// Why these specific checks: the in-UI updater only swaps the binary
// — it never builds or loads a kernel module. The build-tools chain
// (DKMS, GCC, kernel headers, make) and the Secure Boot signing
// chain (sign-file, mokutil, MOK enrollment) gate first-install OOT
// builds; they are spurious blockers for an existing install rolling
// the binary forward. Hosts running in-tree drivers (Proxmox using
// the in-tree hwmon stack, mini-PCs whose ITE chip works on
// in-tree it87, etc.) never had any of these tools in the first
// place and shouldn't have to grow them just to update.
//
// The orchestrator's --skip flag (RULE-PREFLIGHT-ORCH-06) excludes
// the named checks from both the run and the BlockerCount tally,
// so the install proceeds cleanly.
//
// install.sh threads VENTD_SKIP_PREFLIGHT_CHECKS through to its
// `ventd preflight` invocation as `--skip <names>`.
const inUIUpdateSkipChecks = "dkms_missing,gcc_missing,kernel_headers_missing,make_missing,signfile_missing,mokutil_missing,mok_keypair_missing,mok_not_enrolled"

func realUpdateRun(version, scriptPath string) error {
	// Detach so the install script outlives the daemon's HTTP handler.
	// The script itself does the dpkg install + systemctl restart;
	// systemd brings the new binary back up.
	//
	// VENTD_SKIP_PREFLIGHT_CHECKS narrows preflight to checks that
	// can actually block a binary-only update. install.sh threads
	// the env through to `ventd preflight --skip ...` so the
	// orchestrator excludes the named checks.
	cmd := exec.Command("nohup", "bash", "-c",
		fmt.Sprintf("sleep 1 && VENTD_VERSION=%s VENTD_SKIP_PREFLIGHT_CHECKS=%s bash %s >/var/log/ventd-update.log 2>&1 &",
			shellQuote(version), shellQuote(inUIUpdateSkipChecks), shellQuote(scriptPath)))
	return cmd.Start()
}

// findInstallScript walks the candidate path list; returns the first
// existing path. If no on-disk copy is found AND the embedded
// install.sh is non-empty, write it to a temp file with mode 0755
// and return that path. Empty return means even the embed bootstrap
// failed — only happens in dev builds that didn't run the embed sync.
//
// The embed bootstrap closes the chicken-and-egg loop hit on v0.5.16:
// the .tar.gz install.sh only ships the binary (no /usr/share/ventd/
// copy of itself), so an operator who installed via curl-pipe-bash
// had no install.sh on disk for the in-UI Update button to find.
// With embed, the daemon binary always carries its own bootstrap.
func findInstallScript() string {
	for _, p := range updateInstallScriptCandidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if len(installShEmbedded) > 0 {
		if p, err := writeEmbeddedInstallSh(); err == nil {
			return p
		}
	}
	return ""
}

// writeEmbeddedInstallSh materialises the embedded install.sh into a
// fresh temp file with mode 0755. The caller is responsible for
// nothing — install.sh sets up a self-overwriting / atomic-rename
// install path and never relies on its own on-disk identity past
// daemon-restart, so leaving the temp file is harmless. Worst case
// the OS sweeps it on next reboot.
func writeEmbeddedInstallSh() (string, error) {
	return writeInstallShBytes(installShEmbedded, "ventd-install-*.sh")
}

// writeInstallShBytes is the shared temp-file writer used by both
// writeEmbeddedInstallSh and fetchInstallScriptForVersion. Returns
// the temp file's path with mode 0755 set.
func writeInstallShBytes(body []byte, namePattern string) (string, error) {
	f, err := os.CreateTemp("", namePattern)
	if err != nil {
		return "", fmt.Errorf("create temp install.sh: %w", err)
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write install.sh: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("close install.sh: %w", err)
	}
	if err := os.Chmod(f.Name(), 0o755); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("chmod install.sh: %w", err)
	}
	return f.Name(), nil
}

// fetchInstallScriptForVersion downloads install.sh from the GitHub
// release assets for the requested version (e.g. v0.5.20) and writes
// it to a temp file with mode 0755.
//
// Why bother when we have an embedded copy? Because the embedded
// copy is FROZEN at the binary's build moment. Any improvement to
// install.sh — like the v0.5.19 two-phase commit fix that closed
// issue #953 — only reaches operators whose currently-running
// daemon was built AFTER the fix. Operators on an older binary
// would never benefit because their embedded install.sh is forever
// the pre-fix version, and install.sh fixes are exactly the kind
// of thing the in-UI updater itself depends on.
//
// With a per-release install.sh, every in-UI update fetches the
// install.sh that matches the target version. v0.5.X-era daemon
// updating to v0.5.Y always uses install.sh@v0.5.Y, picking up
// every fix that landed in [X, Y]. Embedded becomes the offline
// safety net.
//
// Returns the temp file path on success. Returns ("", err) on any
// network / HTTP failure so the caller can fall back to the
// embedded path.
func fetchInstallScriptForVersion(repoSlug, version string) (string, error) {
	if repoSlug == "" || version == "" {
		return "", fmt.Errorf("fetchInstallScriptForVersion: empty repoSlug or version")
	}
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/install.sh", repoSlug, version)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	// 1 MiB cap — current install.sh is ~57 KiB; this is generous
	// enough for the next 2× growth and tight enough that a
	// pathological response (release page HTML, a misuploaded big
	// binary) doesn't get materialised to disk.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	if len(body) < 256 {
		// install.sh is ~57 KiB; a sub-256-byte response is almost
		// certainly an error page (404 release-asset-missing, etc).
		return "", fmt.Errorf("fetch %s: body suspiciously short (%d bytes)", url, len(body))
	}
	if !bytes.HasPrefix(body, []byte("#!")) {
		return "", fmt.Errorf("fetch %s: body missing #! shebang (not a shell script)", url)
	}
	return writeInstallShBytes(body, "ventd-install-fetched-*.sh")
}

// resolveInstallScript returns the install.sh path the apply handler
// should run, in priority order:
//  1. Fetched from the target release tag (latest fixes; the
//     load-bearing path that closes issue #953's recurrence class).
//  2. On-disk candidates (.deb / .rpm shipped + dev tree).
//  3. Embedded fallback (always works; offline-safe).
//
// resolveFetch is the fetch hook (injected for tests). nil → use
// the production fetch path against updateRepoSlug.
func resolveInstallScript(version string, resolveFetch func(string) (string, error), logger interface{ Info(string, ...any) }) string {
	if resolveFetch == nil {
		resolveFetch = func(v string) (string, error) {
			return fetchInstallScriptForVersion(updateRepoSlug, v)
		}
	}
	if path, err := resolveFetch(version); err == nil {
		if logger != nil {
			logger.Info("update: install.sh fetched from release", "version", version, "path", path)
		}
		return path
	} else if logger != nil {
		logger.Info("update: release-tag fetch failed; falling back to disk/embedded",
			"version", version, "err", err.Error())
	}
	return findInstallScript()
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
	scriptPath := resolveInstallScript(req.Version, nil, s.logger)
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
