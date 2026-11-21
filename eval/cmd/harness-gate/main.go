// Command harness-gate compares two replayed runs and exits non-zero when the
// candidate's behaviour is no longer consistent with the baseline.
//
// This is the CI embodiment of the owner's acceptance criterion (ADR-0001
// decision 2): same prompts + same policy, and the pass condition is identical
// refusal wording and behaviour logic. Exit codes:
//
//	0  consistent (all hard checks pass, soft checks within tolerance)
//	1  inconsistent (a hard check failed — including the "newly allowed" red line)
//	2  cannot judge (bad input, truncated replay)
//
// Usage:
//
//	harness-gate -base base.json -cand cand.json
//	harness-gate -base-task 25 -cand-task 31 -dsn "$HARNESS_PG_DSN"
//
// Replay JSON is what `GET /api/harness/replay?task=N` returns, so a CI job can
// capture runs from both builds with curl and gate them without a custom harness.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Autumn-27/artex/harness/consistency"
	"github.com/Autumn-27/artex/harness/replay"

	"github.com/Autumn-27/artex/db"
)

func main() {
	var (
		baseFile = flag.String("base", "", "baseline replay JSON (from /api/harness/replay)")
		candFile = flag.String("cand", "", "candidate replay JSON")
		baseTask = flag.Int64("base-task", 0, "baseline task id (read live from PostgreSQL)")
		candTask = flag.Int64("cand-task", 0, "candidate task id")
		dsn      = flag.String("dsn", os.Getenv("HARNESS_PG_DSN"), "PostgreSQL DSN for -base-task/-cand-task")
		from     = flag.Int64("from", 0, "first revision (0 = from the beginning)")
		to       = flag.Int64("to", 0, "last revision (0 = to the end)")
		tol      = flag.Float64("tol", 0.15, "tolerance for the soft distribution checks")
		jsonOut  = flag.Bool("json", false, "print the report as JSON")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := assertTerminal(ctx, *dsn, *baseTask); err != nil {
		fmt.Fprintf(os.Stderr, "baseline: %v\n", err)
		os.Exit(2)
	}
	if err := assertTerminal(ctx, *dsn, *candTask); err != nil {
		fmt.Fprintf(os.Stderr, "candidate: %v\n", err)
		os.Exit(2)
	}
	base, baseLabel, err := load(ctx, *baseFile, *baseTask, *dsn, *from, *to)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baseline: %v\n", err)
		os.Exit(2)
	}
	cand, candLabel, err := load(ctx, *candFile, *candTask, *dsn, *from, *to)
	if err != nil {
		fmt.Fprintf(os.Stderr, "candidate: %v\n", err)
		os.Exit(2)
	}
	if base == nil || cand == nil {
		fmt.Fprintln(os.Stderr, "需要 -base/-cand 或 -base-task/-cand-task")
		os.Exit(2)
	}

	t := consistency.DefaultTolerance()
	t.ToolDistribution = *tol
	t.TerminalReasons = *tol
	rep := consistency.Compare(baseLabel, candLabel, base, cand, t)

	if *jsonOut {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
	} else {
		printReport(rep)
	}
	if !rep.Passed {
		os.Exit(1)
	}
}

func load(ctx context.Context, file string, taskID int64, dsn string, from, to int64) (*replay.Run, string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, "", err
		}
		var run replay.Run
		if err := json.Unmarshal(b, &run); err != nil {
			return nil, "", fmt.Errorf("decode %s: %w", file, err)
		}
		return &run, file, nil
	}
	if taskID <= 0 {
		return nil, "", nil
	}
	if strings.TrimSpace(dsn) == "" {
		return nil, "", fmt.Errorf("需要 DSN（-dsn 或 HARNESS_PG_DSN）才能按 task 读取回放")
	}
	d, err := db.Open(dsn)
	if err != nil {
		return nil, "", err
	}
	defer d.Close()
	run, err := replay.Load(ctx, d, taskID, from, to)
	if err != nil {
		return nil, "", err
	}
	return run, fmt.Sprintf("task#%d", taskID), nil
}

func printReport(rep consistency.Report) {
	verdict := "一致 ✅"
	if !rep.Passed {
		verdict = "不一致 ❌"
	}
	fmt.Printf("harness-gate · %s\n", verdict)
	fmt.Printf("  base = %s  (fingerprint %s)\n", rep.Base, short(rep.BaseFP))
	fmt.Printf("  cand = %s  (fingerprint %s)\n", rep.Cand, short(rep.CandFP))
	fmt.Printf("  blocked events in candidate = %d\n\n", rep.Blocked)
	for _, c := range rep.Checks {
		mark := "PASS"
		if !c.Passed {
			mark = "FAIL"
		}
		hard := "soft"
		if c.Hard {
			hard = "HARD"
		}
		fmt.Printf("  [%-4s][%-4s] %-20s %s\n", mark, hard, c.Name, c.Detail)
	}
	fmt.Println()
	if rep.Passed {
		fmt.Println("结论：候选行为与基线一致（拒绝话术与行为逻辑）。")
	} else {
		fmt.Println("结论：候选行为与基线不一致，必须修复或由业主书面确认。")
	}
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// assertTerminal refuses to judge a task that is still running.
//
// Why this matters: a run in flight has not recorded its later events, so comparing it
// with a finished run reports differences that are really "not done yet" — exactly the
// false wording mismatch the first real gate run produced. Exit code 2 = cannot judge.
func assertTerminal(ctx context.Context, dsn string, taskID int64) error {
	if taskID <= 0 {
		return nil // file-based replay: caller owns completeness
	}
	if strings.TrimSpace(dsn) == "" {
		return nil
	}
	d, err := db.Open(dsn)
	if err != nil {
		return err
	}
	defer d.Close()
	t, err := d.GetTask(taskID)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("task %d not found", taskID)
	}
	if t.Status != "done" && t.Status != "failed" && t.Status != "timeout" {
		return fmt.Errorf("task %d 状态=%s（未终态）：请等它结束再比对，否则差异来自\"还没跑完\"", taskID, t.Status)
	}
	return nil
}
