package agent

import (
	"context"
	"testing"
	"time"
)

// TestSubagentContextSurvivesParentCancel is the regression test for the bug fixed in
// docs/08 §1: a delegation running when the parent's turn budget is exhausted used to
// die with "subagent failed: context canceled". The child context must no longer
// inherit the parent's cancellation.
func TestSubagentContextSurvivesParentCancel(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	sub, subCancel := subagentContext(parent)
	defer subCancel()

	if sub.Err() != nil {
		t.Fatalf("fresh child must not be canceled: %v", sub.Err())
	}
	parentCancel() // simulate the parent's turn budget aborting in-flight tools

	// Give the cancellation a chance to propagate (it must not).
	select {
	case <-sub.Done():
		t.Fatalf("child context was canceled by the parent: %v", sub.Err())
	case <-time.After(50 * time.Millisecond):
	}
	if sub.Err() != nil {
		t.Fatalf("parent cancellation leaked into the child: %v", sub.Err())
	}

	// And it is still usable: a value survives, and only our own cancel ends it.
	if err := sub.Err(); err != nil {
		t.Fatalf("unexpected early termination: %v", err)
	}
	subCancel()
	if sub.Err() == nil {
		t.Fatal("child must stop when its own CancelFunc is called")
	}
}

// TestSubagentContextHasOwnDeadline: detaching from cancellation must NOT mean running
// unbounded — the child carries its own timeout.
func TestSubagentContextHasOwnDeadline(t *testing.T) {
	sub, cancel := subagentContext(context.Background())
	defer cancel()
	dl, ok := sub.Deadline()
	if !ok {
		t.Fatal("subagent context must carry a deadline")
	}
	if remaining := time.Until(dl); remaining <= 0 || remaining > subagentTimeout()+time.Second {
		t.Fatalf("deadline out of range: %s (budget %s)", remaining, subagentTimeout())
	}
}

func TestSubagentTimeoutResolution(t *testing.T) {
	cases := map[string]time.Duration{
		"":     defaultSubagentTimeout,
		"90s":  90 * time.Second,
		"10m":  10 * time.Minute,
		"120":  120 * time.Second,
		"nope": defaultSubagentTimeout,
		"-5":   defaultSubagentTimeout,
		"0":    defaultSubagentTimeout,
	}
	for raw, want := range cases {
		t.Setenv(subagentTimeoutEnv, raw)
		if got := subagentTimeout(); got != want {
			t.Errorf("subagentTimeout(%q) = %s, want %s", raw, got, want)
		}
	}
	// Unset behaves like the default.
	t.Setenv(subagentTimeoutEnv, "")
	if got := subagentTimeout(); got != defaultSubagentTimeout {
		t.Errorf("unset: got %s, want %s", got, defaultSubagentTimeout)
	}
}

// TestShouldStartSubagent: an already-canceled turn must not START new delegated work
// (the detached context is for runs already in flight, not for new ones).
func TestShouldStartSubagent(t *testing.T) {
	if !shouldStartSubagent(context.Background()) {
		t.Fatal("a live context must be allowed to start")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if shouldStartSubagent(canceled) {
		t.Fatal("a canceled turn must not start new subagents")
	}
	// Also for deadline-expired parents.
	expired, c2 := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer c2()
	if shouldStartSubagent(expired) {
		t.Fatal("an expired turn must not start new subagents")
	}
}

// TestSubagentTimeoutBoundsRun documents the intended contract: a detached child still
// terminates on its own budget, so a paused parent can never leave work running forever.
func TestSubagentTimeoutBoundsRun(t *testing.T) {
	t.Setenv(subagentTimeoutEnv, "30s")
	sub, cancel := subagentContext(context.Background())
	defer cancel()
	dl, ok := sub.Deadline()
	if !ok {
		t.Fatal("missing deadline")
	}
	if d := time.Until(dl); d > 31*time.Second || d <= 0 {
		t.Fatalf("env override not honoured: %s", d)
	}
}
