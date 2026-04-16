package main

import (
	"io"

	"github.com/ventd/ventd/internal/web"
)

// printVersion writes the build metadata populated by -ldflags -X. When
// asJSON is true it emits a single-line JSON object whose shape matches
// GET /api/version; otherwise the plain-text form "ventd <v> (<c>) <d>\n".
// Extracted so TestVersionFlag can exercise both shapes without re-exec.
func printVersion(w io.Writer, asJSON bool) error {
	return web.NewVersionInfo(version, commit, buildDate).Print(w, asJSON)
}
