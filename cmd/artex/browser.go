// 启动后自动打开控制台：监听地址就绪（/api/health 有响应）后，在默认浏览器里
// 打开控制台首页。只在有图形会话时尝试（Linux 需 DISPLAY 或 WAYLAND_DISPLAY，
// macOS / Windows 直接尝试）；无头服务器、容器、CI 里静默跳过，启动本身永不因此失败。
// 用 -open-browser=false 关闭。
package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// consoleURL turns a listen address into the console URL to open.
// ":8797" / "0.0.0.0:8797" / "[::]:8797" all become http://127.0.0.1:8797/;
// a real hostname is kept as-is. Returns "" when the address has no port.
func consoleURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return ""
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + port + "/"
}

// canOpenBrowser reports whether this process plausibly has a graphical
// session to open a browser in.
func canOpenBrowser() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	default:
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	}
}

// openBrowserCmd builds the OS-specific command that opens url.
func openBrowserCmd(url string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return exec.Command("xdg-open", url)
	}
}

// maybeOpenBrowser waits (up to 5s) for the console to answer /api/health,
// then opens it in the default browser. Runs in the background; a failed
// open only logs a hint and never affects the server.
func maybeOpenBrowser(addr string, enabled bool) {
	if !enabled {
		return
	}
	url := consoleURL(addr)
	if url == "" || !canOpenBrowser() {
		return
	}
	go func() {
		health := url + "api/health"
		ready := false
		for i := 0; i < 25; i++ {
			resp, err := http.Get(health)
			if err == nil && resp.StatusCode == http.StatusOK {
				_ = resp.Body.Close()
				ready = true
				break
			}
			if err == nil {
				_ = resp.Body.Close()
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !ready {
			log.Printf("[browser] 控制台未在 5 秒内就绪，未自动打开浏览器，请手动访问 %s", url)
			return
		}
		if err := openBrowserCmd(url).Start(); err != nil {
			log.Printf("[browser] 无法自动打开浏览器：%v，请手动访问 %s", err, url)
			return
		}
		log.Printf("[browser] 已在浏览器中打开控制台 %s", url)
	}()
}
