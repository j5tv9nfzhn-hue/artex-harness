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
	"os"
	"path/filepath"
	"sort"
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
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok || e.rec.Status.Terminal() {
		return
	}
	e.rec.Status = status
	e.rec.Result = result
	e.rec.Error = errText
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
	}
}

// SetAgentID attaches the sidechain transcript id once the child run has one.
func (r *Registry) SetAgentID(id, agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[id]; ok {
		e.rec.AgentID = agentID
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
// only on first creation.
func (g *global) For(taskID, outputDir string) *Registry {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r, ok := g.regs[taskID]; ok {
		return r
	}
	r := New(outputDir)
	g.regs[taskID] = r
	return r
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
