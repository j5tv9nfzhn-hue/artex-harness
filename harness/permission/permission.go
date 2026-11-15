// Package permission is the HARNESS-side permission policy (Sprint 1.2).
//
// It is deliberately a POLICY layer, not a second gate. The one and only veto
// point remains guard.Guard / intercept.Interceptor (the PreToolUse hook chain).
// This package answers a narrower question: "given a tool call and the operator's
// saved policy, should we ASK, and if so why?" — and produces a typed decision
// with a reason that can be replayed.
//
// Design reference: mature harnesses separate rule matching (allow / deny / ask),
// permission modes, and decision reasons. norma/permission already implements the
// six modes and the deny-priority pipeline; this package adds the operator-facing
// rule/policy surface on top of it, without duplicating the SDK evaluator.
package permission

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"

	acperm "github.com/Autumn-27/norma/permission"
)

// Mode is a permission mode, re-exported as strings so the API/console does not
// depend on norma's type. The values are the SDK's own mode strings.
type Mode string

const (
	ModeDefault     Mode = "default"
	ModeAcceptEdits Mode = "acceptEdits"
	ModeBypass      Mode = "bypassPermissions"
	ModePlan        Mode = "plan"
	ModeDontAsk     Mode = "dontAsk"
	ModeAuto        Mode = "auto"
)

// ValidModes is the accepted set, in display order.
var ValidModes = []Mode{ModeDefault, ModeAcceptEdits, ModePlan, ModeDontAsk, ModeAuto, ModeBypass}

func ParseMode(s string) (Mode, error) {
	for _, m := range ValidModes {
		if string(m) == s {
			return m, nil
		}
	}
	return "", errors.New("unknown permission mode: " + s)
}

// ToSDK maps a policy mode onto the SDK mode. Unknown input fails closed to
// ModeDefault rather than silently widening permissions.
func (m Mode) ToSDK() acperm.Mode {
	switch m {
	case ModeAcceptEdits:
		return acperm.ModeAcceptEdits
	case ModeBypass:
		return acperm.ModeBypass
	case ModePlan:
		return acperm.ModePlan
	case ModeDontAsk:
		return acperm.ModeDontAsk
	case ModeAuto:
		return acperm.ModeAuto
	default:
		return acperm.ModeDefault
	}
}

// Behavior is the rule outcome.
type Behavior string

const (
	BehaviorAllow Behavior = "allow"
	BehaviorDeny  Behavior = "deny"
	BehaviorAsk   Behavior = "ask"
)

// Rule is one operator-defined permission rule. Rules are evaluated in priority
// order (highest first); the first match wins. Pattern is a literal tool name,
// a "Tool(pattern)" form, or "*" (any tool).
type Rule struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Pattern  string   `json:"pattern"`
	Behavior Behavior `json:"behavior"`
	Priority int      `json:"priority"`
	Enabled  bool     `json:"enabled"`
}

// DecisionReason is a replayable explanation of a permission decision.
type DecisionReason string

const (
	ReasonAllowRule DecisionReason = "allow_rule"
	ReasonDenyRule  DecisionReason = "deny_rule"
	ReasonAskRule   DecisionReason = "ask_rule"
	ReasonMode      DecisionReason = "mode"
	ReasonNoRule    DecisionReason = "no_rule"
	ReasonSession   DecisionReason = "session_grant"
	ReasonReadOnly  DecisionReason = "read_only"
	ReasonPlatform  DecisionReason = "platform_gate"
)

// Decision is the typed outcome of a policy evaluation.
type Decision struct {
	Behavior Behavior       `json:"behavior"`
	Reason   DecisionReason `json:"reason"`
	Rule     string         `json:"rule,omitempty"`
	Mode     Mode           `json:"mode"`
	Message  string         `json:"message,omitempty"`
}

// Config is the persisted shape (stored in the settings KV table as JSON).
type Config struct {
	Mode  Mode   `json:"mode"`
	Rules []Rule `json:"rules"`
}

// DefaultConfig keeps today's behaviour: bypass mode at the SDK layer, with the
// platform hooks (intercept rules / approvals) as the real gate. No rules means
// no new asks are introduced by this layer.
func DefaultConfig() Config {
	return Config{Mode: ModeBypass, Rules: nil}
}

// ParseConfig accepts an empty value as the default and rejects a bad mode.
func ParseConfig(raw string) (Config, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultConfig(), nil
	}
	var c Config
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Config{}, err
	}
	if c.Mode == "" {
		c.Mode = ModeBypass
	} else if _, err := ParseMode(string(c.Mode)); err != nil {
		return Config{}, err
	}
	for i := range c.Rules {
		switch c.Rules[i].Behavior {
		case BehaviorAllow, BehaviorDeny, BehaviorAsk:
		default:
			return Config{}, errors.New("rule " + c.Rules[i].Name + " has an invalid behavior")
		}
	}
	return c, nil
}

// Policy is a concurrency-safe, in-memory view of Config plus session-scoped
// grants ("allow for this session"). Session grants are never persisted: they
// deliberately die with the process, like the session-scoped approvals in a
// desktop harness.
type Policy struct {
	mu      sync.RWMutex
	cfg     Config
	session map[string]bool // "Tool" or "Tool(pattern)" → granted
}

func NewPolicy(c Config) *Policy {
	if c.Mode == "" {
		c.Mode = ModeBypass
	}
	return &Policy{cfg: c, session: map[string]bool{}}
}

// Snapshot returns a copy of the config (for the API/console).
func (p *Policy) Snapshot() Config {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := p.cfg
	out.Rules = append([]Rule(nil), p.cfg.Rules...)
	return out
}

// Replace swaps the config, clearing session grants: a policy change must not
// leave a grant that was made under the old rules.
func (p *Policy) Replace(c Config) error {
	if c.Mode == "" {
		c.Mode = ModeBypass
	}
	if _, err := ParseMode(string(c.Mode)); err != nil {
		return err
	}
	for i := range c.Rules {
		switch c.Rules[i].Behavior {
		case BehaviorAllow, BehaviorDeny, BehaviorAsk:
		default:
			return errors.New("rule " + c.Rules[i].Name + " has an invalid behavior")
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cfg = c
	p.session = map[string]bool{}
	return nil
}

// GrantSession records a session-scoped allow for a tool (or tool pattern).
func (p *Policy) GrantSession(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	p.mu.Lock()
	p.session[key] = true
	p.mu.Unlock()
}

// SessionGrants returns the granted keys, sorted for stable output.
func (p *Policy) SessionGrants() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.session))
	for k := range p.session {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Evaluate applies rules (priority DESC), then session grants, then mode.
//
// Order matters: an explicit deny rule must win even in bypass mode — same
// deny-priority invariant as norma/permission.Evaluate. The platform hooks are
// NOT consulted here; they still run in the PreToolUse chain and remain the only
// veto point.
func (p *Policy) Evaluate(tool string, isReadOnly bool) Decision {
	p.mu.RLock()
	cfg := p.cfg
	rules := append([]Rule(nil), cfg.Rules...)
	granted := map[string]bool{}
	for k, v := range p.session {
		granted[k] = v
	}
	p.mu.RUnlock()

	sort.SliceStable(rules, func(i, j int) bool { return rules[i].Priority > rules[j].Priority })
	for _, r := range rules {
		if !r.Enabled || !match(r.Pattern, tool) {
			continue
		}
		switch r.Behavior {
		case BehaviorDeny:
			return Decision{Behavior: BehaviorDeny, Reason: ReasonDenyRule, Rule: r.Name, Mode: cfg.Mode,
				Message: "规则 [" + r.Name + "] 禁止此调用"}
		case BehaviorAllow:
			return Decision{Behavior: BehaviorAllow, Reason: ReasonAllowRule, Rule: r.Name, Mode: cfg.Mode}
		case BehaviorAsk:
			if matchGrant(granted, tool) {
				return Decision{Behavior: BehaviorAllow, Reason: ReasonSession, Rule: r.Name, Mode: cfg.Mode}
			}
			return Decision{Behavior: BehaviorAsk, Reason: ReasonAskRule, Rule: r.Name, Mode: cfg.Mode,
				Message: "规则 [" + r.Name + "] 要求审批"}
		}
	}

	if matchGrant(granted, tool) {
		return Decision{Behavior: BehaviorAllow, Reason: ReasonSession, Mode: cfg.Mode}
	}
	// Read-only tools never need an ask in the default/dontAsk/auto modes; the
	// SDK evaluator already auto-allows them. Reporting that fact keeps the
	// decision reason honest.
	if isReadOnly && cfg.Mode != ModePlan {
		return Decision{Behavior: BehaviorAllow, Reason: ReasonReadOnly, Mode: cfg.Mode}
	}
	if cfg.Mode == ModeBypass || cfg.Mode == ModeAcceptEdits {
		return Decision{Behavior: BehaviorAllow, Reason: ReasonMode, Mode: cfg.Mode}
	}
	return Decision{Behavior: BehaviorAllow, Reason: ReasonNoRule, Mode: cfg.Mode}
}

// match mirrors norma/permission.matchRule: a bare tool name, "*", or
// "Name(pattern)" whose Name part matches.
func match(pattern, tool string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "*" || pattern == tool {
		return true
	}
	if i := strings.IndexByte(pattern, '('); i > 0 && pattern[:i] == tool {
		return true
	}
	return false
}

func matchGrant(grants map[string]bool, tool string) bool {
	for k, v := range grants {
		if v && match(k, tool) {
			return true
		}
	}
	return false
}
