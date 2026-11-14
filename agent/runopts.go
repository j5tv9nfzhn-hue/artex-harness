// Shared model/runtime defaults for conversational runners (harness S4).
//
// Upstream had three conversational runners (MainAgent on the task page,
// ChatAgent on the chat page, SideQuestionService for side questions) and each
// duplicated the same ~30 lines: recording-proxy wiring, optional web search,
// compaction window, streaming mode, output cap, Bash env. The evaluation
// (docs/01 §3 ②) found the real cost: any change had to be made in two or three
// places, and the two entry points could silently drift apart — which is exactly
// what "同一句话在不同入口得到不同权限与行为" looked like from the outside.
//
// This file is the single place those fields are decided. It is deliberately
// about the MODEL/RUNTIME plumbing only: what differs on purpose between the
// runners (system prompt, tool surface, working directory, settlement policy,
// todo store) stays with each runner and is not flattened here.
package agent

import (
	"github.com/Autumn-27/norma/agentcore"

	actool "github.com/Autumn-27/norma/tool"
)

// ModelRunConfig is the shared subset of conversational runner settings.
type ModelRunConfig struct {
	// ProxyAddr / ProxyCACert are the recording MITM proxy. Web fetch and Bash
	// subprocesses both go through it so every request is recorded, and the CA is
	// trusted rather than TLS verification being disabled.
	ProxyAddr   string
	ProxyCACert string
	// WebSearch is the optional search backend. Each runner decides whether to
	// gate it (ChatAgent lets the caller turn it off per request), but the mapping
	// onto Options is identical.
	WebSearch WebSearchOpts
	// ContextWindow is the compaction window in tokens (see CompactionWindow).
	ContextWindow int
	// NonStreaming selects Provider.Complete instead of the SSE path.
	NonStreaming bool
	// MaxTokens caps the reply; 0 = no cap sent (endpoint default applies).
	MaxTokens int
}

// ApplyModelDefaults writes the shared model/runtime fields onto opts.
//
// Order of application is fixed and every field is assigned explicitly (not left
// to the zero value) so a caller that forgets one cannot inherit a stale default
// from a reused Options value.
func (c ModelRunConfig) ApplyModelDefaults(opts *agentcore.Options) {
	// WebFetch goes through the recording proxy and trusts its CA, so HTTPS that
	// the proxy re-signs still validates normally (rather than skipping checks).
	opts.EnableWebFetch = true
	opts.WebFetchProxy = c.ProxyAddr
	opts.WebFetchCACert = c.ProxyCACert

	// Optional web search. Empty/disabled leaves the SDK feature off entirely.
	opts.EnableWebSearch = c.WebSearch.Enabled
	opts.WebSearchBackend = c.WebSearch.Backend
	opts.BraveSearchAPIKey = c.WebSearch.BraveKey
	opts.TavilySearchAPIKey = c.WebSearch.TavilyKey
	opts.DeepSeekSearchBaseURL = c.WebSearch.DeepSeekBaseURL
	opts.DeepSeekSearchAPIKey = c.WebSearch.DeepSeekAPIKey
	opts.DeepSeekSearchModel = c.WebSearch.DeepSeekModel
	opts.WebSearchProxy = c.WebSearch.Proxy

	// Bash children default to the proxy + the CA bundle, so shell-driven HTTP is
	// recorded too without the model having to pass -x / -k.
	opts.BashEnv = proxyEnv(c.ProxyAddr, c.ProxyCACert)

	opts.Compaction = compactionConfig(c.ContextWindow)
	opts.NonStreaming = c.NonStreaming
	opts.MaxTokens = c.MaxTokens
}

// WithTodos attaches a session-scoped todo store. Both conversational runners use
// a fresh store per run (todos are planning scratch space, not persisted state).
func (c ModelRunConfig) WithTodos(opts *agentcore.Options) {
	if opts.Todos == nil {
		opts.Todos = actool.NewTodoStore()
	}
}
