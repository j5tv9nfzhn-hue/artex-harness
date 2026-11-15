package agent

import (
	"reflect"
	"testing"

	"github.com/Autumn-27/norma/agentcore"
)

// TestConversationalRunnersShareModelDefaults is the S4 parity assertion.
//
// The evaluation (docs/01 §3 ②) found the two conversational entry points could
// silently diverge: the same ~30 lines were maintained twice, so a proxy/search/
// limit fix could land on one path only. Both runners now derive those fields from
// ModelRunConfig; this test fails the moment they stop agreeing.
func TestConversationalRunnersShareModelDefaults(t *testing.T) {
	ws := WebSearchOpts{
		Enabled: true, Backend: "ddgs",
		BraveKey: "brave-k", TavilyKey: "tavily-k",
		DeepSeekBaseURL: "http://ds", DeepSeekAPIKey: "ds-k", DeepSeekModel: "ds-m",
		Proxy: "socks5://127.0.0.1:1080",
	}
	caps := func() int { return 4096 }
	streamOff := func() bool { return true }

	m := &MainAgent{
		proxyAddr: "127.0.0.1:8788", proxyCACert: "/data/_ca.pem",
		webSearch: ws, window: 128000,
		maxTokensFn: caps, nonStreamingFn: streamOff,
	}
	c := &ChatAgent{
		proxyAddr: "127.0.0.1:8788", proxyCACert: "/data/_ca.pem",
		webSearch: ws, window: 128000,
		maxTokensFn: caps, nonStreamingFn: streamOff,
	}

	var mo, co agentcore.Options
	m.modelRunConfig().ApplyModelDefaults(&mo)
	c.modelRunConfig(true).ApplyModelDefaults(&co)

	if !reflect.DeepEqual(mo.WebSearchBackend, co.WebSearchBackend) ||
		mo.EnableWebSearch != co.EnableWebSearch ||
		mo.BraveSearchAPIKey != co.BraveSearchAPIKey ||
		mo.TavilySearchAPIKey != co.TavilySearchAPIKey ||
		mo.DeepSeekSearchBaseURL != co.DeepSeekSearchBaseURL ||
		mo.DeepSeekSearchAPIKey != co.DeepSeekSearchAPIKey ||
		mo.DeepSeekSearchModel != co.DeepSeekSearchModel ||
		mo.WebSearchProxy != co.WebSearchProxy {
		t.Fatalf("web-search options diverge:\nmain  = %+v\nchat  = %+v", mo, co)
	}
	if mo.EnableWebFetch != co.EnableWebFetch ||
		mo.WebFetchProxy != co.WebFetchProxy ||
		mo.WebFetchCACert != co.WebFetchCACert {
		t.Fatalf("web-fetch options diverge:\nmain=%+v\nchat=%+v", mo, co)
	}
	if !reflect.DeepEqual(mo.BashEnv, co.BashEnv) {
		t.Fatalf("BashEnv diverges:\nmain=%v\nchat=%v", mo.BashEnv, co.BashEnv)
	}
	if mo.NonStreaming != co.NonStreaming || mo.MaxTokens != co.MaxTokens {
		t.Fatalf("streaming/cap diverge: main(%v,%d) chat(%v,%d)",
			mo.NonStreaming, mo.MaxTokens, co.NonStreaming, co.MaxTokens)
	}
	if (mo.Compaction == nil) != (co.Compaction == nil) {
		t.Fatalf("compaction divergence: main=%v chat=%v", mo.Compaction, co.Compaction)
	}
	if mo.Compaction == nil {
		t.Fatal("compaction must be configured (a nil window would disable context management)")
	}
}

// TestChatAgentPerRequestWebSearchGate: the only intentional difference between
// the runners is that the chat page may switch search OFF for one request. It must
// switch off, not merely leave the backend selected.
func TestChatAgentPerRequestWebSearchGate(t *testing.T) {
	c := &ChatAgent{webSearch: WebSearchOpts{Enabled: true, Backend: "tavily"}}

	var on, off agentcore.Options
	c.modelRunConfig(true).ApplyModelDefaults(&on)
	c.modelRunConfig(false).ApplyModelDefaults(&off)

	if !on.EnableWebSearch {
		t.Fatal("search must stay on when the request allows it")
	}
	if off.EnableWebSearch {
		t.Fatal("per-request off must disable search")
	}
}

// TestApplyModelDefaultsIsExplicit guards against leaving a stale value in a
// reused Options (the SDK option struct is passed by pointer).
func TestApplyModelDefaultsIsExplicit(t *testing.T) {
	opts := agentcore.Options{
		EnableWebSearch:   true,
		WebSearchBackend:  "tavily",
		NonStreaming:      true,
		MaxTokens:         999,
		EscalateMaxTokens: true,
	}
	ModelRunConfig{}.ApplyModelDefaults(&opts)
	if opts.EnableWebSearch {
		t.Fatal("zero config must explicitly turn search off, not inherit the previous value")
	}
	if opts.NonStreaming {
		t.Fatal("zero config must explicitly select streaming")
	}
	if opts.MaxTokens != 0 {
		t.Fatal("zero config must clear the cap")
	}
	if opts.EscalateMaxTokens {
		t.Fatal("zero config must explicitly disable max-tokens escalation")
	}
	if opts.WebSearchBackend != "" {
		t.Fatal("zero config must clear the backend label")
	}
}

// TestWithTodosDoesNotClobber: a caller that already installed a shared todo store
// (planner keeps one across wakes) must keep it.
func TestWithTodosDoesNotClobber(t *testing.T) {
	existing := agentcore.Options{Todos: nil}
	ModelRunConfig{}.WithTodos(&existing)
	if existing.Todos == nil {
		t.Fatal("WithTodos must install a store when none is present")
	}
	keep := existing.Todos
	ModelRunConfig{}.WithTodos(&existing)
	if existing.Todos != keep {
		t.Fatal("WithTodos must not replace an existing store")
	}
}
