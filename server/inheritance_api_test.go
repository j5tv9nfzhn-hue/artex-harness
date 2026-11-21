package server

import (
	"fmt"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// TestInheritedTaskRelationDeletionOnSourceDelete 覆盖任务继承关系的删除：
// 删掉 source 任务后，current 任务的 SourceTaskIDs 必须随之清空。
//
// 2026-11-21 S6-TRIM 改写：原测试还断言了 GET /api/exploration/activity/{id}
// 返回的跨任务可见性（planner 记录不透传、worker 结果透传），该端点已删，
// 可见性规则随 handler 一起没了，这部分断言无法保留。
func TestInheritedTaskRelationDeletionOnSourceDelete(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer m.Close()

	source, err := m.CreateTaskWithOptions("detail source", "source goal", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sourceID := mustTaskID(t, source.ID)
	current, err := m.CreateTaskWithOptions("detail current", "current goal", db.TaskCreateOptions{SourceTaskIDs: []int64{sourceID}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = m.DeleteTask(current.ID, DeleteTaskOptions{})
		_, _ = m.DeleteTask(source.ID, DeleteTaskOptions{})
	})

	if _, err := source.Store.AppendActivity(db.Activity{
		Worker: "planner", Kind: "text", Summary: "private planner", Detail: "private planner detail",
	}); err != nil {
		t.Fatal(err)
	}
	intentID, err := source.Store.AddIntent(map[string]any{"summary": "completed source work"}, 1, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Store.SetIntentState(intentID, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Store.AppendActivity(db.Activity{
		NodeID: &intentID, Worker: "worker", Kind: "result", Summary: "shared result", Detail: "shared worker detail",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.DeleteTask(source.ID, DeleteTaskOptions{}); err != nil {
		t.Fatal(err)
	}
	currentAfter, ok := m.Task(current.ID)
	if !ok || len(currentAfter.SourceTaskIDs) != 0 {
		t.Fatalf("deleted source remained in live task DTO state: %+v", currentAfter)
	}
}

func mustTaskID(t *testing.T, raw string) int64 {
	t.Helper()
	var id int64
	if _, err := fmt.Sscan(raw, &id); err != nil || id <= 0 {
		t.Fatalf("bad task id %q: %v", raw, err)
	}
	return id
}
