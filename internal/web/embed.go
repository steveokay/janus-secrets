// Package web embeds the built React SPA and serves it same-origin from the
// Go binary. It holds no secrets and no business logic — only static assets and
// an SPA fallback so client-side routing owns deep links.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler serves the embedded SPA: a real asset when the request path matches a
// built file, otherwise index.html (so a deep link like /projects/x/configs/y
// loads the shell and React Router takes over). Every response carries a
// restrictive CSP; the app is self-contained so 'self' is sufficient.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time; failure is a build bug
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
				"object-src 'none'; frame-ancestors 'none'; base-uri 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		if p := strings.TrimPrefix(r.URL.Path, "/"); p != "" && p != "index.html" {
			if f, err := sub.Open(p); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		h.Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
