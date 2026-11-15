package server

// Browser session wiring (docs/09 §2–§3).
//
// The Manager owns one session per task (Playwright + chrome-devtools on the same
// session), a tab model, idle GC and rebuild-on-failure. This file is the seam: it
// builds the Manager from the mcp_servers rows, exposes it, and hands its tools to the
// harness agent in place of the per-run MCP clients.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/harness/browser"
	normamcp "github.com/Autumn-27/norma/mcp"
	actool "github.com/Autumn-27/norma/tool"
)

const (
	// browserIdleTimeout closes a session nobody touched for this long.
	browserIdleTimeout = 30 * time.Minute
	// browserThumbTTL / browserThumbMax bound the screenshot store.
	browserThumbTTL = 24 * time.Hour
	browserThumbMax = 2000
)

// newBrowserManager builds the Manager from the DB rows (+ screenshot store).
func (s *Server) newBrowserManager() *browser.Manager {
	thumbs, err := browser.NewThumbStore(s.m.dir+"/browser", browserThumbTTL, browserThumbMax)
	if err != nil {
		log.Printf("[harness/browser] 截图存储不可用: %v", err)
		thumbs = nil
	}
	specs := map[string]browser.CommandSpec{}
	if s.m != nil && s.m.pg != nil {
		if rows, lerr := s.m.pg.ListMCP(); lerr == nil {
			for _, r := range rows {
				if r.Transport != "stdio" || !r.Enabled {
					continue
				}
				args := decodeStrings(r.Args)
				specs[r.Name] = browser.CommandSpec{
					Command: r.Command,
					Args:    args,
					Env:     decodeStringMap(r.Env),
				}
			}
		}
	}
	cfg := browser.DefaultConfig()
	cfg.IdleTimeout = browserIdleTimeout
	cfg.Specs = specs

	launcher := func(ctx context.Context, server string) (browser.Conn, error) {
		spec, ok := specs[server]
		if !ok || strings.TrimSpace(spec.Command) == "" {
			return nil, errNoSpec(server)
		}
		c, err := normamcp.NewStdioClient(ctx, server, spec.Command, spec.Env, spec.Args...)
		if err != nil {
			return nil, err
		}
		return &mcpConn{c: c}, nil
	}
	return browser.New(cfg, launcher, thumbs)
}

type errNoSpec string

func (e errNoSpec) Error() string {
	return "MCP 服务器 " + string(e) + " 未配置或未启用（系统 → MCP）"
}

// mcpConn adapts norma's client to browser.Conn.
type mcpConn struct{ c *normamcp.Client }

func (m *mcpConn) Tools(ctx context.Context) ([]actool.CoreTool, error) { return m.c.Tools(ctx) }
func (m *mcpConn) Close() error                                         { return m.c.Close() }

func decodeStrings(raw json.RawMessage) []string {
	var out []string
	_ = json.Unmarshal(raw, &out)
	return out
}

func decodeStringMap(raw json.RawMessage) map[string]string {
	var out map[string]string
	_ = json.Unmarshal(raw, &out)
	return out
}

// startBrowserGC reclaims idle sessions and expired screenshots periodically.
func (s *Server) startBrowserGC(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.browser == nil {
				continue
			}
			if n := s.browser.GC(time.Now()); n > 0 {
				log.Printf("[harness/browser] 回收 %d 个空闲会话", n)
			}
			if ts := s.browser.Thumbs(); ts != nil {
				if removed := ts.GC(time.Now()); removed > 0 {
					log.Printf("[harness/browser] 清理 %d 张过期截图", removed)
				}
			}
		}
	}
}

// ── HTTP ─────────────────────────────────────────────────────────────────

// harnessBrowserSessions reports the Manager's view of a task's session.
func (s *Server) harnessBrowserSessions(w http.ResponseWriter, r *http.Request) {
	if s.browser == nil {
		writeErr(w, http.StatusServiceUnavailable, "浏览器管理器未初始化")
		return
	}
	out := map[string]any{"manager": true}
	if id := r.URL.Query().Get("task"); id != "" {
		t, ok := s.m.Task(id)
		if !ok {
			writeErr(w, http.StatusNotFound, "task not found")
			return
		}
		var taskID int64
		if n, err := parseI64(t.ID); err == nil {
			taskID = n
		}
		out["session"] = s.browser.Status(taskID)
		out["task"] = t.ID
	}
	if ts := s.browser.Thumbs(); ts != nil {
		out["thumbs"] = map[string]any{"dir": ts.Dir(), "count": ts.Count()}
	}
	writeJSON(w, http.StatusOK, out)
}

// harnessBrowserRestart force-rebuilds a task's session.
func (s *Server) harnessBrowserRestart(w http.ResponseWriter, r *http.Request) {
	if s.browser == nil {
		writeErr(w, http.StatusServiceUnavailable, "浏览器管理器未初始化")
		return
	}
	var req struct {
		Task string `json:"task"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req)
	t, ok := s.m.Task(req.Task)
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	taskID, _ := parseI64(t.ID)
	start := time.Now()
	err := s.browser.Recreate(r.Context(), taskID)
	elapsed := time.Since(start)
	resp := map[string]any{"task": t.ID, "elapsed_ms": elapsed.Milliseconds(), "session": s.browser.Status(taskID)}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// harnessBrowserTabs opens/closes logical tabs.
func (s *Server) harnessBrowserTabs(w http.ResponseWriter, r *http.Request) {
	if s.browser == nil {
		writeErr(w, http.StatusServiceUnavailable, "浏览器管理器未初始化")
		return
	}
	switch r.Method {
	case http.MethodGet:
		t, ok := s.m.Task(r.URL.Query().Get("task"))
		if !ok {
			writeErr(w, http.StatusNotFound, "task not found")
			return
		}
		taskID, _ := parseI64(t.ID)
		writeJSON(w, http.StatusOK, map[string]any{"task": t.ID, "tabs": s.browser.Status(taskID).Tabs})
	case http.MethodPost:
		var req struct {
			Task   string `json:"task"`
			Action string `json:"action"`
			URL    string `json:"url"`
			ID     string `json:"id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		t, ok := s.m.Task(req.Task)
		if !ok {
			writeErr(w, http.StatusNotFound, "task not found")
			return
		}
		taskID, _ := parseI64(t.ID)
		if _, err := s.browser.Ensure(r.Context(), taskID); err != nil {
			// Tab model is still usable without a live server (it is the console's view).
			log.Printf("[harness/browser] 会话不可用（标签仍记录）: %v", err)
		}
		switch req.Action {
		case "open":
			tab, err := s.browser.OpenTab(taskID, req.URL)
			if err != nil {
				writeErr(w, http.StatusConflict, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"task": t.ID, "tab": tab})
		case "close":
			s.browser.CloseTab(taskID, req.ID)
			writeJSON(w, http.StatusOK, map[string]any{"task": t.ID, "closed": req.ID})
		default:
			writeErr(w, http.StatusBadRequest, "action 必须是 open|close")
		}
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// harnessShot serves a stored screenshot (content-addressed, immutable).
func (s *Server) harnessShot(w http.ResponseWriter, r *http.Request) {
	if s.browser == nil || s.browser.Thumbs() == nil {
		writeErr(w, http.StatusServiceUnavailable, "截图存储未启用")
		return
	}
	h := r.PathValue("hash")
	data, err := s.browser.Thumbs().Get(h)
	if err != nil {
		writeErr(w, http.StatusNotFound, "screenshot not found")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	// Content-addressed: the bytes for a hash never change.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(data)
}

// browserToolsFor exposes the Manager's tools to the harness agent.
func (s *Server) browserToolsFor(ctx context.Context, taskID int64) ([]actool.CoreTool, error) {
	if s.browser == nil {
		return nil, nil
	}
	return s.browser.Tools(ctx, taskID)
}

func parseI64(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }
