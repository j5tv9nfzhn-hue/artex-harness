package server

import (
	"net/http"
)

// Intervention primitives: the per-intent levers (steer/kill/reorder/replan)
// were deleted with S6-TRIM (planner/worker 专属管理面). Only the global
// stop button remains — it is UNCERTAIN (U9), kept pending a product
// decision, and it works for harness tasks too (it reuses applyTaskControl).

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
