package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Stage 2（S6-P1）HTTP 表面回归测试：确认验证门、审计链、流量重放、专业报告。
// 2026-11-21 真库验证时补：此前这四个口没有任何测试覆盖。

// TestStage2ConfirmGateBlocksAndOverridePasses：无证据/无复现/无复测的发现
// 标 confirmed 必须 422 并给出原因；人工越过（带理由）放行，且越过记录进审计链。
func TestStage2ConfirmGateBlocksAndOverridePasses(t *testing.T) {
	s, initial, request := trafficEvidenceServer(t)

	r := request("PATCH", fmt.Sprintf("/api/exploration/findings/%d", initial.FindingID), `{"status":"confirmed"}`)
	if r.Code != 422 {
		t.Fatalf("unproven confirm: %d %s", r.Code, r.Body.String())
	}
	var blocked struct {
		Error   string   `json:"error"`
		Reasons []string `json:"reasons"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &blocked); err != nil || len(blocked.Reasons) == 0 {
		t.Fatalf("422 must list failing checks: %s (err=%v)", r.Body.String(), err)
	}

	r = request("PATCH", fmt.Sprintf("/api/exploration/findings/%d", initial.FindingID),
		`{"status":"confirmed","human_override":"人工复核：已在隔离环境复现"}`)
	if r.Code != 200 {
		t.Fatalf("human override: %d %s", r.Code, r.Body.String())
	}

	// 越过记录进审计链，且链校验通过。
	f, err := s.m.pg.GetFinding(initial.FindingID)
	if err != nil || f == nil || f.TaskID == nil {
		t.Fatal("finding without task", err, f)
	}
	r = request("GET", "/api/harness/audit-chain?task="+fmt.Sprint(*f.TaskID), "")
	if r.Code != 200 {
		t.Fatalf("audit-chain after override: %d %s", r.Code, r.Body.String())
	}
	var chain struct {
		Valid   bool `json:"valid"`
		Entries int  `json:"entries"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &chain); err != nil || !chain.Valid || chain.Entries < 1 {
		t.Fatalf("override must land in a valid audit chain: %s (err=%v)", r.Body.String(), err)
	}
}

// TestStage2AuditChainAndReport：审计链端点校验通过、报告端点渲染出发现标题。
func TestStage2AuditChainAndReport(t *testing.T) {
	s, initial, request := trafficEvidenceServer(t)
	f, err := s.m.pg.GetFinding(initial.FindingID)
	if err != nil || f == nil || f.TaskID == nil {
		t.Fatal("finding without task", err, f)
	}
	task := fmt.Sprint(*f.TaskID)

	r := request("GET", "/api/harness/audit-chain?task="+task, "")
	if r.Code != 200 {
		t.Fatalf("audit-chain: %d %s", r.Code, r.Body.String())
	}
	var chain struct {
		Valid   bool `json:"valid"`
		Entries int  `json:"entries"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &chain); err != nil || !chain.Valid {
		t.Fatalf("audit chain must verify: %s (err=%v)", r.Body.String(), err)
	}

	r = request("GET", "/api/report/pentest?task="+task, "")
	if r.Code != 200 {
		t.Fatalf("pentest report: %d %s", r.Code, r.Body.String())
	}
	if !strings.Contains(r.Body.String(), "Evidence API") {
		t.Fatalf("report must list the finding: %.200s", r.Body.String())
	}
}

// TestStage2TrafficReplayReproduces：重放一条指向本地测试服务器的记录，
// 原始 200、重放 200 → reproduced=true。
func TestStage2TrafficReplayReproduces(t *testing.T) {
	s, _, request := trafficEvidenceServer(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	id := "replay-200"
	if _, err := s.m.traffic.DB().Exec(
		`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path) VALUES(?,1,'127.0.0.1','GET',?,?,200,'text/plain',0,5,'')`,
		id, srv.URL, srv.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.traffic.DB().Exec(
		`INSERT INTO exchange_bodies(id,req_head,resp_head,req_body,resp_body) VALUES(?,?,'HTTP/1.1 200 OK',?,?)`,
		id, "GET "+srv.URL+" HTTP/1.1", []byte{}, []byte("hello")); err != nil {
		t.Fatal(err)
	}

	r := request("POST", "/api/traffic/replay", fmt.Sprintf(`{"exchange_id":%q}`, id))
	if r.Code != 200 {
		t.Fatalf("replay: %d %s", r.Code, r.Body.String())
	}
	var res struct {
		Reproduced     bool `json:"reproduced"`
		Status         int  `json:"status"`
		OriginalStatus int  `json:"original_status"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Reproduced || res.Status != 200 || res.OriginalStatus != 200 {
		t.Fatalf("replay must reproduce 200: %+v", res)
	}
}
