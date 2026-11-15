package server

// 子代理会话详情。
//
// 为什么单独一个文件：一次委派的**明细不在 activity 表里**——activity 只有父会话的
// 事件流（worker=harness）。子代理自己的那串消息写在磁盘 transcript 的 sidechain：
//
//	<dataDir>/transcripts/exp<EXP>-harness/subagents/agent-<agentID>.jsonl
//
// （写入方见 third_party/norma/transcript：主会话与每个子代理分开存，便于单独计费。）
// 所以控制台要"点进一个子代理看它做了什么"，只能从这里读。
//
// 关联方式：activity 行里没有 agentID，只能拿委派提示词去和 sidechain 文件的第一条
// user 消息比对（提示词可能被 activity 侧截断到 160 字，所以用前缀双向包含匹配）。

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
)

// subagentStep 是摊平后的一步：文本 / 思考 / 工具调用（结果就地挂在调用上）。
type subagentStep struct {
	Role     string `json:"role"`
	TS       string `json:"ts,omitempty"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	Tool     string `json:"tool,omitempty"`
	ToolID   string `json:"tool_id,omitempty"`
	Args     string `json:"args,omitempty"`
	Result   string `json:"result,omitempty"`
	IsError  bool   `json:"is_error,omitempty"`
}

// subagentStore 返回本服务的 transcript 目录。
func (s *Server) subagentStore() *transcript.Store {
	return transcript.NewStore(filepath.Join(s.m.dir, "transcripts"))
}

// subagentSessionID 是 harness 主会话的 id，子代理 sidechain 挂在它下面。
func subagentSessionID(expID int64) string {
	return fmt.Sprintf("exp%d-harness", expID)
}

// subagentMeta 是 sidechain 文件的识别信息：首条 user 消息（= 派发提示词）与它的时间。
type subagentMeta struct {
	FirstText string
	FirstAt   time.Time
}

// subagentIndex 扫一次 sidechain 目录，返回 agentID → 识别信息。
// 只有子代理真正跑过（写过 transcript）才会出现。
func (s *Server) subagentIndex(expID int64) map[string]subagentMeta {
	out := map[string]subagentMeta{}
	session := subagentSessionID(expID)
	dir := filepath.Join(s.m.dir, "transcripts", session, "subagents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	tx := s.subagentStore()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
		recs, err := tx.Load(filepath.Join(dir, name))
		if err != nil || len(recs) == 0 {
			continue
		}
		meta := subagentMeta{}
		if ts, terr := time.Parse(time.RFC3339Nano, recs[0].Timestamp); terr == nil {
			meta.FirstAt = ts
		}
		for _, r := range recs {
			if r.Type != "message" || r.Message == nil || string(r.Message.Role) != "user" {
				continue
			}
			for _, b := range r.Message.Content {
				if strings.TrimSpace(b.Text) != "" {
					meta.FirstText = strings.TrimSpace(b.Text)
					break
				}
			}
			break
		}
		out[id] = meta
	}
	return out
}

// matchSubagent 用提示词把一条 delegation 对到它的 sidechain 文件。
// activity 侧的提示词可能被截断，所以按前缀互相包含来判断；同一提示词跑过多次时，
// 取开始时间最接近的那一个（否则会随机配到别的轮次上）。
func matchSubagent(idx map[string]subagentMeta, prompt string, startedAt time.Time) string {
	p := strings.TrimSpace(prompt)
	if p == "" {
		return ""
	}
	cand := make([]string, 0, len(idx))
	for id, m := range idx {
		if m.FirstText == p {
			cand = append(cand, id)
		}
	}
	if len(cand) == 0 {
		key := func(a, b string) bool {
			short, long := a, b
			if len(short) > len(long) {
				short, long = long, short
			}
			short = strings.TrimSpace(strings.TrimSuffix(short, "已截断"))
			if len(short) > 40 {
				short = short[:40]
			}
			return len(short) >= 12 && strings.Contains(long, short)
		}
		for id, m := range idx {
			if key(p, m.FirstText) {
				cand = append(cand, id)
			}
		}
	}
	if len(cand) == 0 {
		return ""
	}
	if len(cand) == 1 || startedAt.IsZero() {
		return cand[0]
	}
	best, bestDelta := cand[0], time.Duration(1<<62)
	for _, id := range cand {
		at := idx[id].FirstAt
		if at.IsZero() {
			continue
		}
		d := at.Sub(startedAt)
		if d < 0 {
			d = -d
		}
		if d < bestDelta {
			best, bestDelta = id, d
		}
	}
	return best
}

// harnessSubagentDetail 服务 GET /api/harness/subagent/{agent}?task=N
func (s *Server) harnessSubagentDetail(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	agentID := strings.TrimSpace(r.PathValue("agent"))
	if agentID == "" || strings.ContainsAny(agentID, "/\\") {
		writeErr(w, http.StatusBadRequest, "agent is required")
		return
	}
	session := subagentSessionID(t.ExpID)
	path := s.subagentStore().AgentPath(session, agentID)
	recs, err := s.subagentStore().Load(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(recs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"task": t.ID, "agent_id": agentID, "session": session,
			"steps": []subagentStep{},
			"note":  "这个子代理还没有落盘的会话记录（可能刚派发，或运行在旧版本上）。",
		})
		return
	}
	steps := stepsFromRecords(recs)
	writeJSON(w, http.StatusOK, map[string]any{
		"task": t.ID, "agent_id": agentID, "session": session,
		"steps": steps,
	})
}

// stepsFromRecords 把 transcript 记录摊平成前端能直接渲染的一串步骤，并把
// tool_result 合并回它对应的 tool_use（按 tool_use_id），一次调用只出一张卡。
func stepsFromRecords(recs []transcript.Record) []subagentStep {
	out := make([]subagentStep, 0, len(recs))
	callIndex := map[string]int{}
	for _, r := range recs {
		if r.Type != "message" || r.Message == nil {
			continue
		}
		role := string(r.Message.Role)
		for _, b := range r.Message.Content {
			switch string(b.Type) {
			case "text":
				if strings.TrimSpace(b.Text) == "" {
					continue
				}
				out = append(out, subagentStep{Role: role, TS: r.Timestamp, Text: b.Text})
			case "thinking":
				if strings.TrimSpace(b.Thinking) == "" {
					continue
				}
				out = append(out, subagentStep{Role: role, TS: r.Timestamp, Thinking: b.Thinking})
			case "tool_use":
				callIndex[b.ID] = len(out)
				out = append(out, subagentStep{
					Role: role, TS: r.Timestamp, Tool: b.Name, ToolID: b.ID,
					Args: strings.TrimSpace(string(b.Input)),
				})
			case "tool_result":
				text := blockTexts(b.Content)
				if i, ok := callIndex[b.ToolUseID]; ok {
					out[i].Result = text
					out[i].IsError = b.IsError
					continue
				}
				out = append(out, subagentStep{Role: role, TS: r.Timestamp, ToolID: b.ToolUseID, Result: text, IsError: b.IsError})
			}
		}
	}
	return out
}

// blockTexts 把一个 tool_result 的嵌套内容摊平成字符串。
func blockTexts(bs []llm.ContentBlock) string {
	parts := make([]string, 0, len(bs))
	for _, b := range bs {
		if strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
