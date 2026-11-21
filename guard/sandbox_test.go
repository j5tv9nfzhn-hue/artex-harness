package guard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/harness/sandbox"
	"github.com/Autumn-27/artex/intercept"
)

// S6-FIX-PROXY-SANDBOX: the Guard enforces the per-task sandbox policy in
// PreToolUse — file tools contained to the task dir, loopback/private
// egress denied unless the task's RoE scope authorizes the host.

func sandboxGuard(t *testing.T, workdir string, scopes map[int64][]db.TaskScope) *Guard {
	t.Helper()
	sb, err := sandbox.NewLocalSandbox(sandbox.Policy{WorkDir: workdir})
	if err != nil {
		t.Fatal(err)
	}
	g := New(WithSandbox(sb))
	if scopes != nil {
		g.SetScopeChecker(&StaticScopeChecker{ScopesByTask: scopes})
	}
	return g
}

func taskCtx(taskID string) context.Context {
	return intercept.WithTaskContext(context.Background(), taskID, "test", nil)
}

func sandboxBlocked(t *testing.T, g *Guard, ctx context.Context, tool string, input any) bool {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := g.Hooks().PreToolUse(ctx, tool, raw)
	return b
}

func TestSandboxReadOutsideWorkdirBlocked(t *testing.T) {
	work := t.TempDir()
	g := sandboxGuard(t, work, nil)
	ctx := taskCtx("1")
	if !sandboxBlocked(t, g, ctx, "Read", map[string]string{"file_path": "/etc/passwd"}) {
		t.Error("Read /etc/passwd should be blocked by the sandbox")
	}
	if !sandboxBlocked(t, g, ctx, "Read", map[string]string{"file_path": filepath.Join(work, "..", "secret.txt")}) {
		t.Error("Read escaping the workdir should be blocked")
	}
	inside := filepath.Join(work, "notes.txt")
	if err := os.WriteFile(inside, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if sandboxBlocked(t, g, ctx, "Read", map[string]string{"file_path": inside}) {
		t.Error("Read inside the workdir should pass")
	}
	if sandboxBlocked(t, g, ctx, "Write", map[string]string{"file_path": filepath.Join(work, "out.txt"), "content": "x"}) {
		t.Error("Write inside the workdir should pass")
	}
	// The range source tree from real-task #1 must be unreachable.
	if !sandboxBlocked(t, g, ctx, "Read", map[string]string{"file_path": "/home/hatch/workspace/target-range/app.py"}) {
		t.Error("Read of the range source should be blocked")
	}
	// Symlink escape through a file tool.
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(work, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if !sandboxBlocked(t, g, ctx, "Read", map[string]string{"file_path": link}) {
		t.Error("Read through a symlink escaping the workdir should be blocked")
	}
}

func TestSandboxLoopbackNeedsRoeScope(t *testing.T) {
	work := t.TempDir()
	// The scope row real-task #1 got from the seed: ip 127.0.0.1/32.
	scopes := map[int64][]db.TaskScope{
		1: {{Kind: "ip", Net: "127.0.0.1/32"}},
	}
	g := sandboxGuard(t, work, scopes)
	ctx := taskCtx("1")
	// RoE-authorized loopback target: reachable.
	if sandboxBlocked(t, g, ctx, "WebFetch", map[string]string{"url": "http://127.0.0.1:18080/"}) {
		t.Error("WebFetch to the RoE-authorized loopback target should pass")
	}
	if sandboxBlocked(t, g, ctx, "Bash", map[string]string{"command": "curl -s http://127.0.0.1:18080/"}) {
		t.Error("Bash curl to the RoE-authorized loopback target should pass")
	}
	// Other private nets are not in scope: denied by the sandbox.
	if !sandboxBlocked(t, g, ctx, "WebFetch", map[string]string{"url": "http://192.168.1.10/admin"}) {
		t.Error("WebFetch to a private IP outside RoE scope should be blocked")
	}
	if !sandboxBlocked(t, g, ctx, "Bash", map[string]string{"command": "curl -s http://10.0.0.5/"}) {
		t.Error("Bash curl to a private IP outside RoE scope should be blocked")
	}
}

func TestSandboxPublicHostPassesFloor(t *testing.T) {
	work := t.TempDir()
	g := sandboxGuard(t, work, nil) // no scope checker: sandbox floor only
	ctx := taskCtx("1")
	if sandboxBlocked(t, g, ctx, "WebFetch", map[string]string{"url": "https://example.com/docs"}) {
		t.Error("WebFetch to a public host should pass the sandbox floor")
	}
}

func TestSandboxUnanchoredTaskFailsOpenForNetwork(t *testing.T) {
	work := t.TempDir()
	scopes := map[int64][]db.TaskScope{} // checker present, but task 2 has no rows
	g := sandboxGuard(t, work, scopes)
	ctx := taskCtx("2")
	// Legacy fail-open (mirrors CheckScope): no scope rows, loopback allowed.
	if sandboxBlocked(t, g, ctx, "WebFetch", map[string]string{"url": "http://127.0.0.1:18080/"}) {
		t.Error("unanchored task should fail open for loopback (legacy rule)")
	}
	// File containment still applies.
	if !sandboxBlocked(t, g, ctx, "Read", map[string]string{"file_path": "/etc/passwd"}) {
		t.Error("file containment must still apply for unanchored tasks")
	}
}

func TestNoSandboxNoEnforcement(t *testing.T) {
	g := New() // no sandbox wired
	ctx := taskCtx("1")
	if sandboxBlocked(t, g, ctx, "Read", map[string]string{"file_path": "/etc/passwd"}) {
		t.Error("guard without a sandbox must not block file reads")
	}
}
