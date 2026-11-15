package server

// Per-turn workspace checkpoints (Sprint 2).
//
//	GET  /api/harness/checkpoints/{id}              — list restore points
//	GET  /api/harness/checkpoints/{id}/{rev}        — preview a restore (no mutation)
//	POST /api/harness/checkpoints/{id}/{rev}/restore — apply a restore
//
// A checkpoint is captured at every OpenTurn by the Engine hook installed in
// wireCheckpoints. Restore is destructive to the task workspace, so it is a
// POST with an explicit revision and the UI must show Preview first.

import (
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Autumn-27/artex/harness/checkpoint"
	"github.com/Autumn-27/artex/harness/protocol"
)

// checkpointStore resolves the snapshot store for a task. Project-mode tasks keep
// bookkeeping under <data>/checkpoints/<taskID> so a user's project tree is never
// polluted (same rule as baselines in harness_files.go).
func (s *Server) checkpointStore(taskID int64) *checkpoint.Store {
	ws := s.taskWorkspace(taskID)
	var dir string
	if t, ok := s.m.Task(strconv.FormatInt(taskID, 10)); ok && t.ProjectDir != "" {
		dir = filepath.Join(s.m.dir, "checkpoints", strconv.FormatInt(taskID, 10))
	}
	return checkpoint.New(ws, dir)
}

// wireCheckpoints installs the Engine hook that snapshots the workspace at the
// start of every turn. The snapshot is best-effort: a failure is logged and the
// turn continues (a missing restore point degrades the UI, not the run).
func (s *Server) wireCheckpoints() {
	if s.engine == nil {
		return
	}
	s.engine.SetTurnOpenedHook(func(t *Task, turn protocol.Turn) {
		if t == nil || turn.Revision <= 0 || !t.HarnessMode {
			return
		}
		id, err := strconv.ParseInt(t.ID, 10, 64)
		if err != nil {
			return
		}
		if _, err := s.checkpointStore(id).Capture(turn.Revision, time.Now()); err != nil {
			log.Printf("[checkpoint] task %s rev=%d 快照失败: %v", t.ID, turn.Revision, err)
		}
	})
}

func (s *Server) harnessCheckpoints(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	store := s.checkpointStore(taskID)

	if revRaw := r.PathValue("rev"); revRaw != "" {
		rev, err := strconv.ParseInt(revRaw, 10, 64)
		if err != nil || rev <= 0 {
			writeErr(w, http.StatusBadRequest, "bad revision")
			return
		}
		plan, err := store.Preview(rev)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plan": plan})
		return
	}

	cps, err := store.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task": t.ID, "checkpoints": cps, "dir": store.Dir(),
		"note": "检查点在每轮开始时对工作区做哈希快照；回滚只恢复被捕获的常规文件，Shell 写到工作区外的改动不会回滚。",
	})
}

// harnessCheckpointRestore is the only mutating checkpoint endpoint.
func (s *Server) harnessCheckpointRestore(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	rev, err := strconv.ParseInt(r.PathValue("rev"), 10, 64)
	if err != nil || rev <= 0 {
		writeErr(w, http.StatusBadRequest, "bad revision")
		return
	}
	plan, err := s.checkpointStore(taskID).Restore(rev)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restored": rev, "plan": plan})
}
