package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// webDir is the path to the top-level web/ directory relative to this
// package's source directory (internal/web/).
const webDir = "../../web"

// TestUI_NoExternalCDN asserts RULE-UI-01: no http:// or https:// URL literals
// appear in web/**/*.{html,css,js} outside of HTML comments.
// Allowlist: //www.w3.org/ in xmlns attributes.
func TestUI_NoExternalCDN(t *testing.T) {
	t.Helper()
	extURL := regexp.MustCompile(`https?://`)

	err := filepath.WalkDir(webDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".html" && ext != ".css" && ext != ".js" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(webDir, path)
		for i, line := range strings.Split(string(data), "\n") {
			if !extURL.MatchString(line) {
				continue
			}
			// Allowlist: xmlns attributes referencing www.w3.org.
			if strings.Contains(line, "//www.w3.org/") {
				continue
			}
			// Skip HTML comment lines.
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "<!--") {
				continue
			}
			// Skip CSS/JS comment lines (// or *).
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
				continue
			}
			t.Errorf("RULE-UI-01: external URL in %s:%d: %s", rel, i+1, strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", webDir, err)
	}
}

// TestUI_TokenOnlyColors asserts RULE-UI-02: page-specific CSS files (outside
// web/shared/) must not contain literal hex colors or rgb()/hsl()/rgba()
// function calls. Lines that include var() are allowed (opacity composition).
func TestUI_TokenOnlyColors(t *testing.T) {
	t.Helper()
	hexColor := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	colorFn := regexp.MustCompile(`\b(rgb|hsl|hsla|rgba)\s*\(`)

	err := filepath.WalkDir(webDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".css" {
			return nil
		}
		rel, _ := filepath.Rel(webDir, path)
		// Entire shared/ directory is the design system — exempt from this rule.
		if strings.HasPrefix(rel, "shared/") || strings.HasPrefix(rel, "shared\\") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			// Skip comment lines.
			if strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "//") {
				continue
			}
			// Lines using var() are allowed — color composition via variables is OK.
			if strings.Contains(line, "var(") {
				continue
			}
			if hexColor.MatchString(line) {
				t.Errorf("RULE-UI-02: literal hex color in %s:%d: %s", rel, i+1, trimmed)
			}
			if colorFn.MatchString(line) {
				t.Errorf("RULE-UI-02: literal color function in %s:%d: %s", rel, i+1, trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", webDir, err)
	}
}

// TestUI_SidebarConsistency asserts RULE-UI-03: every page in web/*.html that
// contains a sidebar must have sidebar markup matching web/shared/sidebar.html
// after whitespace normalisation.
func TestUI_SidebarConsistency(t *testing.T) {
	t.Helper()
	canonical, err := os.ReadFile(filepath.Join(webDir, "shared", "sidebar.html"))
	if err != nil {
		t.Fatalf("read shared/sidebar.html: %v", err)
	}
	canonicalNorm := normalizeWhitespace(string(canonical))

	pages, err := filepath.Glob(filepath.Join(webDir, "*.html"))
	if err != nil {
		t.Fatalf("glob web/*.html: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("no html pages found in web/")
	}

	for _, page := range pages {
		data, readErr := os.ReadFile(page)
		if readErr != nil {
			t.Errorf("read %s: %v", page, readErr)
			continue
		}
		content := string(data)
		// Pages without a sidebar are skipped (e.g. index.html landing page).
		if !strings.Contains(content, `<aside class="sidebar">`) {
			continue
		}
		extracted := extractSidebar(content)
		if extracted == "" {
			rel, _ := filepath.Rel(webDir, page)
			t.Errorf("RULE-UI-03: %s has <aside class=\"sidebar\"> open tag but sidebar block not extracted", rel)
			continue
		}
		norm := normalizeWhitespace(extracted)
		if norm != canonicalNorm {
			rel, _ := filepath.Rel(webDir, page)
			t.Errorf("RULE-UI-03: sidebar in %s does not match shared/sidebar.html", rel)
		}
	}
}

// extractSidebar returns the content of the first <aside class="sidebar">…</aside>
// block in html, including the tags themselves. Returns "" if not found.
func extractSidebar(html string) string {
	const open = `<aside class="sidebar">`
	const close = `</aside>`
	start := strings.Index(html, open)
	if start == -1 {
		return ""
	}
	end := strings.Index(html[start+len(open):], close)
	if end == -1 {
		return ""
	}
	return html[start : start+len(open)+end+len(close)]
}

// normalizeWhitespace collapses runs of whitespace (spaces, tabs, newlines) to
// a single space and trims leading/trailing space. Used for sidebar comparison.
func normalizeWhitespace(s string) string {
	ws := regexp.MustCompile(`\s+`)
	return strings.TrimSpace(ws.ReplaceAllString(s, " "))
}

// TestUI_CanonFixtureSync asserts RULE-UI-04: web/shared/canon.md exists and
// is non-empty. Full fixture-binding to specific test data lands in PR 2.
func TestUI_CanonFixtureSync(t *testing.T) {
	t.Helper()
	path := filepath.Join(webDir, "shared", "canon.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("RULE-UI-04: canon.md not found at %s: %v", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		t.Errorf("RULE-UI-04: canon.md is empty")
	}
}
