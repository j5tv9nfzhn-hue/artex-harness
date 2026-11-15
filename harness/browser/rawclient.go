package browser

// Raw MCP client for the ONE thing the SDK cannot carry: image bytes.
//
// norma's llm.ContentBlock union is text/thinking/tool_use/tool_result only, so an
// MCP tool that returns an image (Playwright's screenshot) loses the bytes by the time
// it reaches the agent. Rather than widen the SDK's content union (which would touch
// every provider's serializer), the Manager keeps its own minimal stdio JSON-RPC client
// and asks the same server for the screenshot directly. Text tools keep flowing through
// the SDK; only pixels use this path.

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// rawClient is a minimal MCP stdio client: initialize + tools/call.
type rawClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	mu      sync.Mutex
	nextID  int
	pending map[int]chan rawResponse
	closed  chan struct{}
	once    sync.Once
}

type rawRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rawResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// startRaw spawns an MCP server over stdio and performs the handshake.
func startRaw(ctx context.Context, command string, args []string, env map[string]string) (*rawClient, error) {
	cmd := exec.Command(command, args...)
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &rawClient{cmd: cmd, stdin: stdin, pending: map[int]chan rawResponse{}, closed: make(chan struct{})}
	go c.readLoop(stdout)
	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := c.call(initCtx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "artex-harness", "version": "1"},
	}); err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	_ = c.notify("notifications/initialized", map[string]any{})
	return c, nil
}

func (c *rawClient) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 32*1024*1024) // screenshots are large
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp rawResponse
		if json.Unmarshal(line, &resp) != nil || resp.ID == 0 {
			continue
		}
		c.mu.Lock()
		ch := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
	c.once.Do(func() { close(c.closed) })
}

func (c *rawClient) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, _ := json.Marshal(rawRequest{JSONRPC: "2.0", Method: method, Params: params})
	_, err := c.stdin.Write(append(data, '\n'))
	return err
}

func (c *rawClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rawResponse, 1)
	c.pending[id] = ch
	data, _ := json.Marshal(rawRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	_, err := c.stdin.Write(append(data, '\n'))
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, fmt.Errorf("mcp: connection closed")
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// Screenshot asks the server for the current page as PNG bytes.
//
// Tool name is configurable because Playwright and DevTools name it differently;
// the default matches Playwright MCP.
func (c *rawClient) Screenshot(ctx context.Context, tool string) ([]byte, error) {
	res, err := c.call(ctx, "tools/call", map[string]any{"name": tool, "arguments": map[string]any{}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Data string `json:"data"`
			Mime string `json:"mimeType"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, fmt.Errorf("decode tools/call: %w", err)
	}
	if out.IsError {
		return nil, fmt.Errorf("screenshot tool returned an error")
	}
	for _, c := range out.Content {
		if c.Type != "image" || c.Data == "" {
			continue
		}
		raw, derr := base64.StdEncoding.DecodeString(c.Data)
		if derr != nil {
			return nil, fmt.Errorf("decode image: %w", derr)
		}
		return raw, nil
	}
	return nil, fmt.Errorf("screenshot tool returned no image content")
}

// Close terminates the server process.
func (c *rawClient) Close() error {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.stdin.Close()
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	return nil
}
