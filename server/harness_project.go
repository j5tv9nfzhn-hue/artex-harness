package server

// Project-directory exclusivity (docs/09 §4).
//
// Rule: one directory can be bound to ONE harness task at a time. Why this is
// necessary rather than nice-to-have: the diff panel compares against a baseline
// snapshot, and "who changed this file" stops being answerable when two tasks write
// into the same tree. The guard is deliberately coarse (per directory, for the whole
// task lifetime) because anything finer needs a real lock protocol in the runtime.
//
// It lives in the server (not the DB) so a second instance in the same process gets a
// clear error instead of a race; on startup the table is rebuilt from living tasks.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type projectGuard struct {
	mu   sync.Mutex
	dirs map[string]string // cleaned dir -> task id
}

func newProjectGuard() *projectGuard { return &projectGuard{dirs: map[string]string{}} }

// cleanDir normalises a project path so two spellings of the same dir collide.
func cleanDir(dir string) string {
	d := filepath.Clean(strings.TrimSpace(dir))
	if d == "." || d == "" {
		return ""
	}
	return d
}

// claim binds dir to taskID. Returns an error naming the current owner.
func (g *projectGuard) claim(dir, taskID string) error {
	d := cleanDir(dir)
	if d == "" {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if owner, ok := g.dirs[d]; ok && owner != taskID {
		return fmt.Errorf("目录 %s 已被任务 #%s 占用：同一目录同一时刻只允许一个 HARNESS 任务使用（先删除或改绑那个任务）", d, owner)
	}
	g.dirs[d] = taskID
	return nil
}

// release frees dir if taskID is the owner.
func (g *projectGuard) release(dir, taskID string) {
	d := cleanDir(dir)
	if d == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if owner, ok := g.dirs[d]; ok && owner == taskID {
		delete(g.dirs, d)
	}
}

// owner reports the task holding dir ("" = free).
func (g *projectGuard) owner(dir string) string {
	d := cleanDir(dir)
	if d == "" {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.dirs[d]
}

// rebuild re-registers every living harness task that has a project_dir. Called at
// startup and after a task list reload so a restart does not forget the binding.
func (s *Server) rebuildProjectGuard() {
	if s.projectDirs == nil {
		return
	}
	for _, t := range s.m.List() {
		if t == nil || !t.HarnessMode || strings.TrimSpace(t.ProjectDir) == "" {
			continue
		}
		if err := s.projectDirs.claim(t.ProjectDir, t.ID); err != nil {
			log.Printf("[harness/project] 启动时目录冲突（保留先到者）: %v", err)
		}
	}
}

// releaseProjectDir is called on task delete/archive.
func (s *Server) releaseProjectDir(t *Task) {
	if s.projectDirs == nil || t == nil {
		return
	}
	s.projectDirs.release(t.ProjectDir, t.ID)
}

// harnessProject validates and binds a project directory for a task, rejecting
// conflicts with a reason the operator can act on.
func (s *Server) harnessProject(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	var req struct {
		ProjectDir *string `json:"project_dir"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ProjectDir == nil {
		writeErr(w, http.StatusBadRequest, "缺少 project_dir（传 \"\" 表示改回任务自有目录）")
		return
	}
	dir := cleanDir(*req.ProjectDir)
	if dir != "" {
		if !filepath.IsAbs(dir) {
			writeErr(w, http.StatusBadRequest, "project_dir 必须是绝对路径")
			return
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			writeErr(w, http.StatusBadRequest, "目录不存在或不是目录: "+dir)
			return
		}
		if err := s.projectDirs.claim(dir, t.ID); err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
	}
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	if err := s.m.pg.SetTaskProjectDir(taskID, dir); err != nil {
		// roll back the claim so a failed write does not leak a reservation
		s.projectDirs.release(dir, t.ID)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if prev := strings.TrimSpace(t.ProjectDir); prev != "" && prev != dir {
		s.projectDirs.release(prev, t.ID)
	}
	t.ProjectDir = dir
	writeJSON(w, http.StatusOK, map[string]any{"task": t.ID, "project_dir": dir})
}
