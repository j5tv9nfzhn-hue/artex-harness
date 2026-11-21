package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/skill"
)

// validSkillName checks the agentskills.io name constraints, widened so a skill can
// also be named in Chinese (or any other script): ASCII must stay lowercase
// alphanumeric + hyphens, while non-ASCII letters/digits are accepted as-is.
// 1-64 runes, must start with a letter, no leading/trailing/consecutive hyphens.
// The name doubles as a directory name under skillDir, so nothing that could carry a
// path (separators, dots, spaces, control characters) is allowed through.
func validSkillName(name string) bool {
	if name == "" || !utf8.ValidString(name) {
		return false
	}
	rs := []rune(name)
	if len(rs) > 64 {
		return false
	}
	isLetter := func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r > unicode.MaxASCII && unicode.IsLetter(r))
	}
	if !isLetter(rs[0]) || rs[len(rs)-1] == '-' {
		return false
	}
	for _, r := range rs {
		switch {
		case isLetter(r), r >= '0' && r <= '9', r == '-':
		case r > unicode.MaxASCII && unicode.IsDigit(r):
		default:
			return false
		}
	}
	return !strings.Contains(name, "--")
}

// reAgentKey mirrors the agents.key DB check: lowercase letter start, then
// lowercase letters / digits / underscores.
var reAgentKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// pgReady returns the PG handle, or writes 503 and returns nil if unavailable.
func (s *Server) pg(w http.ResponseWriter) *db.DB {
	if s.m.pg == nil {
		writeErr(w, 503, "管理后台数据源(PostgreSQL)未连接")
		return nil
	}
	return s.m.pg
}

func pathInt(r *http.Request, name string) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return n, err == nil
}

func decode(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }

// ---------- tasks (delete) ----------

const taskDeleteDrainTimeout = 10 * time.Second

func canonicalTaskID(raw string) (string, bool) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return "", false
	}
	return strconv.FormatInt(n, 10), true
}

// beginTaskDelete serializes the delete barrier with pause, resume, admission,
// and FIFO reconciliation. Every lifecycle path rechecks deleting after taking
// concMu, so none can commit a contradictory state once this returns.
func (s *Server) beginTaskDelete(taskID string) bool {
	s.concMu.Lock()
	defer s.concMu.Unlock()
	if s.engine.IsDeleting(taskID) {
		return false
	}
	return s.engine.BeginDelete(taskID)
}

// abortTaskDelete restores the execution barrier from the committed task row,
// not from the in-memory state observed before deletion began. Keeping the task
// paused on an unreadable row is conservative: a later explicit resume can
// safely recover it without allowing work to escape an uncertain delete.
func (s *Server) abortTaskDelete(taskID string) {
	s.concMu.Lock()
	defer s.concMu.Unlock()
	keepPaused := true
	if id, err := strconv.ParseInt(taskID, 10, 64); err == nil && s.m != nil && s.m.pg != nil {
		if persisted, getErr := s.m.pg.GetTask(id); getErr == nil && persisted != nil {
			keepPaused = persisted.Paused || persisted.Queued
		} else if task, ok := s.m.Task(taskID); ok {
			state := task.lifecycleSnapshot()
			keepPaused = state.Paused || state.Queued
			if getErr != nil {
				log.Printf("[task-delete] task %s 读取持久状态失败，使用内存状态恢复屏障: %v", taskID, getErr)
			}
		} else if getErr == nil {
			// The request targeted a task that does not exist. Do not retain a
			// synthetic pause entry after releasing its temporary delete barrier.
			keepPaused = false
		}
	}
	s.engine.AbortDelete(taskID, keepPaused)
}

func (s *Server) pgDeleteTask(w http.ResponseWriter, r *http.Request) {
	id, ok := canonicalTaskID(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "任务 id 无效")
		return
	}
	var opts DeleteTaskOptions
	if err := decode(r, &opts); err != nil && err != io.EOF {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	if !s.beginTaskDelete(id) {
		writeErr(w, http.StatusConflict, "任务正在删除")
		return
	}
	deleted := false
	defer func() {
		if !deleted {
			s.abortTaskDelete(id)
		}
	}()

	// Main-agent runs use a separate context from planner/workers. Cancel it, then
	// wait until both execution domains have returned before removing transcripts.
	s.cancelTaskChat(id, agent.AbortTaskDeleted)
	// Stop the task-scoped loops (planner/worker/harness) before draining. A harness
	// loop parks on its message queue after a turn, so a task that had simply finished
	// kept a live goroutine and the drain hit its timeout: the task became impossible
	// to delete ("任务仍有运行中的 Agent") even though nothing was running.
	s.engine.StopTask(id)
	drainCtx, cancelDrain := context.WithTimeout(r.Context(), taskDeleteDrainTimeout)
	defer cancelDrain()
	if err := s.waitTaskQuiescent(drainCtx, id); err != nil {
		writeErr(w, http.StatusConflict, "任务仍有运行中的 Agent，删除已取消")
		return
	}

	if err := s.drainTaskSideQuestions(drainCtx, id); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	result, err := s.m.DeleteTask(id, opts)
	if err != nil {
		var committed *taskDeleteCommittedError
		if errors.As(err, &committed) {
			// PostgreSQL is already gone. Complete runtime teardown and return the
			// auditable counts together with the post-commit cleanup warning.
			s.engine.StopTask(id)
			s.taskAgentMu.Lock()
			delete(s.taskAgents, id)
			s.taskAgentMu.Unlock()
			deleted = true
			writeCommittedTaskDelete(w, result, err)
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	// Manager has removed the task from the registry, so no new API operation can
	// resolve it. Now stop and join every task-owned Engine goroutine and clear its
	// lifecycle maps before releasing the delete barrier.
	s.engine.StopTask(id)
	s.taskAgentMu.Lock()
	delete(s.taskAgents, id)
	s.taskAgentMu.Unlock()
	deleted = true
	writeJSON(w, 200, result)
}

func writeCommittedTaskDelete(w http.ResponseWriter, result DeleteTaskResult, err error) {
	result.CleanupWarning = err.Error()
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) waitTaskQuiescent(ctx context.Context, taskID string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.chatMu.Lock()
		chatBusy := s.chatBusy[taskID]
		s.chatMu.Unlock()
		if s.engine.inflightCount(taskID) == 0 && !chatBusy {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ---------- agents ----------

// overlay per-agent binding counts (mcp/skill by id, tools by key) — best-effort.

// pgCreateAgent creates a CUSTOM conversational agent (builtin=false, role
// 'assistant') and seeds it a starter prompt so its editor isn't blank.

// starter prompt so the editor shows something editable from the start.

// pgUpdateAgent updates a custom agent's name/description (built-in agents rejected).

// pgDeleteAgent removes a custom agent (built-in rejected). Prompts/vars/visibility
// cascade via FK; tool-binding cleanup is best-effort.

// pgSaveAgentConfig updates an agent's runtime config (currently max_turns) and
// re-applies the live LLM so the change takes effect immediately.

// All fields optional (pointers) so a partial patch (e.g. the triggers tab sending
// only trigger_* fields) leaves the untouched settings alone instead of resetting
// max_turns/run_seconds to 0.

// llm_profile_id 三态:字段缺省=不动;显式 null=解绑(跟随任务/全局);数字=绑定该 profile。

// P3 触发后处理策略(三者一起可选,提供任一即整体写入;未提供则不动)。

// key present (数字 或 null)

// 绑定:校验目标 profile 有效

// P3 触发策略:三者作为一组写入(SetAgentTriggerBehavior 一次写三列),缺的字段用
// 当前存量值回填,避免只传一个把另两个覆盖成默认。

// an agent's LLM binding change also invalidates pinned-task / per-profile caches
// so their next round re-resolves each agent's bound model.

// Task bundles capture operational Agent settings (turn/time budgets, tools,
// web search). Rebuild them even when no global profile is configured.

// rebuild the live agents so the new max_turns/run_seconds/binding apply without a restart.

// 可选 LLM 配置列表(id/name/model/是否默认),供前端渲染 "默认模型" 下拉;当前绑定见 agent.llm_profile_id。

// 可绑定的 LLM 配置候选

// 已保存的收尾提示词(空=用内置默认)
// 内置默认(供占位/恢复默认)
// 已保存的收尾轮数(0=用内置默认)
// 内置默认轮数(供 "0=默认N" 提示)
// 任务级超时收尾词(仅 worker/planner 有内置默认;task_timeout_supported 供前端决定是否显示该分区)

// pgResetPrompt restores an agent's prompt body to the in-code built-in default
// (段 [A]). Only built-in agents have a code default; custom agents have none.

// pgSaveWrapup stores an agent's wrap-up (settlement) prompt — the text injected on
// timeout / step-exhaustion. Empty body clears the override → built-in default.

// MaxTurns is optional (pointer): omit to leave the stored turn budget untouched.

// pgResetWrapup clears an agent's wrap-up prompt override so the code built-in
// default is used again.

// pgSaveTaskTimeoutWrapup stores an agent's TASK-TIMEOUT wrap-up prompt + turn budget
// (worker/planner only). Empty prompt / 0 turns clear the override → built-in default.

// 未传则保留原值

// pgResetTaskTimeoutWrapup clears the task-timeout wrap-up override → built-in default.

// ---------- tools (内置工具目录) ----------

// Usage is best-effort decoration. Runtime tool resolution keeps using the plain
// catalog query, so agent assembly never pays for this aggregate.

// pgUpdateTool saves the page-editable fields of a built-in tool: description,
// parameter schema (structure is expected unchanged — only per-param description/
// default move), agent binding, and enabled. key is taken from the path and never
// changes (it is welded to the Go handler).

// pgResetTool overwrites a tool row with its code-defined defaults (description,
// schema, agent binding) and re-enables it — the explicit "恢复默认" action, since
// startup seeding is first-insert-only and never overwrites edits.

// orchestration/platform tools (auto agent) — default-bind to "auto".

// ---------- mcp ----------

func (s *Server) pgListMCP(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	ms, err := pg.ListMCP()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if ms == nil {
		ms = []*db.MCPServer{}
	}
	writeJSON(w, 200, map[string]any{"servers": ms})
}

// On initial add, auto-discover + cache the tool list so the UI shows it right
// away (bounded so a slow/broken server can't hang the request). Skipped on
// plain updates (e.g. enable toggles) to avoid re-spawning the server each time.

// pgRefreshMCP re-discovers one MCP's tools on demand and re-caches them, so a
// config change or an earlier discovery failure can be fixed without a restart.

// pgMCPTools returns one MCP's cached tools (name + description) for the detail UI.

// ---------- skills (文件系统) ----------

type skillFileNode struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	License       string   `json:"license,omitempty"`
	Compatibility string   `json:"compatibility,omitempty"`
	MCPs          []string `json:"mcps,omitempty"`
	Files         []string `json:"files"`
	// Usage ledger (db/skill_usage.go). Calls is 0 and LastUsed nil for a skill that
	// was never invoked — the ledger only has rows for skills agents actually loaded.
	Calls    int        `json:"calls"`
	Tasks    int        `json:"tasks"`
	Agents   []string   `json:"usage_agents"`
	LastUsed *time.Time `json:"last_used,omitempty"`
}

func (s *Server) fsListSkills(w http.ResponseWriter, r *http.Request) {
	_ = os.MkdirAll(s.skillDir, 0o755)
	allReg, _ := skill.LoadDir(s.skillDir)
	metaByDir := map[string]skill.Skill{}
	if allReg != nil {
		for _, sk := range allReg.List() {
			if sk.Dir != "" {
				metaByDir[filepath.Base(sk.Dir)] = sk
			}
		}
	}
	entries, err := os.ReadDir(s.skillDir)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// Usage is best-effort decoration: a ledger read failure leaves the counts at
	// zero rather than failing the skill list itself.
	statBySkill := map[string]db.SkillStat{}
	if s.m.pg != nil {
		stats, err := s.m.pg.SkillStats()
		if err != nil {
			log.Printf("[skills] 读取调用统计失败: %v", err)
		}
		for _, st := range stats {
			statBySkill[st.Skill] = st
		}
	}
	nodes := []skillFileNode{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirName := e.Name()
		node := skillFileNode{Name: dirName, Agents: []string{}}
		if st, ok := statBySkill[dirName]; ok {
			node.Calls, node.Tasks, node.Agents, node.LastUsed = st.Calls, st.Tasks, st.Agents, st.LastUsed
		}
		if meta, ok := metaByDir[dirName]; ok {
			node.Description = meta.Description
			node.License = meta.License
			node.Compatibility = meta.Compatibility
			node.MCPs = meta.MCPs
		}
		node.Files, _ = walkSkillFiles(filepath.Join(s.skillDir, dirName))
		if node.Files == nil {
			node.Files = []string{}
		}
		nodes = append(nodes, node)
	}
	writeJSON(w, 200, map[string]any{"skills": nodes})
}

// fsSkillUsage returns one skill's recent invocations, newest first (detail panel).

// fsMissingSkills lists skill names agents asked for that do not exist — the gap
// list, i.e. which procedures are worth writing next.

// cleanStrs trims each string and drops empties.

// required per agentskills.io spec
// optional
// optional
// optional; MCP servers this skill unlocks
// optional; scaffolded if empty

// Build a spec-compliant SKILL.md (agentskills.io format):
//   YAML frontmatter: name (required), description (required),
//                     license, compatibility (optional)
//   Markdown body:    step-by-step instructions

// mcps: MCP servers this skill unlocks on load (skill-gated deferred tools).

// scaffold a minimal Markdown body so the file is immediately useful

// fsUpdateSkillMeta rewrites the SKILL.md frontmatter fields that are safe to
// change without touching the instruction body: mcps, license, compatibility,
// description. Only fields present in the request body are updated; omitted
// fields are left as-is (the whole frontmatter is reconstructed from the
// parsed values, so the write is a clean replace, not a line-patch).

// rewriteSkillFrontmatter parses the SKILL.md YAML frontmatter and replaces the
// fields given as non-nil pointers. The instruction body (after the closing ---) is
// preserved verbatim. Returns an error if the file has no recognisable frontmatter.

// Collect existing key → value from frontmatter (preserve unknown keys)

// preserve non-key lines verbatim

// Apply updates (nil pointer = no change)

// Remove existing mcps line

// Reconstruct

// Body (lines after closing ---)

// zip-upload safety caps (defeat zip bombs / runaway archives).
const (
	maxSkillZipBytes   = 20 << 20  // 20MB compressed request body
	maxSkillTotalBytes = 100 << 20 // 100MB total uncompressed
	maxSkillFileBytes  = 20 << 20  // 20MB per extracted file
	maxSkillEntries    = 4000      // max files in the archive
)

// skillNameFromFrontmatter extracts the `name:` value from a SKILL.md's YAML
// frontmatter (the block between the first two `---` lines). "" if absent.

// end of frontmatter

// name: "中文技能" 也认

// fsUploadSkill installs a skill from an uploaded .zip. The archive must contain a
// SKILL.md (at the root or under a single top-level dir); the skill name is taken
// from that file's `name:` frontmatter (falling back to the top dir / zip name).
// Zip-slip is defeated by validating every entry path with skillRelPath, and a set
// of size/count caps guard against zip bombs. POST ?overwrite=true replaces an
// existing skill of the same name.

// entries carry UTF-8-decoded names (GBK 包也能读) and exclude archiver junk.

// locate the shallowest SKILL.md → its directory is the skill root inside the zip.

// "." when SKILL.md is at the zip root

// derive + validate the skill name from the SKILL.md frontmatter.

// extract into a temp dir first, then atomically swap in — a bad entry aborts
// the whole upload without leaving a half-written skill.

// no-op after a successful rename

// only files under the skill root; skip anything outside it.

// readZipEntry reads a single entry's bytes (capped). Takes the *zip.File rather than
// a name because zr.Open rejects names that aren't valid UTF-8 (GBK-named archives).

// skillPathBlocked lists the ASCII characters a skill-relative path may not contain.
// '\' is a Windows separator; '%' keeps double-URL-encoding tricks from surviving
// (Go's net/http URL-decodes PathValue once — %2F→/ %2e→. — so an attacker sending
// %252e%252e arrives here as the literal "%2e%2e" and is rejected on the '%'); '#'
// and '?' would truncate the path when it travels back through a URL; the rest are
// reserved on Windows filesystems.
const skillPathBlocked = `\%#?*:"<>|`

// skillPathRune reports whether r may appear in a client-supplied skill path.
// It is a blacklist over Unicode rather than an ASCII whitelist so that 中文 (and any
// other script) file names work, while everything that makes path validation hard is
// still refused: control/format characters, look-alike whitespace, separators.
func skillPathRune(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f, r == utf8.RuneError:
		return false // NUL & control characters, invalid UTF-8
	case strings.ContainsRune(skillPathBlocked, r):
		return false
	case unicode.Is(unicode.Cf, r), unicode.Is(unicode.Co, r), unicode.Is(unicode.Cs, r):
		return false // zero-width joiners, bidi overrides (RLO 文件名伪装), private use
	case r != ' ' && unicode.IsSpace(r):
		return false // NBSP / 全角空格 之类：看着是空格，其实不是
	}
	return true
}

// maxSkillPathLen caps a relative path so a pathological name can't reach the syscall.
const maxSkillPathLen = 512

// skillRelPath validates a relative file path supplied by the client.
// Returns the cleaned path and an empty error string on success.
// Validation order matters: character check first (before Clean) so encoding tricks
// can't survive normalization.
func skillRelPath(file string) (string, string) {
	// 1. Character check — before normalization. Defeats null bytes, backslash,
	//    '%', control characters and unicode look-alikes; CJK names pass through.
	if file == "" || len(file) > maxSkillPathLen {
		return "", "invalid path: empty or too long"
	}
	if !utf8.ValidString(file) {
		return "", "invalid path: not valid UTF-8"
	}
	for _, r := range file {
		if !skillPathRune(r) {
			return "", "invalid path: illegal character " + strconv.QuoteRune(r)
		}
	}
	// 2. Reject ".." explicitly. With the whitelist above, encoding bypass is already
	//    impossible, but we keep this check so the intent is obvious to reviewers.
	if strings.Contains(file, "..") {
		return "", "invalid path: '..' not allowed"
	}
	// 3. Reject leading slash and empty segments (double slash).
	//    Leading slash would survive filepath.Clean as an absolute path.
	if strings.HasPrefix(file, "/") || strings.Contains(file, "//") {
		return "", "invalid path: must be relative with no empty segments"
	}
	// 4. Normalize and final safety re-check after Clean.
	//    filepath.Clean removes redundant separators and resolves single dots.
	clean := filepath.Clean(file)
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", "invalid path"
	}
	return clean, ""
}

// walkSkillFiles returns all files under root (relative to root), sorted,
// including files in subdirectories (scripts/, references/, assets/, etc.).
// walkSkillFiles returns all entries under root relative to root.
// Directories are included with a trailing "/" so the frontend can distinguish
// them from files and render empty folders in the tree.
func walkSkillFiles(root string) ([]string, error) {
	var entries []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			entries = append(entries, rel+"/")
		} else {
			entries = append(entries, rel)
		}
		return nil
	})
	return entries, err
}

// create parent subdirectory if needed (e.g. scripts/, references/, assets/)

// ---------- skill visibility ----------

// ---------- visibility (MCP resource side + toggle) ----------

// ---------- llm profiles ----------

func (s *Server) pgListProfiles(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	ps, err := pg.ListProfiles()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"profiles": llmProfileDTOs(ps)})
}

func (s *Server) pgSaveProfile(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	// APIKey has json:"-" on db.LLMProfile so it is never leaked to the UI on
	// read; accept it here via a sibling field for create/update. Streaming is a
	// *bool shadowing the embedded json:"streaming": an absent field must default
	// to streaming (true), which a plain bool zero value (false = non-streaming)
	// would get wrong — legacy/partial clients that never send it stay streaming.
	var body struct {
		db.LLMProfile
		APIKey    string `json:"api_key"`
		Streaming *bool  `json:"streaming"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	p := body.LLMProfile
	p.APIKey = body.APIKey
	p.Streaming = body.Streaming == nil || *body.Streaming
	// 输出上限:负数无意义,归零(= 不发送该字段)。字段名开关只有 Chat Completions
	// 用得上——anthropic 与 openai-responses 各自定死了字段名,存下来只会误导后续读者,
	// 故非 openai 格式一律清空。未知取值同样清空,避免把 DB CHECK 的报错甩给用户。
	if p.MaxTokens < 0 {
		p.MaxTokens = 0
	}
	if p.Format != "openai" || p.MaxTokensField != llm.MaxTokensFieldCompletion {
		p.MaxTokensField = ""
	}
	id, err := pg.SaveProfile(&p)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// Editing a profile rebuilds any task pinned to it on its next round. Reapply
	// the active profile too: the global fallback and explicit task chains must
	// repopulate the same provider-cache entry and therefore share one limiter.
	s.invalidateProfileAgents()
	// Hot-apply. Not just when the ACTIVE profile changed: with failover on, any
	// profile's key/model/priority/pool_exclude edit reshapes the chain, so the
	// engine's provider has to be rebuilt either way.
	s.reapplyActiveProfile()
	writeJSON(w, 200, map[string]any{"id": id})
}

// pgGetLLMRetryPolicy 返回全局重试策略(五层各自的次数+间隔)。未配置过 → 全零，
// 前端把零显示成「默认」。

// pgSaveLLMRetryPolicy 保存全局重试策略。三个「跟着端点走」的层(建连/空响应/同
// provider 安全窗口)是 provider 的构建参数或调用参数，改完必须让缓存里的 provider
// 重建；熔断参数则直接推给进程级 Registry。

func (s *Server) pgDeleteProfile(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, _ := pathInt(r, "id")
	if err := pg.DeleteProfileContext(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, db.ErrActiveLLMProfileDelete):
			writeErr(w, 409, "当前激活的 LLM 配置不能删除，请先激活其他配置")
		case errors.Is(err, db.ErrLLMProfileReferencesChanged):
			writeErr(w, 409, "LLM 配置正在被任务或会话修改，请重试")
		case errors.Is(err, context.DeadlineExceeded):
			writeErr(w, 409, "等待 LLM 配置引用释放超时，请重试")
		case errors.Is(err, db.ErrLLMProfileNotFound):
			writeErr(w, 404, err.Error())
		default:
			writeErr(w, 500, err.Error())
		}
		return
	}
	s.invalidateProfileAgents() // drop cached agents for the removed profile
	s.llmHealth.Reset(id)       // its breaker state is meaningless now (row is FK-cascaded away)
	s.restoreTasksAfterProfileDelete(pg)
	// Cache invalidation also removed the active profile's shared provider entry.
	// Reapply after task-state sync so the wake-up observes the post-delete chain.
	s.reapplyActiveProfile()
	writeJSON(w, 200, map[string]any{"deleted": id})
}

func (s *Server) restoreTasksAfterProfileDelete(pg *db.DB) {
	for _, task := range s.m.List() {
		if taskID, err := strconv.ParseInt(task.ID, 10, 64); err == nil {
			if pt, err := pg.GetTask(taskID); err == nil && pt != nil {
				s.syncTaskLLMState(pt)
				// Deleting the active entry may advance the task to a ready successor,
				// or clear the explicit chain and restore its Agent/global fallback.
				// Resume only intents that were blocked by the exhausted chain; a
				// paused task remains paused and terminal tasks remain immutable.
				if !isTerminalStatus(task.lifecycleSnapshot().Status) && s.taskRuntimeAvailable(task, "planner", "worker") {
					if _, reopenErr := task.Store.ReopenIntentsByBlockedReason(db.IntentBlockedLLMQuota); reopenErr != nil {
						log.Printf("[llm-profile] task %s reopen quota-blocked intents after profile delete: %v", task.ID, reopenErr)
					}
				}
				task.Notify()
			}
		}
	}
}

func (s *Server) pgActivateProfile(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := pg.SetActiveProfile(body.ID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.invalidateProfileAgents() // active change may affect pinned-task fallbacks
	s.reapplyActiveProfile()    // switch the running engine to the newly activated profile
	writeJSON(w, 200, map[string]any{"ok": true})
}

// pgLLMPoolStatus reports the failover ("轮询") switches, the resolved chain order
// and every profile's circuit-breaker state — what the LLM page renders as the
// "轮询顺序" strip and the per-card health badges.

// pgLLMPoolReset clears a tripped profile's circuit breaker so the next call
// tries it again immediately ("立即恢复"). id=0 clears every profile.

// 0 / omitted = all

// pgListModels fetches available models from the provider's API endpoint.
// Supports OpenAI-format (GET /models) and Anthropic-format (GET /v1/models); for
// Anthropic-compatible third parties (e.g. DeepSeek) whose model list lives only on
// the OpenAI path, it falls back to the OpenAI endpoint at the stripped root.
func (s *Server) pgListModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider  string `json:"provider"` // "openai" | "anthropic"
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
		Proxy     string `json:"proxy"`
		ProfileID *int64 `json:"profile_id"` // fallback: use stored key from this profile
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// Resolve API key: form input > profile stored key.
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" && req.ProfileID != nil {
		if p, err := s.m.pg.ProfileByID(*req.ProfileID); err == nil && p != nil {
			apiKey = p.APIKey
		}
	}
	if apiKey == "" {
		writeJSON(w, 200, map[string]any{"ok": false, "error": "未提供 API Key"})
		return
	}

	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	provider := strings.TrimSpace(req.Provider)

	// Candidate endpoints to try in order. Some Anthropic-compatible providers
	// (e.g. DeepSeek) implement /v1/messages under an /anthropic path but expose
	// the model list only on their OpenAI-format path — so for anthropic we fall
	// back to the OpenAI endpoint at the stripped root.
	type candidate struct {
		url string
		hdr http.Header
	}
	bearerHdr := func() http.Header {
		h := http.Header{}
		h.Set("Authorization", "Bearer "+apiKey)
		return h
	}
	anthropicHdr := func() http.Header {
		h := http.Header{}
		h.Set("x-api-key", apiKey)
		h.Set("anthropic-version", "2023-06-01")
		return h
	}

	var candidates []candidate
	switch provider {
	case "openai", "openai-responses":
		// Responses API shares the OpenAI model list at /v1/models; tolerate a full
		// endpoint URL of either format.
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		b := strings.TrimRight(strings.TrimSuffix(strings.TrimSuffix(baseURL, "/chat/completions"), "/responses"), "/")
		candidates = append(candidates, candidate{b + "/models", bearerHdr()})
	default: // anthropic
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		b := strings.TrimRight(strings.TrimSuffix(baseURL, "/v1/messages"), "/")
		candidates = append(candidates, candidate{b + "/v1/models", anthropicHdr()})
		// Fallback for Anthropic-compatible third parties whose model list lives on
		// the OpenAI path: strip a trailing /anthropic and try the OpenAI endpoint.
		if root := strings.TrimRight(strings.TrimSuffix(b, "/anthropic"), "/"); root != b {
			candidates = append(candidates,
				candidate{root + "/models", bearerHdr()},
				candidate{root + "/v1/models", bearerHdr()},
			)
		}
	}

	// Build HTTP client with optional proxy.
	transport := &http.Transport{}
	if p := strings.TrimSpace(req.Proxy); p != "" {
		if pu, err := url.Parse(p); err == nil {
			transport.Proxy = http.ProxyURL(pu)
		}
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}

	// Try each candidate; return the first that yields a non-empty model list.
	var lastErr string
	emptyOK := false
	for _, c := range candidates {
		httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, c.url, nil)
		if err != nil {
			lastErr = "构建请求失败: " + err.Error()
			continue
		}
		httpReq.Header = c.hdr
		resp, err := client.Do(httpReq)
		if err != nil {
			lastErr = "请求失败: " + err.Error()
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Sprintf("API 返回 %d: %s", resp.StatusCode, string(body[:min(len(body), 512)]))
			continue
		}
		// Both OpenAI and Anthropic return {"data": [{"id": "..."},...]}. Aggregators
		// (OpenRouter et al.) additionally report capacity per model; when they do,
		// that number beats anything the catalog knows.
		var parsed struct {
			Data []struct {
				ID             string `json:"id"`
				DisplayName    string `json:"display_name"`
				Name           string `json:"name"`
				ContextLength  int    `json:"context_length"`
				ContextWindow  int    `json:"context_window"`
				MaxInputTokens int    `json:"max_input_tokens"`
				MaxTokens      int    `json:"max_tokens"`
				MaxOutput      int    `json:"max_output_tokens"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			lastErr = "解析响应失败: " + err.Error()
			continue
		}
		models := make([]string, 0, len(parsed.Data))
		details := make([]map[string]any, 0, len(parsed.Data))
		firstPos := func(vals ...int) int {
			for _, v := range vals {
				if v > 0 {
					return v
				}
			}
			return 0
		}
		for _, m := range parsed.Data {
			if m.ID == "" {
				continue
			}
			models = append(models, m.ID)
			// Catalog first, endpoint second: every catalog field stays a fallback.
			d := catalogSuggestion(m.ID)
			if c := firstPos(m.ContextLength, m.ContextWindow, m.MaxInputTokens); c > 0 {
				d["context_window_k"] = (c + 1023) / 1024
				d["context_source"] = "endpoint"
			} else if _, ok := d["context_window_k"]; ok {
				d["context_source"] = "catalog"
			} else {
				d["context_source"] = "unknown"
			}
			if o := firstPos(m.MaxOutput, m.MaxTokens); o > 0 {
				d["max_output_k"] = (o + 1023) / 1024
			}
			if lbl := m.DisplayName; lbl != "" {
				d["label"] = lbl
			} else if lbl := m.Name; lbl != "" {
				d["label"] = lbl
			}
			details = append(details, d)
		}
		if len(models) > 0 {
			writeJSON(w, 200, map[string]any{"ok": true, "models": models, "details": details, "endpoint": c.url})
			return
		}
		emptyOK = true // 200 but no model ids — keep trying other candidates
	}
	if emptyOK {
		writeJSON(w, 200, map[string]any{"ok": true, "models": []string{}})
		return
	}
	if lastErr == "" {
		lastErr = "未获取到模型列表"
	}
	writeJSON(w, 200, map[string]any{"ok": false, "error": lastErr})
}

// pgModelCatalog 返回公开目录里登记的全部模型家族，供前端在没有密钥时也能
// 给出建议（例如用户手动输入 "claude-sonnet-4-20250514" 时自动补上下文）。
func (s *Server) pgModelCatalog(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	out := make([]ModelCatalogEntry, 0, len(modelCatalogTable))
	for _, e := range modelCatalogTable {
		if q != "" && !strings.Contains(e.Prefix, q) && !strings.Contains(strings.ToLower(e.Label), q) {
			continue
		}
		out = append(out, e)
	}
	if out == nil {
		out = []ModelCatalogEntry{}
	}
	writeJSON(w, 200, map[string]any{"entries": out, "count": len(out)})
}

// --- prompt template helpers (Go text/template + catalog 白名单) ---

// globalPromptVars are runtime variables available to EVERY agent (built-in and
// custom) regardless of its per-agent catalog. Each agent's render path fills them
// (see agent.nowStr, rendered fresh each turn), so a prompt may always reference
// {{.Now}} — e.g. subtract it from a fixed start stamp to reason about elapsed time.

// withGlobalVars appends the universal runtime vars onto an agent's own catalog,
// so validation / the UI variable list / preview all recognize {{.Now}} etc.
// A stored catalog entry that collides with a global name is dropped: the global
// runtime var is authoritative (it's what the render path actually resolves), and
// this keeps the returned list name-unique so the UI never sees duplicate keys.

// shadowed by the authoritative global runtime var

// validateTemplate parses the template and rejects any {{.Var}} not in the catalog.
// Returns "" if valid, otherwise an error message.

// renderPrompt renders with example values (catalog example overridden by sample).

// templateFields returns the distinct top-level {{.X}} field names referenced by
// the template (used to validate against the catalog whitelist).
