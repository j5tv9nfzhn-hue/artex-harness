package server

// Capability registry + evidence integrity endpoints (S4).
//
//	GET /api/harness/capabilities?task=
//	GET /api/harness/evidence-integrity?task=
//
// Both are read-only views: the registry answers "what can this deployment
// actually do?", the evidence report answers "which conclusions have real
// evidence behind them?". Neither enforces anything (decision 5).

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/harness/capability"
	actool "github.com/Autumn-27/norma/tool"
)

// assembleCapabilityTools builds the HARNESS tool pool once (no stores needed —
// every constructor only closes over them) so the registry can report the real
// IsConcurrencySafe verdict of each handler. This is the same base pool the run
// uses, minus the per-run MCP/browser augmentation, which has no tools-table row.
func assembleCapabilityTools() []actool.CoreTool {
	ts := agent.NewToolSet(nil, "harness")
	tools := ts.MainAgentTools()
	tools = append(tools, actool.DefaultTools()...)
	return tools
}

// capabilityConcurrencyVerdicts reports a tool's concurrency safety with an empty
// input. Tools whose safety depends on arguments are reported conservatively: the
// run may still parallelise them, the registry just does not claim they are
// always safe.
func capabilityConcurrencyVerdicts() map[string]bool {
	out := map[string]bool{}
	for _, t := range assembleCapabilityTools() {
		out[t.Name()] = t.IsConcurrencySafe(json.RawMessage(`{}`))
	}
	return out
}

// collectCapabilities gathers the three DB/disk inputs and normalises them.
func (s *Server) collectCapabilities() []capability.Capability {
	var tools []capability.ToolRow
	if s.m != nil && s.m.pg != nil {
		rows, err := s.m.pg.ListTools()
		if err != nil {
			log.Printf("[capability] 读取工具表失败: %v", err)
		}
		for _, t := range rows {
			tools = append(tools, capability.ToolRow{
				Key:      t.Key,
				Kind:     t.Kind,
				Desc:     t.Description,
				Enabled:  t.Enabled,
				Agents:   t.Agents,
				Deferred: t.Deferred,
			})
		}
	}
	var mcps []capability.MCPRow
	if s.m != nil && s.m.pg != nil {
		servers, err := s.m.pg.ListMCP()
		if err != nil {
			log.Printf("[capability] 读取 MCP 表失败: %v", err)
		}
		for _, m := range servers {
			mcps = append(mcps, capability.MCPRow{
				Name:    m.Name,
				Enabled: m.Enabled,
				Tools:   len(m.Tools),
			})
		}
	}
	return capability.ClassifyRuntime(
		capability.Classify(tools, mcps, s.collectSkills()),
		capabilityConcurrencyVerdicts(),
	)
}

// collectSkills lists the skill bundles on disk. Skills are files, not rows, so
// this is a directory scan with the same bounded shape the rest of the skill
// surface uses (one directory per <name>/SKILL.md bundle).
func (s *Server) collectSkills() []capability.SkillRow {
	if strings.TrimSpace(s.skillDir) == "" {
		return nil
	}
	entries, err := os.ReadDir(s.skillDir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[capability] 读取 skill 目录失败: %v", err)
		}
		return nil
	}
	out := make([]capability.SkillRow, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files := countFiles(filepath.Join(s.skillDir, e.Name()), 200)
		out = append(out, capability.SkillRow{Name: e.Name(), Files: files})
	}
	return out
}

// countFiles counts regular files under dir, stopping at limit so a pathological
// tree cannot make the endpoint slow.
func countFiles(dir string, limit int) int {
	n := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip, don't fail the whole scan
		}
		if d.IsDir() {
			return nil
		}
		n++
		if n >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return n
	}
	return n
}

// harnessCapabilities reports every capability this deployment exposes.
func (s *Server) harnessCapabilities(w http.ResponseWriter, _ *http.Request) {
	caps := s.collectCapabilities()
	parallel, exclusive := capability.ConcurrencySummary(caps)
	writeJSON(w, http.StatusOK, map[string]any{
		"capabilities": caps,
		"summary":      capability.Summary(caps),
		"concurrency": map[string]int{
			"parallel":  parallel,
			"exclusive": exclusive,
		},
		"note": "能力注册表：harness 只把这些供给面统一成同一种『能力』，" +
			"不实现扫描/利用逻辑（判据见 docs/02 §6.1）。exec=true 表示它能在主机上执行代码或发起请求；" +
			"concurrency_safe=true 表示可与其它安全能力并行（由流式执行器按 FIFO+互斥规则调度）。" +
			"两者都是事实描述，不是风险评分。",
	})
}

// harnessEvidenceIntegrity reports which findings have bound traffic evidence.
//
// Report only — decision 5 forbids new hard constraints, and docs/02 §6.3 puts the
// evidence gate under the operator's judgement, not the platform's.
func (s *Server) harnessEvidenceIntegrity(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	taskID, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad task id")
		return
	}
	rep, err := s.m.pg.FindingEvidenceIntegrity(r.Context(), taskID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
