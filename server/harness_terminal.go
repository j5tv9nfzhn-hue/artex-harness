package server

// Operator terminal + "take over one step" (docs/07 §P4).
//
// Two related needs, one endpoint:
//
//   - a terminal drawer: run a command in the task workspace and SEE the output;
//   - taking over a step: run the command yourself, and (optionally) hand the result
//     to the agent as its next input instead of re-explaining what you did.
//
// Scope and safety: this executes on the operator's own machine, inside the task
// workspace, with the same account the agent uses. It is NOT a privilege boundary —
// anyone who can call this API can already drive the agent's Bash tool. What it adds
// is that the action is recorded in the same transcript with a distinct actor, so a
// replay shows "a human did this" rather than attributing it to the model.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
)

const (
	maxExecCommandRunes = 8000
	maxExecOutputBytes  = 64 << 10
	defaultExecTimeout  = 60 * time.Second
)

// harnessExec runs one shell command in the task workspace.
func (s *Server) harnessExec(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	var req struct {
		Command string `json:"command"`
		// Inject hands the output to the agent as the next turn's input.
		Inject  bool `json:"inject"`
		Timeout int  `json:"timeout_seconds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	cmdline := strings.TrimSpace(req.Command)
	if cmdline == "" {
		writeErr(w, http.StatusBadRequest, "命令不能为空")
		return
	}
	if len([]rune(cmdline)) > maxExecCommandRunes {
		writeErr(w, http.StatusBadRequest, "命令过长")
		return
	}
	timeout := defaultExecTimeout
	if req.Timeout > 0 && req.Timeout <= 300 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	dir := s.taskWorkspace(taskID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "工作区不可用: "+err.Error())
		return
	}

	// Record the human action first: even if the command hangs, the transcript shows
	// who ran what and when.
	s.engine.emitActivity(t, activityForHumanExec(cmdline, true))

	start := time.Now()
	out, exitCode, err := runShellCommand(r.Context(), dir, cmdline, timeout)
	dur := time.Since(start)

	summary := truncateSummary(strings.TrimSpace(out), 400)
	if summary == "" {
		summary = "(无输出)"
	}
	act := activityForHumanExec(cmdline, false)
	act.Summary = summary
	act.Detail = string(out)
	act.IsError = err != nil || exitCode != 0
	s.engine.emitActivity(t, act)

	injected := false
	if req.Inject {
		msg := fmt.Sprintf("人工执行（接管一步）\n$ %s\n退出码 %d，用时 %s\n输出：\n%s",
			cmdline, exitCode, dur.Round(time.Millisecond), clip4LLM(string(out)))
		if t.HarnessMode {
			if rev, serr := s.engine.Say(t, msg); serr == nil {
				injected = true
				_ = rev
			} else {
				// Say refuses when the task is paused; surface that instead of
				// silently dropping the hand-off.
				writeErr(w, http.StatusConflict, "已执行但未能交给 agent："+serr.Error())
				return
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"task":        t.ID,
		"command":     cmdline,
		"output":      string(out),
		"exit_code":   exitCode,
		"duration_ms": dur.Milliseconds(),
		"injected":    injected,
		"cwd":         dir,
	})
}

func activityForHumanExec(cmd string, start bool) (a db.Activity) {
	a.Worker = "human"
	a.Tool = "human.sh"
	if start {
		a.Kind = "tool_use"
		a.Summary = "$ " + truncateSummary(cmd, 200)
	} else {
		a.Kind = "tool_result"
	}
	return a
}

// runShellCommand executes cmdline with bash -lc inside dir, bounded by timeout and
// by an output cap so a runaway command cannot blow up the control plane.
func runShellCommand(parent context.Context, dir, cmdline string, timeout time.Duration) (string, int, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	c := exec.CommandContext(ctx, "bash", "-lc", cmdline)
	c.Dir = dir
	out, err := c.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		return string(clipBytes(out, maxExecOutputBytes)) + fmt.Sprintf("\n[超时：超过 %s 被终止]", timeout), 124, fmt.Errorf("timeout")
	}
	return string(clipBytes(out, maxExecOutputBytes)), code, err
}

// clipBytes cuts b to n bytes on a rune boundary.
func clipBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	cut := n
	for cut > 0 && !utf8StartByte(b[cut]) {
		cut--
	}
	return append(b[:cut:cut], []byte("\n…(输出已截断)")...)
}

func utf8StartByte(b byte) bool { return b&0xC0 != 0x80 }

// clip4LLM bounds what gets handed to the model: the agent does not need 64 KB of
// terminal scrollback, and injecting it would burn the context window.
func clip4LLM(s string) string {
	const n = 6000
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n…(已截断)"
}
