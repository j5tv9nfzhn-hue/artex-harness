package server

// Workspace views for the HARNESS console: file tree, diff-against-baseline, token
// budget and in-flight delegations.
//
// Design note: the console needs "what did the agent change" without requiring the
// target machine to have a VCS. The runtime therefore snapshots the task workspace
// once, when the task starts (baseline), and diffs the live tree against it
// (harness/diff). Nothing else is stored — no per-write history, no index.

import (
	"bytes"
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
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/harness/delegation"
	"github.com/Autumn-27/artex/harness/diff"
)

const (
	harnessBaselineDir   = ".harness-baseline"
	harnessCheckpointDir = ".harness-checkpoints"
	maxTreeEntries       = 2000
	maxDiffFileBytes     = 1 << 20 // 1 MiB per file
)

// taskWorkspace is the tree the agent works in: the task's project_dir when it was
// given one, else <data>/tasks/<taskID> (docs/09 §4).
func (s *Server) taskWorkspace(taskID int64) string {
	if t, ok := s.m.Task(strconv.FormatInt(taskID, 10)); ok {
		if dir := strings.TrimSpace(t.ProjectDir); dir != "" {
			return dir
		}
	}
	return filepath.Join(s.m.dir, "tasks", strconv.FormatInt(taskID, 10))
}

// baselineDir is where the diff reference lives.
//
// For a task's own workspace the baseline sits inside it (and is skipped by every
// walk). For a PROJECT directory we must not write our bookkeeping into the user's
// tree, so the reference lives under <data>/baselines/<taskID> instead.
func (s *Server) baselineDir(taskID int64) string {
	if t, ok := s.m.Task(strconv.FormatInt(taskID, 10)); ok && strings.TrimSpace(t.ProjectDir) != "" {
		return filepath.Join(s.m.dir, "baselines", strconv.FormatInt(taskID, 10))
	}
	return filepath.Join(s.taskWorkspace(taskID), harnessBaselineDir)
}

// isSidecar reports whether a path is harness bookkeeping rather than user content.
// Checkpoints are internal too: they live under the task workspace unless the task
// has a project_dir, and must never appear in the file tree or the DIFF.
func isSidecar(name string) bool {
	return name == harnessBaselineDir || name == harnessCheckpointDir
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
		if rel == skipName || strings.HasPrefix(rel, skipName+string(os.PathSeparator)) ||
			rel == harnessCheckpointDir || strings.HasPrefix(rel, harnessCheckpointDir+string(os.PathSeparator)) {
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
		if rel == harnessBaselineDir || strings.HasPrefix(rel, harnessBaselineDir+string(os.PathSeparator)) ||
			rel == harnessCheckpointDir || strings.HasPrefix(rel, harnessCheckpointDir+string(os.PathSeparator)) {
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
		// Checkpoints are harness bookkeeping, not user content — never diff them.
		if rel == harnessCheckpointDir || strings.HasPrefix(rel, harnessCheckpointDir+string(os.PathSeparator)) {
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
		"note": "与任务启动时的基线对比（无需 git）。baseline=false 表示尚未建立基线：下一个任务轮次会自动建立。" +
			" 如果基线是刚刚建立的，此前已有的文件不会显示为变更——之后的新改动才会。",
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

// ── file read (workbench) ─────────────────────────────────────────────────

// harnessFileRead returns one task-workspace file's text so the console's file
// tree can preview it. It is deliberately task-scoped (unlike /api/workspace/*,
// which is rooted at the server data dir): a project-mode task must show the
// user's project files, not the server's own storage.
func (s *Server) harnessFileRead(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	if rel == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}
	abs, ok := safeJoin(s.taskWorkspace(taskID), rel)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法路径")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil {
		writeErr(w, http.StatusNotFound, "文件不存在")
		return
	}
	if fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "是目录，不能作为文件读取")
		return
	}
	if fi.Size() > maxDiffFileBytes {
		writeJSON(w, http.StatusOK, map[string]any{
			"path": rel, "size": fi.Size(), "too_large": true,
			"note": "超过 1 MiB，控制台不内联预览。",
		})
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		writeJSON(w, http.StatusOK, map[string]any{
			"path": rel, "size": fi.Size(), "binary": true,
			"note": "二进制文件，控制台不内联预览。",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task": t.ID, "path": rel, "size": fi.Size(), "content": string(data),
	})
}

// safeJoin resolves rel under root and rejects any path that escapes it
// (absolute paths, "..", or a symlink-resolved target outside the root).
func safeJoin(root, rel string) (string, bool) {
	root = filepath.Clean(root)
	if filepath.IsAbs(rel) {
		return "", false
	}
	abs := filepath.Clean(filepath.Join(root, rel))
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return "", false
	}
	return abs, true
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
	series, compactions := t.Store.InputTokenSeries()
	// Recorded events vs inferred drops: the inference misses a compaction whose
	// shrink is diluted by the tool output that triggered it, and it used to count a
	// new turn as one. Both are reported so the panel never has to guess.
	obsMicro, obsAuto := t.Store.CompactEventCounts()
	writeJSON(w, http.StatusOK, map[string]any{
		"task":               t.ID,
		"window":             window,
		"last_input":         lastInput,
		"totals":             stats,
		"pct":                pct(lastInput, window),
		"run_budget_seconds": int(agent.RunBudget().Seconds()),
		// series = 每轮 usage 的输入 token；compactions = 输入 token 明显回落的次数。
		// SDK 不提供压缩事件，所以这是**推断**（回落 ≥20% 计一次），API 里也这样标注。
		"series":      series,
		"compactions": compactions,
		"compactions_observed": map[string]int{
			"micro": obsMicro,
			"auto":  obsAuto,
			"total": obsMicro + obsAuto,
		},
		"note": "pct 基于最近一轮输入 token 估算上下文占用；compactions 由**同一轮次内**输入 token 回落推断（≥20% 计一次，轮次边界不计）。",
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
	for _, a := range acts {
		if a.Tool != "Agent" {
			continue
		}
		if a.Kind == "tool_use" {
			at, prompt := parseAgentSummary(a.Summary)
			did, bg := parseDelegationMeta(a.Metadata)
			out = append(out, delegationRow{
				ActivityID: a.ID, AgentType: at, Prompt: prompt, State: "running",
				StartedAt: a.CreatedAt, DelegationID: did, Background: bg,
			})
			continue
		}
		// tool_result follows the tool_use; attach to the most recent running one,
		// preferring an exact delegation-id match when the result carries one.
		did, _ := parseDelegationMeta(a.Metadata)
		pick := -1
		for i := len(out) - 1; i >= 0; i-- {
			if out[i].State != "running" {
				continue
			}
			if did != "" {
				if out[i].DelegationID == did {
					pick = i
					break
				}
				continue
			}
			pick = i
			break
		}
		if pick < 0 {
			continue
		}
		out[pick].State = "done"
		if a.IsError {
			out[pick].State = "error"
		}
		out[pick].Result = a.Summary
		if !out[pick].StartedAt.IsZero() && !a.CreatedAt.IsZero() {
			out[pick].DurationMS = a.CreatedAt.Sub(out[pick].StartedAt).Milliseconds()
		}
	}
	rows := dedupeDelegations(out)
	// A still-running delegation reports elapsed time so far; the sidebar can show a
	// live clock without inventing a start time on the client.
	now := time.Now()
	for i := range rows {
		if rows[i].State == "running" && !rows[i].StartedAt.IsZero() {
			rows[i].DurationMS = now.Sub(rows[i].StartedAt).Milliseconds()
		}
	}
	// 明细在磁盘 sidechain 里，activity 行没有 agent id：用提示词对一次，对上才可点开。
	idx := s.subagentIndex(t.ExpID)
	for i := range rows {
		rows[i].AgentID = matchSubagent(idx, rows[i].Prompt, rows[i].StartedAt)
	}
	// Overlay the delegation registry (Sprint 3): its status is authoritative for
	// background runs (whose tool_result lands minutes later) and it carries the
	// delegation id the stop/inspect endpoints need. Match by delegation id when
	// both sides have one; otherwise fall back to prompt prefix (older rows). A
	// registry entry with no activity match is appended so the panel never
	// silently drops running work.
	if reg := delegation.Global.Peek(t.ID); reg != nil {
		for _, rec := range reg.List() {
			matched := false
			for i := range rows {
				if rows[i].DelegationID != "" && rows[i].DelegationID != rec.ID {
					continue
				}
				prefix := firstRunes(rec.Prompt, 40)
				if rows[i].DelegationID == "" && rec.Prompt != "" && !strings.HasPrefix(rows[i].Prompt, prefix) {
					continue
				}
				rows[i].DelegationID = rec.ID
				rows[i].Background = rec.Background
				rows[i].State = delegationState(rec.Status)
				rows[i].DurationMS = rec.DurationMS
				if rec.Result != "" {
					rows[i].Result = rec.Result
				}
				if rows[i].AgentID == "" {
					rows[i].AgentID = rec.AgentID
				}
				matched = true
				break
			}
			if !matched {
				rows = append(rows, delegationRow{
					AgentType: rec.AgentType, Prompt: rec.Prompt, State: delegationState(rec.Status),
					StartedAt: rec.StartedAt, DurationMS: rec.DurationMS, Result: rec.Result,
					AgentID: rec.AgentID, DelegationID: rec.ID, Background: rec.Background,
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task":        t.ID,
		"harness":     t.HarnessMode,
		"delegations": rows,
		"note":        "HARNESS 没有 worker 池：这里列出 agent 自己派出的子代理（Agent 工具）及其结果。后台委派（run_in_background）可单独停止。",
	})
}

// delegationState maps a registry status onto the panel's running|done|error.
func delegationState(s delegation.Status) string {
	switch s {
	case delegation.StatusDone:
		return "done"
	case delegation.StatusRunning:
		return "running"
	default:
		return "error"
	}
}

// parseAgentSummary extracts (agent_type, prompt) from either summary shape:
//
//	"派发子代理 recon：<prompt>"                      (the readable wrapper emit)
//	`Agent {"agent_type":"recon","prompt":"<prompt>"}` (the raw tool_use row)
//
// Both exist for one call — the harness stream records the tool call, and the
// wrapper adds a readable line — so the panel must understand both.
func parseAgentSummary(s string) (string, string) {
	// The wrapper emits "派发子代理 recon (#d1)：<prompt>" (foreground) or
	// "后台派发子代理 recon (#d1)：<prompt>" (background, Sprint 3). Strip the
	// prefix and the "(#id)" before the colon — the id also lives in metadata,
	// but the summary must stay parseable for rows predating metadata.
	for _, p := range []string{"后台派发子代理 ", "派发子代理 "} {
		if strings.HasPrefix(s, p) {
			rest := strings.TrimPrefix(s, p)
			if idx := strings.Index(rest, "："); idx > 0 {
				at := rest[:idx]
				if i := strings.IndexByte(at, '('); i > 0 {
					at = strings.TrimSpace(at[:i])
				}
				return at, rest[idx+len("："):]
			}
			if i := strings.IndexByte(rest, '('); i > 0 {
				return rest[:i], ""
			}
			return rest, ""
		}
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

// parseDelegationMeta reads (delegation_id, background) from an Agent activity
// row's metadata (Sprint 3). Missing/foreign payloads yield empty values, which
// keeps pre-registry rows pairing by prompt as before.
func parseDelegationMeta(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var m struct {
		DelegationID string `json:"delegation_id"`
		Background   bool   `json:"background"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return "", false
	}
	return m.DelegationID, m.Background
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
			// Two rows describe one delegation; the exchange starts at the earlier
			// timestamp and lasts as long as the longer of the two observations.
			if !d.StartedAt.IsZero() && (out[i].StartedAt.IsZero() || d.StartedAt.Before(out[i].StartedAt)) {
				out[i].StartedAt = d.StartedAt
			}
			if d.DurationMS > out[i].DurationMS {
				out[i].DurationMS = d.DurationMS
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
	// StartedAt / DurationMS let the sidebar show how long a delegation has been
	// running without the client having to reconstruct timestamps from the stream.
	StartedAt  time.Time `json:"started_at"`
	DurationMS int64     `json:"duration_ms"`
	// AgentID 是磁盘 sidechain 文件里的子代理 id；点开一次委派看它的明细要靠它。
	// 空 = 没有找到对应记录（从未真正跑起来，或运行在旧版本上）。
	AgentID string `json:"agent_id,omitempty"`
	// DelegationID / Background come from the delegation registry (Sprint 3):
	// the authoritative in-process state. DelegationID empty = the run predates
	// the registry (or its activity rows came from an older build); stop/inspect
	// via /api/harness/delegations then falls back to activity pairing only.
	DelegationID string `json:"delegation_id,omitempty"`
	Background   bool   `json:"background,omitempty"`
}
