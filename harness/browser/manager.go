// Package browser owns the lifetime of the agent's browser automation session.
//
// Before this package the MCP clients were created per run by the tool-augmentation
// path, which meant: no session reuse, no tab model, no rebuild after a crash, and
// the DevTools (F12) server could never be tied to the same page. docs/09 §2 replaced
// that with this Manager:
//
//	one session per task  →  N MCP connections (playwright + chrome-devtools)
//	                        + tab model + idle GC + rebuild-on-failure
//
// Design constraints kept deliberately:
//   - MCP only. No CDP client: a second browser stack would double the failure modes
//     (two drivers, two debug endpoints, two cookie jars) for the same capability.
//   - Rebuild, don't repair. When a connection errors, the session is marked broken and
//     the next Ensure() relaunches it. Sub-second recovery matters more than saving a
//     process that is already in an unknown state.
//   - The Manager never blocks a turn for longer than its own start budget; a browser
//     that cannot start is reported as an error result, not as a hung agent.
package browser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	actool "github.com/Autumn-27/norma/tool"
)

// Conn is one MCP connection (one server process/stdin-stdout pair).
type Conn interface {
	Tools(ctx context.Context) ([]actool.CoreTool, error)
	Close() error
}

// Launcher starts one MCP server and returns a ready connection.
type Launcher func(ctx context.Context, server string) (Conn, error)

// Config tunes the Manager.
type Config struct {
	// IdleTimeout closes a session that has not been used for this long.
	IdleTimeout time.Duration
	// MaxTabs bounds the logical tab model per session.
	MaxTabs int
	// StartTimeout bounds one server start (a stuck npx must not hang a turn).
	StartTimeout time.Duration
	// Servers to start for each session, in order. DevTools rides the same session.
	Servers []string
	// Specs tells the Manager how to spawn each server when it needs a RAW client
	// (screenshots only — see rawclient.go). Missing entry = screenshots disabled
	// for that server.
	Specs map[string]CommandSpec
}

// CommandSpec is how to start one MCP server.
type CommandSpec struct {
	Command string
	Args    []string
	Env     map[string]string
	// ScreenshotTool is the tool that returns an image (default browser_take_screenshot).
	ScreenshotTool string
}

// DefaultConfig: 30 min idle, 8 tabs, 20 s to start, both browser faces.
func DefaultConfig() Config {
	return Config{
		IdleTimeout:  30 * time.Minute,
		MaxTabs:      8,
		StartTimeout: 20 * time.Second,
		Servers:      []string{"browser", "chrome-devtools"},
	}
}

// Tab is a logical page handle inside a session.
type Tab struct {
	ID        string    `json:"id"`
	URL       string    `json:"url,omitempty"`
	Title     string    `json:"title,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Session is one task's browser session.
type Session struct {
	TaskID   int64
	conns    map[string]Conn
	started  map[string]bool
	broken   map[string]string // server -> reason ("" = healthy)
	fails    map[string]int    // server -> consecutive start failures
	lastUsed time.Time
	tabs     []Tab
	nextTab  int
	// StartFailures counts consecutive failed starts, for the status panel.
	StartFailures int `json:"start_failures"`
	// raw is the side client used only for image capture (see rawclient.go).
	raw    *rawClient
	rawFor string
}

// Tabs returns a copy of the session's tab list.
func (s *Session) Tabs() []Tab {
	out := make([]Tab, len(s.tabs))
	copy(out, s.tabs)
	return out
}

// Manager is the process-wide registry of browser sessions.
type Manager struct {
	mu       sync.Mutex
	cfg      Config
	launch   Launcher
	sessions map[int64]*Session
	// thumbs stores screenshot blobs (content addressed).
	thumbs *ThumbStore
}

// New builds a Manager. thumbs may be nil (screenshot capture disabled).
func New(cfg Config, launch Launcher, thumbs *ThumbStore) *Manager {
	if cfg.MaxTabs <= 0 {
		cfg.MaxTabs = 8
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 30 * time.Minute
	}
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = 20 * time.Second
	}
	if len(cfg.Servers) == 0 {
		cfg.Servers = DefaultConfig().Servers
	}
	return &Manager{cfg: cfg, launch: launch, sessions: map[int64]*Session{}, thumbs: thumbs}
}

// Thumbs exposes the screenshot store (nil-safe callers).
func (m *Manager) Thumbs() *ThumbStore { return m.thumbs }

// Ensure returns a live session for the task, starting or rebuilding as needed.
//
// A server whose previous state is "broken" is relaunched; healthy servers are kept,
// so a Playwright crash does not also tear down DevTools.
func (m *Manager) Ensure(ctx context.Context, taskID int64) (*Session, error) {
	m.mu.Lock()
	s := m.sessions[taskID]
	if s == nil {
		s = &Session{
			TaskID:  taskID,
			conns:   map[string]Conn{},
			started: map[string]bool{},
			broken:  map[string]string{},
			fails:   map[string]int{},
		}
		m.sessions[taskID] = s
	}
	s.lastUsed = time.Now()
	need := make([]string, 0, len(m.cfg.Servers))
	for _, name := range m.cfg.Servers {
		if s.conns[name] == nil {
			need = append(need, name)
		}
	}
	m.mu.Unlock()

	var firstErr error
	for _, name := range need {
		if err := m.startOne(ctx, s, name); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			m.mu.Lock()
			s.broken[name] = err.Error()
			if s.fails == nil {
				s.fails = map[string]int{}
			}
			s.fails[name]++
			m.mu.Unlock()
		}
	}
	if firstErr != nil {
		return s, firstErr
	}
	return s, nil
}

// startOne launches one server outside the manager lock (it can take seconds).
func (m *Manager) startOne(ctx context.Context, s *Session, name string) error {
	if m.launch == nil {
		return fmt.Errorf("browser: no launcher configured")
	}
	startCtx, cancel := context.WithTimeout(ctx, m.cfg.StartTimeout)
	defer cancel()
	c, err := m.launch(startCtx, name)
	if err != nil {
		return fmt.Errorf("启动 %s 失败: %w", name, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old := s.conns[name]; old != nil {
		_ = old.Close()
	}
	s.conns[name] = c
	s.started[name] = true
	delete(s.broken, name)
	if s.fails != nil {
		delete(s.fails, name) // only THIS server recovered
	}
	s.lastUsed = time.Now()
	return nil
}

// MarkBroken records that a server's connection failed; the next Ensure rebuilds it.
func (m *Manager) MarkBroken(taskID int64, server string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[taskID]
	if s == nil {
		return
	}
	if c := s.conns[server]; c != nil {
		_ = c.Close()
		delete(s.conns, server)
	}
	reason := "connection failed"
	if err != nil {
		reason = err.Error()
	}
	s.broken[server] = reason
}

// Tools returns the tools of every live server in the task's session, wrapped so that
// a failure marks that server broken (triggering a rebuild) and so that image results
// are persisted as thumbnails.
func (m *Manager) Tools(ctx context.Context, taskID int64) ([]actool.CoreTool, error) {
	s, err := m.Ensure(ctx, taskID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	names := make([]string, 0, len(s.conns))
	for name := range s.conns {
		names = append(names, name)
	}
	sort.Strings(names)
	conns := make(map[string]Conn, len(names))
	for _, n := range names {
		conns[n] = s.conns[n]
	}
	m.mu.Unlock()

	var out []actool.CoreTool
	for _, name := range names {
		tools, terr := conns[name].Tools(ctx)
		if terr != nil {
			m.MarkBroken(taskID, name, terr)
			continue // a dead server must not break the other one
		}
		for _, t := range tools {
			out = append(out, m.wrap(taskID, name, t))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("浏览器会话没有可用工具（server 启动失败或未连接）")
	}
	return out, nil
}

// wrap makes one remote tool failure-aware and thumbnail-aware.
func (m *Manager) wrap(taskID int64, server string, inner actool.CoreTool) actool.CoreTool {
	name := inner.Name()
	return actool.Build(actool.Spec{
		Name:        name,
		Description: inner.Description(),
		Prompt:      inner.Prompt(),
		Schema:      inner.InputSchema(),
		ReadOnly:    func(in json.RawMessage) bool { return inner.IsReadOnly(in) },
		Permissions: func(ctx context.Context, in json.RawMessage, pc permContext) decision {
			return allowDecision()
		},
		Run: func(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
			res, err := inner.Call(ctx, in, tc)
			if err != nil {
				m.MarkBroken(taskID, server, err)
				// Retry ONCE on a revived server. Without this the agent kept calling a
				// tool bound to a dead pipe for the rest of the turn: one killed Chrome
				// produced "write |1: file already closed" on every navigate/tabs/
				// snapshot/close until the run ended (observed on task 214, ~15 wasted
				// turns). A failed call has not happened, so a single retry is safe.
				if revived, rerr := m.reviveTool(ctx, taskID, server, name); rerr == nil && revived != nil {
					if res2, err2 := revived.Call(ctx, in, tc); err2 == nil {
						res, err = res2, nil
					}
				}
				if err != nil {
					return res, err
				}
			}
			m.touch(taskID)
			// Screenshots are the one thing the SDK cannot carry (its content union has
			// no image type), so the Manager fetches the same image through its own raw
			// client and leaves a marker the console turns into <img> (docs/09 §3).
			if m.thumbs != nil && strings.Contains(strings.ToLower(name), "screenshot") {
				if h, herr := m.CaptureScreenshot(ctx, taskID); herr == nil {
					res.Content = append(res.Content, textBlockFor("[thumb] "+h))
				}
			}
			if res.IsError {
				m.MarkBroken(taskID, server, errors.New(firstText(res)))
			}
			return res, nil
		},
	})
}

// reviveTool restarts one broken server and returns the named tool from the fresh
// connection, so a transport that died mid-turn can be replaced in place.
//
// Ensure() already rebuilds servers whose connection is gone; the gap was that the
// agent held tool values bound to the OLD connection for the whole turn. This is the
// lookup that turns "the pipe died" from a terminal condition into a hiccup.
func (m *Manager) reviveTool(ctx context.Context, taskID int64, server, tool string) (actool.CoreTool, error) {
	s, err := m.Ensure(ctx, taskID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	conn := s.conns[server]
	m.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("browser: %s 未能恢复连接", server)
	}
	tools, err := conn.Tools(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range tools {
		if t.Name() == tool {
			return t, nil
		}
	}
	return nil, fmt.Errorf("browser: %s 恢复后没有工具 %s", server, tool)
}

// CaptureScreenshot grabs one PNG from the task's browser session and stores it
// content-addressed. Returns the hash (the console asks for /api/harness/shot/<hash>).
//
// It uses the Manager's own raw stdio client because norma's llm.ContentBlock union
// has no image type — widening it would touch every provider's serializer for one
// feature. Text tools are unaffected: they still go through norma/mcp.
func (m *Manager) CaptureScreenshot(ctx context.Context, taskID int64) (string, error) {
	if m.thumbs == nil {
		return "", fmt.Errorf("截图存储未启用")
	}
	spec, ok := m.cfg.Specs["browser"]
	if !ok || spec.Command == "" {
		return "", fmt.Errorf("没有 browser 服务器的启动规格，无法截图")
	}
	tool := spec.ScreenshotTool
	if tool == "" {
		tool = "browser_take_screenshot"
	}
	m.mu.Lock()
	s := m.sessions[taskID]
	m.mu.Unlock()
	if s == nil {
		return "", fmt.Errorf("任务没有浏览器会话")
	}

	// Reuse the side client; restart it once if the process died.
	for attempt := 0; attempt < 2; attempt++ {
		m.mu.Lock()
		raw := s.raw
		if raw == nil {
			m.mu.Unlock()
			c, err := startRaw(ctx, spec.Command, spec.Args, spec.Env)
			if err != nil {
				return "", fmt.Errorf("启动截图客户端失败: %w", err)
			}
			m.mu.Lock()
			s.raw, s.rawFor = c, spec.Command
			raw = c
			m.mu.Unlock()
		} else {
			m.mu.Unlock()
		}
		data, err := raw.Screenshot(ctx, tool)
		if err == nil {
			return m.thumbs.Save(data)
		}
		// Fail once → drop the client so the next attempt relaunches it.
		m.mu.Lock()
		if s.raw == raw {
			_ = raw.Close()
			s.raw = nil
		}
		m.mu.Unlock()
		if attempt == 1 {
			return "", err
		}
	}
	return "", fmt.Errorf("截图失败")
}

// noteTab records the URL-ish marker for the tab model (best effort).
func (m *Manager) noteTab(taskID int64, tool, hash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[taskID]
	if s == nil || len(s.tabs) == 0 {
		return
	}
	s.tabs[len(s.tabs)-1].Title = filepath.Base(tool) + " " + hash[:8]
}

func (m *Manager) touch(taskID int64) {
	m.mu.Lock()
	if s := m.sessions[taskID]; s != nil {
		s.lastUsed = time.Now()
	}
	m.mu.Unlock()
}

// OpenTab adds a logical tab (page). Isolation is delegated to the MCP server; the
// Manager's job is to remember which handle belongs to which context so the console
// can show "tab 2 is on /login, tab 1 is on /admin" and so a rebuild can restore them.
func (m *Manager) OpenTab(taskID int64, url string) (Tab, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[taskID]
	if s == nil {
		return Tab{}, fmt.Errorf("会话不存在")
	}
	if len(s.tabs) >= m.cfg.MaxTabs {
		return Tab{}, fmt.Errorf("标签数已达上限 %d", m.cfg.MaxTabs)
	}
	s.nextTab++
	tab := Tab{ID: fmt.Sprintf("t%d", s.nextTab), URL: url, CreatedAt: time.Now()}
	s.tabs = append(s.tabs, tab)
	s.lastUsed = time.Now()
	return tab, nil
}

// CloseTab removes a tab; unknown ids are ignored (idempotent).
func (m *Manager) CloseTab(taskID int64, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[taskID]
	if s == nil {
		return
	}
	for i, t := range s.tabs {
		if t.ID == id {
			s.tabs = append(s.tabs[:i], s.tabs[i+1:]...)
			return
		}
	}
}

// Recreate closes and relaunches every server for a task (the "restart browser"
// button, and what Ensure does implicitly after a crash).
func (m *Manager) Recreate(ctx context.Context, taskID int64) error {
	m.mu.Lock()
	s := m.sessions[taskID]
	if s != nil {
		for name, c := range s.conns {
			_ = c.Close()
			delete(s.conns, name)
			s.broken[name] = "manual restart"
		}
	}
	m.mu.Unlock()
	_, err := m.Ensure(ctx, taskID)
	return err
}

// Release closes a task's session (task delete / shutdown).
func (m *Manager) Release(taskID int64) {
	m.mu.Lock()
	s := m.sessions[taskID]
	delete(m.sessions, taskID)
	m.mu.Unlock()
	if s == nil {
		return
	}
	for _, c := range s.conns {
		_ = c.Close()
	}
	if s.raw != nil {
		_ = s.raw.Close()
	}
}

// GC closes sessions idle beyond the timeout. Returns how many were closed.
func (m *Manager) GC(now time.Time) int {
	m.mu.Lock()
	var expire []int64
	for id, s := range m.sessions {
		if now.Sub(s.lastUsed) > m.cfg.IdleTimeout {
			expire = append(expire, id)
		}
	}
	m.mu.Unlock()
	for _, id := range expire {
		m.Release(id)
	}
	return len(expire)
}

// SessionStatus is the console-facing view of one session.
type SessionStatus struct {
	TaskID        int64             `json:"task_id"`
	Alive         bool              `json:"alive"`
	Servers       map[string]string `json:"servers"` // name -> state (live / broken: reason)
	Started       []string          `json:"started"`
	Tabs          []Tab             `json:"tabs"`
	LastUsedUnix  int64             `json:"last_used_unix"`
	StartFailures int               `json:"start_failures"`
}

// Status reports one task's session (zero value when none).
func (m *Manager) Status(taskID int64) SessionStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[taskID]
	st := SessionStatus{TaskID: taskID, Servers: map[string]string{}}
	if s == nil {
		return st
	}
	st.Alive = true
	st.Tabs = append([]Tab(nil), s.tabs...)
	st.LastUsedUnix = s.lastUsed.Unix()
	for _, n := range s.fails {
		st.StartFailures += n
	}
	for _, name := range m.cfg.Servers {
		if reason, ok := s.broken[name]; ok {
			st.Servers[name] = "broken: " + reason
			continue
		}
		if s.conns[name] != nil {
			st.Servers[name] = "live"
			st.Started = append(st.Started, name)
			continue
		}
		st.Servers[name] = "not started"
	}
	return st
}

// ── thumbnail store ──────────────────────────────────────────────────────

// ThumbStore is a content-addressed PNG store with TTL + count cap.
type ThumbStore struct {
	dir  string
	ttl  time.Duration
	max  int
	mu   sync.Mutex
	seen map[string]time.Time
}

// NewThumbStore prepares dir. ttl<=0 disables expiry; max<=0 disables the cap.
func NewThumbStore(dir string, ttl time.Duration, max int) (*ThumbStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &ThumbStore{dir: dir, ttl: ttl, max: max, seen: map[string]time.Time{}}, nil
}

// Dir exposes the storage path (for tests and the UI note).
func (t *ThumbStore) Dir() string { return t.dir }

// Save writes data (deduped by sha256) and returns the hash.
func (t *ThumbStore) Save(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	path := t.path(h)
	t.mu.Lock()
	_, statErr := os.Stat(path)
	t.mu.Unlock()
	if statErr == nil {
		t.touch(h)
		return h, nil // identical image: keep one copy
	}
	if err := os.WriteFile(path+".tmp", data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return "", err
	}
	t.touch(h)
	return h, nil
}

// Get returns the bytes for a hash.
func (t *ThumbStore) Get(h string) ([]byte, error) {
	if !validHash(h) {
		return nil, fmt.Errorf("bad hash")
	}
	return os.ReadFile(t.path(h))
}

// GC removes expired files and enforces the count cap (oldest first).
func (t *ThumbStore) GC(now time.Time) (removed int) {
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		return 0
	}
	type item struct {
		name string
		mod  time.Time
	}
	live := make([]item, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".png") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if t.ttl > 0 && now.Sub(info.ModTime()) > t.ttl {
			if os.Remove(filepath.Join(t.dir, e.Name())) == nil {
				removed++
			}
			continue
		}
		live = append(live, item{name: e.Name(), mod: info.ModTime()})
	}
	if t.max > 0 && len(live) > t.max {
		sort.Slice(live, func(i, j int) bool { return live[i].mod.Before(live[j].mod) })
		for _, it := range live[:len(live)-t.max] {
			if os.Remove(filepath.Join(t.dir, it.name)) == nil {
				removed++
			}
		}
	}
	return removed
}

// Count reports how many thumbnails are stored (tests / status).
func (t *ThumbStore) Count() int {
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".png") && !e.IsDir() {
			n++
		}
	}
	return n
}

func (t *ThumbStore) path(h string) string { return filepath.Join(t.dir, h+".png") }
func (t *ThumbStore) touch(h string)       { t.mu.Lock(); t.seen[h] = time.Now(); t.mu.Unlock() }

func validHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
