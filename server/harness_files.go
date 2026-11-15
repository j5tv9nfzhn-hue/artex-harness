package server

// Workspace views for the HARNESS console: file tree, diff-against-baseline, token
// budget and in-flight delegations.
//
// Design note: the console needs "what did the agent change" without requiring the
// target machine to have a VCS. The runtime therefore snapshots the task workspace
// once, when the task starts (baseline), and diffs the live tree against it
// (harness/diff). Nothing else is stored — no per-write history, no index.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/harness/diff"
)

const (
	harnessBaselineDir = ".harness-baseline"
	maxTreeEntries     = 2000
	maxDiffFileBytes   = 1 << 20 // 1 MiB per file
)

// taskWorkspace is <data>/tasks/<taskID> — the same tree the agent works in.
func (s *Server) taskWorkspace(taskID int64) string {
	return filepath.Join(s.m.dir, "tasks", strconv.FormatInt(taskID, 10))
}

func (s *Server) baselineDir(taskID int64) string {
	return filepath.Join(s.taskWorkspace(taskID), harnessBaselineDir)
}

// ensureBaseline copies the task workspace once so later diffs have a reference.
// Best effort: if it fails, diffs report "no baseline" instead of failing the task.
func (s *Server) ensureBaseline(taskID int64) {
	src := s.taskWorkspace(taskID)
	if _, err := os.Stat(src); err != nil {
		return // nothing written yet; the first diff will establish it
	}
	dst := s.baselineDir(taskID)
	if _, err := os.Stat(dst); err == nil {
		return // already snapshotted
	}
	if err := copyTree(src, dst, harnessBaselineDir); err != nil {
		log.Printf("[harness] 建立基线失败 task=%d: %v", taskID, err)
	}
}

// copyTree copies regular files under src into dst, skipping skipName at the top
// level and never following symlinks.
func copyTree(src, dst, skipName string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return nil
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if rel == skipName || strings.HasPrefix(rel, skipName+string(os.PathSeparator)) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // symlinks / devices are not part of the diff surface
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() > maxDiffFileBytes {
			return nil
		}
		in, oerr := os.Open(path)
		if oerr != nil {
			return nil
		}
		defer in.Close()
		out, cerr := os.Create(filepath.Join(dst, rel))
		if cerr != nil {
			return nil
		}
		defer out.Close()
		_, _ = io.Copy(out, in)
		return nil
	})
}

// ── file tree ─────────────────────────────────────────────────────────────

type fileNode struct {
	Path   string `json:"path"`
	Dir    bool   `json:"dir"`
	Size   int64  `json:"size,omitempty"`
	Change string `json:"change,omitempty"` // added | modified | deleted
}

// harnessFiles lists the task workspace (bounded), annotated with whether the file
// differs from the baseline.
func (s *Server) harnessFiles(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	root := s.taskWorkspace(taskID)
	changes := s.changedFiles(taskID)
	nodes := make([]fileNode, 0, 64)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." {
			return nil
		}
		if rel == harnessBaselineDir || strings.HasPrefix(rel, harnessBaselineDir+string(os.PathSeparator)) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			nodes = append(nodes, fileNode{Path: rel, Dir: true})
			if len(nodes) >= maxTreeEntries {
				return filepath.SkipAll
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		size := int64(0)
		if info, ierr := d.Info(); ierr == nil {
			size = info.Size()
		}
		nodes = append(nodes, fileNode{Path: rel, Size: size, Change: changes[rel]})
		if len(nodes) >= maxTreeEntries {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
	writeJSON(w, http.StatusOK, map[string]any{
		"task": t.ID, "root": root, "files": nodes,
		"baseline": s.baselineDir(taskID),
	})
}

// changedFiles hashes baseline vs live and returns rel path → status.
func (s *Server) changedFiles(taskID int64) map[string]string {
	out := map[string]string{}
	live := s.taskWorkspace(taskID)
	base := s.baselineDir(taskID)
	liveHashes := hashTree(live, harnessBaselineDir)
	baseHashes := hashTree(base, "")
	for rel, h := range liveHashes {
		if bh, ok := baseHashes[rel]; !ok {
			out[rel] = diff.StatusAdded
		} else if bh != h {
			out[rel] = diff.StatusModified
		}
		delete(baseHashes, rel)
	}
	for rel := range baseHashes {
		out[rel] = diff.StatusDeleted
	}
	return out
}

func hashTree(root, skipName string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." {
			return nil
		}
		if skipName != "" && (rel == skipName || strings.HasPrefix(rel, skipName+string(os.PathSeparator))) {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() > maxDiffFileBytes {
			return nil
		}
		f, oerr := os.Open(path)
		if oerr != nil {
			return nil
		}
		defer f.Close()
		h := sha256.New()
		if _, cerr := io.Copy(h, f); cerr != nil {
			return nil
		}
		out[rel] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	return out
}

// ── diff ──────────────────────────────────────────────────────────────────

// harnessDiff returns the change set for a task workspace. With ?path= it also
// returns that file's unified diff body.
func (s *Server) harnessDiff(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	changes := s.changedFiles(taskID)

	only := strings.TrimSpace(r.URL.Query().Get("path"))
	files := make([]diff.FileDiff, 0, len(changes))
	for rel, status := range changes {
		if only != "" && rel != only {
			continue
		}
		fd := diff.FileDiff{Path: rel, Status: status}
		if only != "" {
			fd = readDiff(taskID, rel, s.taskWorkspace(taskID), s.baselineDir(taskID))
		}
		files = append(files, fd)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	baselineExists := false
	if _, err := os.Stat(s.baselineDir(taskID)); err == nil {
		baselineExists = true
	} else {
		// No snapshot yet (task started before any file existed, or the runtime has
		// not written anything). Establish it now, and say so: from this point on
		// the panel shows real deltas instead of silently showing nothing forever.
		s.ensureBaseline(taskID)
		if _, err := os.Stat(s.baselineDir(taskID)); err == nil {
			baselineExists = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task":     t.ID,
		"baseline": baselineExists,
		"files":    files,
		"changed":  len(files),
		"note":     "与任务启动时的基线对比（无需 git）。baseline=false 表示尚未建立基线：下一个任务轮次会自动建立。",
	})
}

func readDiff(taskID int64, rel, liveRoot, baseRoot string) diff.FileDiff {
	read := func(p string) []byte {
		info, err := os.Stat(p)
		if err != nil || info.Size() > maxDiffFileBytes {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		return b
	}
	cur := read(filepath.Join(liveRoot, rel))
	var base []byte
	if _, err := os.Stat(filepath.Join(baseRoot, rel)); err == nil {
		base = read(filepath.Join(baseRoot, rel))
		if base == nil {
			base = []byte{}
		}
	}
	return diff.Unified(rel, base, cur)
}

// ── budget ────────────────────────────────────────────────────────────────

// harnessBudget reports token consumption and the context window so the console can
// show "how full is the agent's head" instead of hiding it until it overflows.
func (s *Server) harnessBudget(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	stats, err := t.Store.TokenTotal()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	window := 0
	if b := s.agentsForTask(t); b != nil && b.harnessRuntime != nil {
		window = b.harnessRuntime.CompactionWindow()
	}
	// The last turn's input tokens is the best available "current context size".
	lastInput := t.Store.LastUsageInputTokens()
	writeJSON(w, http.StatusOK, map[string]any{
		"task":       t.ID,
		"window":     window,
		"last_input": lastInput,
		"totals":     stats,
		"pct":        pct(lastInput, window),
		"note":       "pct 基于最近一轮的输入 token 估算上下文占用；超过 80% 时压缩阈值即将触发。",
	})
}

func pct(used, window int) float64 {
	if window <= 0 || used <= 0 {
		return 0
	}
	v := float64(used) * 100 / float64(window)
	if v > 100 {
		return 100
	}
	return v
}

// ── delegations (subagents in flight / recent) ────────────────────────────

// harnessWorkers lists delegations and their outcome. In HARNESS mode there is no
// worker pool, so "workers" means: the conversation turn plus the subagents the
// agent decided to dispatch (Agent tool calls and their results).
func (s *Server) harnessWorkers(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	acts, _, err := t.Store.ActivityList(nil, 0, 2000)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]delegationRow, 0, 8)
	pending := map[int64]int{} // activity id → index in out
	for _, a := range acts {
		if a.Tool != "Agent" {
			continue
		}
		if a.Kind == "tool_use" {
			at, prompt := parseAgentSummary(a.Summary)
			out = append(out, delegationRow{ActivityID: a.ID, AgentType: at, Prompt: prompt, State: "running"})
			pending[a.ID] = len(out) - 1
			continue
		}
		// tool_result follows the tool_use; attach to the most recent running one
		for i := len(out) - 1; i >= 0; i-- {
			if out[i].State == "running" {
				out[i].State = "done"
				if a.IsError {
					out[i].State = "error"
				}
				out[i].Result = a.Summary
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task":        t.ID,
		"harness":     t.HarnessMode,
		"delegations": dedupeDelegations(out),
		"note":        "HARNESS 没有 worker 池：这里列出 agent 自己派出的子代理（Agent 工具）及其结果。",
	})
}

// parseAgentSummary extracts (agent_type, prompt) from either summary shape:
//
//	"派发子代理 recon：<prompt>"                      (the readable wrapper emit)
//	`Agent {"agent_type":"recon","prompt":"<prompt>"}` (the raw tool_use row)
//
// Both exist for one call — the harness stream records the tool call, and the
// wrapper adds a readable line — so the panel must understand both.
func parseAgentSummary(s string) (string, string) {
	if strings.HasPrefix(s, "派发子代理 ") {
		rest := strings.TrimPrefix(s, "派发子代理 ")
		if idx := strings.Index(rest, "："); idx > 0 {
			return rest[:idx], rest[idx+len("："):]
		}
		return rest, ""
	}
	if strings.HasPrefix(s, "Agent ") {
		raw := strings.TrimPrefix(s, "Agent ")
		var in struct {
			AgentType string `json:"agent_type"`
			Prompt    string `json:"prompt"`
		}
		if json.Unmarshal([]byte(raw), &in) == nil {
			return in.AgentType, in.Prompt
		}
	}
	return "", ""
}

// dedupeDelegations collapses the two rows recorded for one Agent call (raw
// tool_use + readable wrapper) into a single entry, preferring the readable prompt
// and the most advanced state.
func dedupeDelegations(in []delegationRow) []delegationRow {
	key := func(d delegationRow) string { return d.AgentType + "\x00" + firstRunes(d.Prompt, 80) }
	out := make([]delegationRow, 0, len(in))
	idx := map[string]int{}
	for _, d := range in {
		if d.AgentType == "" {
			continue // not a delegation row (e.g. an unrelated tool named Agent)
		}
		k := key(d)
		if i, ok := idx[k]; ok {
			// merge: keep the friendlier prompt, prefer done/error over running
			if len(d.Prompt) > len(out[i].Prompt) && !strings.Contains(out[i].Prompt, "已截断") {
				out[i].Prompt = d.Prompt
			}
			if out[i].State == "running" && d.State != "running" {
				out[i].State, out[i].Result = d.State, d.Result
			}
			continue
		}
		idx[k] = len(out)
		out = append(out, d)
	}
	return out
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// delegationRow is one Agent (subagent) dispatch, as surfaced to the console.
type delegationRow struct {
	ActivityID int64  `json:"activity_id"`
	AgentType  string `json:"agent_type"`
	Prompt     string `json:"prompt"`
	State      string `json:"state"` // running | done | error
	Result     string `json:"result,omitempty"`
}
