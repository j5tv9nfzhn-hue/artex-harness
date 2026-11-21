package sandbox

import (
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

func TestNewRequiresAbsWorkDir(t *testing.T) {
	if _, err := NewLocalSandbox(Policy{WorkDir: "relative/path"}); err == nil {
		t.Error("relative WorkDir should be rejected")
	}
	if _, err := NewLocalSandbox(Policy{}); err == nil {
		t.Error("empty WorkDir should be rejected")
	}
}
