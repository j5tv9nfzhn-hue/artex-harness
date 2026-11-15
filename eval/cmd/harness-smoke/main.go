// Command harness-smoke proves the HARNESS runtime can complete a turn WITHOUT a
// model endpoint and WITHOUT network: it drives the real agent loop with a
// scripted provider, so CI (and a fresh bare-metal box) can answer "does the kernel
// actually work?" before any credentials exist.
//
// What it exercises: task creation → harness runtime → tool loop → graph write →
// final answer. What it does not exercise: real model behaviour (that is
// `harness-gate`'s job, on recorded runs).
//
// Usage:
//
//	ARTEX_PG_DSN=... go run ./eval/cmd/harness-smoke
//	go run ./eval/cmd/harness-smoke -dsn "$ARTEX_PG_DSN" -keep
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"iter"
	"os"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("ARTEX_PG_DSN"), "PostgreSQL DSN")
	keep := flag.Bool("keep", false, "keep the smoke task in the database")
	verbose := flag.Bool("v", false, "print every activity")
	flag.Parse()

	if strings.TrimSpace(*dsn) == "" {
		fmt.Fprintln(os.Stderr, "需要 -dsn 或 ARTEX_PG_DSN")
		os.Exit(2)
	}
	if err := run(*dsn, *keep, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "SMOKE FAILED:", err)
		os.Exit(1)
	}
	fmt.Println("SMOKE OK")
}

func run(dsn string, keep, verbose bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d, err := db.Open(dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer d.Close()

	expID, err := d.CreateExploration("harness smoke", "离线冒烟：只验证运行时可跑")
	if err != nil {
		return fmt.Errorf("create exploration: %w", err)
	}
	on := true
	task, err := d.CreateTaskWithOptions("harness smoke", "用 record_fact 写 1 条事实，然后结束。", db.TaskCreateOptions{
		Name:        "harness-smoke",
		HarnessMode: &on,
	})
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	if !task.HarnessMode {
		return fmt.Errorf("task was not created in harness mode")
	}
	fmt.Printf("task #%d (exploration %d) · harness_mode=%v\n", task.ID, expID, task.HarnessMode)

	workDir, err := os.MkdirTemp("", "harness-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	hx := agent.NewHarnessAgent(&scriptedProvider{}, "scripted", workDir, nil, 64000, 8)
	hx.SetNonStreaming(func() bool { return true }) // use Complete, so the fake returns whole messages
	hx.SetMaxTokens(func() int { return 512 })

	var emitted []db.Activity
	store := d.Exploration(task.ExplorationID)
	text, reason, runErr := hx.Run(ctx, agent.HarnessRunRequest{
		TaskID:      task.ID,
		Goal:        task.Goal,
		Description: task.Description,
		Input:       "开始。",
		Assets:      d.Assets(),
		Store:       store,
		Emit: func(a db.Activity) {
			emitted = append(emitted, a)
			if verbose {
				fmt.Printf("  · %-10s %-10s %s\n", a.Kind, a.Tool, truncate(a.Summary, 90))
			}
		},
	})

	if !keep {
		defer func() {
			_, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID)
			_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
		}()
	}

	if runErr != nil {
		return fmt.Errorf("runtime returned error (reason=%s): %w", reason, runErr)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("runtime produced no final text (reason=%s)", reason)
	}

	// The tool loop must have run: a tool_use and its result are both present.
	var toolUses, toolResults int
	for _, a := range emitted {
		switch a.Kind {
		case "tool_use":
			toolUses++
		case "tool_result":
			toolResults++
		}
	}
	if toolUses == 0 {
		return fmt.Errorf("no tool_use activity: the agent loop never called a tool (%d activities)", len(emitted))
	}
	if toolResults < toolUses {
		return fmt.Errorf("unpaired tool calls: %d uses vs %d results", toolUses, toolResults)
	}

	// And the graph write must be visible in the store (tool effects are real).
	facts, err := store.ListByKind(db.KindFact, 50)
	if err != nil {
		return fmt.Errorf("read facts: %w", err)
	}
	confirmed := 0
	for _, n := range facts {
		if n.State == "confirmed" {
			confirmed++
		}
	}
	if confirmed == 0 {
		return fmt.Errorf("record_fact produced no confirmed fact (runtime/tool wiring broken)")
	}

	fmt.Printf("activities=%d tool_use=%d tool_result=%d facts=%d final=%q\n",
		len(emitted), toolUses, toolResults, confirmed, truncate(text, 120))
	return nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// scriptedProvider returns a fixed three-step script:
//  1. record_fact (a real graph write)
//  2. Bash echo (a real tool execution)
//  3. final text
//
// Deliberately no network, no model, no key.
type scriptedProvider struct{ step int }

func (p *scriptedProvider) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{}, fmt.Errorf("scripted provider is non-streaming only (set NonStreaming)"))
	}
}

func (p *scriptedProvider) Complete(_ context.Context, _ llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	defer func() { p.step++ }()
	asst := llm.Message{Role: llm.RoleAssistant}
	switch p.step {
	case 0:
		asst.Content = []llm.ContentBlock{{
			Type:  llm.BlockToolUse,
			ID:    "call_fact",
			Name:  "record_fact",
			Input: json.RawMessage(`{"facts":[{"summary":"harness 离线冒烟：运行时可完成一轮工具调用","confidence":"confirmed"}]}`),
		}}
		return asst, "tool_use", llm.Usage{InputTokens: 100, OutputTokens: 20}, nil
	case 1:
		asst.Content = []llm.ContentBlock{{
			Type:  llm.BlockToolUse,
			ID:    "call_bash",
			Name:  "Bash",
			Input: json.RawMessage(`{"command":"echo harness-smoke"}`),
		}}
		return asst, "tool_use", llm.Usage{InputTokens: 120, OutputTokens: 15}, nil
	default:
		asst.Content = []llm.ContentBlock{llm.TextBlock("离线冒烟完成：工具链与图谱写入都正常。")}
		return asst, "stop", llm.Usage{InputTokens: 140, OutputTokens: 12}, nil
	}
}

var _ = harness.ReasonCompleted
