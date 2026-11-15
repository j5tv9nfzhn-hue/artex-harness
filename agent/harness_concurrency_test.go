package agent

// Sprint 1.1 — the HARNESS tool pool must expose the same scheduling metadata
// norma's streaming executor consumes. This is a contract test, not a re-test of
// the SDK: it pins that OUR assembled tools correctly declare read-only /
// concurrency-safe, because a wrong declaration is the one way to make a write
// run in parallel with another write.
//
// Design reference: mature harnesses partition tool calls into read-only batches
// (parallel) and write batches (serial). norma already does this; these tests
// ensure the artex layer does not silently defeat it.

import (
	"encoding/json"
	"testing"

	"github.com/Autumn-27/artex/harness/protocol"
	actool "github.com/Autumn-27/norma/tool"
)

func harnessToolPool() []actool.CoreTool {
	ts := NewToolSet(nil, "harness")
	return append(ts.MainAgentTools(), actool.DefaultTools()...)
}

func findTool(t *testing.T, name string) actool.CoreTool {
	t.Helper()
	for _, tool := range harnessToolPool() {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("tool %q missing from the HARNESS pool", name)
	return nil
}

func TestHarnessReadToolsAreParallel(t *testing.T) {
	for _, name := range []string{"graph_overview", "list_findings", "list_facts", "Read", "Grep", "Glob", "LS"} {
		t.Run(name, func(t *testing.T) {
			tl := findTool(t, name)
			in := json.RawMessage(`{}`)
			if !tl.IsReadOnly(in) {
				t.Fatalf("%q should be read-only", name)
			}
			if !tl.IsConcurrencySafe(in) {
				t.Fatalf("%q should be concurrency-safe", name)
			}
			class, _ := protocol.ClassifyToolExecution(tl.IsConcurrencySafe(in), tl.IsReadOnly(in))
			if class != protocol.ToolParallel {
				t.Fatalf("%q class = %q, want parallel", name, class)
			}
		})
	}
}

func TestHarnessWriteToolsAreExclusive(t *testing.T) {
	for _, name := range []string{"add_intent", "record_fact", "Write", "Edit", "Bash"} {
		t.Run(name, func(t *testing.T) {
			tl := findTool(t, name)
			in := json.RawMessage(`{}`)
			if tl.IsConcurrencySafe(in) {
				t.Fatalf("%q must not be concurrency-safe (writes need exclusive access)", name)
			}
			class, _ := protocol.ClassifyToolExecution(tl.IsConcurrencySafe(in), tl.IsReadOnly(in))
			if class != protocol.ToolExclusive {
				t.Fatalf("%q class = %q, want exclusive", name, class)
			}
		})
	}
}

// The Agent tool is re-declared through actool.Build without a Concurrent
// function, so it is exclusive by default. A delegation must never overlap a
// write in the same turn.
func TestHarnessAgentToolIsExclusive(t *testing.T) {
	h := &HarnessAgent{}
	tl := h.agentTool(harnessToolPool(), nil, 1)
	if tl.IsConcurrencySafe(json.RawMessage(`{}`)) {
		t.Fatal("the Agent delegation tool must be exclusive")
	}
}
