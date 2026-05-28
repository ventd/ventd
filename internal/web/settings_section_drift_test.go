package web

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestSettingsSectionDrift_HTMLCSSStayInSync prevents the
// JS-vs-CSS section-allow-list drift that landed both #1398 and
// #1414. The settings page hides every section by default
// (`set-content[data-active-section] > .set-section { display: none; }`)
// and re-shows the active one by id. PR #1398 fixed the JS
// allow-list (which the click handler consulted to decide whether to
// navigate); the CSS rule that actually shows the section is
// hand-maintained alongside, and #1414 was the symptom of those two
// lists drifting — the operator could navigate to Calibration via the
// sidebar but the content pane stayed blank because the CSS lacked
// the matching `[data-active-section="calibration"] #calibration`
// selector.
//
// This test asserts: every `<article class="set-section card" id=...>`
// in settings.html has a matching `[data-active-section="<id>"]`
// selector in settings.css. A future contributor adding a section
// without the CSS rule fails CI here.
func TestSettingsSectionDrift_HTMLCSSStayInSync(t *testing.T) {
	html, err := os.ReadFile("../../web/settings.html")
	if err != nil {
		t.Fatalf("read settings.html: %v", err)
	}
	css, err := os.ReadFile("../../web/settings.css")
	if err != nil {
		t.Fatalf("read settings.css: %v", err)
	}

	// Section IDs declared by the HTML.
	htmlSectionRE := regexp.MustCompile(`<article[^>]*class="[^"]*set-section[^"]*"[^>]*id="([a-z-]+)"`)
	htmlMatches := htmlSectionRE.FindAllStringSubmatch(string(html), -1)
	if len(htmlMatches) == 0 {
		t.Fatalf("no .set-section articles found in settings.html — regex broken or markup changed shape")
	}
	htmlIDs := make([]string, 0, len(htmlMatches))
	for _, m := range htmlMatches {
		htmlIDs = append(htmlIDs, m[1])
	}
	sort.Strings(htmlIDs)

	// Section IDs that CSS makes visible when data-active-section matches.
	cssRE := regexp.MustCompile(`\.set-content\[data-active-section="([a-z-]+)"\]\s+#([a-z-]+)`)
	cssMatches := cssRE.FindAllStringSubmatch(string(css), -1)
	if len(cssMatches) == 0 {
		t.Fatalf("no data-active-section CSS selectors found in settings.css — selector shape changed")
	}
	cssIDs := make([]string, 0, len(cssMatches))
	for _, m := range cssMatches {
		if m[1] != m[2] {
			t.Errorf("CSS selector mismatch: data-active-section=%q targets #%q (the two ids must match)", m[1], m[2])
		}
		cssIDs = append(cssIDs, m[1])
	}
	sort.Strings(cssIDs)

	// Symmetric difference.
	missingFromCSS := diffStringSets(htmlIDs, cssIDs)
	missingFromHTML := diffStringSets(cssIDs, htmlIDs)
	if len(missingFromCSS) > 0 {
		t.Errorf("settings.html sections without matching CSS show-rule: %s — these will render blank when activated (#1414)",
			strings.Join(missingFromCSS, ", "))
	}
	if len(missingFromHTML) > 0 {
		t.Errorf("settings.css show-rules without matching HTML section: %s — dead CSS",
			strings.Join(missingFromHTML, ", "))
	}
}

// diffStringSets returns elements present in a but not in b.
func diffStringSets(a, b []string) []string {
	bSet := make(map[string]struct{}, len(b))
	for _, s := range b {
		bSet[s] = struct{}{}
	}
	out := make([]string, 0)
	for _, s := range a {
		if _, ok := bSet[s]; !ok {
			out = append(out, s)
		}
	}
	return out
}
