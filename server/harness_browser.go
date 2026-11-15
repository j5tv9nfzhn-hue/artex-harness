package server

// Browser automation for the HARNESS runtime (docs/07 §P3).
//
// Upstream ships a Playwright MCP server row ("browser", stdio via npx) but leaves
// it DISABLED, so "the agent can browse" was never true out of the box — the
// readiness report in P0 exists partly because of exactly this failure mode.
//
// What this file does:
//   - enables the seeded browser MCP by default (setting harness_browser, default
//     on) so a fresh install can actually open a page;
//   - reports an honest status: configured / enabled / how many tools are cached /
//     whether the `npx` launcher exists on this machine.
//
// What it deliberately does NOT claim: a live connection probe. The transport is
// stdio (a child process), and probing it from an HTTP handler would spawn a
// browser on every status poll. The console therefore shows cached tool count and
// the last browser tool activity instead of pretending to ping.

import (
	"encoding/json"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// settingHarnessBrowser toggles "browser automation available to the agent".
const settingHarnessBrowser = "harness_browser"

const browserMCPServer = "browser"

// devtoolsMCPServer is the F12 surface: console messages, network requests,
// performance traces. Playwright MCP drives pages; DevTools MCP *inspects* them.
// They are separate servers on purpose (different lifetimes, different risks).
const devtoolsMCPServer = "chrome-devtools"

// ensureBrowserEnabled flips the seeded browser MCP on (default behaviour) so the
// capability is real rather than theoretical. A human can still disable it in the
// MCP page; this only acts when the setting says "on" AND the server is off.
func (s *Server) ensureBrowserEnabled() {
	if s.m == nil || s.m.pg == nil {
		return
	}
	if !s.m.pg.GetBool(settingHarnessBrowser, true) {
		return
	}
	servers, err := s.m.pg.ListMCP()
	if err != nil {
		log.Printf("[harness/browser] 读取 MCP 列表失败: %v", err)
		return
	}
	for _, srv := range servers {
		if srv.Name != browserMCPServer || srv.Enabled {
			continue
		}
		srv.Enabled = true
		if _, err := s.m.pg.SaveMCP(srv); err != nil {
			log.Printf("[harness/browser] 启用 browser MCP 失败: %v", err)
			return
		}
		log.Printf("[harness/browser] 已启用 browser MCP（Playwright，stdio）；如需关闭请在「系统 → MCP」停用")
	}
}

// ensureDevtoolsMCP seeds the F12-capable MCP server (Chrome DevTools) so console
// messages / network requests / performance data are obtainable by the agent.
//
// It is seeded DISABLED-BY-DEFAULT-OFF? No: it is seeded enabled, but it still needs
// a Chrome with remote debugging available; the status endpoint reports that
// honestly instead of pretending the tools work.
func (s *Server) ensureDevtoolsMCP() {
	if s.m == nil || s.m.pg == nil {
		return
	}
	if !s.m.pg.GetBool(settingHarnessBrowser, true) {
		return
	}
	if _, err := s.m.pg.Exec(`
INSERT INTO mcp_servers(name, transport, command, args, env, enabled)
VALUES ($1,'stdio','npx','["-y","chrome-devtools-mcp@latest"]'::jsonb,'{}'::jsonb,true)
ON CONFLICT (name) DO NOTHING`, devtoolsMCPServer); err != nil {
		log.Printf("[harness/devtools] 预置 %s 失败: %v", devtoolsMCPServer, err)
	}
}

// mcpRowStatus is the shared shape for one browser-side MCP server.
type mcpRowStatus struct {
	Name        string `json:"name"`
	Configured  bool   `json:"configured"`
	Enabled     bool   `json:"enabled"`
	Transport   string `json:"transport,omitempty"`
	Command     string `json:"command,omitempty"`
	ToolsCached int    `json:"tools_cached"`
	Ready       bool   `json:"ready"`
	Hint        string `json:"hint,omitempty"`
}

func (s *Server) mcpStatus(name string) mcpRowStatus {
	st := mcpRowStatus{Name: name}
	servers, err := s.m.pg.ListMCP()
	if err != nil {
		st.Hint = err.Error()
		return st
	}
	for _, srv := range servers {
		if srv.Name != name {
			continue
		}
		st.Configured, st.Enabled, st.Transport, st.Command = true, srv.Enabled, srv.Transport, srv.Command
	}
	var cached int
	_ = s.m.pg.QueryRow(`SELECT count(*) FROM mcp_tools_cache WHERE server=$1`, name).Scan(&cached)
	st.ToolsCached = cached
	st.Ready = st.Enabled && cached > 0
	return st
}

// harnessBrowserStatus reports what the browser surface can actually do right now.
func (s *Server) harnessBrowserStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	servers, err := s.m.pg.ListMCP()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var row *db.MCPServer
	for _, srv := range servers {
		if srv.Name == browserMCPServer {
			row = srv
			break
		}
	}
	if row == nil {
		out["configured"] = false
		out["hint"] = "没有 browser MCP 行：在「系统 → MCP」添加 Playwright（stdio, npx @playwright/mcp@latest）"
		writeJSON(w, http.StatusOK, out)
		return
	}
	var cached int
	_ = s.m.pg.QueryRow(`SELECT count(*) FROM mcp_tools_cache WHERE server=$1`, browserMCPServer).Scan(&cached)

	dev := s.mcpStatus(devtoolsMCPServer)
	out["devtools"] = dev

	npxPath, npxErr := exec.LookPath("npx")
	out["configured"] = true
	out["enabled"] = row.Enabled
	out["transport"] = row.Transport
	out["command"] = row.Command
	out["tools_cached"] = cached
	out["npx"] = map[string]any{"found": npxErr == nil, "path": strings.TrimSpace(npxPath)}
	out["setting_on"] = s.m.pg.GetBool(settingHarnessBrowser, true)

	ready := row.Enabled && cached > 0
	out["ready"] = ready
	switch {
	case !row.Enabled:
		out["hint"] = "浏览器已配置但未启用：在「系统 → MCP」启用，或打开设置 harness_browser"
	case cached == 0:
		out["hint"] = "已启用但还没有工具缓存：首个使用浏览器的 agent 轮次会通过 npx 启动 Playwright 并缓存工具列表"
	default:
		out["hint"] = "可直接使用：agent 侧工具名为 mcp__browser__*"
	}
	if npxErr != nil {
		out["hint"] = "找不到 npx：Playwright / DevTools MCP 都需要 Node（或在镜像里预装）"
		ready = false
		out["ready"] = ready
	}
	if dev.Configured && !dev.Ready {
		out["f12_hint"] = "F12 面（chrome-devtools）已登记但尚未连通：需要一个开着远程调试的 Chrome（--remote-debugging-port=9222）"
	}

	// The Manager's live view wins over the (stale) DB tool cache: a server that is
	// actually connected is ready even if nothing ever wrote mcp_tools_cache.
	if s.browser != nil {
		var taskID int64
		if id := r.URL.Query().Get("task"); id != "" {
			if t, ok := s.m.Task(id); ok {
				taskID, _ = parseI64(t.ID)
			}
		}
		if taskID > 0 {
			sess := s.browser.Status(taskID)
			if sess.Servers["browser"] == "live" {
				ready = true
				out["ready"] = true
			}
			if sess.Servers["chrome-devtools"] == "live" {
				dev.Ready, dev.ToolsCached = true, dev.ToolsCached
				dev.Hint = ""
				out["devtools"] = dev
			}
		}
	}

	// Last browser activity, so the console can show "what the agent last saw".
	if id := r.URL.Query().Get("task"); id != "" {
		if t, ok := s.m.Task(id); ok {
			acts, _, aerr := t.Store.ActivityList(nil, 0, 500)
			if aerr == nil {
				type item struct {
					ID      int64  `json:"id"`
					Tool    string `json:"tool"`
					Kind    string `json:"kind"`
					Summary string `json:"summary"`
				}
				items := make([]item, 0, 8)
				for i := len(acts) - 1; i >= 0 && len(items) < 12; i-- {
					a := acts[i]
					if !strings.HasPrefix(a.Tool, "mcp__browser__") && !strings.HasPrefix(a.Tool, "mcp__chrome-devtools__") {
						continue
					}
					items = append(items, item{ID: a.ID, Tool: a.Tool, Kind: a.Kind, Summary: truncateSummary(a.Summary, 200)})
				}
				out["recent"] = items
				out["task"] = t.ID
				ces, unparsed := consoleErrors(acts, t.Store.ActivityDetail, 20)
				out["console_errors"] = ces
				out["console_errors_unparsed"] = unparsed
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func truncateSummary(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// consoleError is one warn/error-level console message lifted out of the
// chrome-devtools MCP output.
type consoleError struct {
	ID    int64  `json:"id"`
	Level string `json:"level"`
	Text  string `json:"text"`
	Tool  string `json:"tool"`
	At    string `json:"at,omitempty"`
}

var consoleLevelRe = regexp.MustCompile(`(?i)\[(error|warn|warning|fatal|critical)\]`)

// consoleErrors extracts the warn/error lines from the agent's DevTools console
// reads (docs/10 §3.2 — the F12 panel shows this list and nothing else).
//
// Deliberately line-based rather than schema-based: the chrome-devtools MCP server
// prints a human-readable message list whose layout changes between releases, so a
// strict parser would break on upgrade and, worse, would report "no errors" when it
// had merely failed to parse. Lines without a level marker are ignored; blobs that
// cannot even be turned into text are counted in `unparsed` so the panel can say so
// instead of showing an empty list that looks clean.
func consoleErrors(acts []db.Activity, detail func(int64) (string, error), limit int) ([]consoleError, int) {
	if limit <= 0 {
		limit = 20
	}
	out := make([]consoleError, 0, limit)
	seen := make(map[string]bool, limit)
	unparsed := 0
	for i := len(acts) - 1; i >= 0 && len(out) < limit; i-- {
		a := acts[i]
		if a.Kind != "tool_result" || detail == nil {
			continue
		}
		if !strings.HasPrefix(a.Tool, "mcp__chrome-devtools__") || !strings.Contains(strings.ToLower(a.Tool), "console") {
			continue
		}
		blob, err := detail(a.ID)
		if err != nil || strings.TrimSpace(blob) == "" {
			unparsed++
			continue
		}
		for _, line := range strings.Split(mcpText(blob), "\n") {
			m := consoleLevelRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			text := strings.TrimSpace(line)
			if seen[text] {
				continue
			}
			seen[text] = true
			out = append(out, consoleError{
				ID:    a.ID,
				Level: normalizeConsoleLevel(m[1]),
				Text:  truncateSummary(text, 300),
				Tool:  strings.TrimPrefix(a.Tool, "mcp__chrome-devtools__"),
				At:    a.CreatedAt.Format("15:04:05"),
			})
			if len(out) >= limit {
				break
			}
		}
	}
	return out, unparsed
}

func normalizeConsoleLevel(s string) string {
	if strings.EqualFold(s, "warn") || strings.EqualFold(s, "warning") {
		return "warn"
	}
	return "error" // error / fatal / critical
}

// mcpText unwraps the MCP tool result envelope ({"content":[{"type":"text",...}]})
// to plain text; anything that is not that envelope is returned as-is.
func mcpText(blob string) string {
	t := strings.TrimSpace(blob)
	if !strings.HasPrefix(t, "{") {
		return blob
	}
	var env struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(t), &env); err != nil || len(env.Content) == 0 {
		return blob
	}
	var b strings.Builder
	for _, c := range env.Content {
		if c.Text != "" {
			b.WriteString(c.Text)
			b.WriteString("\n")
		}
	}
	if b.Len() == 0 {
		return blob
	}
	return b.String()
}
