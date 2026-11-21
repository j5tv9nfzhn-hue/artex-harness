package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/harness/delegation"
	actool "github.com/Autumn-27/norma/tool"
)

// A subagent refusal arrives as an ordinary successful text result — the
// child model answered with refusal text and no error — so without an
// explicit mark it sinks into a plain "done". finishDelegation must flag it.
func TestFinishDelegationMarksRefusal(t *testing.T) {
	h := NewHarnessAgent(&fakeSubagentProvider{}, "fake", t.TempDir(), nil, 64000, 4)
	reg := delegation.New(t.TempDir())
	id := reg.Start("recon", "enumerate hosts", "recon hosts", "", false, time.Now())

	var emitted []db.Activity
	emit := func(a db.Activity) { emitted = append(emitted, a) }
	h.finishDelegation(reg, id, actool.Text("I'm sorry, but I can't help with penetration testing."), nil, emit, false, 999)

	rec, ok := reg.Get(id)
	if !ok {
		t.Fatal("delegation record missing")
	}
	if !rec.Refused {
		t.Fatal("refusal text was not marked Refused")
	}
	if rec.Status != delegation.StatusDone {
		t.Fatalf("status = %s, want done (marking must not change Status)", rec.Status)
	}
	if len(emitted) == 0 {
		t.Fatal("no tool_result activity emitted")
	}
	if !strings.Contains(emitted[0].Summary, "拒答") {
		t.Fatalf("summary = %q, want it to say 子代理拒答", emitted[0].Summary)
	}
	var meta map[string]any
	if err := json.Unmarshal(emitted[0].Metadata, &meta); err != nil {
		t.Fatalf("metadata is not JSON: %v", err)
	}
	if meta["refused"] != true {
		t.Fatalf("metadata = %v, want refused:true", meta)
	}
}

// Ordinary failures and ordinary successes must NOT be marked refused:
// the flag is for model refusals only, not for every failed delegation.
func TestFinishDelegationDoesNotMarkOrdinaryFailure(t *testing.T) {
	h := NewHarnessAgent(&fakeSubagentProvider{}, "fake", t.TempDir(), nil, 64000, 4)
	reg := delegation.New(t.TempDir())

	failed := reg.Start("recon", "p", "", "", false, time.Now())
	h.finishDelegation(reg, failed, actool.Errorf("Error: subagent failed: connection reset"), nil, nil, false, 998)
	if rec, _ := reg.Get(failed); rec.Refused {
		t.Fatal("ordinary error text was marked Refused")
	} else if rec.Status != delegation.StatusFailed {
		t.Fatalf("status = %s, want failed", rec.Status)
	}

	done := reg.Start("verify", "p", "", "", false, time.Now())
	h.finishDelegation(reg, done, actool.Text("已复现：结论成立，证据见输出。"), nil, nil, false, 998)
	if rec, _ := reg.Get(done); rec.Refused {
		t.Fatal("normal success text was marked Refused")
	}
}
