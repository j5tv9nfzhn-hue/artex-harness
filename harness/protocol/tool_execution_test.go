package protocol

import "testing"

func TestClassifyToolExecution(t *testing.T) {
	cases := []struct {
		name   string
		safe   bool
		ro     bool
		want   ToolExecutionClass
		wantRO bool
	}{
		{"read-only safe", true, true, ToolParallel, true},
		{"safe but mutating", true, false, ToolParallel, false},
		{"exclusive read", false, true, ToolExclusive, true},
		{"exclusive write", false, false, ToolExclusive, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ro := ClassifyToolExecution(c.safe, c.ro)
			if got != c.want {
				t.Fatalf("class = %q, want %q", got, c.want)
			}
			if ro != c.wantRO {
				t.Fatalf("readOnly = %v, want %v", ro, c.wantRO)
			}
		})
	}
}

// A tool with no concurrency verdict must be exclusive, and a read-only tool is
// not automatically parallel: the two facts are independent.
func TestClassifyToolExecutionFailClosed(t *testing.T) {
	got, _ := ClassifyToolExecution(false, true)
	if got != ToolExclusive {
		t.Fatalf("no verdict must be exclusive, got %q", got)
	}
}
