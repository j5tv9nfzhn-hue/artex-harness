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
//   RUN_STARTED / RUN_FINISHED       — one connection's replay+follow window
//   TEXT_MESSAGE_START/CONTENT/END   — kind=text and kind=thinking
//   TOOL_CALL_START/ARGS/END/RESULT  — kind=tool_use paired with kind=tool_result
//   STEP_STARTED/STEP_FINISHED       — turn boundaries (one step per turn)
//
// Resuming (added for long runs):
//   GET /api/agui/agent?task=N&since=<seq>&follow=1
//
//   since   only activities with id > since are sent, so a client that lost the
//           connection reconnects and asks for exactly what it missed instead of
//           replaying a multi-thousand-event transcript each time.
//   follow  1 = keep the connection open, poll for new activities, and heartbeat
//           while the task is alive. Without it the handler replays and returns.
//
// Every event carries `seq` (the activity id it came from), which is what the
// client echoes back as `since`. A bare `: ping` comment frame is written every
// heartbeatInterval so a long silent tool run cannot be mistaken for a dead
// connection by a proxy or by the browser.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
)

const (
	// aguiPageSize bounds one read; a replay of a long task arrives in pages.
	aguiPageSize = 500
	// aguiPollInterval is how often a following connection looks for new rows.
	aguiPollInterval = 800 * time.Millisecond
	// aguiHeartbeat keeps idle connections alive through proxies.
	aguiHeartbeat = 15 * time.Second
	// aguiMaxFollow bounds one connection's lifetime so a forgotten tab cannot
	// hold a handler forever; the client reconnects and resumes from `since`.
	aguiMaxFollow = 30 * time.Minute
)

type aguiEvent struct {
	Type    string `json:"type"`
	ThreadD string `json:"threadId,omitempty"`
	RunID   string `json:"runId,omitempty"`
	MsgID   string `json:"messageId,omitempty"`
	Role    string `json:"role,omitempty"`
	Delta   string `json:"delta,omitempty"`
	CallID  string `json:"toolCallId,omitempty"`
	Name    string `json:"toolCallName,omitempty"`
	Content string `json:"content,omitempty"`
	Step    string `json:"stepName,omitempty"`
	Err     string `json:"message,omitempty"`
	// Status carries ok | error | compacted for tool results. It used to live in a
	// non-serialized Raw map, so the client could never tell a failed tool call
	// from a successful one.
	Status string `json:"status,omitempty"`
	// Seq is the activity id this event was projected from — the client's resume
	// cursor. 0 for synthetic frames (RUN_STARTED / STEP_* / heartbeat).
	Seq int64 `json:"seq,omitempty"`
}

// aguiFromActivity projects one recorded activity onto zero or more AG-UI events.
// The projection is deliberately mechanical: the harness already records everything
// the UI needs, so the endpoint invents nothing.
func aguiFromActivity(a db.Activity, threadID, runID string, turnSeq int) []aguiEvent {
	msgID := fmt.Sprintf("m%d", a.ID)
	callID := fmt.Sprintf("t%d", a.ID)
	// Summary 是 firstLine(s,200)，只够做索引；正文在 Detail。
	body := func() string {
		if strings.TrimSpace(a.Detail) != "" {
			return a.Detail
		}
		return a.Summary
	}
	switch a.Kind {
	case "text":
		return []aguiEvent{
			{Type: "TEXT_MESSAGE_START", ThreadD: threadID, RunID: runID, MsgID: msgID, Role: "assistant", Seq: a.ID},
			{Type: "TEXT_MESSAGE_CONTENT", ThreadD: threadID, RunID: runID, MsgID: msgID, Delta: body(), Seq: a.ID},
			{Type: "TEXT_MESSAGE_END", ThreadD: threadID, RunID: runID, MsgID: msgID, Seq: a.ID},
		}
	case "thinking":
		// 思考块以前只发 CONTENT、没有 START：客户端无法知道这是思考，只能当普通正文
		// 渲染，于是整段推理和一问一答混在一起，看起来就像"话说不完整"。
		return []aguiEvent{
			{Type: "TEXT_MESSAGE_START", ThreadD: threadID, RunID: runID, MsgID: msgID, Role: "thinking", Seq: a.ID},
			{Type: "TEXT_MESSAGE_CONTENT", ThreadD: threadID, RunID: runID, MsgID: msgID, Delta: body(), Seq: a.ID},
			{Type: "TEXT_MESSAGE_END", ThreadD: threadID, RunID: runID, MsgID: msgID, Seq: a.ID},
		}
	case "tool_use":
		return []aguiEvent{
			{Type: "TOOL_CALL_START", ThreadD: threadID, RunID: runID, MsgID: msgID, CallID: callID, Name: a.Tool, Seq: a.ID},
			{Type: "TOOL_CALL_ARGS", ThreadD: threadID, RunID: runID, CallID: callID, Delta: strings.TrimSpace(a.Detail), Seq: a.ID},
			{Type: "TOOL_CALL_END", ThreadD: threadID, RunID: runID, CallID: callID, Seq: a.ID},
		}
	case "tool_result":
		status := "ok"
		if a.IsError {
			status = "error"
		}
		return []aguiEvent{{
			Type: "TOOL_CALL_RESULT", ThreadD: threadID, RunID: runID,
			MsgID: msgID, CallID: callID, Content: a.Summary, Status: status, Seq: a.ID,
		}}
	case "compact":
		return []aguiEvent{{
			Type: "TOOL_CALL_RESULT", ThreadD: threadID, RunID: runID,
			MsgID: msgID, CallID: callID, Content: a.Summary, Status: "compacted", Seq: a.ID,
		}}
	}
	return nil
}

// taskIsLive reports whether a task may still produce activities.
//
// The DB column `tasks.status` only moves on *terminal* transitions, so a task that
// is actively running still reads "created" there. The derived status
// (resolvedTaskStatus) is the one the console shows, and the only safe answer to
// "will more events arrive?".
func (s *Server) taskIsLive(t *Task) bool {
	if t == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(s.resolvedTaskStatus(t))) {
	case "done", "failed", "timeout", "cancelled", "archived", "deleted":
		return false
	}
	return true
}

// aguiAgent serves GET /api/agui/agent as an SSE stream: replay from `since`, then
// (with follow=1) keep streaming new activities until the task reaches a terminal
// state and goes quiet.
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
	since, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("since")), 10, 64)
	if since < 0 {
		since = 0
	}
	follow := r.URL.Query().Get("follow") == "1"

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)

	threadID := "task-" + t.ID
	runID := "run-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	send := func(ev aguiEvent) bool {
		b, _ := json.Marshal(ev)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	heartbeat := func() bool {
		if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}

	send(aguiEvent{Type: "RUN_STARTED", ThreadD: threadID, RunID: runID, Seq: since})

	// One STEP per turn keeps the upstream UI's turn grouping intact. `open` is
	// carried across polls so a step that straddles two reads stays one step.
	rev := 0
	open := false
	openStep := func() {
		rev++
		open = true
		send(aguiEvent{Type: "STEP_STARTED", ThreadD: threadID, RunID: runID,
			Step: fmt.Sprintf("turn-%d", rev)})
	}
	closeStep := func() {
		if !open {
			return
		}
		open = false
		send(aguiEvent{Type: "STEP_FINISHED", ThreadD: threadID, RunID: runID,
			Step: fmt.Sprintf("turn-%d", rev)})
	}

	// emitPage drains one page of activities starting after cursor.
	emitPage := func(cursor int64) (int64, bool) {
		for {
			acts, next, err := t.Store.ActivityList(nil, cursor, aguiPageSize)
			if err != nil {
				send(aguiEvent{Type: "RUN_ERROR", ThreadD: threadID, RunID: runID, Err: err.Error()})
				return cursor, false
			}
			for _, a := range acts {
				if a.Kind == "lifecycle" {
					continue
				}
				if a.Kind == "result" {
					closeStep()
					continue
				}
				if !open {
					openStep()
				}
				// ActivityList omits Detail (it is json:"-" and lazy-loaded), but the UI needs
				// the FULL text: the recorded Summary is only the first line, capped at 200
				// runes. Without this every assistant message arrives cut in half.
				if (a.Kind == "tool_use" || a.Kind == "text" || a.Kind == "thinking") && strings.TrimSpace(a.Detail) == "" {
					if d, derr := t.Store.ActivityDetail(a.ID); derr == nil {
						a.Detail = d
					}
				}
				for _, ev := range aguiFromActivity(a, threadID, runID, rev) {
					if !send(ev) {
						return next, false
					}
				}
			}
			cursor = next
			if len(acts) < aguiPageSize {
				return cursor, true
			}
		}
	}

	cursor, okStream := emitPage(since)
	if !okStream {
		return
	}
	if !follow {
		if open {
			closeStep()
		}
		send(aguiEvent{Type: "RUN_FINISHED", ThreadD: threadID, RunID: runID, Seq: cursor})
		return
	}

	poll := time.NewTicker(aguiPollInterval)
	defer poll.Stop()
	hb := time.NewTicker(aguiHeartbeat)
	defer hb.Stop()
	deadline := time.After(aguiMaxFollow)
	// idle counts polls that found nothing; a terminal task that has been quiet for
	// a couple of polls is finished, and the client is told so it can stop retrying.
	idle := 0

	for {
		select {
		case <-r.Context().Done():
			return
		case <-deadline:
			// Not an error: the client resumes with since=cursor on a fresh socket.
			if open {
				closeStep()
			}
			send(aguiEvent{Type: "RUN_FINISHED", ThreadD: threadID, RunID: runID, Seq: cursor})
			return
		case <-hb.C:
			if !heartbeat() {
				return
			}
		case <-poll.C:
			before := cursor
			var okPage bool
			cursor, okPage = emitPage(cursor)
			if !okPage {
				return
			}
			if cursor != before {
				idle = 0
				continue
			}
			idle++
			if !s.taskIsLive(t) && idle >= 2 {
				if open {
					closeStep()
				}
				send(aguiEvent{Type: "RUN_FINISHED", ThreadD: threadID, RunID: runID, Seq: cursor})
				return
			}
		}
	}
}
