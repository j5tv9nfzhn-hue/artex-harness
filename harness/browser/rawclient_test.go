package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestMain doubles as the fake MCP server: when GO_WANT_FAKE_MCP is set the test
// binary speaks minimal JSON-RPC over stdio instead of running tests. This is the
// standard Go helper-process trick and keeps the test dependency-free (no npx, no
// network, no Chrome).
func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_FAKE_MCP") == "1" {
		fakeMCPServer()
		return
	}
	os.Exit(m.Run())
}

func fakeMCPServer() {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	const pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	for {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		if req.ID == 0 {
			continue // notification
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
				"serverInfo": map[string]any{"name": "fake", "version": "0"}}
		case "tools/call":
			result = map[string]any{"content": []any{
				map[string]any{"type": "text", "text": "screenshot taken"},
				map[string]any{"type": "image", "mimeType": "image/png", "data": pngBase64},
			}}
		default:
			result = map[string]any{}
		}
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
}

// TestRawClientScreenshot exercises the ONE path the SDK cannot carry: an image tool
// result decoded from base64 over a real stdio process.
func TestRawClientScreenshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c, err := startRaw(ctx, exe, nil, map[string]string{"GO_WANT_FAKE_MCP": "1"})
	if err != nil {
		t.Fatalf("startRaw: %v", err)
	}
	defer c.Close()

	png, err := c.Screenshot(ctx, "browser_take_screenshot")
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if len(png) < 8 || string(png[1:4]) != "PNG" {
		t.Fatalf("not a PNG: %q", png[:min(8, len(png))])
	}
	// Round-trip through the store the Manager uses.
	ts, err := NewThumbStore(t.TempDir(), time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	h, err := ts.Save(png)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 64 {
		t.Fatalf("bad hash: %q", h)
	}
	if _, err := base64.StdEncoding.DecodeString(""); err != nil {
		t.Fatal(err)
	}
}

func TestRawClientScreenshotErrorPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// A binary that exits immediately: startRaw must fail fast, not hang.
	c, err := startRaw(ctx, "true", nil, nil)
	if err == nil {
		c.Close()
		t.Fatal("expected initialize to fail against a non-MCP process")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = exec.Command
