package web

import (
	"io/fs"
	"net/http"

	webstatic "github.com/ventd/ventd/web"
)

// sharedSubFS returns the embedded shared/ design-system tree rooted so
// callers can mount it directly under /shared/ via http.StripPrefix.
func sharedSubFS() fs.FS {
	sub, err := fs.Sub(webstatic.FS, "shared")
	if err != nil {
		panic("web: shared embed missing 'shared' subdirectory: " + err.Error())
	}
	return sub
}

// registerSharedAssets wires the /shared/ static route.
//
// sidebar.html and canon.md are test fixtures — they are not embedded in
// webstatic.FS so the FileServer returns 404 for them naturally.
// CSS and JS assets receive Cache-Control: public, max-age=3600 via the
// shared uiStaticHandler (ETags computed from content at boot).
func (s *Server) registerSharedAssets() {
	s.mux.Handle("/shared/", http.StripPrefix("/shared/", uiStaticHandler(sharedSubFS())))
}

// readNewIndex reads the new design-system index.html from the embedded FS.
func readNewIndex() []byte {
	b, err := webstatic.FS.ReadFile("index.html")
	if err != nil {
		panic("web: embedded web/index.html missing: " + err.Error())
	}
	return b
}
