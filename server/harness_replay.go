package server

// Replay endpoint (S3).
//
//	GET  /api/harness/replay?task=&from=&to=&format=json|md
//
// Read-only over recorded history: it never touches the exploration state.
// (POST /api/harness/consistency and GET /api/harness/scenarios were deleted
// in S6-TRIM round 2: the Eval tab that used them was never wired.)

import (
	"net/http"
	"strconv"

	"github.com/Autumn-27/artex/harness/eval"
	"github.com/Autumn-27/artex/harness/replay"
)

// harnessReplay returns a task's replayable history.
func (s *Server) harnessReplay(w http.ResponseWriter, r *http.Request) {
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
	q := r.URL.Query()
	from := int64(atoiDefault(q.Get("from"), 0))
	to := int64(atoiDefault(q.Get("to"), 0))

	run, err := replay.Load(r.Context(), s.m.pg, taskID, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if q.Get("format") == "md" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(replay.RenderMarkdown(run)))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run":         run,
		"markdown":    replay.RenderMarkdown(run),
		"hash":        run.Hash(),
		"fingerprint": eval.Compute(run),
		"blocks":      eval.Blocks(run),
	})
}
