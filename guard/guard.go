// Package guard implements the safety boundary layer (docs §11): an audit log,
// user-configured intercept-rule evaluation, and Observer/G5 failure attribution.
// Every tool call passes through the PreToolUse hook before executing.
//
// Stage 1 (P0) safety floor: destructive/exfil gating is no longer only in
// the DB intercept rules (seeded as ordinary [内置] rules, so users could
// disable or delete them). A deterministic intent classifier runs first and
// hard-denies the extreme set (rm -rf /, mkfs, dd to /dev, DROP/TRUNCATE)
// mechanically — see intent.go. RoE scope enforcement is restored in
// scope.go: when a task has anchored scope rows, a network target outside
// them is denied until registered via add_task_scope.
package guard

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/harness/sandbox"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/hook"
)

// AuditEntry records one gated tool call.
type AuditEntry struct {
	TS      int64  `json:"ts"`
	Tool    string `json:"tool"`
	Action  string `json:"action"` // allow|block
	Reason  string `json:"reason,omitempty"`
	Command string `json:"command,omitempty"`
	Intent  string `json:"intent,omitempty"`
	Host    string `json:"host,omitempty"`
}

// Guard enforces the side-effect policy via agent-core hooks.
type Guard struct {
	mu           sync.Mutex
	audit        []AuditEntry
	attrib       map[string]int // failure attribution counts (Observer / G5)
	reg          *hook.Registry
	interceptor  *intercept.Interceptor // optional; nil disables user-configured rules
	hardDeny     bool                   // Stage 1: mechanical deny for critical intents; default true
	scopeChecker ScopeChecker           // Stage 1: RoE scope enforcement; nil disables it
	sandbox      sandbox.Sandbox        // S6-FIX: per-task isolation policy; nil disables sandbox checks
}

// GuardOption configures a Guard.
type GuardOption func(*Guard)

// WithHardDeny enables or disables the mechanical hard-deny floor.
// Default is true. Disabling it is only for tests that need the old
// passthrough behaviour; production code must not disable it.
func WithHardDeny(enabled bool) GuardOption {
	return func(g *Guard) { g.hardDeny = enabled }
}

// WithScopeChecker installs a RoE scope checker. Nil disables scope checks.
func WithScopeChecker(sc ScopeChecker) GuardOption {
	return func(g *Guard) { g.scopeChecker = sc }
}

// WithSandbox installs the per-task isolation policy (file containment +
// private-network egress). Nil disables sandbox checks.
func WithSandbox(sb sandbox.Sandbox) GuardOption {
	return func(g *Guard) { g.sandbox = sb }
}

// New creates a Guard without user-configured intercept rules (used for pentest
// tasks where the Interceptor is not yet available).
func New(opts ...GuardOption) *Guard { return newGuard(nil, opts...) }

// NewWithInterceptor creates a Guard with user-configured intercept rules.
func NewWithInterceptor(ic *intercept.Interceptor, opts ...GuardOption) *Guard {
	return newGuard(ic, opts...)
}

func newGuard(ic *intercept.Interceptor, opts ...GuardOption) *Guard {
	g := &Guard{attrib: map[string]int{}, interceptor: ic, hardDeny: true}
	for _, o := range opts {
		o(g)
	}
	g.reg = hook.NewRegistry().
		On(hook.PreToolUse, g.preToolUse).
		On(hook.PostToolUse, g.postToolUse)
	return g
}

// SetScopeChecker installs or replaces the RoE scope checker at runtime.
func (g *Guard) SetScopeChecker(sc ScopeChecker) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.scopeChecker = sc
}

// SetSandbox installs or replaces the per-task isolation policy at runtime.
func (g *Guard) SetSandbox(sb sandbox.Sandbox) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sandbox = sb
}

// SetHardDeny enables or disables the mechanical hard-deny floor at runtime.
func (g *Guard) SetHardDeny(enabled bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hardDeny = enabled
}

// Hooks returns the hook registry to attach to an agent session.
func (g *Guard) Hooks() *hook.Registry { return g.reg }

func (g *Guard) preToolUse(ctx context.Context, ev hook.Event) hook.Result {
	// Extract the shell-command surface for the audit log: Bash + the interactive-shell
	// tools (shell_open's command, shell_send's text). Other tools record an empty command.
	var cmd string
	switch ev.ToolName {
	case "Bash", "shell_open":
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(ev.Input, &in)
		cmd = in.Command
	case "shell_send":
		var in struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(ev.Input, &in)
		cmd = in.Text
	}
	// Stage 1, step 1: deterministic intent classification. This runs
	// before the DB rules so deleting a DB rule cannot remove the floor.
	intent := ClassifyIntent(ev.ToolName, ev.Input)
	g.mu.Lock()
	hardDeny := g.hardDeny
	checker := g.scopeChecker
	g.mu.Unlock()
	if hardDeny && intent.IsHardDeny() {
		if g.interceptor != nil {
			g.interceptor.Log(ctx, intercept.ConvIDFromContext(ctx), intercept.Decision{Action: "deny", Message: "[意图级硬拦截] " + intent.Reason, RuleName: "intent-hard-deny"}, ev.ToolName, ev.Input, "denied")
		}
		return g.blockWithIntent(ev.ToolName, systemBlockMessage(intent.Reason), cmd, string(intent.Kind), "")
	}
	// S6-FIX-PROXY-SANDBOX, step 1.5: per-task isolation. File tools are
	// contained to the task workdir (symlinks resolved); loopback/private
	// egress is denied unless the task's RoE scope authorizes the host.
	// Runs after the intent hard-deny (a destructive intent keeps its own
	// reason) and before the scope check (which governs public hosts).
	g.mu.Lock()
	sb := g.sandbox
	g.mu.Unlock()
	if sb != nil {
		if res := g.checkSandbox(ctx, sb, checker, ev.ToolName, ev.Input, cmd); res != nil {
			return *res
		}
	}
	// Stage 1, step 2: RoE scope. Only network intents are checked, only
	// when the task has anchored scope, only when a host was extracted.
	// Unanchored tasks fail open here (logged by CheckScope) so legacy
	// tasks do not suddenly break.
	if checker != nil && (intent.Kind == IntentNetworkOutbound || intent.Kind == IntentLangExec) {
		if taskIDStr := intercept.TaskIDFromContext(ctx); taskIDStr != "" {
			if taskID, err := strconv.ParseInt(taskIDStr, 10, 64); err == nil && taskID > 0 {
				dec := CheckScope(ctx, checker, taskID, ev.ToolName, ev.Input)
				if dec.Checked && dec.HasScope && !dec.InScope {
					return g.blockWithIntent(ev.ToolName, systemBlockMessage(dec.Reason), cmd, string(intent.Kind), dec.Host)
				}
				if dec.Checked && dec.Host != "" {
					// In-scope or unanchored: keep the host in the audit trail.
					g.recordWithIntent(ev.ToolName, "allow", "", cmd, string(intent.Kind), dec.Host)
				} else {
					g.record(ev.ToolName, "allow", "", cmd)
				}
			} else {
				g.record(ev.ToolName, "allow", "", cmd)
			}
		} else {
			g.record(ev.ToolName, "allow", "", cmd)
		}
	} else {
		g.record(ev.ToolName, "allow", "", cmd)
	}
	// Sprint 2: make the policy layer's view of this call replayable. The event
	// carries no verdict — the server-side emitter attaches the active policy's
	// decision. This is observation only; applyIntercept below is still the veto.
	intercept.EmitPolicyDecision(ctx, db.Activity{
		Worker: "harness", Kind: "permission_decision", Tool: ev.ToolName,
		Summary: "权限策略评估：" + ev.ToolName,
	})
	return g.applyIntercept(ctx, ev)
}

// sandboxFileTools maps each file tool to its path argument (SDK field
// names, see third_party/norma/tool). Glob/Grep/LS take a directory;
// Read/Write/Edit/MultiEdit take a file.
var sandboxFileTools = map[string]string{
	"Read": "file_path", "Write": "file_path", "Edit": "file_path",
	"MultiEdit": "file_path", "LS": "path", "Glob": "path", "Grep": "path",
}

// checkSandbox enforces the per-task isolation policy on one tool call.
// Returns nil to allow, or a block result. File containment always
// applies; the network check only governs loopback/private targets —
// public hosts are left to the RoE scope check that runs next.
func (g *Guard) checkSandbox(ctx context.Context, sb sandbox.Sandbox, checker ScopeChecker, tool string, input []byte, cmd string) *hook.Result {
	if key, ok := sandboxFileTools[tool]; ok {
		var in map[string]json.RawMessage
		if err := json.Unmarshal(input, &in); err == nil {
			if raw, ok := in[key]; ok {
				var p string
				if err := json.Unmarshal(raw, &p); err == nil && strings.TrimSpace(p) != "" {
					if err := sb.ValidatePath(p); err != nil {
						res := g.block(tool, systemBlockMessage("沙箱拦截："+err.Error()), cmd)
						return &res
					}
				}
			}
		}
		return nil
	}
	switch tool {
	case "WebFetch", "Bash":
		for _, host := range sandboxPrivateHosts(tool, input) {
			if res := g.checkSandboxHost(ctx, sb, checker, tool, host, cmd); res != nil {
				return res
			}
		}
	}
	return nil
}

// sandboxPrivateHosts extracts the loopback/private hosts a network tool
// call would touch. web_search is deliberately excluded: its query text
// is not a connection target (the search backend fetches it).
func sandboxPrivateHosts(tool string, input []byte) []string {
	var hosts []string
	if tool == "WebFetch" {
		var in struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(input, &in); err == nil && in.URL != "" {
			if u, err := url.Parse(in.URL); err == nil && u.Hostname() != "" {
				hosts = []string{u.Hostname()}
			}
		}
	} else {
		hosts = ExtractHosts(tool, input)
	}
	var out []string
	for _, h := range hosts {
		if isPrivateHost(h) {
			out = append(out, h)
		}
	}
	return out
}

// isPrivateHost reports whether h is a loopback/private literal (IP or
// the localhost name). Public hostnames are not the sandbox's business.
func isPrivateHost(h string) bool {
	h = strings.ToLower(strings.TrimSpace(h))
	if h == "localhost" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified()
	}
	return false
}

// checkSandboxHost decides one private/loopback target. A host inside the
// task's anchored RoE scope is allowed (the task explicitly authorizes
// it — e.g. a local range at 127.0.0.1). Unanchored tasks (no scope rows)
// and guards without a checker fail open here, mirroring CheckScope's
// legacy rule; file containment above still applies.
func (g *Guard) checkSandboxHost(ctx context.Context, sb sandbox.Sandbox, checker ScopeChecker, tool, host, cmd string) *hook.Result {
	if checker != nil {
		if taskIDStr := intercept.TaskIDFromContext(ctx); taskIDStr != "" {
			if taskID, err := strconv.ParseInt(taskIDStr, 10, 64); err == nil && taskID > 0 {
				scopes, err := checker.Scopes(ctx, taskID)
				if err == nil && len(scopes) > 0 {
					if CheckHostAgainstScopes(host, scopes) {
						return nil
					}
					if err := sb.CheckHost(host); err != nil {
						res := g.block(tool, systemBlockMessage("沙箱拦截："+err.Error()), cmd)
						return &res
					}
					return nil
				}
			}
		}
	}
	return nil
}

// applyIntercept evaluates user-configured intercept rules against the tool call.
// Both rules and the fallback judge receive the complete tool input.
func (g *Guard) applyIntercept(ctx context.Context, ev hook.Event) hook.Result {
	if g.interceptor == nil {
		return hook.Result{}
	}
	if !g.interceptor.IsToolEnabled(ev.ToolName) {
		return hook.Result{}
	}
	ctx = intercept.WithCall(ctx, ev.ToolName, ev.Input)
	dec, matched := g.interceptor.Match(ev.ToolName, ev.Input)
	if !matched {
		// No rule matched. Ask the LLM fallback judge (if enabled); when it is off
		// or unwired, keep current behavior and allow.
		d, judged := g.interceptor.Judge(ctx, ev.ToolName, ev.Input)
		if !judged {
			return hook.Result{}
		}
		dec = d
	}
	switch dec.Action {
	case "deny":
		// 观测:deny 命中不阻塞审批,直接记一条 denied（历史/任务拦截页可见）。
		g.interceptor.Log(ctx, intercept.ConvIDFromContext(ctx), dec, ev.ToolName, ev.Input, "denied")
		return g.block(ev.ToolName, systemBlockMessage(dec.Message), "")
	case "allow":
		// Record explicit rule and model approvals so review details remain auditable.
		g.interceptor.Log(ctx, intercept.ConvIDFromContext(ctx), dec, ev.ToolName, ev.Input, "allowed")
		return hook.Result{}
	case "ask":
		// If the worker context is already cancelled (task stopped / killed), block
		// immediately without creating a pending record — avoids orphaned DB entries
		// and makes execOne complete fast, reducing the race against drainSynthetic.
		if ctx.Err() != nil {
			return g.block(ev.ToolName, systemBlockMessage("工作已取消，平台安全管控阻止执行"), "")
		}
		convID := intercept.ConvIDFromContext(ctx)
		if !g.interceptor.HandleAsk(ctx, convID, dec, ev.ToolName, ev.Input) {
			return g.block(ev.ToolName, systemBlockMessage("人工审批未通过（用户拒绝或审批超时）"), "")
		}
		return hook.Result{}
	}
	return hook.Result{}
}

// systemBlockMessage frames an intercept block as an ARTEX platform-governance
// decision so the agent does not mistake it for a target-side defense.
//
// The bare reasons ("禁止执行此工具" / "用户拒绝") read exactly like a WAF/403 on
// the target, so a pentest agent's instinct is to bypass them — rewrite the
// command, swap the payload, re-encode, retry. That is both futile (the platform
// blocks the class of action, not one string) and wrong (it's a policy decision,
// not an obstacle to defeat). This prefix states plainly that the block comes
// from the platform, is not the target's protection, and that the operation is
// forbidden — so the agent pivots to another approach instead of evading it.
// Audit/history rows keep the raw reason (see Interceptor.Log); only the
// model-facing tool_result carries this framing.
func systemBlockMessage(reason string) string {
	return "【ARTEX 平台管控·非目标防御】此调用被平台拦截。" +
		"原因：" + reason + "。此操作被禁止。"
}

var reBlocked = regexp.MustCompile(`(?i)\b(403|forbidden|waf|blocked|rate.?limit|429|captcha|denied)\b`)

// postToolUse is the Observer failure-attribution hook (G5): it classifies tool
// results into blocked / error / ok so the planner can change strategy instead
// of giving up at a WAF.
func (g *Guard) postToolUse(_ context.Context, ev hook.Event) hook.Result {
	if ev.ToolName != "Bash" {
		return hook.Result{}
	}
	class := "ok"
	switch {
	case reBlocked.Match(ev.Result):
		class = "blocked"
	case ev.IsError:
		class = "error"
	}
	g.mu.Lock()
	g.attrib[class]++
	g.mu.Unlock()
	return hook.Result{}
}

// Attributions returns failure-attribution counts (Observer / G5).
func (g *Guard) Attributions() map[string]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]int, len(g.attrib))
	for k, v := range g.attrib {
		out[k] = v
	}
	return out
}

func (g *Guard) block(tool, reason, cmd string) hook.Result {
	g.record(tool, "block", reason, cmd)
	return hook.Result{Decision: "block", Message: reason}
}

func (g *Guard) blockWithIntent(tool, reason, cmd, intent, host string) hook.Result {
	g.recordWithIntent(tool, "block", reason, cmd, intent, host)
	return hook.Result{Decision: "block", Message: reason}
}

func (g *Guard) record(tool, action, reason, cmd string) {
	g.recordWithIntent(tool, action, reason, cmd, "", "")
}

func (g *Guard) recordWithIntent(tool, action, reason, cmd, intent, host string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.audit = append(g.audit, AuditEntry{TS: time.Now().Unix(), Tool: tool, Action: action, Reason: reason, Command: cmd, Intent: intent, Host: host})
	if len(g.audit) > 2000 {
		g.audit = g.audit[len(g.audit)-2000:]
	}
}

// Audit returns a snapshot of recent gated calls (most recent last).
func (g *Guard) Audit() []AuditEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]AuditEntry, len(g.audit))
	copy(out, g.audit)
	return out
}
