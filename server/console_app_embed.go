package server

// 轻量控制台（web/ 构建产物）的托管入口。
//
// 与 server/console 的零构建页面并存：/console/ 是旧版，/app/ 是 React 版。
// 产物用 go:embed 打进二进制，部署仍然是一个文件，不需要 node/bun。
import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:console-app
var consoleAppFS embed.FS

// consoleAppHandler serves the built SPA under /app/.
//
// 前端是 SPA：任何非静态资源路径都回落到 index.html，刷新子路由才不会 404。
func (s *Server) consoleAppHandler() http.Handler {
	root, err := fs.Sub(consoleAppFS, "console-app")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/app")
		if p == "" || p == "/" {
			p = "/"
		}
		if strings.Contains(p, "..") {
			http.NotFound(w, r)
			return
		}
		// 入口 HTML 绝不能被缓存：它指向带哈希的资源名，一旦被缓存住，
		// 升级后就永远加载旧的 JS（实测：改了设置页结构，服务端已是新版，
		// 浏览器仍渲染旧 tab）。带哈希的静态资源反之要长缓存。
		if p == "/" || !strings.Contains(p, ".") {
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			b, rerr := fs.ReadFile(root, "index.html")
			if rerr != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		r2 := r.Clone(r.Context())
		r2.URL.Path = p
		fileServer.ServeHTTP(w, r2)
	})
}
