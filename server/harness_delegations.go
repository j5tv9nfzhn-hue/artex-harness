package server

// Delegation API (Sprint 3).
//
//	GET  /api/harness/delegations/{id}          — every delegation for one task
//	    ?state=running|done|failed|killed       — optional filter
//	GET  /api/harness/delegations/{id}/{did}    — one delegation's result body
//	POST /api/harness/delegations/{id}/{did}/stop — stop a running background run
//
// The authoritative source is delegation.Global (one in-process registry per
// task, shared by the runtime that spawns the run and this handler that reports
// it). activity pairing is the fallback for delegations started before this
// feature existed, so historical tasks still show their Agent tool calls.

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Autumn-27/artex/harness/delegation"
)

func (s *Server) delegationRegistry(taskID string) *delegation.Registry {
	id, _ := strconv.ParseInt(taskID, 10, 64)
	return delegation.Global.For(taskID, s.delegationOutputDir(id))
}

func (s *Server) delegationOutputDir(taskID int64) string {
	return filepath.Join(s.m.dir, "delegations", strconv.FormatInt(taskID, 10))
}

// settingDelegationRetentionHours is how long a finished background delegation's
// isolated scratch directory is kept. 0 disables the sweep. The result body and
// the registry record are kept regardless — only the directory goes.
const settingDelegationRetentionHours = "harness_delegation_retention_hours"

const defaultDelegationRetention = 72 * time.Hour

// delegationRetention reads the retention window. Missing/zero means "sweep
// disabled" only when the operator explicitly set 0; an absent setting uses the
// 72h default so a stock install does not accumulate directories forever.
func (s *Server) delegationRetention() time.Duration {
	if s.m == nil || s.m.pg == nil {
		return defaultDelegationRetention
	}
	raw := s.m.pg.GetInt(settingDelegationRetentionHours, int(defaultDelegationRetention/time.Hour))
	if raw <= 0 {
		return 0
	}
	return time.Duration(raw) * time.Hour
}

// startDelegationGC runs the delegation retention sweep (Sprint 3 listed
// "isolated directories have no TTL" as a known gap). Hourly is enough: the
// window is measured in days.
func (s *Server) startDelegationGC(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			win := s.delegationRetention()
			if win <= 0 {
				continue
			}
			if n := delegation.Global.SweepAll(time.Now(), win); n > 0 {
				log.Printf("[delegation] 回收 %d 个过期委派目录（保留 %s）", n, win)
			}
		}
	}
}

func (s *Server) harnessDelegations(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	reg := s.delegationRegistry(t.ID)
	state := r.URL.Query().Get("state")
	all := reg.List()
	rows := make([]delegation.Record, 0, len(all))
	for _, rec := range all {
		if state != "" && string(rec.Status) != state {
			continue
		}
		rows = append(rows, rec)
	}
	sum := reg.Summary()
	writeJSON(w, http.StatusOK, map[string]any{
		"task":        t.ID,
		"delegations": rows,
		"summary":     sum,
		"pending":     len(reg.PeekNotifications()),
		"note":        "委派注册表是进程内权威状态：同步委派即时返回，后台委派（run_in_background）完成后以 task-notification 通知父会话。Agent Teams / 跨会话编排不在本产品范围（ADR-0005）。",
	})
}

func (s *Server) harnessDelegationOne(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	rec, found := s.delegationRegistry(t.ID).Get(r.PathValue("did"))
	if !found {
		writeErr(w, http.StatusNotFound, "delegation not found")
		return
	}
	body := rec.Result
	if body == "" && rec.OutputPath != "" {
		if b, err := os.ReadFile(rec.OutputPath); err == nil {
			body = string(b)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"delegation": rec, "result": body})
}

func (s *Server) harnessDelegationStop(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	killed := s.delegationRegistry(t.ID).Kill(r.PathValue("did"))
	if !killed {
		writeErr(w, http.StatusBadRequest, "委派已结束或不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stopped": r.PathValue("did")})
}
