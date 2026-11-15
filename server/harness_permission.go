package server

// HARNESS permission policy endpoints (Sprint 1.2).
//
//	GET  /api/harness/permission?task=   — snapshot of the active policy
//	PUT  /api/harness/permission?task=   — replace the policy (clears session grants)
//	POST /api/harness/permission/grant?task=&tool= — session-scoped allow
//
// These endpoints are POLICY, not a gate. The one veto point is still the
// guard/intercept PreToolUse hook chain; nothing here can widen or bypass it.
// The HARNESS run also stays in ModeBypass at the SDK layer by default — the
// platform hooks are the gate. See harness/permission for the semantics.

import (
	"encoding/json"
	"net/http"
	"strings"

	hperm "github.com/Autumn-27/artex/harness/permission"
)

const settingHarnessPermission = "harness_permission_policy"

// harnessPermission returns the policy for the request's task, loading it from
// the settings KV table on first use and caching it on the Server. A bad stored
// value falls back to the default and is reported in the response, never silently
// applied as a widened policy.
func (s *Server) harnessPermission() *hperm.Policy {
	s.harnessPermMu.Lock()
	defer s.harnessPermMu.Unlock()
	if s.harnessPerm != nil {
		return s.harnessPerm
	}
	cfg := hperm.DefaultConfig()
	note := ""
	if s.m != nil && s.m.pg != nil {
		raw, ok, err := s.m.pg.GetSetting(settingHarnessPermission)
		if err != nil {
			note = "读取策略失败，已使用默认策略：" + err.Error()
		} else if ok {
			parsed, perr := hperm.ParseConfig(raw)
			if perr != nil {
				note = "已保存的策略无效，已回退默认策略：" + perr.Error()
			} else {
				cfg = parsed
			}
		}
	}
	s.harnessPerm = hperm.NewPolicy(cfg)
	s.harnessPermNote = note
	return s.harnessPerm
}

func (s *Server) harnessPermissionView(w http.ResponseWriter, _ *http.Request) {
	p := s.harnessPermission()
	s.harnessPermMu.Lock()
	note := s.harnessPermNote
	s.harnessPermMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"policy":         p.Snapshot(),
		"session_grants": p.SessionGrants(),
		"modes":          hperm.ValidModes,
		"note":           note,
		"veto_point":     "guard/intercept PreToolUse hooks（此端点只是策略，不是审批门）",
	})
}

func (s *Server) harnessPermissionReplace(w http.ResponseWriter, r *http.Request) {
	var cfg hperm.Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid policy: "+err.Error())
		return
	}
	p := s.harnessPermission()
	if err := p.Replace(cfg); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.m != nil && s.m.pg != nil {
		raw, _ := json.Marshal(p.Snapshot())
		if err := s.m.pg.SetSetting(settingHarnessPermission, string(raw)); err != nil {
			writeErr(w, http.StatusInternalServerError, "策略已在内存生效，但持久化失败："+err.Error())
			return
		}
	}
	s.harnessPermMu.Lock()
	s.harnessPermNote = ""
	s.harnessPermMu.Unlock()
	s.harnessPermissionView(w, r)
}

func (s *Server) harnessPermissionGrant(w http.ResponseWriter, r *http.Request) {
	tool := strings.TrimSpace(r.URL.Query().Get("tool"))
	if tool == "" {
		writeErr(w, http.StatusBadRequest, "tool is required")
		return
	}
	s.harnessPermission().GrantSession(tool)
	s.harnessPermissionView(w, r)
}
