// Harness console assets (S2/S5).
//
// Embedded UNCONDITIONALLY (not behind the `embedui` build tag that gates the
// upstream Next.js export), because the console is the harness's own surface and
// must exist in every build — including a bare-metal `go build ./cmd/artex` with
// no Node toolchain anywhere. It is a zero-build static app: plain HTML/CSS/ES2020
// served from the same origin, so it inherits the auth cookie without any token
// plumbing, and there is nothing to install or compile on the target box.
package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:console
var harnessConsole embed.FS

// consoleHandler serves the harness console under /console/.
//
// It is public (no JWT) for the same reason the upstream SPA is: the shell must
// load before any credential exists, and every /api call it makes is still
// authenticated server-side. Serving the shell does not leak data.
func (s *Server) consoleHandler() http.Handler {
	root, err := fs.Sub(harnessConsole, "console")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/console")
		// Only plain assets are reachable; the embed FS has no directories to list.
		if strings.Contains(p, "..") {
			http.NotFound(w, r)
			return
		}
		// index.html is written directly rather than through http.FileServer:
		// FileServer canonicalises any path ending in "/index.html" with a redirect
		// to "./", which would bounce /console/ → /console/ forever (and it also
		// prevents serving the shell at "/" at all).
		if p == "" || p == "/" || p == "/index.html" {
			b, err := fs.ReadFile(root, "index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(b)
			return
		}
		switch {
		case strings.HasSuffix(p, ".css"):
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case strings.HasSuffix(p, ".js"):
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case strings.HasSuffix(p, ".html"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		// The console is a control surface: never let a proxy or the browser serve
		// a stale shell after an upgrade.
		w.Header().Set("Cache-Control", "no-cache")
		r2 := r.Clone(r.Context())
		r2.URL.Path = p
		fileServer.ServeHTTP(w, r2)
	})
}
