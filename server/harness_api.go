package server

// Harness protocol HTTP surface (S1).
//
// Three read-only endpoints, deliberately separate from the legacy
// /api/exploration/* routes so the existing UI keeps working untouched:
//
//	GET /api/harness/thread?task=ID          → thread identity + revision bounds
//	GET /api/harness/revisions?task=ID&since= → Turn list (catch-up for a client)
//	GET /api/harness/stream?task=ID&since=   → SSE envelope stream
//
// The stream's SSE `id:` is the ITEM's activity id (so Last-Event-ID resume works
// exactly like the legacy stream), while each payload carries `rev` — the client
// uses `rev` for grouping and the id for resuming. Two different cursors on
// purpose: activity ids are global and dense, revisions are per-turn and semantic.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/Autumn-27/artex/harness/protocol"
)

// harnessThread reports the thread identity and the revision window a client can
// ask for. `latest` is what a fresh client should start from.
func (s *Server) harnessThread(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	min, max, err := s.m.pg.RevisionRange(taskID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"thread":     protocol.Thread{TaskID: t.ID, ExplorationID: t.ExpID},
		"latest":     max,
		"first":      min,
		"current":    s.engine.CurrentTurn(t.ID),
		"status":     t.Status,
		"paused":     t.Paused,
		"revision":   max,
		"can_stream": true,
	})
}

// harnessRevisions returns Turn boundaries after `since`.
func (s *Server) harnessRevisions(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	since := int64(atoiDefault(r.URL.Query().Get("since"), 0))
	limit := atoiDefault(r.URL.Query().Get("limit"), 200)
	rows, err := s.m.pg.ListRevisions(taskID, since, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	turns := make([]protocol.Turn, 0, len(rows))
	for _, rv := range rows {
		turns = append(turns, protocol.Turn{
			Revision: rv.Revision,
			Kind:     rv.TurnKind,
			Ref:      rv.TurnRef,
			Summary:  rv.Summary,
			At:       rv.CreatedAt,
		})
	}
	_, latest, _ := s.m.pg.RevisionRange(taskID)
	writeJSON(w, http.StatusOK, protocol.RevisionsResponse{
		Task:      t.ID,
		Since:     since,
		Revisions: turns,
		Latest:    latest,
		HasMore:   len(rows) == limit,
	})
}

// harnessStream is the SSE envelope stream.
//
// Order of operations mirrors the legacy stream for the same reason: subscribe
// BEFORE the backlog replay so events landing in between are not lost, then skip
// the ones the replay already covered.
func (s *Server) harnessStream(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	// Cursor precedence matches the legacy stream: the browser's auto-reconnect
	// Last-Event-ID (an activity id) wins over ?since=, which a fresh client uses
	// to start from a snapshot position.
	sinceItem := int64(atoiDefault(r.URL.Query().Get("since_item"), 0))
	if le := r.Header.Get("Last-Event-ID"); le != "" {
		if n, err := strconv.ParseInt(le, 10, 64); err == nil {
			sinceItem = n
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := s.engine.Broadcaster().SubscribeEnvelopes(t.ID)
	defer unsub()

	taskID, _ := strconv.ParseInt(t.ID, 10, 64)

	send := func(env protocol.Envelope, itemID int64) {
		b, _ := json.Marshal(env)
		// event: harness lets a client attach a typed listener while still
		// receiving everything on the default message channel.
		fmt.Fprintf(w, "event: harness\nid: %d\ndata: %s\n\n", itemID, b)
		flusher.Flush()
	}

	// Backlog: map activity ids → their revision, then wrap each row. Rows that
	// predate revisions (rev 0) are still emitted — the client treats rev 0 as a
	// legacy prefix rather than dropping history.
	const batch = 500
	replayed := int64(0)
	for {
		acts, cursor, err := t.Store.ActivityList(nil, sinceItem, batch)
		if err != nil {
			log.Printf("[harness/stream] task=%s 回放 since=%d: %v", t.ID, sinceItem, err)
			return
		}
		for _, a := range acts {
			rev := int64(0)
			if s.m.pg != nil {
				rev = s.m.pg.TurnOfActivity(taskID, a.ID)
			}
			turn := protocol.Turn{Revision: rev, At: a.CreatedAt}
			if rev > 0 {
				if rows, err := s.m.pg.ListRevisions(taskID, rev-1, 1); err == nil && len(rows) > 0 && rows[0].Revision == rev {
					turn.Kind, turn.Ref, turn.Summary = rows[0].TurnKind, rows[0].TurnRef, rows[0].Summary
				}
			}
			send(protocol.FromActivity(t.ID, t.ExpID, turn, 0, a), a.ID)
			replayed++
		}
		if cursor > sinceItem {
			sinceItem = cursor
		}
		if len(acts) < batch {
			break
		}
	}
	flusher.Flush()

	ctx := r.Context()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-ch:
			if !ok {
				return
			}
			// Dedup: the backlog replay already covered everything up to
			// sinceItem, and the broadcaster buffers live events meanwhile.
			if id := env.Item.ActivityID; id > 0 && id <= sinceItem {
				continue
			}
			send(env, env.Item.ActivityID)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
