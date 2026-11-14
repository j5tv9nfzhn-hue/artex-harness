package server

// Replay + consistency endpoints (S3).
//
//	GET  /api/harness/replay?task=&from=&to=&format=json|md
//	POST /api/harness/consistency   {base_task, cand_task, from, to, tol}
//
// Both are read-only over recorded history: they never touch the exploration
// state, so running a comparison against a live task cannot perturb it.

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Autumn-27/artex/harness/consistency"
	"github.com/Autumn-27/artex/harness/eval"
	"github.com/Autumn-27/artex/harness/replay"
)

// harnessReplay returns a task's replayable history.
func (s *Server) harnessReplay(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	taskID, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad task id")
		return
	}
	q := r.URL.Query()
	from := int64(atoiDefault(q.Get("from"), 0))
	to := int64(atoiDefault(q.Get("to"), 0))

	run, err := replay.Load(r.Context(), s.m.pg, taskID, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if q.Get("format") == "md" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(replay.RenderMarkdown(run)))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run":         run,
		"markdown":    replay.RenderMarkdown(run),
		"hash":        run.Hash(),
		"fingerprint": eval.Compute(run),
		"blocks":      eval.Blocks(run),
	})
}

// harnessConsistency compares two tasks' runs. Used by the console's Eval tab and
// by anyone who wants the gate without the CLI.
func (s *Server) harnessConsistency(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		BaseTask string  `json:"base_task"`
		CandTask string  `json:"cand_task"`
		From     int64   `json:"from"`
		To       int64   `json:"to"`
		Tol      float64 `json:"tol"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	load := func(id string, label string) (*replay.Run, string, error) {
		t := s.m.ResolveTask(id)
		if t == nil {
			return nil, "", errNotFound(label + " task not found")
		}
		n, err := strconv.ParseInt(t.ID, 10, 64)
		if err != nil {
			return nil, "", errNotFound(label + " bad task id")
		}
		run, err := replay.Load(r.Context(), s.m.pg, n, req.From, req.To)
		if err != nil {
			return nil, "", err
		}
		return run, label + "#" + t.ID, nil
	}
	base, baseLabel, err := load(req.BaseTask, "base")
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	cand, candLabel, err := load(req.CandTask, "cand")
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	tol := consistency.DefaultTolerance()
	if req.Tol > 0 {
		tol.ToolDistribution, tol.TerminalReasons = req.Tol, req.Tol
	}
	rep := consistency.Compare(baseLabel, candLabel, base, cand, tol)
	writeJSON(w, http.StatusOK, rep)
}

// errNotFound is a tiny helper so the two load branches report the same way.
type notFoundError string

func (e notFoundError) Error() string { return string(e) }

func errNotFound(msg string) error { return notFoundError(msg) }

// evalScenarios reports the scenario catalogue the gate can run. The catalogue is
// documentation-plus-machine-readable: a scenario names its target class, the
// prompt snapshot it expects and which metrics are hard.
func (s *Server) evalScenarios(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"dir": "eval/scenarios",
		"note": "场景集以文件形式随仓库分发（离线优先，不含真实目标）。" +
			"一致性判定的实现见 harness/consistency，CLI 见 eval/cmd/harness-gate。",
		"hard_checks": []string{"refusal-wording", "blocked-calls", "no-newly-allowed", "complete-history"},
		"soft_checks": []string{"tool-distribution", "terminal-reasons", "fingerprint"},
	})
}
