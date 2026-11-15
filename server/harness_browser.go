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
	"log"
	"net/http"
	"os/exec"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// settingHarnessBrowser toggles "browser automation available to the agent".
const settingHarnessBrowser = "harness_browser"

const browserMCPServer = "browser"

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
		out["hint"] = "找不到 npx：Playwright MCP 需要 Node（或在镜像里预装）"
		ready = false
		out["ready"] = ready
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
					if !strings.HasPrefix(a.Tool, "mcp__browser__") {
						continue
					}
					items = append(items, item{ID: a.ID, Tool: a.Tool, Kind: a.Kind, Summary: truncateSummary(a.Summary, 200)})
				}
				out["recent"] = items
				out["task"] = t.ID
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
