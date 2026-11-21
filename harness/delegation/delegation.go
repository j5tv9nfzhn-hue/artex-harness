// Package delegation tracks one task's delegated subagent runs.
//
// Why it exists: the console used to infer delegation state by pairing Agent
// tool_use/tool_result activity rows. A background delegation breaks that
// assumption — the tool returns immediately with a task id, and the result lands
// minutes later. This package is the authoritative in-process registry for both
// synchronous and background delegations, and produces the completion
// notifications the parent conversation consumes.
//
// Design boundary (ADR-0005): this is still ONE conversational agent deciding
// to delegate. There are no teams, no leader/member mailboxes, no cross-session
// orchestration. A delegation is a unit of work the agent chose to spawn.
package delegation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status is the lifecycle state of one delegation.
type Status string

const (
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusKilled  Status = "killed"
)

func (s Status) Terminal() bool { return s != StatusRunning }

// Record is an immutable snapshot of one delegation for the API/panel.
type Record struct {
	ID          string    `json:"id"`
	AgentType   string    `json:"agent_type"`
	Prompt      string    `json:"prompt"`
	Description string    `json:"description,omitempty"`
	Status      Status    `json:"status"`
	Background  bool      `json:"background"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at,omitempty"`
	DurationMS  int64     `json:"duration_ms"`
	OutputPath  string    `json:"output_path,omitempty"`
	Result      string    `json:"result,omitempty"`
	Error       string    `json:"error,omitempty"`
	WorkDir     string    `json:"work_dir,omitempty"`
	AgentID     string    `json:"agent_id,omitempty"` // sidechain transcript id
	// Reclaimed marks a delegation whose isolated work directory was removed by
	// the retention sweep (TTL). The record itself is kept — it is the audit
	// trail and the console still lists it; only the scratch directory is gone.
	Reclaimed bool `json:"reclaimed,omitempty"`
	// Refused marks a delegation whose final text was a model content refusal
	// (llmpool.Detect), rather than an ordinary failure: the subagent said
	// no instead of doing the work. Status is untouched — a refused run is
	// still StatusFailed when it errored, StatusDone when it answered with
	// refusal text — so front-end and statistics keep reading Status, and
	// Refused is the extra signal that says *why* it ended that way.
	Refused bool `json:"refused,omitempty"`
}

type entry struct {
	rec    Record
	cancel context.CancelFunc
}

// Notification is what the parent conversation receives when a background
// delegation finishes. It deliberately carries the output path, not the full
// body — the model reads it on demand with Read/TaskOutput.
type Notification struct {
	TaskID     string `json:"task_id"`
	AgentType  string `json:"agent_type"`
	Status     Status `json:"status"`
	OutputPath string `json:"output_path"`
	Summary    string `json:"summary"`
}

func (n Notification) String() string {
	return fmt.Sprintf("<task-notification>\n"+
		"  <task-id>%s</task-id>\n"+
		"  <agent-type>%s</agent-type>\n"+
		"  <status>%s</status>\n"+
		"  <output-file>%s</output-file>\n"+
		"  <summary>%s</summary>\n"+
		"</task-notification>",
		n.TaskID, n.AgentType, n.Status, n.OutputPath, n.Summary)
}

// Registry owns the delegations for ONE task. Safe for concurrent use.
//
// Durability: records are journaled to <outputDir>/delegations.jsonl so that a
// process restart keeps the audit trail. Live cancel functions cannot survive a
// restart (there is no process to cancel into), so a restored record is always
// terminal — see Restore.
type Registry struct {
	mu      sync.Mutex
	entries map[string]*entry
	order   []string
	pending []Notification
	dir     string // output directory
	seq     int
}

// New creates a registry. outputDir is where per-delegation result files are
// written; it is created lazily on the first background delegation.
func New(outputDir string) *Registry {
	return &Registry{entries: map[string]*entry{}, dir: outputDir}
}

// journalPath is where this registry's records are persisted for restart
// recovery. It lives beside the result bodies so deleting a task's data
// directory also drops its journal.
func (r *Registry) journalPath() string {
	if r.dir == "" {
		return ""
	}
	return filepath.Join(r.dir, "delegations.jsonl")
}

// persistLocked appends the current snapshot of ONE record. The journal is a
// line-per-version log (not a rewrite): a crash mid-write loses at most the last
// line, and Restore keeps the last version of each id. Caller holds r.mu.
//
// Best-effort by design: a read-only or full disk must not break delegation
// execution, so the failure is reported to the caller, not fatal.
func (r *Registry) persistLocked(id string) {
	path := r.journalPath()
	if path == "" {
		return
	}
	e, ok := r.entries[id]
	if !ok {
		return
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return
	}
	line, err := json.Marshal(e.rec)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// Restore loads journaled records for this task. Records that were still
// "running" when the process died are rewritten as "failed" with an explicit
// reason: nothing is running now, and silently keeping them "running" would make
// the panel lie forever (there is no goroutine left to finish them).
//
// Idempotent: calling it twice does not duplicate records.
func (r *Registry) Restore() (int, error) {
	path := r.journalPath()
	if path == "" {
		return 0, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	type kv struct {
		id  string
		rec Record
	}
	var seqRecs []kv
	seen := map[string]int{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.ID == "" {
			continue // torn or foreign line: skip rather than fail the whole restore
		}
		if i, ok := seen[rec.ID]; ok {
			seqRecs[i].rec = rec // last version wins
			continue
		}
		seen[rec.ID] = len(seqRecs)
		seqRecs = append(seqRecs, kv{id: rec.ID, rec: rec})
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	restored := 0
	for _, item := range seqRecs {
		rec := item.rec
		if _, exists := r.entries[rec.ID]; exists {
			continue
		}
		if !rec.Status.Terminal() {
			rec.Status = StatusFailed
			rec.Error = "进程重启：该委派已中断（历史记录已保留，无法再停止）"
			if rec.EndedAt.IsZero() {
				rec.EndedAt = rec.StartedAt
			}
			rec.DurationMS = rec.EndedAt.Sub(rec.StartedAt).Milliseconds()
		}
		r.entries[rec.ID] = &entry{rec: rec}
		r.order = append(r.order, rec.ID)
		if n, err := strconv.Atoi(strings.TrimPrefix(rec.ID, "d")); err == nil && n > r.seq {
			r.seq = n
		}
		restored++
	}
	sort.SliceStable(r.order, func(i, j int) bool { return seqOf(r.order[i]) < seqOf(r.order[j]) })
	return restored, nil
}

func seqOf(id string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "d"))
	if err != nil {
		return 0
	}
	return n
}

// SweepStats reports what a retention pass did.
type SweepStats struct {
	Scanned   int `json:"scanned"`
	Reclaimed int `json:"reclaimed"`
	Kept      int `json:"kept"`
	Failed    int `json:"failed"`
}

// Sweep removes isolated work directories of TERMINAL delegations older than
// olderThan. This is the retention policy the Sprint 3 notes listed as missing:
// a long-running task used to accumulate one directory per delegation forever.
//
// Rules (deliberate, so the operator can predict it):
//   - running delegations are never touched;
//   - the result body (content-addressed, in the parent dir) is never touched —
//     only the scratch directory under <outputDir>/<id>/;
//   - the record stays in the registry (marked Reclaimed) so the panel keeps
//     showing what happened;
//   - olderThan <= 0 disables the sweep.
func (r *Registry) Sweep(now time.Time, olderThan time.Duration) SweepStats {
	var st SweepStats
	if olderThan <= 0 || r.dir == "" {
		return st
	}
	type victim struct {
		id  string
		dir string
	}
	var victims []victim
	r.mu.Lock()
	for id, e := range r.entries {
		if !e.rec.Status.Terminal() {
			continue
		}
		st.Scanned++
		if e.rec.Reclaimed {
			continue
		}
		if e.rec.WorkDir == "" {
			continue
		}
		ended := e.rec.EndedAt
		if ended.IsZero() {
			ended = e.rec.StartedAt
		}
		if now.Sub(ended) < olderThan {
			st.Kept++
			continue
		}
		victims = append(victims, victim{id: id, dir: e.rec.WorkDir})
	}
	r.mu.Unlock()

	for _, v := range victims {
		// Refuse to walk outside the delegation store: WorkDir is registry data and
		// must never be able to point the sweep at the task workspace or "/".
		if clean := filepath.Clean(v.dir); !strings.HasPrefix(clean, filepath.Clean(r.dir)+string(os.PathSeparator)) {
			continue
		}
		if err := os.RemoveAll(v.dir); err != nil {
			st.Failed++
			continue
		}
		r.mu.Lock()
		if e, ok := r.entries[v.id]; ok {
			e.rec.Reclaimed = true
			e.rec.WorkDir = ""
			r.persistLocked(v.id)
		}
		r.mu.Unlock()
		st.Reclaimed++
	}
	return st
}

// Start registers a delegation before its goroutine begins. The returned id is
// what the Agent tool gives the model for a background run.
func (r *Registry) Start(agentType, prompt, description, workDir string, background bool, started time.Time) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	id := fmt.Sprintf("d%d", r.seq)
	if started.IsZero() {
		started = time.Now()
	}
	r.entries[id] = &entry{rec: Record{
		ID: id, AgentType: agentType, Prompt: prompt, Description: description,
		Status: StatusRunning, Background: background, StartedAt: started, WorkDir: workDir,
	}}
	r.order = append(r.order, id)
	r.persistLocked(id)
	return id
}

// AttachCancel stores the cancel func for a running delegation.
func (r *Registry) AttachCancel(id string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[id]; ok {
		e.cancel = cancel
	}
}

// Finish records a terminal state. It is idempotent: a delegation finished
// twice (e.g. kill racing with completion) keeps the first terminal state.
func (r *Registry) Finish(id string, status Status, result, errText string) {
	r.FinishWithRefused(id, status, result, errText, false)
}

// FinishWithRefused is Finish plus the refusal mark: refused=true when the
// delegation's final text was a model content refusal rather than an
// ordinary failure. The journal persists the flag automatically (Record is
// marshalled whole), so a restarted process still lists it.
func (r *Registry) FinishWithRefused(id string, status Status, result, errText string, refused bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok || e.rec.Status.Terminal() {
		return
	}
	e.rec.Status = status
	e.rec.Result = result
	e.rec.Error = errText
	e.rec.Refused = refused
	e.rec.EndedAt = time.Now()
	e.rec.DurationMS = e.rec.EndedAt.Sub(e.rec.StartedAt).Milliseconds()
	if e.cancel != nil {
		e.cancel()
	}
	if e.rec.Background {
		r.pending = append(r.pending, Notification{
			TaskID: id, AgentType: e.rec.AgentType, Status: status,
			OutputPath: e.rec.OutputPath, Summary: clip(result, 200),
		})
	}
	r.persistLocked(id)
}

// Kill terminates a running delegation. Returns false if unknown/terminal.
func (r *Registry) Kill(id string) bool {
	r.mu.Lock()
	e, ok := r.entries[id]
	if !ok || e.rec.Status.Terminal() {
		r.mu.Unlock()
		return false
	}
	cancel := e.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.Finish(id, StatusKilled, "", "delegation stopped by operator")
	return true
}

// SetOutputPath records where a delegation's result body is stored.
func (r *Registry) SetOutputPath(id, path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[id]; ok {
		e.rec.OutputPath = path
		r.persistLocked(id)
	}
}

// SetWorkDir rebinds a delegation's working directory. The runtime calls this
// to move a background run into an isolated directory (outside the task
// workspace, so concurrent delegations and their file writes never pollute the
// parent's file tree / DIFF / checkpoints).
func (r *Registry) SetWorkDir(id, dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[id]; ok {
		e.rec.WorkDir = dir
		r.persistLocked(id)
	}
}

// SetAgentID attaches the sidechain transcript id once the child run has one.
func (r *Registry) SetAgentID(id, agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[id]; ok {
		e.rec.AgentID = agentID
		r.persistLocked(id)
	}
}

// WriteOutput persists a delegation result body (content-addressed by hash so
// repeated identical results do not pile up) and returns its path.
func (r *Registry) WriteOutput(id, body string) (string, error) {
	if r.dir == "" {
		return "", fmt.Errorf("no output directory configured")
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(body))
	name := hex.EncodeToString(h[:12]) + ".txt"
	path := filepath.Join(r.dir, name)
	if _, err := os.Stat(path); err == nil {
		r.SetOutputPath(id, path)
		return path, nil
	}
	tmp, err := os.CreateTemp(r.dir, ".out-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	r.SetOutputPath(id, path)
	return path, nil
}

// DrainNotifications returns and clears the pending completion notifications.
func (r *Registry) DrainNotifications() []Notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]Notification(nil), r.pending...)
	r.pending = nil
	return out
}

// PeekNotifications returns notifications without clearing them (for the API).
func (r *Registry) PeekNotifications() []Notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Notification(nil), r.pending...)
}

// List returns all delegations in start order, with live elapsed time for
// running ones.
func (r *Registry) List() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	out := make([]Record, 0, len(r.order))
	for _, id := range r.order {
		e := r.entries[id]
		rec := e.rec
		if !rec.Status.Terminal() {
			rec.DurationMS = now.Sub(rec.StartedAt).Milliseconds()
		}
		out = append(out, rec)
	}
	return out
}

// Get returns one delegation (copy) by id.
func (r *Registry) Get(id string) (Record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return Record{}, false
	}
	return e.rec, true
}

// Summary counts delegations by status.
func (r *Registry) Summary() map[Status]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[Status]int{}
	for _, e := range r.entries {
		out[e.rec.Status]++
	}
	return out
}

// Global is a process-wide map of taskID → Registry, so the HTTP layer and the
// agent runtime (which are different objects) see the same delegations.
var Global = &global{regs: map[string]*Registry{}}

type global struct {
	mu   sync.Mutex
	regs map[string]*Registry
}

// For returns (creating if needed) the registry for a task. outputDir is used
// only on first creation. A newly created registry is seeded from its on-disk
// journal, so a restarted process still lists the delegations it had (they come
// back terminal — see Restore).
func (g *global) For(taskID, outputDir string) *Registry {
	g.mu.Lock()
	if r, ok := g.regs[taskID]; ok {
		g.mu.Unlock()
		return r
	}
	r := New(outputDir)
	g.regs[taskID] = r
	g.mu.Unlock()

	// Do the file IO outside the global lock: restoring one task must not block
	// every other task's registry lookup.
	if _, err := r.Restore(); err != nil {
		log.Printf("[delegation] 任务 %s 的委派journal恢复失败: %v", taskID, err)
	}
	return r
}

// SweepAll applies the retention policy to every known task registry. Returns
// the number of isolated directories removed.
func (g *global) SweepAll(now time.Time, olderThan time.Duration) int {
	g.mu.Lock()
	regs := make([]*Registry, 0, len(g.regs))
	for _, r := range g.regs {
		regs = append(regs, r)
	}
	g.mu.Unlock()

	removed := 0
	for _, r := range regs {
		removed += r.Sweep(now, olderThan).Reclaimed
	}
	return removed
}

// Drop removes a task's registry (task deleted / shutdown).
func (g *global) Drop(taskID string) {
	g.mu.Lock()
	delete(g.regs, taskID)
	g.mu.Unlock()
}

// Peek returns an existing registry without creating one (for read-only views).
func (g *global) Peek(taskID string) *Registry {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.regs[taskID]
}

// ToJSON is a small helper for activity metadata payloads.
func ToJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// SortedIDs is exported for tests/API determinism checks.
func (r *Registry) SortedIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]string(nil), r.order...)
	sort.Strings(out)
	return out
}
