package server

// HARNESS runtime loop (docs/07-HARNESS-RUNTIME.md).
//
// One task = one conversation. There is no planner deciding directions and no
// worker pool claiming them: the agent runs to completion, reports, and waits for
// the next human message. Task status reflects that: "running" while a turn is in
// flight, "done" when a turn ends with nothing queued (unlike the legacy pipeline,
// which only ends when every goal is proved).
//
// Everything else is shared with the rest of the engine on purpose: pause/delete
// barriers, the deadline coordinator, revisions (OpenTurn), activity persistence
// and the SSE envelope stream. That is what keeps the harness path from becoming a
// second, subtly different engine.

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/harness/protocol"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/harness"
)

// harnessFirstInput is what the agent receives to start the task. Kept short: the
// system prompt already carries the goal and the situational block.
const harnessFirstInput = "开始执行这个任务。先看清已有态势，再决定第一步并动手。"

// SetHarnessResolver wires the per-task harness runtime (nil = harness mode
// unavailable, which makes harness tasks idle rather than silently falling back to
// the legacy pipeline).
func (e *Engine) SetHarnessResolver(fn func(t *Task) (*agent.HarnessAgent, bool)) {
	e.harnessResolve = fn
}

func (e *Engine) snapshotHarness(t *Task) (*agent.HarnessAgent, bool) {
	if e.harnessResolve == nil {
		return nil, false
	}
	return e.harnessResolve(t)
}

// harnessLoop runs the task's conversation until the task is paused/deleted or a
// human stops sending messages.
func (e *Engine) harnessLoop(ctx context.Context, t *Task) {
	// A queued message means this loop is being (re)started to handle it, not to
	// begin the task: sending the generic "start" input first would burn a turn
	// and leave the real message for later.
	first := !t.HasPendingHarnessMessage()
	for {
		var input string
		if first {
			input = harnessFirstInput
			first = false
		} else {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-t.HarnessInbox():
				if !ok {
					return
				}
				input = msg
			}
		}
		if e.IsPaused(t.ID) || e.IsDeleting(t.ID) {
			return
		}
		if isTerminalStatus(e.m.TaskStatus(t.ID)) && !first {
			// A done task re-enters only through Say(), which flips the status back
			// to running before pushing the message.
		}
		if !e.beginTaskOperation(t.ID) {
			return
		}
		h, ok := e.snapshotHarness(t)
		if !ok {
			e.decInflight(t.ID)
			// No runtime yet (no model configured): wait rather than spin.
			if sleepCtx(ctx, 2*time.Second) {
				return
			}
			first = true
			continue
		}
		if e.IsPaused(t.ID) || e.IsDeleting(t.ID) {
			e.decInflight(t.ID)
			return
		}

		// A turn is about to run: the task must not keep looking "created" while the
		// agent is actively working. Harness tasks are created before the runtime
		// exists, and only a human message used to flip the status to running, so a
		// freshly created task showed "created" for its entire first turn (and the
		// console's status badge lied about what was happening).
		if st := e.m.TaskStatus(t.ID); st != "running" {
			_, _ = e.m.SetTaskStatusGuarded(t.ID, "running")
		}

		e.OpenTurn(t, db.TurnHarness, "run", "会话执行")
		e.stampFirstRun(t)
		e.touch(t.ID)
		emit := func(r db.Activity) {
			if r.Worker == "" {
				r.Worker = "harness"
			}
			e.emitActivity(t, r)
		}

		// Task-scoped intercept context: an `ask` verdict must land in THIS task's
		// stream and resolve against THIS task's rules (same contract as a work run).
		// The optional policyEmit hook lets the server layer write typed
		// permission-decision rows into the same stream (Sprint 2).
		runCtx := intercept.WithTaskContext(ctx, t.ID, "harness", emit, e.policyEmit)
		e.BeginLLMCall(t.ID)
		text, reason, err := h.Run(runCtx, agent.HarnessRunRequest{
			TaskID:      taskIDInt(t.ID),
			Goal:        t.Goal,
			Description: t.Description,
			Input:       input,
			Round:       1,
			Assets:      e.m.assets,
			Store:       t.Store,
			Emit:        emit,
		})
		e.EndLLMCall(t.ID)
		e.decInflight(t.ID)

		switch {
		case ctx.Err() != nil:
			return // paused / deleted / shutdown: keep state as-is
		case err != nil && isAbnormalReason(reason):
			log.Printf("[harness] task %s 本轮结束: reason=%s err=%v", t.ID, reason, err)
		}
		if text == "" && err != nil {
			log.Printf("[harness] task %s 本轮无输出: %v", t.ID, err)
		}
		e.touch(t.ID)
		// Record WHY the run ended when it was cut short, so console/replay shows
		// "时间到 / 步数上限" instead of the stream just stopping.
		if isAbnormalReason(reason) {
			label := map[harness.TerminalReason]string{
				harness.ReasonTimeout:  "墙钟预算用尽（已收尾）",
				harness.ReasonMaxTurns: "回合预算用尽（已收尾）",
			}[reason]
			if label == "" {
				label = "运行结束：" + string(reason)
			}
			e.emitActivity(t, db.Activity{Worker: "harness", Kind: "lifecycle", Summary: label})
		}

		// A turn is finished. If nothing was queued while it ran, the conversation
		// is at rest: mark the task done so it stops looking "running" forever.
		//
		// Park, do NOT return. The loop goroutine IS this task's harness consumer:
		// returning left e.started set (only StopTask clears it) while the goroutine
		// was gone, so the next Say()/background-delegation wake called Resume→Notify
		// on a channel with nobody reading it — the message was accepted (revision
		// returned) and then never ran. That was reachable purely through the UI: rest
		// a HARNESS task, send another message, nothing happens. Parking on the inbox
		// keeps one goroutine per live idle task — exactly the "等待人工指示" state it
		// represents — and both the human inbox and <task-notification> wake it. Delete
		// and shutdown cancel rt.ctx, so the parked select unblocks and exits cleanly.
		if !t.HasPendingHarnessMessage() {
			if won, serr := e.m.SetTaskStatusGuarded(t.ID, "done"); serr != nil {
				log.Printf("[harness] task %s 落完成态失败: %v", t.ID, serr)
			} else if won {
				e.emitActivity(t, db.Activity{
					Worker: "harness", Kind: "lifecycle",
					Summary: "本轮结束，等待人工指示（发送消息即继续）",
				})
			}
		}
	}
}

// taskIDInt parses a task id that the manager keeps as a string. Named apart from
// the test helper mustTaskID(t, raw) in this package: two functions with one name
// made the whole package's test build fail.
func taskIDInt(id string) int64 {
	n, _ := strconv.ParseInt(id, 10, 64)
	return n
}

// isAbnormalReason reports whether a run ended for a reason a human should look at
// (as opposed to the model simply finishing its turn).
func isAbnormalReason(r harness.TerminalReason) bool {
	switch r {
	case harness.ReasonModelError, harness.ReasonAbortedStreaming, harness.ReasonAbortedTools,
		harness.ReasonPromptTooLong, harness.ReasonBlockingLimit, harness.ReasonMaxTurns,
		harness.ReasonTimeout, harness.ReasonImageError:
		return true
	}
	return false
}

// harnessWakePreamble frames a background delegation notification as the input of
// an automatically resumed turn. The notification block itself follows.
const harnessWakePreamble = "后台子代理已完成。请阅读下面的通知，必要时读取输出文件，然后继续任务或给出结论。\n\n"

// NotifyDelegation delivers a background delegation's completion to a resting
// task. It returns true only when the notification was handed to the parent.
//
// Why the return value matters: the agent drains the notification from the
// delegation registry ONLY on success. Before this contract the notifier was
// fire-and-forget, so a completion that could not be delivered (paused task,
// full inbox) was still dropped — and when the notifier was unwired entirely the
// parent model polled TaskList and slept waiting for a result it already had.
//
// Deliberately does nothing when the task is paused or being deleted: operator
// intent wins, and the completion stays queued for a later resume.
func (e *Engine) NotifyDelegation(t *Task, text string) bool {
	if t == nil || !t.HarnessMode {
		return false
	}
	if e.IsDeleting(t.ID) || e.IsPaused(t.ID) {
		return false
	}
	body := strings.TrimSpace(text)
	if body == "" {
		return false
	}
	if !t.PushHarnessMessage(harnessWakePreamble + body) {
		return false
	}
	// A resting task must look alive again: the loop picks the message up and the
	// console should show the turn it is about to run.
	if e.m != nil && e.m.TaskStatus(t.ID) != "running" {
		_, _ = e.m.SetTaskStatusGuarded(t.ID, "running")
	}
	e.emitActivity(t, db.Activity{
		Worker: "system", Kind: "lifecycle",
		Summary: "后台委派完成，自动唤醒会话",
	})
	return true
}

// Say delivers a human message to a harness task, reviving it if it had settled.
//
// This is the HARNESS equivalent of typing in a chat: it is a first-class API, not
// a prompt the model has to translate (docs/01 §3 ①/③).
func (e *Engine) Say(t *Task, message string) (int64, error) {
	if t == nil {
		return 0, fmt.Errorf("task not found")
	}
	if !t.HarnessMode {
		return 0, fmt.Errorf("该任务不是 HARNESS 运行模式（harness_mode=false），不支持会话消息")
	}
	if e.IsDeleting(t.ID) {
		return 0, fmt.Errorf("任务正在删除")
	}
	if e.IsPaused(t.ID) {
		return 0, fmt.Errorf("任务已暂停：恢复后再发送消息")
	}
	// Record the human turn first so the transcript and the revision stream show
	// the message even if the agent turn fails immediately.
	turn := e.OpenTurn(t, db.TurnSystem, "say", "人工消息")
	e.emitActivity(t, db.Activity{Worker: "human", Kind: "lifecycle", Summary: message})

	revived := false
	if isTerminalStatus(e.m.TaskStatus(t.ID)) {
		if won, err := e.m.SetTaskStatusGuarded(t.ID, "running"); err == nil && won {
			revived = true
		}
	}
	if !t.PushHarnessMessage(message) {
		return turn.Revision, fmt.Errorf("消息队列已满（上一轮还在处理），请稍后再发")
	}
	if revived || !e.Started(t.ID) {
		e.Resume(t)
	}
	return turn.Revision, nil
}

// protocolTurn builds the protocol view of the current turn (used by the console's
// activity feed when it needs a turn header without an item).
func protocolTurn(rev int64, kind, ref, summary string) protocol.Turn {
	return protocol.Turn{Revision: rev, Kind: kind, Ref: ref, Summary: summary}
}
