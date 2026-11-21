package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustSandbox(t *testing.T) *LocalSandbox {
	t.Helper()
	s, err := NewLocalSandbox(Policy{WorkDir: "/tmp/hdata/tasks/1"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestValidatePath(t *testing.T) {
	s := mustSandbox(t)
	if err := s.ValidatePath("/tmp/hdata/tasks/1/a.txt"); err != nil {
		t.Errorf("in-workdir should pass: %v", err)
	}
	if err := s.ValidatePath("a/b.txt"); err != nil {
		t.Errorf("relative should resolve inside workdir: %v", err)
	}
	if err := s.ValidatePath("/tmp/hdata/tasks/1/../2/secret"); err == nil {
		t.Error("traversal outside workdir should be denied")
	}
	if err := s.ValidatePath("/etc/passwd"); err == nil {
		t.Error("/etc/passwd should be denied")
	}
	if err := s.ValidatePath("../../etc/passwd"); err == nil {
		t.Error("relative traversal should be denied")
	}
}

func TestScrubEnv(t *testing.T) {
	s := mustSandbox(t)
	env := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/tmp/hdata/tasks/1",
		"OPENAI_API_KEY=sk-secret",
		"MY_TOKEN=abc",
		"ARTEX_PG_DSN=postgres://x:y@host/db",
		"LANG=C.UTF-8",
	}
	out := s.ScrubEnv(env)
	joined := strings.Join(out, "\n")
	if strings.Contains(joined, "sk-secret") || strings.Contains(joined, "MY_TOKEN") || strings.Contains(joined, "ARTEX_PG_DSN") {
		t.Errorf("sensitive env should be scrubbed, got %v", out)
	}
	if !strings.Contains(joined, "PATH=") || !strings.Contains(joined, "LANG=") {
		t.Errorf("benign env should pass, got %v", out)
	}
}

func TestCheckHostPrivateDenied(t *testing.T) {
	s := mustSandbox(t)
	for _, h := range []string{"10.0.0.5", "192.168.1.1", "127.0.0.1", "localhost"} {
		if err := s.CheckHost(h); err == nil {
			t.Errorf("private host %s should be denied", h)
		}
	}
	if err := s.CheckHost("8.8.8.8"); err != nil {
		t.Errorf("public IP should pass when no allowlist: %v", err)
	}
	if err := s.CheckHost("example.com"); err != nil {
		t.Errorf("public hostname should pass when no allowlist: %v", err)
	}
}

func TestCheckHostAllowlist(t *testing.T) {
	s, err := NewLocalSandbox(Policy{WorkDir: "/tmp/hdata/tasks/1", AllowedHosts: []string{"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckHost("api.example.com"); err != nil {
		t.Errorf("suffix of allowlist should pass: %v", err)
	}
	if err := s.CheckHost("evil.com"); err == nil {
		t.Error("evil.com should be denied by allowlist")
	}
}

func TestValidatePathSymlinkEscape(t *testing.T) {
	work := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewLocalSandbox(Policy{WorkDir: work})
	if err != nil {
		t.Fatal(err)
	}
	// Symlink to a file outside the workdir.
	link := filepath.Join(work, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidatePath(link); err == nil {
		t.Error("symlink inside workdir pointing outside should be denied")
	}
	// Symlink to an outside directory, then a child path under it.
	dirLink := filepath.Join(work, "range")
	if err := os.Symlink(outside, dirLink); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidatePath(filepath.Join(dirLink, "secret.txt")); err == nil {
		t.Error("path through a symlinked dir escaping the workdir should be denied")
	}
	// A symlink staying inside the workdir still passes.
	inner := filepath.Join(work, "inner.txt")
	if err := os.WriteFile(inner, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	innerLink := filepath.Join(work, "inner-link.txt")
	if err := os.Symlink(inner, innerLink); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidatePath(innerLink); err != nil {
		t.Errorf("symlink staying inside workdir should pass: %v", err)
	}
}

func TestValidatePathDenyPrefixes(t *testing.T) {
	s := mustSandbox(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if err := s.ValidatePath(filepath.Join(home, ".ssh", "id_ed25519")); err == nil {
		t.Error("~/.ssh should be denied even if inside an allowed root")
	}
	if err := s.ValidatePath("/proc/self/status"); err == nil {
		t.Error("/proc should be denied")
	}
	if err := s.ValidatePath("/tmp/hdata/tasks/1/notes.txt"); err != nil {
		t.Errorf("ordinary in-workdir file should pass: %v", err)
	}
}

func TestEnvScrubOverrides(t *testing.T) {
	t.Setenv("SANDBOX_TEST_DEMO_TOKEN", "sk-secret-value")
	t.Setenv("SANDBOX_TEST_BENIGN_VAR", "plain")
	over := EnvScrubOverrides()
	found := false
	for _, e := range over {
		if e == "SANDBOX_TEST_DEMO_TOKEN=" {
			found = true
		}
		if strings.Contains(e, "sk-secret-value") {
			t.Errorf("override leaked the secret value: %q", e)
		}
		if strings.HasPrefix(e, "SANDBOX_TEST_BENIGN_VAR=") {
			t.Errorf("benign var must not be overridden: %q", e)
		}
	}
	if !found {
		t.Errorf("expected override for SANDBOX_TEST_DEMO_TOKEN, got %v", over)
	}
}

func TestNewRequiresAbsWorkDir(t *testing.T) {
	if _, err := NewLocalSandbox(Policy{WorkDir: "relative/path"}); err == nil {
		t.Error("relative WorkDir should be rejected")
	}
	if _, err := NewLocalSandbox(Policy{}); err == nil {
		t.Error("empty WorkDir should be rejected")
	}
}
