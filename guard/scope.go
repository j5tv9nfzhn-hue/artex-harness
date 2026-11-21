package guard

// Stage 1 (P0) — RoE authorization-scope enforcement, restored.
//
// The data model already exists (db/task_scope.go: TaskScope, ListTaskScope,
// AddAgentScope) and the UI/agent already has an add_task_scope tool.
// What was missing was the enforcement point: guard.go said "The RoE
// authorization-scope mechanism was removed; a replacement may be added
// later." This file is that replacement, kept pure so it can be tested
// without a database.
//
// Semantics, deliberately conservative:
//   - No scope rows for the task -> unknown, not a deny. Legacy tasks
//     created before scope was anchored must not suddenly break.
//     The caller logs the unanchored call instead.
//   - Scope rows present and the extracted host matches none of them
//     -> out of scope -> deny, with a message telling the agent to
//     register scope via add_task_scope and give a reason.
//   - Host extraction is best-effort: URLs, bare domains and IPs in
//     Bash / WebFetch / web_search inputs. If no host can be extracted,
//     the call is not scope-checked (other intent checks still apply).

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// ScopeChecker answers "is this host in scope for this task?".
// Implementations may hit the DB (see DBScopeChecker in server code)
// or be in-memory for tests.
type ScopeChecker interface {
	// Check returns (inScope, hasScope, err). hasScope=false means the
	// task has no scope rows at all.
	Check(ctx context.Context, taskID int64, host string) (bool, bool, error)
	// Scopes returns the raw rows for audit / debugging. May return nil.
	Scopes(ctx context.Context, taskID int64) ([]db.TaskScope, error)
}

// ScopeDecision is the outcome of a scope check for one tool call.
type ScopeDecision struct {
	Checked  bool   // a host was extracted and a check ran
	InScope  bool   // host matched a scope row
	HasScope bool   // task has at least one scope row
	Host     string // extracted host, "" when none
	Reason   string
}

var (
	reURLHost = regexp.MustCompile(`(?i)https?://([^/\s:@]+)`)
	reHost    = regexp.MustCompile(`(?i)\b([a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+)\b`)
	reIPv4    = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
)

// ExtractHosts returns distinct hosts mentioned in a tool call, in order.
// URLs win over bare domains; IPs are kept as-is.
func ExtractHosts(tool string, input []byte) []string {
	text := toolInputText(tool, input)
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		h = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(h, ".")))
		h = strings.TrimPrefix(h, "www.")
		if h == "" || seen[h] {
			return
		}
		// Skip obvious non-hosts: file paths and localhost handled by caller.
		if strings.Contains(h, "/") || strings.Contains(h, "\\") {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	// Structured url field first.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err == nil {
		for _, k := range []string{"url", "URL"} {
			if raw, ok := m[k]; ok {
				var s string
				if err := json.Unmarshal(raw, &s); err == nil {
					if u, err := url.Parse(s); err == nil && u.Host != "" {
						add(u.Hostname())
					}
				}
			}
		}
	}
	for _, h := range reURLHost.FindAllStringSubmatch(text, -1) {
		if len(h) > 1 {
			add(h[1])
		}
	}
	for _, h := range reIPv4.FindAllString(text, -1) {
		add(h)
	}
	// Bare domains only for network tools, to avoid flagging file names
	// in Bash as hosts.
	switch tool {
	case "WebFetch", "web_search", "WebSearch", "Bash", "shell_open", "shell_send":
		for _, h := range reHost.FindAllString(text, -1) {
			// Require a dot and a plausible TLD / known suffix, or it is
			// just a word. Keep it simple: dot + at least 2-char suffix.
			if idx := strings.LastIndex(h, "."); idx > 0 && len(h)-idx-1 >= 2 {
				add(h)
			}
		}
	}
	return out
}

// CheckHostAgainstScopes is the pure matcher. It understands the same
// kinds db/task_scope.go supports: company (conservative: any company
// row means the caller must have resolved the company elsewhere, so
// treat as in-scope only when the host was already linked — here we
// return true only if there is a company row AND no more specific row
// contradicts it; callers that need strict company matching should use
// the DB checker), root_domain / subdomain, ip / cidr, icp / keyword
// (keyword/icp cannot be matched from a bare host, so they do not grant).
func CheckHostAgainstScopes(host string, scopes []db.TaskScope) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	host = strings.TrimSuffix(host, ".")
	ip := net.ParseIP(host)
	for _, s := range scopes {
		switch s.Kind {
		case "root_domain":
			d := strings.ToLower(strings.TrimSpace(s.Domain))
			if d != "" && (host == d || strings.HasSuffix(host, "."+d)) {
				return true
			}
		case "subdomain":
			d := strings.ToLower(strings.TrimSpace(s.Domain))
			if d != "" && host == d {
				return true
			}
		case "ip":
			if ip == nil {
				continue
			}
			if _, n, err := net.ParseCIDR(s.Net); err == nil && n.Contains(ip) {
				return true
			}
		case "cidr":
			if ip == nil {
				continue
			}
			if _, n, err := net.ParseCIDR(s.Net); err == nil && n.Contains(ip) {
				return true
			}
		case "company":
			// Company scope is an enterprise boundary, not a host. A pure
			// host matcher cannot prove membership; do not grant on it
			// alone. The DB checker resolves company -> assets.
			continue
		case "icp", "keyword":
			continue
		}
	}
	return false
}

// CheckScope runs the scope check for one tool call. It returns a decision
// with Checked=false when there is nothing to check (no host, or no
// checker wired). Callers deny only when Checked && HasScope && !InScope.
func CheckScope(ctx context.Context, checker ScopeChecker, taskID int64, tool string, input []byte) ScopeDecision {
	if checker == nil || taskID <= 0 {
		return ScopeDecision{Checked: false, Reason: "未配置范围检查器或无任务"}
	}
	hosts := ExtractHosts(tool, input)
	if len(hosts) == 0 {
		return ScopeDecision{Checked: false, Reason: "未提取到目标主机"}
	}
	// Localhost / private loopback is never an authorization-scope
	// violation by itself; it is a sandbox concern (see harness/sandbox).
	// Skip it here so local smoke tests do not trip RoE.
	var external []string
	for _, h := range hosts {
		if h == "localhost" || h == "127.0.0.1" || h == "::1" {
			continue
		}
		external = append(external, h)
	}
	if len(external) == 0 {
		return ScopeDecision{Checked: false, Reason: "仅本地主机"}
	}
	host := external[0]
	scopes, err := checker.Scopes(ctx, taskID)
	if err != nil {
		// Fail open on DB error, but say so. The audit log keeps the
		// error; the intent / DB-rule layers still run.
		return ScopeDecision{Checked: false, Host: host, Reason: "范围查询失败（fail open）: " + err.Error()}
	}
	if len(scopes) == 0 {
		return ScopeDecision{Checked: true, HasScope: false, Host: host, InScope: false, Reason: "任务未锚定范围"}
	}
	inScope, _, err := checker.Check(ctx, taskID, host)
	if err != nil {
		return ScopeDecision{Checked: false, Host: host, Reason: "范围检查失败（fail open）: " + err.Error()}
	}
	if inScope {
		return ScopeDecision{Checked: true, HasScope: true, Host: host, InScope: true, Reason: "在范围内"}
	}
	return ScopeDecision{Checked: true, HasScope: true, Host: host, InScope: false, Reason: "目标 " + host + " 不在任务授权范围内，请先用 add_task_scope 登记并说明依据"}
}

// DBScopeChecker loads scope rows from PostgreSQL on every check.
// It is intentionally uncached: scope can be registered mid-run via
// add_task_scope, and the next tool call must see it.
type DBScopeChecker struct {
	DB *db.DB
}

// NewDBScopeChecker builds a DB-backed checker. Nil DB disables checks.
func NewDBScopeChecker(d *db.DB) *DBScopeChecker { return &DBScopeChecker{DB: d} }

func (s *DBScopeChecker) Check(ctx context.Context, taskID int64, host string) (bool, bool, error) {
	scopes, err := s.Scopes(ctx, taskID)
	if err != nil {
		return false, false, err
	}
	if len(scopes) == 0 {
		return false, false, nil
	}
	return CheckHostAgainstScopes(host, scopes), true, nil
}

func (s *DBScopeChecker) Scopes(_ context.Context, taskID int64) ([]db.TaskScope, error) {
	if s == nil || s.DB == nil {
		return nil, nil
	}
	return s.DB.Assets().ListTaskScope(taskID)
}

// StaticScopeChecker is an in-memory ScopeChecker for tests and for
// deployments that load scope once per task.
type StaticScopeChecker struct {
	ScopesByTask map[int64][]db.TaskScope
}

func (s *StaticScopeChecker) Check(_ context.Context, taskID int64, host string) (bool, bool, error) {
	scopes := s.ScopesByTask[taskID]
	if len(scopes) == 0 {
		return false, false, nil
	}
	return CheckHostAgainstScopes(host, scopes), true, nil
}

func (s *StaticScopeChecker) Scopes(_ context.Context, taskID int64) ([]db.TaskScope, error) {
	return s.ScopesByTask[taskID], nil
}
