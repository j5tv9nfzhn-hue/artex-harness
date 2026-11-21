package main

import (
	"runtime"
	"testing"
)

func TestConsoleURL(t *testing.T) {
	cases := []struct {
		addr string
		want string
	}{
		{":8797", "http://127.0.0.1:8797/"},
		{"127.0.0.1:9000", "http://127.0.0.1:9000/"},
		{"0.0.0.0:9000", "http://127.0.0.1:9000/"},
		{"[::]:8797", "http://127.0.0.1:8797/"},
		{"localhost:8797", "http://localhost:8797/"},
		{"example.local:8797", "http://example.local:8797/"},
		{"[::1]:8797", "http://[::1]:8797/"},
		{"noport", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := consoleURL(c.addr); got != c.want {
			t.Errorf("consoleURL(%q) = %q, want %q", c.addr, got, c.want)
		}
	}
}

func TestCanOpenBrowser(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		if !canOpenBrowser() {
			t.Errorf("canOpenBrowser() = false on %s, want true", runtime.GOOS)
		}
		return
	}
	t.Setenv("DISPLAY", ":0")
	t.Setenv("WAYLAND_DISPLAY", "")
	if !canOpenBrowser() {
		t.Errorf("canOpenBrowser() = false with DISPLAY set, want true")
	}
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-1")
	if !canOpenBrowser() {
		t.Errorf("canOpenBrowser() = false with WAYLAND_DISPLAY set, want true")
	}
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	if canOpenBrowser() {
		t.Errorf("canOpenBrowser() = true with no display, want false")
	}
}

func TestOpenBrowserCmd(t *testing.T) {
	cmd := openBrowserCmd("http://127.0.0.1:8797/")
	if cmd == nil || len(cmd.Args) < 2 || cmd.Args[len(cmd.Args)-1] != "http://127.0.0.1:8797/" {
		t.Fatalf("openBrowserCmd() = %v, want the URL as the last argument", cmd)
	}
	switch runtime.GOOS {
	case "darwin":
		if cmd.Args[0] != "open" {
			t.Errorf("openBrowserCmd() = %q, want open on darwin", cmd.Args[0])
		}
	case "windows":
		if cmd.Args[0] != "rundll32" {
			t.Errorf("openBrowserCmd() = %q, want rundll32 on windows", cmd.Args[0])
		}
	default:
		if cmd.Args[0] != "xdg-open" {
			t.Errorf("openBrowserCmd() = %q, want xdg-open on %s", cmd.Args[0], runtime.GOOS)
		}
	}
}
