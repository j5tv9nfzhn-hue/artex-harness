package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestFindingPaginationParam(t *testing.T) {
	for _, test := range []struct {
		raw, name             string
		fallback, upper, want int
	}{
		{name: "missing", raw: "", fallback: 10, upper: 100, want: 10},
		{name: "invalid", raw: "not-a-number", fallback: 10, upper: 100, want: 10},
		{name: "zero", raw: "0", fallback: 10, upper: 100, want: 10},
		{name: "negative", raw: "-2", fallback: 1, upper: 0, want: 1},
		{name: "bounded", raw: "500", fallback: 10, upper: 100, want: 100},
		{name: "valid", raw: "25", fallback: 10, upper: 100, want: 25},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := findingPaginationParam(test.raw, test.fallback, test.upper); got != test.want {
				t.Fatalf("findingPaginationParam(%q,%d,%d)=%d, want %d",
					test.raw, test.fallback, test.upper, got, test.want)
			}
		})
	}
}

func TestFindingGroupsReturnsTaskBucketsAndNormalizesPagination(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	marker := fmt.Sprintf("__server_finding_groups_%d__", time.Now().UnixNano())
	createdTaskIDs := []int64{}
	defer func() {
		_, _ = m.pg.Exec(`DELETE FROM findings WHERE vulnclass=$1`, marker)
		for _, taskID := range createdTaskIDs {
			_ = m.pg.DeleteTask(taskID)
		}
	}()

	taskA, err := m.CreateTask("finding group A", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	createdTaskIDs = append(createdTaskIDs, mustTaskID(t, taskA.ID))
	taskB, err := m.CreateTask("finding group B", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	createdTaskIDs = append(createdTaskIDs, mustTaskID(t, taskB.ID))

	for i, seed := range []struct {
		taskID   string
		severity string
	}{
		{taskA.ID, db.SeverityCritical},
		{taskA.ID, db.SeverityHigh},
		{taskB.ID, db.SeverityLow},
	} {
		if _, err := m.pg.AddFinding(mustTaskID(t, seed.taskID), 0, marker, "", seed.severity,
			fmt.Sprintf("finding %d", i), "", "test", nil); err != nil {
			t.Fatalf("AddFinding[%d]: %v", i, err)
		}
	}

	s := &Server{m: m}
	request := httptest.NewRequest(http.MethodGet,
		"/api/exploration/findings/groups?vulnclass="+marker+"&sort=severity&page=1&limit=1", nil)
	recorder := httptest.NewRecorder()
	s.findingGroups(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("groups status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page struct {
		Items        []db.FindingGroup `json:"items"`
		Total        int               `json:"total"`
		FindingTotal int               `json:"finding_total"`
		Page         int               `json:"page"`
		PageSize     int               `json:"page_size"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Total != 2 || page.FindingTotal != 3 || page.Page != 1 || page.PageSize != 1 {
		t.Fatalf("unexpected grouped page: %+v", page)
	}
	if page.Items[0].TaskID == nil || *page.Items[0].TaskID != mustTaskID(t, taskA.ID) ||
		page.Items[0].Count != 2 || page.Items[0].Critical != 1 || page.Items[0].High != 1 {
		t.Fatalf("unexpected first group: %+v", page.Items[0])
	}

	request = httptest.NewRequest(http.MethodGet,
		"/api/exploration/findings/groups?vulnclass="+marker+"&q=finding+2&page=1&limit=10", nil)
	recorder = httptest.NewRecorder()
	s.findingGroups(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("searched groups status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	page = struct {
		Items        []db.FindingGroup `json:"items"`
		Total        int               `json:"total"`
		FindingTotal int               `json:"finding_total"`
		Page         int               `json:"page"`
		PageSize     int               `json:"page_size"`
	}{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Total != 1 || page.FindingTotal != 1 || page.Items[0].TaskID == nil ||
		*page.Items[0].TaskID != mustTaskID(t, taskB.ID) {
		t.Fatalf("unexpected searched group page: %+v", page)
	}

	request = httptest.NewRequest(http.MethodGet,
		"/api/exploration/findings/groups?vulnclass="+marker+"&page=-4&limit=0", nil)
	recorder = httptest.NewRecorder()
	s.findingGroups(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("normalized groups status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	page = struct {
		Items        []db.FindingGroup `json:"items"`
		Total        int               `json:"total"`
		FindingTotal int               `json:"finding_total"`
		Page         int               `json:"page"`
		PageSize     int               `json:"page_size"`
	}{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Page != 1 || page.PageSize != 10 || len(page.Items) != 2 {
		t.Fatalf("pagination was not normalized: %+v", page)
	}
}

// admission is exercised without starting background planner/worker work

// The retained finding can no longer be deepened after its task and node are
// deleted; this must be a conflict rather than silently creating orphan work.

// Keep the in-memory handle and graph available while forcing the atomic
// admission UPDATE to reject this soft-deleted row.
