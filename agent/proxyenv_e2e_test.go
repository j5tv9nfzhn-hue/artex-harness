package agent

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/traffic"
)

// TestProxyEnvLoopbackIsRecorded is the end-to-end proof for the real-task #1
// bug: with the host's no_proxy=127.0.0.1 in force, a curl through the env
// proxyEnv builds must still reach the recording proxy for a loopback
// target — and the proxy must record the exchange.
func TestProxyEnvLoopbackIsRecorded(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl not available")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	// Free loopback port for the recording proxy.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyAddr := ln.Addr().String()
	ln.Close()

	tr, err := traffic.Open(filepath.Join(t.TempDir(), "traffic"), proxyAddr)
	if err != nil {
		t.Fatalf("traffic.Open: %v", err)
	}
	defer tr.Close()
	go func() { _ = tr.Start() }()
	// Wait until the proxy accepts connections.
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", proxyAddr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recording proxy did not start on %s: %v", proxyAddr, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	runCurl := func(env ...string) (string, error) {
		cmd := exec.Command(curl, "-s", "-o", "/dev/null", "-w", "%{http_code}", target.URL)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	count := func() int {
		n, err := tr.Count()
		if err != nil {
			t.Fatalf("traffic.Count: %v", err)
		}
		return n
	}

	// Control: the host's no_proxy alone (no proxyEnv) → direct dial, the
	// proxy records nothing. This is the real-task #1 failure mode.
	if code, err := runCurl("no_proxy=127.0.0.1", "NO_PROXY=127.0.0.1"); err != nil || code != "200" {
		t.Fatalf("control curl: code=%q err=%v", code, err)
	}
	if n := count(); n != 0 {
		t.Fatalf("control: proxy recorded %d exchanges without proxy env, want 0", n)
	}

	// With proxyEnv (capture on: CA present) the same hostile no_proxy is
	// overridden by the empty NO_PROXY/no_proxy entries — the request must
	// go through the proxy and be recorded.
	proxied := append([]string{"no_proxy=127.0.0.1", "NO_PROXY=127.0.0.1"},
		proxyEnv(tr.ProxyAddr(), tr.CACertPath())...)
	if code, err := runCurl(proxied...); err != nil || code != "200" {
		t.Fatalf("proxied curl: code=%q err=%v", code, err)
	}
	// Recording is async; poll briefly.
	deadline = time.Now().Add(5 * time.Second)
	for count() == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if n := count(); n == 0 {
		t.Fatal("proxy recorded 0 exchanges for the loopback request through proxyEnv")
	} else {
		t.Logf("proxy recorded %d exchange(s) for the loopback request", n)
	}
}
