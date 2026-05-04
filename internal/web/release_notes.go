// Package web — release-notes endpoint. The in-UI Update button
// (#934) rolls the daemon forward to a new tag without an operator
// terminal session. After the daemon comes back under the new
// binary, the operator's first page load triggers a patch-notes
// modal that surfaces the CHANGELOG section(s) for everything
// since their last-seen version.
//
// Source-of-truth is /usr/share/doc/ventd/CHANGELOG.md (shipped
// by .goreleaser.yml's nfpms.contents). For dev builds we fall
// back to the source tree's ./CHANGELOG.md. The parser splits the
// file on `## [vX.Y.Z]` headings and serves a slice of sections.
//
// Endpoint: GET /api/v1/release-notes?since=vX.Y.Z
//   - since absent → return ONLY the current version's section
//   - since present → return every section newer than `since` up
//     to and including the current version
//   - current version derived from s.version.Version (set at
//     daemon start via SetVersionInfo)
//
// Privacy: the CHANGELOG is operator-readable repo content; no
// secrets in there. Endpoint is auth=true so an unauthenticated
// scrape is refused, but every authenticated operator already has
// access to the same content via /api/v1/version + GitHub.
package web

import (
	_ "embed"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
)

// changelogEmbedded is the canonical CHANGELOG.md baked into the
// daemon binary at build time. Used as a last-resort fallback when
// no on-disk copy exists in any candidate path — covers operators
// who installed via the curl-pipe-bash path (which only extracts
// the binary from the .tar.gz, not auxiliary files into /usr/share/).
//
//go:embed CHANGELOG.md.embedded
var changelogEmbedded []byte

// releaseNotesCandidates is the list of paths the daemon checks for
// CHANGELOG.md, in priority order. The .deb / .rpm packagers drop it
// at /usr/share/doc/ventd/; dev builds find it at ./CHANGELOG.md.
var releaseNotesCandidates = []string{
	"/usr/share/doc/ventd/CHANGELOG.md",
	"/usr/local/share/doc/ventd/CHANGELOG.md",
	"./CHANGELOG.md",
}

type releaseNotesSection struct {
	Version  string `json:"version"`        // e.g. "v0.5.16"
	Date     string `json:"date,omitempty"` // ISO-ish from the heading
	Markdown string `json:"markdown"`       // full section body, original markdown
}

type releaseNotesResponse struct {
	Current  string                `json:"current"`         // daemon's running version
	Since    string                `json:"since,omitempty"` // echoed from the query param
	Sections []releaseNotesSection `json:"sections"`        // newest-first
	Error    string                `json:"error,omitempty"` // populated when CHANGELOG can't be read
}

// changelogCache memoises the parsed CHANGELOG so we don't re-read +
// re-regex on every poll. Invalidated on daemon restart only — the
// CHANGELOG.md content is only updated by an install + restart cycle,
// so the cache lifetime matches exactly.
var (
	changelogCacheMu       sync.Mutex
	changelogCacheSections []releaseNotesSection
	changelogCacheErr      string
)

// changelogHeadingRE matches `## [v0.5.16] - 2026-05-04` or
// `## [vX.Y.Z]` (date optional).
var changelogHeadingRE = regexp.MustCompile(`^## \[(v[0-9][0-9.A-Za-z._-]*)\]\s*(?:-\s*(.+))?$`)

// loadChangelog reads + parses the first existing candidate and
// caches the result. Subsequent calls are O(1).
func loadChangelog() ([]releaseNotesSection, string) {
	changelogCacheMu.Lock()
	defer changelogCacheMu.Unlock()
	if changelogCacheSections != nil || changelogCacheErr != "" {
		return changelogCacheSections, changelogCacheErr
	}
	var data []byte
	for _, p := range releaseNotesCandidates {
		b, err := os.ReadFile(p)
		if err == nil {
			data = b
			break
		}
	}
	if data == nil && len(changelogEmbedded) > 0 {
		// Embed bootstrap: the curl-pipe-bash install path only
		// extracts the binary, not auxiliary docs. Without this
		// fallback the patch-notes modal silently no-ops on every
		// such install. The embedded copy is fixed at build time
		// to the project's CHANGELOG.md as of the build commit, so
		// operators always see the section for the version they
		// just rolled forward to.
		data = changelogEmbedded
	}
	if data == nil {
		changelogCacheErr = "CHANGELOG.md not found in any of " + strings.Join(releaseNotesCandidates, " | ")
		return nil, changelogCacheErr
	}
	changelogCacheSections = parseChangelog(string(data))
	return changelogCacheSections, ""
}

// parseChangelog walks the CHANGELOG line by line, splitting on
// `## [vX.Y.Z]` headings. Anything before the first heading is
// front-matter and gets dropped. Sections are returned newest-first
// (the order they appear in the file — newer at top per Keep-a-
// Changelog convention).
func parseChangelog(s string) []releaseNotesSection {
	var sections []releaseNotesSection
	var cur *releaseNotesSection
	var buf strings.Builder
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		m := changelogHeadingRE.FindStringSubmatch(line)
		if m != nil {
			// Flush previous section, if any.
			if cur != nil {
				cur.Markdown = strings.TrimRight(buf.String(), "\n") + "\n"
				sections = append(sections, *cur)
				buf.Reset()
			}
			cur = &releaseNotesSection{Version: m[1], Date: strings.TrimSpace(m[2])}
			continue
		}
		if cur != nil {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	if cur != nil {
		cur.Markdown = strings.TrimRight(buf.String(), "\n") + "\n"
		sections = append(sections, *cur)
	}
	return sections
}

func (s *Server) handleReleaseNotes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	sections, errStr := loadChangelog()
	resp := releaseNotesResponse{Current: s.version.Version, Sections: []releaseNotesSection{}}
	if errStr != "" {
		resp.Error = errStr
		s.writeJSON(r, w, resp)
		return
	}

	since := r.URL.Query().Get("since")
	resp.Since = since

	current := normalizeVersion(s.version.Version)
	sinceNorm := normalizeVersion(since)

	for _, sec := range sections {
		secNorm := normalizeVersion(sec.Version)
		// Always include the current version's section. For sections
		// older than current: include only when newer than `since`.
		if secNorm == current {
			resp.Sections = append(resp.Sections, sec)
			continue
		}
		if since == "" {
			continue
		}
		if versionGreater(secNorm, sinceNorm) && !versionGreater(secNorm, current) {
			resp.Sections = append(resp.Sections, sec)
		}
	}
	s.writeJSON(r, w, resp)
}

// normalizeVersion strips a leading 'v' so "v0.5.16" and "0.5.16"
// compare equal in versionGreater.
func normalizeVersion(v string) string {
	if v == "" {
		return ""
	}
	if v[0] == 'v' {
		return v[1:]
	}
	return v
}

// versionGreater reports whether a > b under dotted-numeric semantics.
// Both arguments are assumed to be normalised (no 'v' prefix). Empty
// b means "any non-empty a is greater". Tolerant of trailing
// suffixes (e.g. "0.5.16-snapshot") — strips them before compare.
func versionGreater(a, b string) bool {
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	aP := splitNumeric(a)
	bP := splitNumeric(b)
	for i := 0; i < len(aP) || i < len(bP); i++ {
		var ai, bi int
		if i < len(aP) {
			ai = aP[i]
		}
		if i < len(bP) {
			bi = bP[i]
		}
		if ai != bi {
			return ai > bi
		}
	}
	return false
}

func splitNumeric(v string) []int {
	// Drop anything past the first non-digit/non-dot character.
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c < '0' || c > '9') && c != '.' {
			v = v[:i]
			break
		}
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out = append(out, n)
	}
	return out
}
