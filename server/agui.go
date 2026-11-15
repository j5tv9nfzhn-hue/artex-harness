package server

// AG-UI endpoint: exposes a task's recorded activity as an AG-UI event stream.
//
// Why this exists (docs/10 §4): the harness console is being replaced by an upstream
// agent UI (OpenBot, MIT) that speaks AG-UI — the public agent↔frontend protocol
// (ag-ui-protocol/ag-ui). Emitting AG-UI here means the frontend is a consumer of a
// documented spec instead of a re-implementation of somebody's private REST API:
// swap the UI again later and this endpoint does not change.
//
// AG-UI events used:
//   RUN_STARTED / RUN_FINISHED       — one task replay
//   TEXT_MESSAGE_START/CONTENT/END   — kind=text and kind=thinking
//   TOOL_CALL_START/ARGS/END/RESULT  — kind=tool_use paired with kind=tool_result
//   STEP_STARTED/STEP_FINISHED       — turn boundaries (harness_revisions)

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
)

type aguiEvent struct {
	Type    string         `json:"type"`
	ThreadD string         `json:"threadId,omitempty"`
	RunID   string         `json:"runId,omitempty"`
	MsgID   string         `json:"messageId,omitempty"`
	Role    string         `json:"role,omitempty"`
	Delta   string         `json:"delta,omitempty"`
	CallID  string         `json:"toolCallId,omitempty"`
	Name    string         `json:"toolCallName,omitempty"`
	Content string         `json:"content,omitempty"`
	Step    string         `json:"stepName,omitempty"`
	Err     string         `json:"message,omitempty"`
	Raw     map[string]any `json:"-"`
}

// aguiFromActivity projects one recorded activity onto zero or more AG-UI events.
// The projection is deliberately mechanical: the harness already records everything
// the UI needs, so the endpoint invents nothing.
func aguiFromActivity(a db.Activity, threadID, runID string, turnSeq int) []aguiEvent {
	msgID := fmt.Sprintf("m%d", a.ID)
	callID := fmt.Sprintf("t%d", a.ID)
	base := func(t string) aguiEvent {
		return aguiEvent{Type: t, ThreadD: threadID, RunID: runID}
	}
	switch a.Kind {
	case "text":
		return []aguiEvent{
			{Type: "TEXT_MESSAGE_START", ThreadD: threadID, RunID: runID, MsgID: msgID, Role: "assistant"},
			{Type: "TEXT_MESSAGE_CONTENT", ThreadD: threadID, RunID: runID, MsgID: msgID, Delta: a.Summary},
			{Type: "TEXT_MESSAGE_END", ThreadD: threadID, RunID: runID, MsgID: msgID},
		}
	case "thinking":
		return []aguiEvent{{
			Type: "TEXT_MESSAGE_CONTENT", ThreadD: threadID, RunID: runID,
			MsgID: msgID, Delta: a.Summary,
		}}
	case "tool_use":
		return []aguiEvent{
			{Type: "TOOL_CALL_START", ThreadD: threadID, RunID: runID, MsgID: msgID, CallID: callID, Name: a.Tool},
			{Type: "TOOL_CALL_ARGS", ThreadD: threadID, RunID: runID, CallID: callID, Delta: strings.TrimSpace(a.Detail)},
			{Type: "TOOL_CALL_END", ThreadD: threadID, RunID: runID, CallID: callID},
		}
	case "tool_result":
		status := "ok"
		if a.IsError {
			status = "error"
		}
		return []aguiEvent{
			{
				Type: "TOOL_CALL_RESULT", ThreadD: threadID, RunID: runID,
				MsgID: msgID, CallID: callID, Content: a.Summary,
				Raw: map[string]any{"status": status},
			},
		}
	case "compact":
		return []aguiEvent{{
			Type: "TOOL_CALL_RESULT", ThreadD: threadID, RunID: runID,
			MsgID: msgID, CallID: callID, Content: a.Summary,
			Raw: map[string]any{"status": "compacted"},
		}}
	}
	_ = base
	return nil
}

// aguiAgent serves GET /api/agui/agent?task=N as an SSE stream: the whole recorded
// history, then RUN_FINISHED. A live run can later append while streaming.
func (s *Server) aguiAgent(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("task"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "task is required")
		return
	}
	t, ok := s.m.Task(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	acts, _, err := t.Store.ActivityList(nil, 0, 2000)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)

	send := func(ev aguiEvent) {
		b, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	threadID := "task-" + t.ID
	runID := "run-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	send(aguiEvent{Type: "RUN_STARTED", ThreadD: threadID, RunID: runID})

	// One STEP per turn keeps the upstream UI's turn grouping intact.
	rev := int64(0)
	open := false
	for _, a := range acts {
		if a.Kind == "lifecycle" || a.Kind == "result" {
			continue
		}
		if !open {
			rev++
			send(aguiEvent{Type: "STEP_STARTED", ThreadD: threadID, RunID: runID,
				Step: fmt.Sprintf("turn-%d", rev)})
			open = true
		}
		// ActivityList omits Detail (it is json:"-" and lazy-loaded), but a tool call
		// without its arguments renders as an empty card upstream, so fetch it here.
		if a.Kind == "tool_use" && strings.TrimSpace(a.Detail) == "" {
			if d, derr := t.Store.ActivityDetail(a.ID); derr == nil {
				a.Detail = d
			}
		}
		for _, ev := range aguiFromActivity(a, threadID, runID, int(rev)) {
			send(ev)
		}
	}
	if open {
		send(aguiEvent{Type: "STEP_FINISHED", ThreadD: threadID, RunID: runID,
			Step: fmt.Sprintf("turn-%d", rev)})
	}
	send(aguiEvent{Type: "RUN_FINISHED", ThreadD: threadID, RunID: runID})
}
