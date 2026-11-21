package guard

import (
	"encoding/json"
	"testing"
)

func mustInput(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestClassifyIntentHardDeny(t *testing.T) {
	cases := []struct {
		tool  string
		input map[string]string
		kind  IntentKind
	}{
		{"Bash", map[string]string{"command": "rm -rf /"}, IntentFilesystemDelete},
		{"Bash", map[string]string{"command": "rm -rf /*"}, IntentFilesystemDelete},
		{"Bash", map[string]string{"command": "mkfs.ext4 /dev/sda1"}, IntentFilesystemDelete},
		{"Bash", map[string]string{"command": "dd if=/dev/zero of=/dev/sda bs=1M"}, IntentFilesystemDelete},
		{"Bash", map[string]string{"command": "shred -u secret.txt"}, IntentFilesystemDelete},
		{"Bash", map[string]string{"command": "psql -c 'DROP DATABASE prod'"}, IntentDatabaseDestructive},
		{"Bash", map[string]string{"command": "mysql -e 'TRUNCATE TABLE users'"}, IntentDatabaseDestructive},
	}
	for _, c := range cases {
		in := ClassifyIntent(c.tool, mustInput(t, c.input))
		if in.Kind != c.kind {
			t.Errorf("tool=%s input=%v: kind=%s want %s", c.tool, c.input, in.Kind, c.kind)
		}
		if !in.IsHardDeny() {
			t.Errorf("tool=%s input=%v: expected hard deny, got risk=%s", c.tool, c.input, in.Risk)
		}
	}
}

func TestClassifyIntentBenign(t *testing.T) {
	cases := []struct {
		tool  string
		input map[string]string
	}{
		{"Bash", map[string]string{"command": "ls -la"}},
		{"Bash", map[string]string{"command": "curl https://example.com/"}},
		{"Bash", map[string]string{"command": "cat README.md"}},
	}
	for _, c := range cases {
		in := ClassifyIntent(c.tool, mustInput(t, c.input))
		if in.IsHardDeny() {
			t.Errorf("benign %v should not be hard deny, got %+v", c.input, in)
		}
	}
}

func TestClassifyIntentHighNotHardDeny(t *testing.T) {
	// High-risk is surfaced, not mechanically denied: sudo and credential
	// reads go to DB rules / ask path.
	in := ClassifyIntent("Bash", mustInput(t, map[string]string{"command": "sudo apt update"}))
	if in.Kind != IntentPrivilegeEscalation {
		t.Errorf("expected privilege_escalation, got %s", in.Kind)
	}
	if in.IsHardDeny() {
		t.Errorf("sudo should be high, not critical")
	}
	in = ClassifyIntent("Bash", mustInput(t, map[string]string{"command": "cat ~/.ssh/id_rsa"}))
	if in.Kind != IntentCredentialAccess {
		t.Errorf("expected credential_access, got %s", in.Kind)
	}
}

func TestClassifyIntentWriteTool(t *testing.T) {
	in := ClassifyIntent("Write", mustInput(t, map[string]string{"file_path": "/tmp/x.txt"}))
	if in.Kind != IntentFilesystemWrite {
		t.Errorf("expected filesystem_write, got %s", in.Kind)
	}
}
