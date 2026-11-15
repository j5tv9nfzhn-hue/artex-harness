package agent

// HARNESS runtime: ONE conversational agent per task.
//
// Why this exists (docs/07-HARNESS-RUNTIME.md): the ARTEX execution base splits a
// task into a planner that decides directions and a pool of workers that execute
// them. That pipeline is fine for unattended runs, but it makes the model's own
// judgement second-class and it is what mature harnesses (Claude Code, Codex,
// cc-haha, opcode…) deliberately do NOT do: there, a single agent holds every
// verb, decides what to do next, and delegates to subagents when that pays off.
//
// HARNESS therefore introduces a second, independent execution path and never
// uses the planner/worker roles: no intent queue, no round-robin claiming, no
// goal-proving protocol. The exploration graph is still used — as knowledge
// (assets / facts / findings / hints), not as a work queue.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/harness/delegation"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	acperm "github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/subagent"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// HarnessAgent is the single agent that drives one task end to end.
type HarnessAgent struct {
	prov            llm.Provider
	model           string
	workDir         string
	tx              *transcript.Store
	window          int
	windowFn        func() int
	maxTurns        int
	proxyAddr       string
	proxyCACert     string
	webSearch       WebSearchOpts
	findingRecorder FindingRecorder
	compactHook     func(kind string, preTokens int)
	// browserTools, when set, returns the session-managed browser tools for a task
	// (docs/09 §2). They replace the per-run MCP clients for those servers.
	browserTools func(ctx context.Context, taskID int64) ([]actool.CoreTool, error)
	// hooks = platform gate (intercept rules / approvals). nil = NO GATE;
	// an unwired harness agent would execute every tool unchecked.
	hooks          harness.HookRunner
	nonStreamingFn func() bool
	noaEnabledFn   func() bool
	maxTokensFn    func() int
	// delegationNotify wakes the parent conversation with a background
	// delegation's <task-notification>. Injected by the server (the agent package
	// must not import it). nil = notifications are still recorded as activities
	// but cannot wake a resting task (the parent must send a message to resume).
	delegationNotify func(taskID int64, text string)
}

// NewHarnessAgent builds the runtime for one task. Same construction contract as
// the other runners so the shared ModelRunConfig applies unchanged.
func NewHarnessAgent(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *HarnessAgent {
	return &HarnessAgent{
		prov: prov, model: model, workDir: workDir, tx: tx,
		window: window, maxTurns: maxTurns,
	}
}

func (h *HarnessAgent) SetProxy(addr, caCert string)   { h.proxyAddr, h.proxyCACert = addr, caCert }
func (h *HarnessAgent) SetWebSearch(o WebSearchOpts)   { h.webSearch = o }
func (h *HarnessAgent) SetNonStreaming(fn func() bool) { h.nonStreamingFn = fn }
func (h *HarnessAgent) SetNoaEnabled(fn func() bool)   { h.noaEnabledFn = fn }
func (h *HarnessAgent) SetMaxTokens(fn func() int)     { h.maxTokensFn = fn }
func (h *HarnessAgent) SetDelegationNotifier(fn func(taskID int64, text string)) {
	h.delegationNotify = fn
}
func (h *HarnessAgent) SetCompactionWindowResolver(fn func() int) { h.windowFn = fn }
func (h *HarnessAgent) SetFindingRecorder(f FindingRecorder)      { h.findingRecorder = f }

// SetCompactHook observes compactions as they happen (kind "micro" or "auto"), so
// the operator sees the event itself instead of a token delta he has to trust.
func (h *HarnessAgent) SetCompactHook(fn func(kind string, preTokens int)) { h.compactHook = fn }

// SetHooks wires the platform gate (guard/intercept hooks). Optional in the type but
// mandatory in practice: an unwired harness agent executes every tool unchecked.
func (h *HarnessAgent) SetHooks(r harness.HookRunner) { h.hooks = r }

// SetBrowserTools wires the session-managed browser tools (nil = use the per-run MCP
// augmentation path, which is what happens before the Manager exists).
func (h *HarnessAgent) SetBrowserTools(fn func(ctx context.Context, taskID int64) ([]actool.CoreTool, error)) {
	h.browserTools = fn
}

func (h *HarnessAgent) nonStreaming() bool {
	return h.nonStreamingFn != nil && h.nonStreamingFn()
}

func (h *HarnessAgent) maxTokens() int {
	if h.maxTokensFn == nil {
		return 0
	}
	return h.maxTokensFn()
}

func (h *HarnessAgent) compactionWindow() int {
	if h.windowFn != nil {
		return h.windowFn()
	}
	return h.window
}

func (h *HarnessAgent) modelRunConfig() ModelRunConfig {
	return ModelRunConfig{
		ProxyAddr:     h.proxyAddr,
		ProxyCACert:   h.proxyCACert,
		WebSearch:     h.webSearch,
		ContextWindow: h.compactionWindow(),
		NonStreaming:  h.nonStreaming(),
		MaxTokens:     h.maxTokens(),
		// A truncated final answer must be resumed, not silently surfaced as a
		// half-written conclusion. The SDK escalates the cap once, then nudges
		// "continue where you left off" up to 3 times (Sprint 1.3).
		EscalateMaxTokens: true,
	}
}

// HarnessRunRequest is one turn of the conversation.
type HarnessRunRequest struct {
	TaskID      int64
	Goal        string
	Description string
	// Input is this turn's human-facing text: the goal on the first turn, the
	// human's message afterwards.
	Input  string
	Round  int
	Assets *db.AssetStore
	Store  *db.ExplorationStore
	Emit   func(db.Activity)
}

// harnessExcludedTools are the ARTEX tools that only make sense inside the
// planner/worker pipeline (intent dispatch, steering a running work, reading a
// worker's trace). HARNESS does not use those concepts, so they are filtered out
// of the agent's surface rather than left dangling.
var harnessExcludedTools = map[string]bool{
	"add_intent":               true, // intent queue = planner concept
	"steer_work":               true, // steering a worker run
	"add_hint":                 true, // hints feed the planner
	"get_worker_output":        true,
	"get_worker_trace":         true,
	"list_worker_traces":       true,
	"search_all_worker_traces": true,
}

// Run executes one turn to completion and returns the agent's final text.
//
// "One turn" means: one prompt in, the agent loop runs until the model stops
// asking for tools. Conversation continuity comes from the transcript store, so
// the next human message resumes the same thread.
func (h *HarnessAgent) Run(ctx context.Context, req HarnessRunRequest) (string, harness.TerminalReason, error) {
	emit := req.Emit
	runDir := h.runDir(req.TaskID)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: req.TaskID, ExplorationID: explorationID(req.Store)})

	tsx := NewToolSet(req.Store, "harness")
	tsx.SetFindingRecorder(h.findingRecorder)
	tsx.SetTaskID(req.TaskID)
	tsx.SetCoverageEnabled(true)
	if req.Assets != nil {
		tsx.SetAssetStore(req.Assets, req.Assets.Companies())
	}

	base := h.domainTools(tsx)
	base = append(base, defaultToolsExcept("MultiEdit")...) // keep Edit/LS/Glob/Grep/Bash/Read/Write
	base = append(base, h.agentTool(base, emit, req.TaskID))
	// Browser tools come from the session Manager (one session per task, shared by
	// Playwright and DevTools). When it is wired we add them here and drop the copies
	// the per-run augmentation would add, so there is exactly one browser stack.
	var managedTools []actool.CoreTool
	if h.browserTools != nil {
		if bt, err := h.browserTools(ctx, req.TaskID); err == nil && len(bt) > 0 {
			base = append(base, bt...)
			managedTools = bt
		} else if err != nil {
			// Was `fmt.Printf("")`: nothing at all was emitted, so a browser stack
			// that failed to start was indistinguishable from one never wired — the
			// panel said "live" while the agent had no browser tools and no clue.
			// The model still gets no tools; the operator gets the reason.
			log.Printf("[harness] task %d 浏览器工具不可用（本轮不注入浏览器工具）: %v", req.TaskID, err)
		}
	}
	tools, def, cleanup := AugmentTools(ctx, "harness", base)
	defer cleanup()
	if len(managedTools) > 0 {
		tools = preferManagedBrowserTools(tools, managedTools)
	}

	sysBody := harnessDefaultPrompt
	sysBody += harnessSituationBlock(tsx)
	system, boundary := deferredSystem(sysBody, def)

	runCfg := h.modelRunConfig()
	opts := agentcore.Options{
		Provider:        h.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  acperm.ModeBypass,
		WorkingDir:      runDir,
		MaxTurns:        h.maxTurns,
		ToolOutputDir:   cmdOutDir(runDir),
		// Wall-clock budget for THIS run (docs/09 §1): a step budget alone lets a slow
		// endpoint run for hours. On expiry the SDK enters the settlement phase with
		// the timeout prompt below, so the agent lands conclusions instead of being
		// killed mid-action.
		MaxDuration: runBudget(),
	}
	if settle := wrapupSettlement("harness", nil); settle != nil {
		// Distinct wording per reason: "time is up" vs "steps are up" mean different
		// things to the model.
		settle.PromptByReason = map[harness.TerminalReason]string{
			harness.ReasonTimeout:  harnessTimeoutWrapup,
			harness.ReasonMaxTurns: harnessMaxTurnsWrapup,
		}
		opts.Settlement = settle
	}
	runCfg.ApplyModelDefaults(&opts)
	runCfg.WithTodos(&opts)
	if h.compactHook != nil && opts.Compaction != nil {
		cfg := *opts.Compaction
		cfg.OnCompact = h.compactHook
		opts.Compaction = &cfg
	}
	if h.hooks != nil {
		opts.Hooks = h.hooks
	}
	if h.tx != nil {
		opts.Transcript = h.tx
		opts.SessionID = fmt.Sprintf("exp%d-harness", req.Store.ID())
	}
	noaSession := fmt.Sprintf("exp%d-harness", req.Store.ID())
	enableNoa(&opts, h.noaEnabledFn, h.workDir, noaSession, noaWarn(noaSession))

	input := fmt.Sprintf(harnessRoundIntro, req.Goal, orNoDescription(req.Description), req.Input)
	return captureRun(ctx, opts, input, emit)
}

func orNoDescription(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（无）"
	}
	return s
}

// runDir is the agent's working directory for a task.
func (h *HarnessAgent) runDir(taskID int64) string {
	return ensureRunDir(h.workDir, taskID, 0)
}

// domainTools is the graph-facing tool surface: knowledge in, knowledge out. The
// intent/steering tools are filtered out on purpose (see harnessExcludedTools).
func (h *HarnessAgent) domainTools(tsx *ToolSet) []actool.CoreTool {
	all := tsx.MainAgentTools()
	out := make([]actool.CoreTool, 0, len(all))
	for _, t := range all {
		if harnessExcludedTools[t.Name()] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// harnessSituationBlock renders a compact "where am I" preamble: the goal, the
// current asset/fact/finding counts and any human hints. Deliberately small — the
// agent can pull detail with graph_overview whenever it wants.
func harnessSituationBlock(tsx *ToolSet) string {
	if tsx == nil || tsx.ts == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n━━ 开局态势 ━━\n")
	if data := tsx.graphOverviewData(); data != nil {
		for _, k := range []string{"findings_total", "facts"} {
			if v, ok := data[k]; ok {
				fmt.Fprintf(&b, "- %s: %v\n", k, v)
			}
		}
		if hints, ok := data["hints"].([]map[string]any); ok && len(hints) > 0 {
			b.WriteString("- 人类提示：\n")
			for _, hint := range hints {
				if txt, ok := hint["text"].(string); ok && strings.TrimSpace(txt) != "" {
					fmt.Fprintf(&b, "  · %s\n", truncOutput(txt, 240))
				}
			}
		}
	}
	b.WriteString("（完整态势用 graph_overview；已有结论不要重复验证。）")
	return b.String()
}

// defaultSubagentTimeout bounds one subagent run.
//
// Why a separate budget at all: a subagent is a *delegated* unit of work with its own
// tool loop, so it must not inherit the parent run's turn budget (that is the bug this
// constant exists next to — see subagentContext).
const defaultSubagentTimeout = 10 * time.Minute

// runBudgetEnv / defaultRunBudget bound ONE harness run (wall clock).
//
// Why: the runtime only had MaxTurns, which is a step budget. A single turn can take
// minutes on a slow endpoint, so a task could run for hours without ever hitting the
// turn cap — "long session" behaviour was simply unobserved (docs/09 §1).
const (
	runBudgetEnv     = "HARNESS_RUN_BUDGET"
	defaultRunBudget = 20 * time.Minute
)

// runBudget resolves the wall-clock budget for one run.
func runBudget() time.Duration {
	raw := strings.TrimSpace(os.Getenv(runBudgetEnv))
	if raw == "" {
		return defaultRunBudget
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return defaultRunBudget
}

// RunBudget exposes the resolved wall-clock budget so the console can show it
// (HARNESS_RUN_BUDGET or the 20-minute default).
func RunBudget() time.Duration { return runBudget() }

// SubagentBudget exposes the resolved per-delegation budget for the same reason.
func SubagentBudget() time.Duration { return subagentTimeout() }

// subagentTimeoutEnv lets an operator raise/lower the cap without a rebuild.
const subagentTimeoutEnv = "HARNESS_SUBAGENT_TIMEOUT"

// subagentTimeout resolves the bound for one subagent run.
func subagentTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(subagentTimeoutEnv))
	if raw == "" {
		return defaultSubagentTimeout
	}
	// Accept "90s", "10m", or a bare number of seconds.
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return defaultSubagentTimeout
}

// subagentContext detaches a subagent run from the parent's cancellation while
// keeping its own deadline.
//
// The failure this fixes: the parent run gets canceled when its turn budget is
// exhausted (norma aborts in-flight tools and synthesizes interrupted results), and
// because the subagent's context was derived from the parent's, the child died with
// "subagent failed: context canceled" mid-delegation — the delegation was recorded as
// an error even though the work was perfectly sound.
//
// Semantics of the returned context:
//   - parent cancellation (turn budget, pause, shutdown) does NOT propagate;
//   - it carries its own timeout, so a subagent can never run unbounded;
//   - the caller must call the returned CancelFunc (defer) to release the timer.
//
// Trade-off, stated plainly: a subagent may outlive a paused/deleted parent by up to
// its timeout. That is deliberate — a half-executed recon leaves the graph in a worse
// state than one that finishes and reports — and it is bounded, since nothing else
// keeps the goroutine alive.
func subagentContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), subagentTimeout())
}

// shouldStartSubagent reports whether a delegation may begin now. A run that is
// already canceled must not START new work (that would just burn tokens after a stop),
// but an already-started subagent is allowed to finish (see subagentContext).
func shouldStartSubagent(ctx context.Context) bool { return ctx.Err() == nil }

// agentTool wraps norma's Agent tool so a delegation is visible in the harness
// event stream: the parent's transcript shows who was dispatched and what came
// back, which is what makes "the model decided to parallelise" auditable.
//
// Sprint 3 adds run_in_background: the model may choose to spawn a delegation
// without blocking its turn. Background runs are tracked in delegation.Global
// (one authoritative registry per task) and announce completion with a
// <task-notification> activity the parent consumes on its next turn.
func (h *HarnessAgent) agentTool(base []actool.CoreTool, emit func(db.Activity), taskID int64) actool.CoreTool {
	registry := actool.NewRegistry(base...)
	cfg := subagent.Config{
		Provider:    h.prov,
		ParentTools: registry,
		Transcript:  h.tx,
		CanUseTool: func(context.Context, string, json.RawMessage, acperm.Context) acperm.Decision {
			return acperm.Allowed()
		},
		// 同一个 gate：子代理也不能绕过破坏性命令拦截。
		Hooks: h.hooks,
		// No definition lists "Agent", so a subagent can never delegate again: the
		// depth guard stays as defence in depth, not as the only limit.
		Agents: h.agentDefinitions(base),
	}
	inner := subagent.NewTool(cfg)
	// Re-declare the same spec, stream the delegation boundaries, and extend the
	// schema with the background flag + a short description for the activity panel.
	schema := inner.InputSchema()
	if props, ok := schema["properties"].(map[string]any); ok {
		props["run_in_background"] = map[string]any{
			"type": "boolean", "description": "若为 true，立即返回委派 id，不阻塞当前轮次；完成后会以 task-notification 通知你。默认 false（等待结果）。",
		}
		props["description"] = map[string]any{
			"type": "string", "description": "给这次委派起 5-10 词的短名，显示在活动面板里。",
		}
	}
	return actool.Build(actool.Spec{
		Name:        inner.Name(),
		Description: inner.Description(),
		Prompt:      inner.Prompt(),
		Schema:      schema,
		Permissions: func(context.Context, json.RawMessage, acperm.Context) acperm.Decision {
			return acperm.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
			agentType, prompt, background, description := peekAgentInput(in)
			// Do not start new delegated work after a stop/pause/shutdown.
			if !shouldStartSubagent(ctx) {
				return actool.Errorf("Error: 本轮已取消，未启动子代理 " + agentType + "（已取消的轮次不再产生新工作）"), nil
			}
			if _, ok := cfg.Agents[agentType]; !ok {
				return actool.Errorf("Error: unknown agent_type " + agentType), nil
			}
			reg := delegation.Global.For(fmt.Sprint(taskID), h.delegationOutputDir(taskID))
			id := reg.Start(agentType, prompt, description, tc.WorkingDir, background, time.Now())
			if emit != nil {
				label := "派发子代理"
				if background {
					label = "后台派发子代理"
				}
				emit(db.Activity{
					Worker: "harness", Kind: "tool_use", Tool: "Agent",
					Summary: fmt.Sprintf("%s %s (#%s)：%s", label, agentType, id, truncOutput(prompt, 160)),
					Metadata: delegation.ToJSON(map[string]any{
						"delegation_id": id, "agent_type": agentType,
						"background": background, "description": description,
					}),
				})
			}
			if background {
				h.startBackgroundDelegation(reg, id, cfg, agentType, in, tc, emit, taskID)
				return actool.Text(fmt.Sprintf(
					"后台子代理已启动：id=%s agent_type=%s。它会独立运行，完成后你会收到 <task-notification>；"+
						"现在可以继续做别的事。用 /api/harness/delegations/%d 查看状态。",
					id, agentType, taskID)), nil
			}
			res, err := h.callDelegation(ctx, inner, in, tc, agentType)
			h.finishDelegation(reg, id, res, err, emit, false, taskID)
			return res, err
		},
	})
}

// agentDefinitions builds the two named subagents with a THREE-LAYER tool pool
// (Sprint 3): global deny list → per-type allow list → MCP tools always allowed.
// "Agent" itself is never in any allow list, so recursion is impossible by
// construction; norma's depth guard remains defence in depth.
func (h *HarnessAgent) agentDefinitions(base []actool.CoreTool) map[string]subagent.Definition {
	// Layer 1: tools no subagent may ever receive, regardless of its allow list.
	globalDeny := map[string]bool{
		"Agent": true, "TaskOutput": true, "TaskStop": true, "TaskList": true,
		"EnterPlanMode": true, "ExitPlanMode": true, "AskUserQuestion": true,
	}
	// Layer 2: per-type allow lists. Layer 3 (MCP) is appended below.
	reconTools := []string{"Bash", "Read", "LS", "Glob", "Grep", "insert_assets", "record_fact", "list_assets", "graph_overview", "node_detail"}
	verifyTools := []string{"Bash", "Read", "Grep", "graph_overview", "node_detail", "list_findings", "record_fact"}
	allowMCP := func(names []string) []string {
		keep := map[string]bool{}
		for _, n := range names {
			if !globalDeny[n] {
				keep[n] = true
			}
		}
		out := make([]string, 0, len(keep)+8)
		for _, t := range base {
			n := t.Name()
			if strings.HasPrefix(n, "mcp__") && !globalDeny[n] {
				keep[n] = true // layer 3: MCP tools are always available to subagents
			}
		}
		for n := range keep {
			out = append(out, n)
		}
		return out
	}
	return map[string]subagent.Definition{
		"recon": {
			Description: "侦察与信息收集：探测入口、指纹、参数、可达性。只写资产与事实，不下漏洞结论。",
			Prompt: []string{
				"你是侦察子代理。只做信息收集：入口、接口、指纹、参数、可达性。",
				"把发现用 insert_assets / record_fact 写回图谱，不要下漏洞结论。",
				"结束后用几句话说清：确认了什么、证据（命令与输出摘要）、还有什么没探。",
			},
			Tools: allowMCP(reconTools), MaxTurns: 0,
		},
		"verify": {
			Description: "复现与验证：对给定结论用独立路径再触发一次，只回答成立/不成立及证据。",
			Prompt: []string{
				"你是验证子代理。任务是一个**待验证的结论**。",
				"用与原始路径**不同**的方法独立复现；复述原证据不算验证。",
				"只回答：成立 / 不成立 / 无法确认，并给出命令与关键输出。",
			},
			Tools: allowMCP(verifyTools), MaxTurns: 0,
		},
	}
}

// callDelegation runs one (foreground) subagent, detached from the parent's
// cancellation but bounded by its own timeout (docs/08 §1).
func (h *HarnessAgent) callDelegation(ctx context.Context, inner actool.CoreTool, in json.RawMessage,
	tc *actool.ToolContext, agentType string) (actool.Result, error) {
	subCtx, cancelSub := subagentContext(ctx)
	defer cancelSub()
	res, err := inner.Call(subCtx, in, tc)
	if err != nil && subCtx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("子代理 %s 超时（%s）：%w", agentType, subagentTimeout(), err)
	}
	return res, err
}

// startBackgroundDelegation runs a delegation in its own goroutine. The child
// context is detached from the parent's turn cancellation (so a finished parent
// turn does not kill it) but keeps its own timeout AND an explicit cancel func
// the operator can trigger through the registry.
func (h *HarnessAgent) startBackgroundDelegation(reg *delegation.Registry, id string, cfg subagent.Config,
	agentType string, in json.RawMessage, tc *actool.ToolContext, emit func(db.Activity), taskID int64) {
	bgCtx, cancel := context.WithTimeout(context.Background(), subagentTimeout())
	reg.AttachCancel(id, cancel)
	_, prompt, _, _ := peekAgentInput(in)
	// Isolate the background run in its own directory under the delegation store,
	// NOT the parent workspace. Concurrent delegations (and their file writes)
	// must not clobber the task's file tree, DIFF baseline, or checkpoints — those
	// are the operator's view of the main agent's work. The isolation dir lives
	// beside the result bodies: <delegations>/<taskID>/<did>/.
	iso := filepath.Join(h.delegationOutputDir(taskID), id)
	_ = os.MkdirAll(iso, 0o755)
	reg.SetWorkDir(id, iso)
	go func() {
		defer cancel()
		text, err := subagent.Run(bgCtx, cfg, agentType, prompt, iso)
		var res actool.Result
		if err != nil {
			res = actool.Errorf("Error: subagent failed: " + err.Error())
		} else {
			res = actool.Text(text)
		}
		h.finishDelegation(reg, id, res, err, emit, true, taskID)
		h.cleanupDelegationDir(iso)
	}()
}

// cleanupDelegationDir removes a background delegation's isolated directory when
// it is empty. A delegation that wrote scratch files keeps them (they are the
// evidence of what it did); an empty one leaves no litter. Best-effort.
func (h *HarnessAgent) cleanupDelegationDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		return
	}
	_ = os.Remove(dir)
}

// finishDelegation records the terminal state, writes the result body to disk,
// emits the paired tool_result (so replay keeps tool_use/tool_result intact) and
// — for background runs — a <task-notification> the parent will consume.
func (h *HarnessAgent) finishDelegation(reg *delegation.Registry, id string, res actool.Result, err error,
	emit func(db.Activity), background bool, taskID int64) {
	body := "(无输出)"
	if len(res.Content) > 0 {
		body = res.Content[0].Text
	}
	status := delegation.StatusDone
	if res.IsError || err != nil {
		status = delegation.StatusFailed
	}
	if _, werr := reg.WriteOutput(id, body); werr != nil {
		log.Printf("[delegation] %s 写结果文件失败: %v", id, werr)
	}
	reg.Finish(id, status, body, errText(err))
	if emit != nil {
		emit(db.Activity{
			Worker: "harness", Kind: "tool_result", Tool: "Agent",
			IsError:  status == delegation.StatusFailed,
			Summary:  fmt.Sprintf("子代理 %s 返回：%s", id, truncOutput(body, 400)),
			Metadata: delegation.ToJSON(map[string]any{"delegation_id": id, "background": background}),
		})
		if background {
			for _, n := range reg.PeekNotifications() {
				if n.TaskID == id {
					emit(db.Activity{
						Worker: "harness", Kind: "lifecycle",
						Summary:  "后台委派完成通知：" + n.Summary,
						Detail:   n.String(),
						Metadata: delegation.ToJSON(n),
					})
					break
				}
			}
			// Deliberately NOT drained here: the notification queue is consumed by
			// the harness loop when it next feeds the agent (the parent may still be
			// mid-turn, and an idle task must be woken by the completion).
			if h.delegationNotify != nil {
				for _, n := range reg.PeekNotifications() {
					if n.TaskID == id {
						h.delegationNotify(taskID, n.String())
						break
					}
				}
			}
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (h *HarnessAgent) delegationOutputDir(taskID int64) string {
	return filepath.Join(h.workDir, "delegations", fmt.Sprint(taskID))
}

// peekAgentInput best-effort reads the delegation arguments for the activity
// summary. It also reports whether the model asked for a background run.
func peekAgentInput(in json.RawMessage) (agentType, prompt string, background bool, description string) {
	type agentIn struct {
		AgentType       string `json:"agent_type"`
		Prompt          string `json:"prompt"`
		RunInBackground bool   `json:"run_in_background"`
		Description     string `json:"description"`
	}
	var v agentIn
	if err := json.Unmarshal(in, &v); err == nil {
		return v.AgentType, v.Prompt, v.RunInBackground, v.Description
	}
	return "?", "", false, ""
}

// harnessWorkDir returns <workDir>/tasks/<taskID> — same layout as the rest of the
// runtime so the console's file tree and the agent see the same tree.
func (h *HarnessAgent) WorkDirFor(taskID int64) string {
	return filepath.Join(h.workDir, "tasks", fmt.Sprint(taskID))
}

// dropManagedBrowserTools removes the per-run MCP copies of the browser servers. The
// names come from norma's MCP tool naming (mcp__<server>__<tool>).
// preferManagedBrowserTools keeps exactly ONE browser stack: the session
// Manager's.
//
// The previous version dropped every mcp__browser__/mcp__chrome-devtools__ name
// from the assembled set — including the Manager's own tools, which had just been
// appended to the base list. The agent therefore ran with zero browser and zero
// DevTools tools while the panel happily reported the servers "live" (a real run
// showed the model answering "my tool list doesn't include chrome-devtools"). Here
// every copy of a managed name is dropped and the Manager's instances are put back,
// so the per-run MCP duplicates vanish and the managed tools survive.
func preferManagedBrowserTools(tools, managedTools []actool.CoreTool) []actool.CoreTool {
	if len(managedTools) == 0 {
		return tools
	}
	managed := make(map[string]bool, len(managedTools))
	for _, t := range managedTools {
		managed[t.Name()] = true
	}
	out := make([]actool.CoreTool, 0, len(tools)+len(managedTools))
	out = append(out, managedTools...)
	for _, t := range tools {
		if managed[t.Name()] {
			continue
		}
		if strings.HasPrefix(t.Name(), "mcp__browser__") || strings.HasPrefix(t.Name(), "mcp__chrome-devtools__") {
			// A browser/DevTools tool the Manager does not expose: keep it only when
			// it is not a stale duplicate of a name we already added.
			continue
		}
		out = append(out, t)
	}
	return out
}
