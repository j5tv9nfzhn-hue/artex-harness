package server

// Intervention primitives (harness S2). This is the highest-value stage: it turns
// "steering the agent" from a request the MODEL has to translate into a
// deterministic API the HUMAN calls directly.
//
// Upstream already had the machinery — SteerWork, KillWork, ControlWork,
// SoftDeleteIntent — but only as tools bound to the planner/main agent
// (docs/01 §3 ③: "运行中纠偏无接口"). These handlers expose the same engine
// methods over HTTP, so a course correction no longer requires a model to choose
// to call steer_work, nor a pause that aborts the in-flight turn.
//
// Deliberate design constraints (see docs/adr/0003-capability-boundary.md):
//   - These only ADD levers for the human; none of them changes what the agent
//     does by default, so they add no restriction and no extra gating.
//   - They never bypass the engine's lifecycle locks: every one of them goes
//     through the same Engine methods the agents use, so pause/delete/settle
//     races behave exactly as they already do.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
)

const maxSteerMessageRunes = 4000

// interventionDTO is the uniform response of every primitive: what was acted on,
// what happened, and the revision the action lands in (so a client can jump
// straight to the turn boundary it just created).
type interventionDTO struct {
	Task     string `json:"task"`
	IntentID int64  `json:"intent_id,omitempty"`
	Action   string `json:"action"`
	State    string `json:"state,omitempty"`
	Priority *int   `json:"priority,omitempty"`
	Revision int64  `json:"revision,omitempty"`
	Applied  int    `json:"applied,omitempty"`
}

// resolveIntent loads (task, intent) from the request path, writing the error
// response itself so every primitive reports the same way.
func (s *Server) resolveIntent(w http.ResponseWriter, r *http.Request) (*Task, int64, *db.Node, bool) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return nil, 0, nil, false
	}
	iid, err := strconv.ParseInt(r.PathValue("iid"), 10, 64)
	if err != nil || iid <= 0 {
		writeErr(w, http.StatusBadRequest, "bad intent id")
		return nil, 0, nil, false
	}
	node, err := t.Store.GetNode(iid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return nil, 0, nil, false
	}
	if node == nil {
		if inherited, srcErr := t.Store.GetNodeWithSources(iid); srcErr == nil && inherited != nil && inherited.Inherited {
			writeErr(w, http.StatusConflict, "继承意图为只读，不能干预")
			return nil, 0, nil, false
		}
		writeErr(w, http.StatusNotFound, "intent not found")
		return nil, 0, nil, false
	}
	if node.Kind != db.KindIntent {
		writeErr(w, http.StatusConflict, "node is not an intent")
		return nil, 0, nil, false
	}
	return t, iid, node, true
}

// normalizeSteerMessage trims and bounds a course-correction message.
func normalizeSteerMessage(raw string) (string, error) {
	msg := strings.TrimSpace(raw)
	if msg == "" {
		return "", fmt.Errorf("纠偏消息不能为空")
	}
	if len([]rune(msg)) > maxSteerMessageRunes {
		return "", fmt.Errorf("纠偏消息不能超过 %d 个字符", maxSteerMessageRunes)
	}
	return msg, nil
}

// InterveneSteer injects a mid-run course correction WITHOUT interrupting the
// work. The worker's PreToolUse hook drains it before its next tool call and
// re-plans — so momentum is preserved (contrast: pause + message aborts the turn).
func (s *Server) interveneSteer(w http.ResponseWriter, r *http.Request) {
	t, iid, _, ok := s.resolveIntent(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	msg, err := normalizeSteerMessage(req.Message)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.engine.SteerWork(iid, msg); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	// Record the human turn so it appears in the transcript/replay with its own
	// revision, instead of being invisible to anyone who reads the log later.
	rev := s.openHumanTurn(t, "steer#"+strconv.FormatInt(iid, 10),
		"人工纠偏 · 意图 #"+strconv.FormatInt(iid, 10), msg)
	writeJSON(w, http.StatusOK, interventionDTO{
		Task: t.ID, IntentID: iid, Action: "steer", State: "running", Revision: rev,
	})
}

// InterveneKill stops one running work without touching the rest of the task.
// (Upstream had this only as the planner's kill_work tool.)
func (s *Server) interveneKill(w http.ResponseWriter, r *http.Request) {
	t, iid, node, ok := s.resolveIntent(w, r)
	if !ok {
		return
	}
	if err := s.engine.KillWork(iid); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	rev := s.openHumanTurn(t, "kill#"+strconv.FormatInt(iid, 10),
		"人工停止 · 意图 #"+strconv.FormatInt(iid, 10), "由人停止该 work")
	writeJSON(w, http.StatusOK, interventionDTO{
		Task: t.ID, IntentID: iid, Action: "kill", State: node.State, Revision: rev,
	})
}

// InterveneEditIntent rewrites an intent's content (summary / asset_ids /
// priority). This is the primitive upstream is completely missing (docs/01 §3 ①):
// there was no UPDATE path for intent content at all.
//
// A running work keeps the prompt it started with — the edit lands for the next
// round, which is documented behaviour, not a silent no-op.
func (s *Server) interveneEditIntent(w http.ResponseWriter, r *http.Request) {
	t, iid, _, ok := s.resolveIntent(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var patch db.IntentPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	node, err := t.Store.UpdateIntentContent(iid, patch)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrIntentEditEmpty):
			writeErr(w, http.StatusBadRequest, "没有要修改的字段（summary / asset_ids / priority 至少给一个）")
		case errors.Is(err, db.ErrIntentNotEditable):
			writeErr(w, http.StatusConflict, err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	// A re-prioritised or re-scoped direction should be considered immediately, so
	// wake the planner (upstream has no way to request this).
	t.Notify()
	rev := s.openHumanTurn(t, "edit#"+strconv.FormatInt(iid, 10),
		"人工修改意图 · #"+strconv.FormatInt(iid, 10), "意图内容被人工修改")
	prio := node.Priority
	writeJSON(w, http.StatusOK, interventionDTO{
		Task: t.ID, IntentID: iid, Action: "edit", State: node.State,
		Priority: &prio, Revision: rev,
	})
}

// InterveneOrder rewrites the frontier order. Implemented as strictly decreasing
// priorities, because the frontier query is already `priority DESC, id ASC` — no
// schema change, no change to the claim path.
func (s *Server) interveneOrder(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if len(req.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "ids 不能为空")
		return
	}
	if len(req.IDs) > 500 {
		writeErr(w, http.StatusBadRequest, "一次最多重排 500 条意图")
		return
	}
	top, err := t.Store.OrderFrontier(r.Context(), req.IDs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	t.Notify()
	rev := s.openHumanTurn(t, "order", "人工重排 frontier", fmt.Sprintf("按给定顺序重排 %d 条意图", len(req.IDs)))
	prio := top
	writeJSON(w, http.StatusOK, interventionDTO{
		Task: t.ID, Action: "order", Applied: len(req.IDs), Priority: &prio, Revision: rev,
	})
}

// InterveneReplan asks for a planning round NOW. Upstream only wakes the planner
// on a graph change or on the heartbeat (>=10 min), so "re-judge the goal now" was
// impossible without touching the graph first.
func (s *Server) interveneReplan(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	if s.engine.IsPaused(t.ID) {
		writeErr(w, http.StatusConflict, "任务已暂停：恢复后再触发规划")
		return
	}
	if s.engine.IsDeleting(t.ID) {
		writeErr(w, http.StatusConflict, "任务正在删除")
		return
	}
	rev := s.openHumanTurn(t, "replan", "人工触发重规划", "由人请求立即重判目标并补方向")
	t.Notify()
	writeJSON(w, http.StatusOK, interventionDTO{Task: t.ID, Action: "replan", Revision: rev})
}

// IntervenePauseAll is the global stop button: pause every running task. Exists
// because "stop everything now" had no single entry point (only per-task pause, or
// the 100-item batch endpoint that still requires listing the ids).
func (s *Server) intervenePauseAll(w http.ResponseWriter, r *http.Request) {
	tasks := s.m.List()
	paused := 0
	failed := make([]string, 0)
	for _, t := range tasks {
		if isTerminalStatus(t.Status) || t.Paused || s.engine.IsDeleting(t.ID) {
			continue
		}
		if _, err := s.applyTaskControl(t, "pause"); err != nil {
			failed = append(failed, t.ID+": "+err.Error())
			continue
		}
		paused++
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"action": "pause-all",
		"paused": paused,
		"failed": failed,
	})
}

// openHumanTurn records a human action as its own Turn, so the intervention has a
// revision and shows up in the transcript / replay alongside agent turns.
func (s *Server) openHumanTurn(t *Task, ref, summary, detail string) int64 {
	turn := s.engine.OpenTurn(t, db.TurnSystem, ref, summary)
	s.engine.emitActivity(t, db.Activity{
		Worker: "human", Kind: "lifecycle", Summary: summary, Detail: detail,
	})
	return turn.Revision
}

// Interventions lists the primitives for the console's capability discovery, so
// the UI does not hard-code which levers exist.
func (s *Server) interventionsIndex(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"primitives": []map[string]any{
			{"name": "steer", "method": "POST", "path": "/api/tasks/{id}/intents/{iid}/steer", "note": "运行中纠偏，不中断当前轮"},
			{"name": "kill", "method": "POST", "path": "/api/tasks/{id}/intents/{iid}/kill", "note": "停止单条 work"},
			{"name": "edit-intent", "method": "PATCH", "path": "/api/tasks/{id}/intents/{iid}", "note": "改 summary / asset_ids / priority"},
			{"name": "order", "method": "POST", "path": "/api/tasks/{id}/frontier/order", "note": "重排 frontier，按给定顺序"},
			{"name": "replan", "method": "POST", "path": "/api/tasks/{id}/replan", "note": "立即触发一轮规划"},
			{"name": "pause-all", "method": "POST", "path": "/api/tasks/pause-all", "note": "全局急停"},
		},
	})
}
