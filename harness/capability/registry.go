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
	// ConcurrencySafe reports that this capability may run alongside other
	// concurrency-safe capabilities. It mirrors norma's CoreTool.IsConcurrencySafe:
	// the streaming executor runs safe tools in parallel and gives non-safe tools
	// exclusive access. A factual property, like Exec — no policy is attached.
	ConcurrencySafe bool `json:"concurrency_safe"`
	// Deferred reports that the capability's schema is withheld until the model
	// unlocks it through the deferred-tool access tools.
	Deferred bool `json:"deferred"`
}

// ToolRow is the subset of a tools-table row the classifier needs.
type ToolRow struct {
	Key     string
	Kind    string
	Desc    string
	Enabled bool
	Agents  []string
	// Deferred mirrors tools.deferred. ConcurrencySafe is derived from the
	// runtime tool's IsConcurrencySafe, not the row (see ClassifyRuntime).
	Deferred bool
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
			// A read-only builtin is explicitly safe to run concurrently; anything
			// else is conservatively treated as exclusive. ClassifyRuntime overrides
			// this with the real runtime tool's verdict when available.
			ConcurrencySafe: surface == SurfaceBuiltin && readOnlyBuiltin[t.Key],
			Deferred:        t.Deferred,
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

// ClassifyRuntime overlays the real runtime tool flags onto a classified list.
//
// Why a separate step: Classify is pure (DB/disk inputs only) and is what the
// tests pin. The concurrency flag is a property of the Go handler, not of a
// tools-table row, so it must come from the assembled CoreTool. Passing a map of
// name → verdict keeps this package free of a norma import.
//
// A capability absent from the map keeps Classify's conservative default; a
// capability whose runtime tool is explicitly safe is upgraded to parallelisable.
// MCP/skill entries have no CoreTool in the assembled set and are left alone.
func ClassifyRuntime(caps []Capability, safe map[string]bool) []Capability {
	out := make([]Capability, len(caps))
	copy(out, caps)
	for i := range out {
		if verdict, ok := safe[out[i].Name]; ok {
			out[i].ConcurrencySafe = verdict
		}
	}
	return out
}

// Summary counts capabilities per surface, for a compact header in the console.
func Summary(caps []Capability) map[string]int {
	out := map[string]int{}
	for _, c := range caps {
		out[c.Surface]++
	}
	return out
}

// ConcurrencySummary counts how many capabilities are parallelisable vs exclusive.
func ConcurrencySummary(caps []Capability) (parallel, exclusive int) {
	for _, c := range caps {
		if c.ConcurrencySafe {
			parallel++
		} else {
			exclusive++
		}
	}
	return parallel, exclusive
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
