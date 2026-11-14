package server

// harness turn plumbing (S1).
//
// A Turn is one round of work inside a task: one planner round, or one work run
// for one intent. Opening a Turn allocates the next revision (persisted in
// harness_revisions and mirrored on tasks.revision), remembers it as the task's
// CURRENT turn, and emits a boundary envelope so a live client can start a new
// visual group immediately.
//
// Every activity emitted afterwards is stamped with that revision at the single
// emit point (emitActivity), which is why no call site needed to change.

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/harness/protocol"
)

// OpenTurn allocates and installs the task's current Turn.
//
// A nil / invalid task id, or a deleted task, yields a zero Turn (Rev 0) rather
// than an error: revision bookkeeping must never be able to fail a run.
func (e *Engine) OpenTurn(t *Task, kind, ref, summary string) protocol.Turn {
	if t == nil {
		return protocol.Turn{}
	}
	taskID, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil || taskID <= 0 {
		return protocol.Turn{}
	}
	var rev int64
	if e.m != nil && e.m.pg != nil {
		rev, err = e.m.pg.NextRevision(context.Background(), taskID, t.ExpID, kind, ref, summary)
		if err != nil {
			// Log and continue: a missing revision only degrades replay fidelity,
			// it must not stop the exploration.
			log.Printf("[harness] task %s 记录轮次失败 (%s %s): %v", t.ID, kind, ref, err)
		}
	}
	turn := protocol.Turn{Revision: rev, Kind: kind, Ref: ref, Summary: summary, At: time.Now()}
	e.turnMu.Lock()
	e.turn[t.ID] = turn
	e.itemSeq[t.ID] = 0
	e.turnMu.Unlock()
	// Boundary item so a live subscriber learns about the new turn even if the
	// turn produces no other activity (a planner round that dispatches nothing).
	e.BroadcastTurn(t, turn)
	return turn
}

// CurrentTurn returns the task's current Turn (zero Turn when none is open).
func (e *Engine) CurrentTurn(taskID string) protocol.Turn {
	e.turnMu.Lock()
	defer e.turnMu.Unlock()
	return e.turn[taskID]
}

// ClearTurn drops the task's turn state (task delete / shutdown).
func (e *Engine) ClearTurn(taskID string) {
	e.turnMu.Lock()
	delete(e.turn, taskID)
	delete(e.itemSeq, taskID)
	e.turnMu.Unlock()
}

// nextItemSeq numbers items inside the current turn. 1-based; 0 means "no turn".
func (e *Engine) nextItemSeq(taskID string) int {
	e.turnMu.Lock()
	defer e.turnMu.Unlock()
	if _, ok := e.turn[taskID]; !ok {
		return 0
	}
	e.itemSeq[taskID]++
	return e.itemSeq[taskID]
}

// BroadcastTurn publishes a bare turn boundary envelope (no item payload).
func (e *Engine) BroadcastTurn(t *Task, turn protocol.Turn) {
	if t == nil || e.bc == nil {
		return
	}
	e.bc.PublishEnvelope(t.ID, protocol.Envelope{
		Rev:    turn.Revision,
		Thread: protocol.Thread{TaskID: t.ID, ExplorationID: t.ExpID},
		Turn:   turn,
	})
}

// publishEnvelope stamps and publishes one activity as a protocol envelope.
// Called from emitActivity's single emit point.
func (e *Engine) publishEnvelope(t *Task, a db.Activity) {
	if t == nil || e.bc == nil {
		return
	}
	turn := e.CurrentTurn(t.ID)
	seq := e.nextItemSeq(t.ID)
	env := protocol.FromActivity(t.ID, t.ExpID, turn, seq, a)
	e.bc.PublishEnvelope(t.ID, env)
	// Link the row to its Turn so replay and the stream's catch-up can select by
	// revision. Best effort: losing a link only costs replay precision.
	if turn.Revision > 0 && a.ID > 0 && e.m != nil && e.m.pg != nil {
		if taskID, err := strconv.ParseInt(t.ID, 10, 64); err == nil && taskID > 0 {
			if err := e.m.pg.RecordTurnItem(taskID, turn.Revision, a.ID); err != nil {
				log.Printf("[harness] task %s 记录轮次↔事件映射失败 (rev=%d act=%d): %v",
					t.ID, turn.Revision, a.ID, err)
			}
		}
	}
}
