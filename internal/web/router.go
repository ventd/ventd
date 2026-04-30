package web

import (
	"crypto/sha256"
	"fmt"
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

// registerWebPage wires flat-file routes for a single design-system page
// rooted at the new web/ tree:
//
//	/<name>       → web/<name>.html
//	/<name>.css   → web/<name>.css   (optional — skipped if missing)
//	/<name>.js    → web/<name>.js    (optional — skipped if missing)
//
// The HTML is served no-cache so the page is always fresh; the CSS and
// JS pick up a 1h public cache and a content-hashed ETag. All routes
// are unauthenticated — the page itself decides whether to gate content
// via JS once it has consulted /api/v1/auth/state. Pages whose stylesheet
// is borrowed from another page (e.g. login reuses setup.css) can omit
// their own .css and .js without panicking the daemon.
func (s *Server) registerWebPage(name string) {
	html, err := fs.ReadFile(webstatic.FS, name+".html")
	if err != nil {
		panic("web: missing embedded page " + name + ".html: " + err.Error())
	}
	s.mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		_, _ = w.Write(html)
	})
	if css, err := fs.ReadFile(webstatic.FS, name+".css"); err == nil {
		s.mux.HandleFunc("/"+name+".css", staticAssetHandler(css, "text/css; charset=utf-8"))
	}
	if js, err := fs.ReadFile(webstatic.FS, name+".js"); err == nil {
		s.mux.HandleFunc("/"+name+".js", staticAssetHandler(js, "application/javascript; charset=utf-8"))
	}
}

// staticAssetHandler returns a handler that serves a fixed byte slice
// with a content-hash ETag and a 1-hour public cache. ETag is computed
// once at registration time — the bytes are immutable for the lifetime
// of the binary.
func staticAssetHandler(body []byte, contentType string) http.HandlerFunc {
	sum := sha256.Sum256(body)
	etag := fmt.Sprintf(`"%x"`, sum[:16])
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", contentType)
		h.Set("ETag", etag)
		h.Set("Cache-Control", "public, max-age=3600, must-revalidate")
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(body)
	}
}
