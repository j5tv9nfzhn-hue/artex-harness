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
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:console
var harnessConsole embed.FS

// consoleHandler serves the harness console under /console/.
//
// It is public (no JWT) for the same reason the upstream SPA is: the shell must
// load before any credential exists, and every /api call it makes is still
// authenticated server-side. Serving the shell does not leak data.
// consoleDevDir returns the on-disk console directory when hot-reload is enabled
// (ARTEX_CONSOLE_DIR). Editing HTML/CSS/JS then needs no rebuild: the handler reads
// the files per request and the injected reload script refreshes the page.
//
// Empty string means the embedded copy is served — the shipped behaviour.
func consoleDevDir() string {
	d := strings.TrimSpace(os.Getenv("ARTEX_CONSOLE_DIR"))
	if d == "" {
		return ""
	}
	if !filepath.IsAbs(d) {
		if wd, err := os.Getwd(); err == nil {
			d = filepath.Join(wd, d)
		}
	}
	if st, err := os.Stat(filepath.Join(d, "index.html")); err != nil || st.IsDir() {
		return ""
	}
	return d
}

// consoleRev fingerprints the console sources so the reload script can notice an edit.
func consoleRev(dir string) string {
	var b strings.Builder
	for _, name := range []string{"index.html", "app.css", "app.js"} {
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", name, st.Size(), st.ModTime().UnixNano())
		}
	}
	return fmt.Sprintf("%x", stableHash(b.String()))
}

func stableHash(s string) uint64 {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

const consoleDevScriptTag = `<script src="/console/__dev.js"></script>
`

func (s *Server) consoleHandler() http.Handler {
	devDir := consoleDevDir()
	var root fs.FS
	var err error
	if devDir != "" {
		root = os.DirFS(devDir)
	} else if root, err = fs.Sub(harnessConsole, "console"); err != nil {
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
		if devDir != "" {
			switch p {
			case "/__rev":
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				_, _ = w.Write([]byte(consoleRev(devDir)))
				return
			case "/__dev.js":
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
				_, _ = w.Write([]byte(consoleDevScript))
				return
			}
		}
		if p == "" || p == "/" || p == "/index.html" {
			b, err := fs.ReadFile(root, "index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			if devDir != "" {
				b = []byte(strings.Replace(string(b), "</body>", consoleDevScriptTag+"</body>", 1))
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

// consoleDevScript polls the console revision and reloads the page when a source
// file changed. Deliberately tiny and dependency-free: it exists only when
// ARTEX_CONSOLE_DIR is set, and it must never make the console depend on a
// websocket or a dev server.
const consoleDevScript = `(function () {
  var last = null;
  function tick() {
    fetch("/console/__rev", { cache: "no-store" })
      .then(function (r) { return r.text(); })
      .then(function (t) {
        if (last === null) last = t;
        else if (t !== last) { location.reload(); return; }
      })
      .catch(function () {})
      .then(function () { setTimeout(tick, 700); });
  }
  tick();
})();
`
