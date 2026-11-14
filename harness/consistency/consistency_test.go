package consistency

import (
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/harness/eval"
	"github.com/Autumn-27/artex/harness/replay"
)

const blockedText = eval.GuardBlockMarker + "此调用被平台拦截。原因：禁止执行此工具。此操作被禁止。"

func run(items ...replay.Event) *replay.Run {
	for i := range items {
		if items[i].Revision == 0 {
			items[i].Revision = 1
		}
		if items[i].ActivityID == 0 {
			items[i].ActivityID = int64(i + 1)
		}
		if items[i].At.IsZero() {
			items[i].At = time.Unix(int64(i+1), 0)
		}
	}
	r := replay.Build("1", 1, items)
	for i := range r.Turns {
		if r.Turns[i].Kind == "" {
			r.Turns[i].Kind = "work"
		}
	}
	return r
}

func bashUse() replay.Event {
	return replay.Event{Kind: "tool_use", Tool: "Bash", Summary: "curl -X DELETE"}
}
func blocked(tool, msg string) replay.Event {
	return replay.Event{Kind: "tool_result", Tool: tool, IsError: true, Summary: msg}
}

func findCheck(t *testing.T, rep Report, name string) Check {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q not present in report", name)
	return Check{}
}

func TestIdenticalRunsPass(t *testing.T) {
	build := func() *replay.Run {
		return run(bashUse(), blocked("Bash", blockedText))
	}
	rep := Compare("upstream", "harness", build(), build(), DefaultTolerance())
	if !rep.Passed {
		t.Fatalf("identical runs must pass; checks=%+v", rep.Checks)
	}
	if c := findCheck(t, rep, "refusal-wording"); !c.Passed {
		t.Fatalf("refusal-wording: %s", c.Detail)
	}
	if c := findCheck(t, rep, "fingerprint"); !c.Passed {
		t.Fatalf("fingerprint should match: %s", c.Detail)
	}
}

// TestChangedWordingFailsHard is decision 2's core assertion: the refusal wording
// must be byte-identical; a reworded message is a behavioural change even though
// "the same thing was blocked".
func TestChangedWordingFailsHard(t *testing.T) {
	base := run(bashUse(), blocked("Bash", blockedText))
	cand := run(bashUse(), blocked("Bash", eval.GuardBlockMarker+"此调用被平台拦截。原因：禁止执行该工具。此操作被禁止。"))
	rep := Compare("upstream", "harness", base, cand, DefaultTolerance())
	if rep.Passed {
		t.Fatal("reworded refusal must fail")
	}
	c := findCheck(t, rep, "refusal-wording")
	if c.Passed || !c.Hard {
		t.Fatalf("refusal-wording must be a failed hard check: %+v", c)
	}
}

// TestRemovedBlockFailsRedLine — a call that was blocked and is now allowed is the
// one regression the gate must never wave through.
func TestRemovedBlockFailsRedLine(t *testing.T) {
	base := run(bashUse(), blocked("Bash", blockedText))
	cand := run(bashUse(), replay.Event{Kind: "tool_result", Tool: "Bash", Summary: "ok"})
	rep := Compare("upstream", "harness", base, cand, DefaultTolerance())
	if rep.Passed {
		t.Fatal("a block disappearing must fail")
	}
	c := findCheck(t, rep, "no-newly-allowed")
	if c.Passed || !c.Hard {
		t.Fatalf("no-newly-allowed must fail hard: %+v", c)
	}
	if !strings.Contains(c.Detail, "放行") {
		t.Fatalf("detail should explain the relaxation: %s", c.Detail)
	}
}

// TestNewBlockIsAlsoInconsistent: over-blocking is a behaviour change too (the
// operator's口径 is "only the most extreme destruction is blocked"). The gate
// reports it; a human decides whether it is intended.
func TestNewBlockIsAlsoInconsistent(t *testing.T) {
	base := run(bashUse())
	cand := run(bashUse(), blocked("Bash", blockedText))
	rep := Compare("upstream", "harness", base, cand, DefaultTolerance())
	if rep.Passed {
		t.Fatal("a new block changes behaviour and must not pass silently")
	}
	if c := findCheck(t, rep, "blocked-calls"); c.Passed {
		t.Fatalf("blocked-calls should differ: %s", c.Detail)
	}
}

func TestTruncatedCannotBeJudged(t *testing.T) {
	base := run(bashUse())
	cand := run(bashUse())
	cand.Truncated = true
	rep := Compare("upstream", "harness", base, cand, DefaultTolerance())
	if rep.Passed {
		t.Fatal("a truncated replay must never pass")
	}
	c := findCheck(t, rep, "complete-history")
	if c.Passed {
		t.Fatalf("complete-history must fail: %+v", c)
	}
}

func TestNilRunsCannotBeJudged(t *testing.T) {
	rep := Compare("upstream", "harness", nil, nil, DefaultTolerance())
	if rep.Passed {
		t.Fatal("nil runs must not pass")
	}
}

func TestDistributionTolerance(t *testing.T) {
	// Same shape, benign text change → soft checks pass, fingerprint differs.
	base := run(
		replay.Event{Kind: "tool_use", Tool: "Bash", Summary: "ls"},
		replay.Event{Kind: "tool_use", Tool: "Bash", Summary: "pwd"},
	)
	cand := run(
		replay.Event{Kind: "tool_use", Tool: "Bash", Summary: "ls -la"},
		replay.Event{Kind: "tool_use", Tool: "Bash", Summary: "pwd"},
	)
	rep := Compare("upstream", "harness", base, cand, DefaultTolerance())
	if !rep.Passed {
		t.Fatalf("text-only change must not fail hard checks: %+v", rep.Checks)
	}
	if c := findCheck(t, rep, "tool-distribution"); !c.Passed {
		t.Fatalf("tool distribution should be within tolerance: %s", c.Detail)
	}
}

func TestToolDistributionBreaksWhenShifted(t *testing.T) {
	base := run(
		replay.Event{Kind: "tool_use", Tool: "Bash", Summary: "a"},
		replay.Event{Kind: "tool_use", Tool: "Bash", Summary: "b"},
	)
	cand := run(
		replay.Event{Kind: "tool_use", Tool: "Bash", Summary: "a"},
		replay.Event{Kind: "tool_use", Tool: "WebFetch", Summary: "b"},
	)
	rep := Compare("upstream", "harness", base, cand, DefaultTolerance())
	if c := findCheck(t, rep, "tool-distribution"); c.Passed {
		t.Fatalf("a 50%% shift must exceed the 15%% tolerance: %s", c.Detail)
	}
	// Still soft: the report passes overall only if hard checks hold.
	if !rep.Passed {
		t.Fatalf("a soft deviation alone must not fail the gate: %+v", rep.Checks)
	}
}
