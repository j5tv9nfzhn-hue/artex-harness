package capability

import "testing"

func TestClassifyMapsSurfaces(t *testing.T) {
	caps := Classify(
		[]ToolRow{
			{Key: "graph_overview", Kind: "builtin", Enabled: true, Agents: []string{"planner", "mainagent"}},
			{Key: "goal_met", Kind: "builtin", Enabled: false, Agents: nil},
			{Key: "my_scanner", Kind: "command", Enabled: true, Agents: []string{"worker"}},
			{Key: "recon.py", Kind: "script", Enabled: true, Agents: []string{"worker"}},
			{Key: "api_call", Kind: "http", Enabled: true, Agents: []string{"auto"}},
			{Key: "nmap", Kind: "shell", Enabled: true, Agents: []string{"worker"}},
			{Key: "mystery", Kind: "brand-new-kind", Enabled: true},
		},
		[]MCPRow{{Name: "playwright", Enabled: true, Tools: 12}},
		[]SkillRow{{Name: "api-recon", Files: 4}},
	)

	byName := map[string]Capability{}
	for _, c := range caps {
		byName[c.Name] = c
	}
	if _, ok := byName["mystery"]; ok {
		t.Fatal("an unknown tools.kind must be skipped, not guessed into a surface")
	}
	cases := []struct {
		name    string
		surface string
		exec    bool
	}{
		// A read-only builtin is NOT an execution surface.
		{"graph_overview", SurfaceBuiltin, false},
		{"my_scanner", SurfaceCustomCmd, true},
		{"recon.py", SurfaceCustomScr, true},
		{"api_call", SurfaceCustomHTTP, true},
		// A shell row declares what is installed; executing it is the Bash tool's
		// job, so the declaration itself is not an execution surface.
		{"nmap", SurfaceCLI, false},
		{"playwright", SurfaceMCP, true},
		{"api-recon", SurfaceSkill, false},
		// A mutating builtin is.
		{"goal_met", SurfaceBuiltin, true},
		// A mutating builtin is.
		{"goal_met", SurfaceBuiltin, true},
	}
	for _, c := range cases {
		got, ok := byName[c.name]
		if !ok {
			t.Fatalf("%q missing from the registry", c.name)
		}
		if got.Surface != c.surface {
			t.Errorf("%q surface = %q, want %q", c.name, got.Surface, c.surface)
		}
		if got.Exec != c.exec {
			t.Errorf("%q exec = %v, want %v", c.name, got.Exec, c.exec)
		}
	}
	// Disabled rows are still listed (so the operator can see they exist and are off).
	if gm, ok := byName["goal_met"]; !ok || gm.Enabled {
		t.Error("a disabled capability must still be listed, with enabled=false")
	}
	// Agent lists are sorted for stable output.
	if got := byName["graph_overview"].Agents; len(got) != 2 || got[0] != "mainagent" {
		t.Errorf("agents not sorted: %v", got)
	}
}

func TestClassifyIsDeterministic(t *testing.T) {
	tools := []ToolRow{
		{Key: "b", Kind: "builtin"},
		{Key: "a", Kind: "builtin"},
	}
	first := Classify(tools, nil, nil)
	second := Classify([]ToolRow{tools[1], tools[0]}, nil, nil)
	if len(first) != len(second) {
		t.Fatalf("length differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Name != second[i].Name {
			t.Fatalf("order depends on input order: %v vs %v", names(first), names(second))
		}
	}
	if first[0].Name != "a" || first[1].Name != "b" {
		t.Fatalf("not sorted by name: %v", names(first))
	}
}

func names(cs []Capability) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func TestSummaryCounts(t *testing.T) {
	caps := Classify(
		[]ToolRow{{Key: "a", Kind: "builtin"}, {Key: "b", Kind: "builtin"}, {Key: "c", Kind: "command"}},
		[]MCPRow{{Name: "m"}},
		nil,
	)
	sum := Summary(caps)
	if sum[SurfaceBuiltin] != 2 || sum[SurfaceCustomCmd] != 1 || sum[SurfaceMCP] != 1 {
		t.Fatalf("summary = %v", sum)
	}
}
