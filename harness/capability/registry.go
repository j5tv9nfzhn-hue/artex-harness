// Package capability is the harness's capability registry (S4).
//
// Design rule P1 from docs/02 §6.1: the harness must not implement scanning or
// exploitation logic. It must instead accept capabilities through a small number
// of supply surfaces and treat them uniformly. Upstream already had all five
// surfaces — they were just never described as one thing, so "what can this
// deployment actually do?" had no single answer.
//
// The five surfaces (docs/02 §6.2):
//
//	builtin      the tools the harness itself ships (graph, intent, finding …)
//	custom-*     user-defined command / script / http tools
//	cli-wrapper  tools.kind='shell' rows: declarations of what is installed in the
//	             Bash environment, not callable tools (upstream semantics)
//	mcp          remote/local MCP servers and the tools they expose
//	skill        SKILL.md bundles on disk, loaded on demand
//
// Classification is pure so it can be tested without a database; the collector
// that feeds it lives in the server package.
package capability

import (
	"fmt"
	"sort"
)

// Surface names. Stable strings — the console groups by them.
const (
	SurfaceBuiltin    = "builtin"
	SurfaceCustomCmd  = "custom-command"
	SurfaceCustomScr  = "custom-script"
	SurfaceCustomHTTP = "custom-http"
	SurfaceCLI        = "cli-wrapper"
	SurfaceMCP        = "mcp"
	SurfaceSkill      = "skill"
)

// Capability is one thing this deployment can do, normalised.
type Capability struct {
	Surface string `json:"surface"`
	Name    string `json:"name"`
	// Detail is a short human-readable note (description, tool count, file count).
	Detail string `json:"detail,omitempty"`
	// Agents lists which agent keys may use it (builtin/custom tools only).
	Agents  []string `json:"agents,omitempty"`
	Enabled bool     `json:"enabled"`
	// Exec reports that this capability can run code / make requests on the host,
	// i.e. it is a real execution surface rather than an information surface. It is
	// a factual property, not a risk score: the harness reports it, the operator
	// judges it (decision 5 — no new hard constraints).
	Exec bool `json:"exec"`
}

// ToolRow is the subset of a tools-table row the classifier needs.
type ToolRow struct {
	Key     string
	Kind    string
	Desc    string
	Enabled bool
	Agents  []string
}

// MCPRow is one configured MCP server.
type MCPRow struct {
	Name    string
	Enabled bool
	Tools   int
}

// SkillRow is one skill bundle found on disk.
type SkillRow struct {
	Name  string
	Files int
}

// Classify normalises the three inputs into one deterministic capability list.
//
// Output order is (surface, name) so the list is stable across runs — the console
// renders it directly and a diff must mean something changed.
func Classify(tools []ToolRow, mcps []MCPRow, skills []SkillRow) []Capability {
	out := make([]Capability, 0, len(tools)+len(mcps)+len(skills))

	for _, t := range tools {
		surface, exec := surfaceOfKind(t.Kind)
		if surface == SurfaceBuiltin && readOnlyBuiltin[t.Key] {
			// A builtin that only reads the graph/records is not an execution
			// surface. Listed explicitly (not inferred from the name) so the flag
			// stays a statement of fact.
			exec = false
		}
		if surface == "" {
			continue // unknown kind: do not invent a surface
		}
		c := Capability{
			Surface: surface,
			Name:    t.Key,
			Detail:  t.Desc,
			Agents:  append([]string(nil), t.Agents...),
			Enabled: t.Enabled,
			Exec:    exec,
		}
		sort.Strings(c.Agents)
		out = append(out, c)
	}
	for _, m := range mcps {
		out = append(out, Capability{
			Surface: SurfaceMCP,
			Name:    m.Name,
			Detail:  fmt.Sprintf("%d tools", m.Tools),
			Enabled: m.Enabled,
			Exec:    true, // an MCP tool runs outside this process
		})
	}
	for _, s := range skills {
		out = append(out, Capability{
			Surface: SurfaceSkill,
			Name:    s.Name,
			Detail:  fmt.Sprintf("%d files", s.Files),
			Enabled: true,
			// A skill is instructions + reference material; it cannot execute by
			// itself (it may *tell* the agent to run something, which is why the
			// execution surfaces are reported separately).
			Exec: false,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Surface != out[j].Surface {
			return out[i].Surface < out[j].Surface
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// surfaceOfKind maps a tools.kind onto a supply surface.
//
// Only the kinds upstream actually writes are recognised; anything else is
// skipped rather than guessed, so a future kind shows up as "missing from the
// registry" instead of being silently mislabelled.
func surfaceOfKind(kind string) (surface string, exec bool) {
	switch kind {
	case "builtin":
		return SurfaceBuiltin, true // graph tools, Bash, custom-tool creators …
	case "command":
		return SurfaceCustomCmd, true
	case "script":
		return SurfaceCustomScr, true
	case "http":
		return SurfaceCustomHTTP, true
	case "shell":
		// Upstream semantics: a shell row is an ENVIRONMENT DECLARATION (what is
		// installed), not a callable tool. It is not exec by itself — the Bash tool
		// is what executes.
		return SurfaceCLI, false
	default:
		return "", false
	}
}

// Summary counts capabilities per surface, for a compact header in the console.
func Summary(caps []Capability) map[string]int {
	out := map[string]int{}
	for _, c := range caps {
		out[c.Surface]++
	}
	return out
}

// readOnlyBuiltin names the builtin capabilities that cannot cause a side effect
// on a host: they read the exploration graph, the records or the traffic store.
//
// Kept as an explicit list rather than a naming heuristic: a wrong guess here
// would mislabel an execution surface as harmless, which is the one direction the
// registry must never be wrong in.
var readOnlyBuiltin = map[string]bool{
	"graph_overview": true, "list_findings": true, "list_facts": true,
	"list_goals": true, "node_detail": true, "list_assets": true,
	"list_untested_assets": true, "list_companies": true,
	"get_worker_output": true, "get_worker_trace": true,
	"list_worker_traces": true, "search_all_worker_traces": true,
	"expand_digest": true, "get_task_graph": true, "get_task_node_detail": true,
	"get_task_worker_trace": true, "list_task_findings": true,
	"list_task_worker_traces": true, "search_task_worker_traces": true,
	"list_tasks": true, "list_llm_profiles": true, "get_finding_traffic": true,
	"get_finding_retest_context": true, "traffic_search": true, "traffic_get": true,
	"traffic_blob": true,
}
