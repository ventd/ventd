package web

import (
	"io/fs"
	"strings"
	"testing"
)

// TestEmptyStateCopyEmbedded locks the empty-state UI copy into the
// embedded render.js and components.css so a refactor that accidentally
// rips it out fails a test rather than silently shipping a bare dashboard
// at first-boot. Each substring below is part of the user-visible text
// users land on when a section is empty — if these strings change, the
// screenshots attached to the v0.3 Session C PR-2a also need updating.
func TestEmptyStateCopyEmbedded(t *testing.T) {
	render, err := fs.ReadFile(uiFS, "ui-old/scripts/render.js")
	if err != nil {
		t.Fatalf("read render.js: %v", err)
	}
	css, err := fs.ReadFile(uiFS, "ui-old/styles/components.css")
	if err != nil {
		t.Fatalf("read components.css: %v", err)
	}
	index, err := fs.ReadFile(uiFS, "ui-old/index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	type check struct {
		src  []byte
		name string
		want string
	}
	cases := []check{
		{render, "render.js", "No sensors configured yet."},
		{render, "render.js", "Open Hardware Monitor"},
		{render, "render.js", "No fans configured."},
		{render, "render.js", "No curves yet."},
		{render, "render.js", "No hardware devices detected."},
		{render, "render.js", "sudo ventd --probe-modules"},
		{css, "components.css", ".empty-state"},
		{css, "components.css", ".empty-state-hint"},
		{css, "components.css", ".empty-state-btn"},
		// sanity: the index.html still mounts the sensor/fan/curve
		// containers the render funcs populate. If any of these ids
		// go missing, the empty-state branches write to null.
		{index, "index.html", `id="sensor-cards"`},
		{index, "index.html", `id="fan-cards"`},
		{index, "index.html", `id="curve-cards"`},
		{index, "index.html", `id="hw-devices"`},
	}
	for _, tc := range cases {
		t.Run(tc.name+": "+tc.want, func(t *testing.T) {
			if !strings.Contains(string(tc.src), tc.want) {
				t.Errorf("%s missing %q", tc.name, tc.want)
			}
		})
	}
}
