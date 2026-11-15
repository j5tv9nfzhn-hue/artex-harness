package server

// Cross-session search (Sprint 2).
//
//	GET /api/harness/search?q=&task=&limit=
//
// task is optional: with it, the search is scoped to that task's exploration
// stream; without it, the caller must pass an explicit task list is NOT accepted
// — to keep the query bounded we search the resolved task only. The console uses
// the active task and offers "search all" by iterating its own task list.

import (
	"net/http"
)

func (s *Server) harnessSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}, "note": "q 为空"})
		return
	}
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 100)
	acts, err := t.Store.ActivitySearch(q, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type hit struct {
		Task      string `json:"task"`
		ID        int64  `json:"id"`
		Kind      string `json:"kind"`
		Tool      string `json:"tool,omitempty"`
		Worker    string `json:"worker,omitempty"`
		Summary   string `json:"summary"`
		Detail    string `json:"detail,omitempty"`
		IsError   bool   `json:"is_error,omitempty"`
		CreatedAt string `json:"created_at,omitempty"`
	}
	out := make([]hit, 0, len(acts))
	for _, a := range acts {
		h := hit{
			Task: t.ID, ID: a.ID, Kind: a.Kind, Tool: a.Tool, Worker: a.Worker,
			Summary: a.Summary, Detail: a.Detail, IsError: a.IsError,
		}
		if !a.CreatedAt.IsZero() {
			h.CreatedAt = a.CreatedAt.Format("2006-01-02 15:04:05")
		}
		out = append(out, h)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task": t.ID, "q": q, "count": len(out), "results": out,
		"limit": limit,
		"note":  "搜索 summary 与完整 detail（大小写不敏感）；detail 是正文，summary 只是首行索引。",
	})
}
