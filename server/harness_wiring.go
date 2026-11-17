package server

// HARNESS wiring: resolver, tool bindings, readiness, and the conversation API.
//
// This file is the seam between the ARTEX execution base and the HARNESS runtime.
// It deliberately keeps the legacy pipeline untouched: existing tasks stay on
// planner/worker, and only tasks created with harness_mode=true run the single
// conversational agent (docs/07-HARNESS-RUNTIME.md).

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/agent"
)

// settingHarnessRuntime decides the mode of NEW tasks (default on).
const settingHarnessRuntime = "harness_runtime"

// harnessToolBindings are the graph-facing tools the HARNESS agent uses. They are
// seeded onto the "harness" agent key so the tools table stays the single place
// where "which agent may call what" is configured (and the UI can change it).
var harnessToolBindings = []string{
	"graph_overview", "list_findings", "list_facts", "node_detail", "expand_digest",
	"insert_assets", "add_company_scope", "list_assets", "report_finding",
	"record_fact", "add_task_scope", "list_untested_assets",
	"set_goals", "set_constraints",
}

// seedHarnessBindings creates the harness agent row (if missing) and binds the
// graph tools to it. Idempotent; never removes a binding a human added.
func (s *Server) seedHarnessBindings() {
	if s.m == nil || s.m.pg == nil {
		return
	}
	if _, err := s.m.pg.Exec(`
INSERT INTO agents(key, name, description, role, builtin, enabled)
VALUES ('harness','HARNESS','HARNESS 运行模式：一个任务一个会话 agent，自己判断、自己执行，可派生只读子代理。','assistant', false, true)
ON CONFLICT (key) DO NOTHING`); err != nil {
		log.Printf("[harness] seed agent: %v", err)
		return
	}
	if _, err := s.m.pg.Exec(`
UPDATE tools
SET agents = agents || '["harness"]'::jsonb
WHERE key = ANY($1) AND NOT (agents @> '["harness"]'::jsonb)`, harnessToolBindings); err != nil {
		log.Printf("[harness] bind tools: %v", err)
	}
}

// harnessAgentFor resolves the task's HARNESS runtime, or false when this task is
// not in harness mode / no model is available (the loop then idles instead of
// silently degrading to the legacy pipeline).
func (s *Server) harnessAgentFor(t *Task) (*agent.HarnessAgent, bool) {
	if t == nil || !t.HarnessMode {
		return nil, false
	}
	if !s.taskRuntimeAvailable(t, "harness") {
		return nil, false
	}
	b := s.agentsForTask(t)
	if b == nil || b.hx == nil {
		return nil, false
	}
	return b.hx, true
}

// harnessRuntimeOn reports the default mode for new tasks.
func (s *Server) harnessRuntimeOn() bool {
	if s.m == nil || s.m.pg == nil {
		return true
	}
	return s.m.pg.GetBool(settingHarnessRuntime, true)
}

// ── conversation API ──────────────────────────────────────────────────────

const maxSayBytes = 16 << 10

// harnessSay delivers a human message to a harness task (the conversational
// equivalent of typing). It revives a settled task and returns the revision the
// message lands in.
func (s *Server) harnessSay(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSayBytes)
	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		writeErr(w, http.StatusBadRequest, "消息不能为空")
		return
	}
	if len([]rune(msg)) > 8000 {
		writeErr(w, http.StatusBadRequest, "消息不能超过 8000 个字符")
		return
	}
	rev, err := s.engine.Say(t, msg)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task": t.ID, "accepted": true, "revision": rev,
	})
}

// harnessMode switches one task between the HARNESS runtime and the legacy
// pipeline. Takes effect on the next run (pause/resume to apply immediately).
func (s *Server) harnessMode(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	var req struct {
		HarnessMode *bool `json:"harness_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if req.HarnessMode == nil {
		writeErr(w, http.StatusBadRequest, "缺少 harness_mode")
		return
	}
	taskID, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad task id")
		return
	}
	if err := s.m.pg.SetTaskHarnessMode(taskID, *req.HarnessMode); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.invalidateTaskAgents()
	writeJSON(w, http.StatusOK, map[string]any{"task": t.ID, "harness_mode": *req.HarnessMode})
}

// ── readiness ─────────────────────────────────────────────────────────────

// harnessReady answers "why is nothing happening?" in one call. Four independent
// preconditions used to look identical from the outside (a silent, empty stream):
// no model, no database, no skills, browser automation not connected.
func (s *Server) harnessReady(w http.ResponseWriter, _ *http.Request) {
	type block struct {
		OK      bool   `json:"ok"`
		Detail  string `json:"detail,omitempty"`
		Hint    string `json:"hint,omitempty"`
		Count   int    `json:"count,omitempty"`
		Enabled bool   `json:"enabled,omitempty"`
	}
	out := map[string]any{}
	ready := true

	// database
	dbOK := false
	if s.m != nil && s.m.pg != nil {
		var one int
		dbOK = s.m.pg.QueryRow(`SELECT 1`).Scan(&one) == nil
	}
	out["pg"] = block{OK: dbOK, Detail: map[bool]string{true: "connected", false: "unreachable"}[dbOK]}
	if !dbOK {
		ready = false
	}

	// model endpoint(s)
	llm := block{}
	if dbOK {
		var total, active int
		_ = s.m.pg.QueryRow(`SELECT count(*) FROM llm_profiles`).Scan(&total)
		_ = s.m.pg.QueryRow(`SELECT count(*) FROM llm_profiles WHERE is_default`).Scan(&active)
		llm.Count = total
		llm.OK = s.engine.Ready()
		if !llm.OK {
			llm.Hint = "在「系统 → LLM 配置」里添加一个端点并激活（或设置环境变量后重启）"
			ready = false
		}
		llm.Detail = strings.TrimSpace(strings.Join([]string{
			"profiles=" + strconv.Itoa(total),
			"active=" + strconv.Itoa(active),
			"engine=" + map[bool]string{true: "ready", false: "idle"}[s.engine.Ready()],
		}, " "))
	}
	out["llm"] = llm

	// skills (on disk)
	sk := block{}
	if s.skillDir != "" {
		if ents, err := os.ReadDir(s.skillDir); err == nil {
			n := 0
			for _, e := range ents {
				if e.IsDir() {
					n++
				}
			}
			sk = block{OK: true, Count: n, Detail: s.skillDir + " · " + strconv.Itoa(n) + " 个技能"}
		} else {
			sk = block{OK: false, Detail: err.Error(), Hint: "技能目录不可读；不配也能跑，只是少一层领域知识"}
		}
	} else {
		sk = block{OK: false, Detail: "未配置技能目录"}
	}
	out["skills"] = sk

	// browser automation
	br := block{}
	if dbOK {
		var enabled bool
		var tools int
		_ = s.m.pg.QueryRow(`SELECT COALESCE(enabled,false) FROM mcp_servers WHERE name='browser'`).Scan(&enabled)
		// mcp_tools_cache keys on server_id (bigint), not on a "server" name column —
		// that column does not exist. The old query errored and the error was
		// discarded, so this block always reported 0 cached tools and the readiness
		// page told the operator "browser MCP 尚未连通" while the tools were in fact
		// cached and usable (same defect class as harness_browser.go, fixed there).
		if err := s.m.pg.QueryRow(`SELECT count(*) FROM mcp_tools_cache c JOIN mcp_servers s ON s.id = c.server_id WHERE s.name = 'browser'`).Scan(&tools); err != nil {
			log.Printf("[harness/ready] 统计 browser 工具缓存失败: %v", err)
		}
		br = block{OK: enabled && tools > 0, Enabled: enabled, Count: tools}
		switch {
		case !enabled:
			br.Detail = "browser MCP 已配置但未启用"
			br.Hint = "在「系统 → MCP」启用 browser（Playwright），或在设置里开启浏览器自动化"
		case tools == 0:
			br.Detail = "browser MCP 已启用但尚未连通（工具列表为空）"
			br.Hint = "确认 npx @playwright/mcp 可用；首次连接需要下载浏览器"
		default:
			br.Detail = "connected · " + strconv.Itoa(tools) + " 个工具"
		}
	}
	out["browser_mcp"] = br

	out["ready"] = ready
	out["runtime"] = "harness"
	out["harness_default"] = s.harnessRuntimeOn()
	if !ready {
		out["hint"] = "引擎未就绪：先解决上面 ok=false 的项"
	}
	out["workspace"] = strings.TrimSpace(filepath.Join(s.m.dir, "tasks"))
	writeJSON(w, http.StatusOK, out)
}
