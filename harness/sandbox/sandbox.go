// Package sandbox is the Stage 1 (P0) per-task isolation boundary.
//
// Before Stage 1, an agent command ran with the service process's
// privileges, in whatever working directory the caller gave it, with the
// full process environment (including LLM keys and tokens) visible.
// That is the single largest gap in the gap analysis (docs/06, docs/02 §3.7
// "isolation over coordination").
//
// This package is deliberately minimal and honest about what it is:
//   - It is a POLICY object, not a container runtime. It validates paths,
//     scrubs secrets from the environment, and checks hosts against an
//     allowlist / private-network policy.
//   - It does NOT yet launch a Docker/Kata container. Container execution
//     is Stage 2 and needs a daemon, image policy and network namespace.
//     The Sandbox interface is the seam where that backend will plug in,
//     so callers do not need to change when it lands.
//   - LocalSandbox is the current backend: same-process execution, but
//     with workdir containment and env scrubbing enforced at the guard
//     layer. It makes accidental escape (path traversal, env leak,
//     private-network egress) a deterministic deny instead of a hope.
//
// Callers: guard (path + host checks before PreToolUse allows a call)
// and server/harness wiring (one Sandbox per task, rooted at the task's
// workdir / project_dir).
package sandbox

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"
)

// Policy is the per-task isolation policy. Zero value is safe: workdir
// containment on, private-network egress denied, secrets scrubbed.
type Policy struct {
	// WorkDir is the task's root. All file access must stay inside it
	// (or inside AllowedRoots).
	WorkDir string
	// AllowedRoots are additional roots a task may read/write, e.g. a
	// bound project_dir. Empty means [WorkDir].
	AllowedRoots []string
	// AllowedHosts, when non-empty, is the network allowlist. A host
	// not matching it (suffix match) is denied. Empty means "no
	// allowlist, only the private-network rule applies".
	AllowedHosts []string
	// DenyPrivateNetwork, when true (default in New), blocks RFC1918,
	// loopback and link-local egress unless the host is explicitly
	// allowlisted. Local smoke tests use 127.0.0.1 and must allowlist it.
	DenyPrivateNetwork bool
	// Timeout is the per-command ceiling the executor should enforce.
	// Zero means the caller's default.
	Timeout time.Duration
	// EnvAllowlist, when non-empty, is the only set of sensitive keys
	// that may survive scrubbing. Empty means no sensitive key survives.
	EnvAllowlist []string
}

// Sandbox is the isolation seam. A future container backend implements
// the same interface.
type Sandbox interface {
	// WorkDir returns the task root.
	WorkDir() string
	// ValidatePath rejects absolute paths outside AllowedRoots and any
	// traversal that escapes them.
	ValidatePath(path string) error
	// ScrubEnv removes secrets from an environment list ("K=V").
	ScrubEnv(env []string) []string
	// CheckHost enforces AllowedHosts + DenyPrivateNetwork.
	CheckHost(host string) error
	// Policy returns the effective policy (for audit / readiness UI).
	Policy() Policy
}

// LocalSandbox is the Stage 1 backend: policy enforcement in-process.
type LocalSandbox struct {
	policy Policy
	roots  []string
	allow  map[string]bool
}

var _ Sandbox = (*LocalSandbox)(nil)

// NewLocalSandbox builds a LocalSandbox. workDir must be absolute;
// roots defaults to [workDir].
func NewLocalSandbox(p Policy) (*LocalSandbox, error) {
	if p.WorkDir == "" {
		return nil, fmt.Errorf("sandbox: WorkDir 不能为空")
	}
	if !filepath.IsAbs(p.WorkDir) {
		return nil, fmt.Errorf("sandbox: WorkDir 必须是绝对路径: %s", p.WorkDir)
	}
	roots := p.AllowedRoots
	if len(roots) == 0 {
		roots = []string{p.WorkDir}
	}
	cleaned := make([]string, 0, len(roots))
	for _, r := range roots {
		if !filepath.IsAbs(r) {
			return nil, fmt.Errorf("sandbox: AllowedRoots 必须是绝对路径: %s", r)
		}
		cleaned = append(cleaned, filepath.Clean(r))
	}
	allow := map[string]bool{}
	for _, h := range p.EnvAllowlist {
		allow[strings.ToUpper(strings.TrimSpace(h))] = true
	}
	// DenyPrivateNetwork defaults to true when the caller did not
	// explicitly set it false via the struct — callers that want it off
	// must set DenyPrivateNetwork=false AND set a flag. To keep the zero
	// value safe, New treats an unset Policy as deny-private. There is
	// no way to distinguish unset from false in a bool, so Stage 1
	// documents: set DenyPrivateNetwork=true explicitly, or use
	// NewLocalSandboxWithOptions. For now, New forces true unless the
	// caller used the AllowPrivateNetwork option below.
	p.DenyPrivateNetwork = true
	p.AllowedRoots = cleaned
	p.WorkDir = filepath.Clean(p.WorkDir)
	return &LocalSandbox{policy: p, roots: cleaned, allow: allow}, nil
}

// NewLocalSandboxAllowPrivate is for local smoke tests that need
// loopback. Production tasks must use NewLocalSandbox.
func NewLocalSandboxAllowPrivate(p Policy) (*LocalSandbox, error) {
	s, err := NewLocalSandbox(p)
	if err != nil {
		return nil, err
	}
	s.policy.DenyPrivateNetwork = false
	return s, nil
}

func (s *LocalSandbox) WorkDir() string { return s.policy.WorkDir }
func (s *LocalSandbox) Policy() Policy  { return s.policy }

func (s *LocalSandbox) ValidatePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("sandbox: 路径为空")
	}
	// Relative paths are resolved against WorkDir.
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.policy.WorkDir, abs)
	}
	abs = filepath.Clean(abs)
	for _, root := range s.roots {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
			return nil
		}
	}
	return fmt.Errorf("sandbox: 路径 %s 超出任务工作区 %v", path, s.roots)
}

var sensitiveSubstrings = []string{
	"TOKEN", "SECRET", "PASSWORD", "PASSWD", "API_KEY", "APIKEY",
	"PRIVATE_KEY", "AWS_", "GCP_", "AZURE_", "JWT", "COOKIE",
	"ARTEX_", "OPENAI_API_KEY", "ANTHROPIC_API_KEY",
}

func isSensitiveKey(k string) bool {
	up := strings.ToUpper(k)
	for _, sub := range sensitiveSubstrings {
		if strings.Contains(up, sub) {
			return true
		}
	}
	return false
}

// ScrubEnv drops sensitive keys unless explicitly allowlisted. Non-sensitive
// keys (PATH, HOME, LANG, …) pass through unchanged.
func (s *LocalSandbox) ScrubEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if isSensitiveKey(k) && !s.allow[strings.ToUpper(k)] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// RFC1918 + CGNAT + unspecified.
	if ip.IsPrivate() || ip.IsUnspecified() {
		return true
	}
	return false
}

// CheckHost enforces the network policy. AllowedHosts is a suffix
// allowlist: "example.com" allows "api.example.com".
func (s *LocalSandbox) CheckHost(host string) error {
	host = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(host, ".")))
	if host == "" {
		return fmt.Errorf("sandbox: 主机为空")
	}
	// Strip port / brackets.
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	// Explicit allowlist wins.
	for _, a := range s.policy.AllowedHosts {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if host == a || strings.HasSuffix(host, "."+a) {
			return nil
		}
	}
	if len(s.policy.AllowedHosts) > 0 {
		return fmt.Errorf("sandbox: 主机 %s 不在任务网络白名单 %v 内", host, s.policy.AllowedHosts)
	}
	if s.policy.DenyPrivateNetwork {
		if ip := net.ParseIP(host); ip != nil {
			if isPrivateIP(ip) {
				return fmt.Errorf("sandbox: 拒绝访问内网/回环地址 %s", host)
			}
			return nil
		}
		// Hostnames that resolve to private IPs are a DNS-rebinding
		// risk; Stage 1 cannot resolve without network, so it denies
		// only literal private IPs and localhost names here. The
		// guard scope check is the second layer for hostnames.
		if host == "localhost" {
			return fmt.Errorf("sandbox: 拒绝访问回环主机 localhost（需显式加入白名单）")
		}
	}
	return nil
}
