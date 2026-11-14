package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestConsoleHandlerServesShell covers the S5 surface: the console is the primary
// UI, so its shell must be served from the embedded FS at /console/ (and at the
// canonical /console/index.html path, which http.FileServer would otherwise
// rewrite into a redirect loop).
func TestConsoleHandlerServesShell(t *testing.T) {
	s := &Server{}
	h := s.consoleHandler()

	for _, path := range []string{"/console/", "/console/index.html", "/console"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%s: content-type = %q", path, ct)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "artex") {
			t.Fatalf("%s: body does not look like the shell", path)
		}
		// The shell must load its assets from the console path, not the site root.
		if !strings.Contains(body, "/console/app.js") {
			t.Fatalf("%s: shell does not reference /console/app.js", path)
		}
	}
}

// TestConsoleHandlerServesAssetsWithTypes: a bare `go build` binary (no Node
// toolchain) still ships a working UI, so the asset types must be right.
func TestConsoleHandlerServesAssetsWithTypes(t *testing.T) {
	s := &Server{}
	h := s.consoleHandler()
	cases := map[string]string{
		"/console/app.js":  "text/javascript",
		"/console/app.css": "text/css",
	}
	for path, want := range cases {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: status = %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, want) {
			t.Fatalf("%s: content-type = %q, want prefix %q", path, ct, want)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s: empty body", path)
		}
		// A control surface must never be served stale from a cache.
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Fatalf("%s: cache-control = %q", path, cc)
		}
	}
}

// TestConsoleHandlerRejectsTraversal: the embedded FS has no directories to list,
// but the path must still refuse `..` rather than rely on the FS catching it.
func TestConsoleHandlerRejectsTraversal(t *testing.T) {
	s := &Server{}
	h := s.consoleHandler()
	for _, path := range []string{"/console/../server.go", "/console/a/../../x"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == 200 {
			t.Fatalf("%s must not be served (got 200)", path)
		}
	}
}
