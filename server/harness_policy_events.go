package server

// Sprint 2: make the permission POLICY observable in the task's activity stream.
//
// The guard layer emits a "permission_decision" activity for every tool call it
// sees, carrying no verdict. This emitter attaches the active policy's evaluation
// and writes the row through the engine's single emit point (so it is replayable
// and appears in the trajectory view). The hooks remain the only veto: nothing
// here can allow or deny a call.

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/harness/protocol"
	"github.com/Autumn-27/artex/intercept"
)

func (s *Server) wirePolicyEvents() {
	if s.engine == nil {
		return
	}
	s.engine.SetPolicyEmitter(func(ctx context.Context, a db.Activity) {
		taskID := intercept.TaskIDFromContext(ctx)
		t, ok := s.m.Task(taskID)
		if !ok || t == nil {
			return
		}
		p := s.harnessPermission()
		cfg := p.Snapshot()
		d := p.Evaluate(a.Tool, false) // read-only is not known here; conservative
		payload, _ := json.Marshal(protocol.PermissionDecisionItem{
			Tool: a.Tool, Behavior: string(d.Behavior), Reason: string(d.Reason),
			Rule: d.Rule, Mode: string(d.Mode),
		})
		a.Kind = "permission_decision"
		a.Worker = "harness"
		a.Summary = "权限策略：" + a.Tool + " → " + string(d.Behavior) + "（" + string(d.Reason) + "）"
		a.Detail = "模式 " + string(cfg.Mode) + " · 规则 " + strconv.Itoa(len(cfg.Rules)) + " 条\n" + d.Message
		a.Metadata = payload
		s.engine.emitActivity(t, a)
	})
}
