package web

import (
	"embed"
	"io/fs"
)

// uiFS holds the static UI assets — HTML documents, CSS stylesheets, and
// JavaScript bundles — served at runtime. Keeping them as real files on
// disk (rather than the prior multi-kilobyte string literals) is what lets
// the server-sent CSP be honest: `script-src 'self'` now covers every
// script the daemon serves, because there are no inline <script> blocks
// left in the HTML. Session B owns any further structural reorganization
// of this tree (see the ventd UI overhaul phase plan).
//
//go:embed ui/index.html ui/login.html ui/styles/*.css ui/scripts/*.js ui/icons/*.svg
var uiFS embed.FS

// uiSubFS returns the embedded tree rooted at `ui/` so callers can mount
// it directly under `/ui/` without a StripPrefix dance.
func uiSubFS() fs.FS {
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		// fs.Sub only fails if the named directory is absent from the
		// embed root. That would be a build-time bug (missing //go:embed
		// entry), not a runtime condition we can recover from.
		panic("web: ui embed missing 'ui' subdirectory: " + err.Error())
	}
	return sub
}

// readUI returns the bytes of a file inside the embedded ui/ tree. The
// path must be relative (e.g. "index.html"). Intended for the one-shot
// HTML responses served at `/` and `/login`, where mounting a FileServer
// would require exposing the document at an extra path.
func readUI(name string) []byte {
	b, err := fs.ReadFile(uiFS, "ui/"+name)
	if err != nil {
		// Same reasoning as uiSubFS: a missing file at this layer is a
		// build error. Returning an empty body would just hide the bug.
		panic("web: embedded ui asset missing: " + name + ": " + err.Error())
	}
	return b
}
