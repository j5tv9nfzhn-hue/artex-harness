package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// S6-FIX-PUNCHLIST 回归测试（2026-11-21）：真实任务 #1 与浏览器核查攒下的
// 7 项小问题里服务端可测的部分。夹具见 trafficEvidenceServer
// （server/finding_traffic_test.go），它已登记 1 条 low 发现。

func punchlistTaskID(t *testing.T, s *Server, f *db.RecordedFinding) int64 {
	t.Helper()
	got, err := s.m.pg.GetFinding(f.FindingID)
	if err != nil || got == nil || got.TaskID == nil {
		t.Fatalf("GetFinding(%d): %v %+v", f.FindingID, err, got)
	}
	return *got.TaskID
}

// 项 1：GET /api/tasks/{id} 的 findings 须是真实的按严重度计数，
// 不再恒为 0（根因：getTask 从不填充 dto.Findings）。
func TestPunchlistGetTaskFindingsCounts(t *testing.T) {
	s, initial, request := trafficEvidenceServer(t)
	tid := punchlistTaskID(t, s, initial)
	task := s.m.ResolveTask(strconv.FormatInt(tid, 10))
	if task == nil {
		t.Fatalf("task %d not in manager", tid)
	}
	for _, sev := range []string{"critical", "high"} {
		if _, err := s.evidenceStore().Record(context.Background(), db.RecordFindingInput{
			TaskID: tid, ExplorationID: task.ExpID, Worker: "test",
			VulnClass: "TEST", Summary: "punchlist " + sev, Name: "Punchlist " + sev, Severity: sev,
		}, nil); err != nil {
			t.Fatalf("record %s finding: %v", sev, err)
		}
	}
	w := request("GET", "/api/tasks/"+strconv.FormatInt(tid, 10), "")
	if w.Code != 200 {
		t.Fatalf("getTask: %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	findings, _ := out["findings"].(map[string]any)
	if findings == nil {
		t.Fatalf("no findings in task DTO: %v", out)
	}
	for sev, want := range map[string]float64{"critical": 1, "high": 1, "low": 1} {
		if got, _ := findings[sev].(float64); got != want {
			t.Errorf("findings.%s = %v, want %v (full: %v)", sev, got, want, findings)
		}
	}
}

// 项 3：登记发现须写进同一任务的审计链（kind=finding_reported），
// 且链仍能通过 Verify()。
func TestPunchlistFindingReportedInAuditChain(t *testing.T) {
	s, initial, request := trafficEvidenceServer(t)
	tid := punchlistTaskID(t, s, initial)
	w := request("GET", "/api/harness/audit-chain?task="+strconv.FormatInt(tid, 10), "")
	if w.Code != 200 {
		t.Fatalf("audit-chain: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Valid bool `json:"valid"`
		Chain []struct {
			Kind      string `json:"kind"`
			FindingID int64  `json:"finding_id"`
		} `json:"chain"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Valid {
		t.Fatalf("audit chain invalid: %s", w.Body.String())
	}
	found := false
	for _, e := range out.Chain {
		if e.Kind == "finding_reported" && e.FindingID == initial.FindingID {
			found = true
		}
	}
	if !found {
		t.Fatalf("finding_reported for %d not in chain: %+v", initial.FindingID, out.Chain)
	}
}

// 项 4：详情须返回真实的绑定列表（不再只靠 traffic_count 文案），
// 血缘里 finding 节点的 state 须是发现的实际 triage 状态（pending），
// 而不是探索图里写死的 confirmed。
func TestPunchlistFindingDetailBindingsAndLineage(t *testing.T) {
	s, initial, request := trafficEvidenceServer(t)
	seedServerEvidenceFlow(t, s, "punchlist-1", []byte("poc-body"))
	if _, err := s.evidenceStore().Bind(context.Background(), initial.FindingID,
		[]db.TrafficRef{{TrafficID: "punchlist-1", Role: "baseline"}}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	w := request("GET", "/api/exploration/findings/"+strconv.FormatInt(initial.FindingID, 10), "")
	if w.Code != 200 {
		t.Fatalf("getFinding: %d %s", w.Code, w.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	bindings, _ := detail["traffic_bindings"].([]any)
	if len(bindings) != 1 {
		t.Fatalf("traffic_bindings = %d, want 1 (full: %v)", len(bindings), detail)
	}
	if got, _ := detail["traffic_count"].(float64); got != 1 {
		t.Errorf("traffic_count = %v, want 1", got)
	}
	w = request("GET", "/api/exploration/findings/"+strconv.FormatInt(initial.FindingID, 10)+"/lineage", "")
	if w.Code != 200 {
		t.Fatalf("lineage: %d %s", w.Code, w.Body.String())
	}
	var lin struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lin); err != nil {
		t.Fatal(err)
	}
	wantID := strconv.FormatInt(initial.NodeID, 10)
	for _, n := range lin.Nodes {
		if fmt.Sprint(n["id"]) == wantID {
			if n["state"] != "pending" {
				t.Errorf("finding node state = %v, want pending (discovery is pending, not confirmed)", n["state"])
			}
			return
		}
	}
	t.Errorf("finding node %s not in lineage: %v", wantID, lin.Nodes)
}

// 项 2：报告按状态计数（pending 不得写成“确认发现”），修复建议取自
// 发现详细报告里的「修复建议」一节；没有建议时才写未提供。
func TestPunchlistReportWording(t *testing.T) {
	s, initial, request := trafficEvidenceServer(t)
	tid := punchlistTaskID(t, s, initial)
	report := "# 登录 SQL 注入\n\n## 概述\n\n拼接 SQL。\n\n## 修复建议\n\n### 一、必须修复\n\n1. **参数化查询**：用占位符。\n\n## 附录\n\n其他。\n"
	if _, err := s.m.pg.SetFindingReportByNodeID(initial.NodeID, report); err != nil {
		t.Fatalf("SetFindingReportByNodeID: %v", err)
	}
	w := request("GET", "/api/report/pentest?task="+strconv.FormatInt(tid, 10), "")
	if w.Code != 200 {
		t.Fatalf("pentest report: %d %s", w.Code, w.Body.String())
	}
	md := w.Body.String()
	if !strings.Contains(md, "发现共 **1** 个：已确认 **0** 个、待确认 **1** 个") {
		t.Errorf("report should count by status, got:\n%s", md)
	}
	if strings.Contains(md, "确认发现：**1** 个") {
		t.Errorf("pending finding must not be called confirmed:\n%s", md)
	}
	if !strings.Contains(md, "参数化查询") {
		t.Errorf("report should render the finding's own remediation:\n%s", md)
	}
	if strings.Contains(md, "未提供修复建议") {
		t.Errorf("report must not say no remediation when one exists:\n%s", md)
	}
}

// 项 6：建任务可传 run_budget_minutes（分钟）；不传用全局默认；
// 0 与超范围（<5 或 >480）返回 400。默认值不改。
func TestPunchlistCreateTaskRunBudget(t *testing.T) {
	s, _, request := trafficEvidenceServer(t)
	cleanup := func(id string) {
		tid, _ := strconv.ParseInt(id, 10, 64)
		if tid > 0 {
			s.m.pg.Exec(`DELETE FROM task_archives WHERE task_id=$1`, tid)
			s.m.pg.DeleteTask(tid)
		}
	}
	// 传参：60 分钟 → 201，响应里带回该值。
	w := request("POST", "/api/tasks", `{"description":"budget 60","goal":"g","run_budget_minutes":60}`)
	if w.Code != 201 {
		t.Fatalf("create with budget 60: %d %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if got, _ := created["run_budget_minutes"].(float64); got != 60 {
		t.Errorf("run_budget_minutes = %v, want 60 (%v)", got, created)
	}
	cleanup(fmt.Sprint(created["id"]))
	// 不传：201，字段缺省（0=用默认）。
	w = request("POST", "/api/tasks", `{"description":"budget default","goal":"g"}`)
	if w.Code != 201 {
		t.Fatalf("create without budget: %d %s", w.Code, w.Body.String())
	}
	created = map[string]any{} // 复用 map 会残留上一轮的键，必须清空。
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if got, _ := created["run_budget_minutes"].(float64); got != 0 {
		t.Errorf("unset run_budget_minutes = %v, want 0 (default)", got)
	}
	cleanup(fmt.Sprint(created["id"]))
	// 非法值：0、4、481 → 400。
	for _, bad := range []int{0, 4, 481} {
		w = request("POST", "/api/tasks", fmt.Sprintf(`{"description":"budget bad","goal":"g","run_budget_minutes":%d}`, bad))
		if w.Code != 400 {
			t.Errorf("create with budget %d: got %d, want 400 (%s)", bad, w.Code, w.Body.String())
		}
	}
}
