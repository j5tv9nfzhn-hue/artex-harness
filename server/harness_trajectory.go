package server

// Trajectory view endpoint (Sprint 2).
//
//	GET /api/harness/trajectory?task=ID&from=&to=&agent=
//
// It is a read-only projection of the SAME replay history the consistency gate
// consumes (harness/replay.Load), so the trajectory can never disagree with what
// was judged. `agent=` narrows the view to one delegated agent's own rows.

import (
	"net/http"
	"strconv"

	"github.com/Autumn-27/artex/harness/replay"
	"github.com/Autumn-27/artex/harness/trajectory"
)

func (s *Server) harnessTrajectory(w http.ResponseWriter, r *http.Request) {
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
	view := trajectory.Build(run)
	if agent := q.Get("agent"); agent != "" {
		view = trajectory.Filter(view, agent)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"view":            view,
		"system_versions": trajectory.SystemPromptVersions(run),
		"subagents":       trajectory.Subagents(run),
		"note": "轨迹视图是 replay 历史的只读投影：每一行对应一条已记录的活动，" +
			"按 revision 分轮；duration_ms 由该轮首尾活动的时间差得出。未知 kind 一律保留，不丢行。",
	})
}
