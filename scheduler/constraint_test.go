package scheduler

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func TestALAPConstraint(t *testing.T) {
	in := baseInput()
	in.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour}
	in.Tasks["C"] = &Task{ID: "C", Duration: 4 * time.Hour, Predecessors: []Dependency{{PredecessorID: "A"}}}
	in.Tasks["B"] = &Task{ID: "B", Duration: 4 * time.Hour, Constraint: Constraint{Type: ConstraintALAP}}
	res, err := Schedule(in)
	if err != nil {
		t.Fatal(err)
	}
	// 项目完成 = A(8h)+C(4h) = 周二 13:00。B 尽晚开始 = 周二 9:00。
	b := res.Tasks["B"]
	want := time.Date(2025, 3, 4, 9, 0, 0, 0, time.UTC)
	if !b.Start.Equal(want) {
		t.Fatalf("ALAP B.Start = %v, want %v", b.Start, want)
	}
	// ASAP 对照：无约束任务周一开始。
	if !res.Tasks["A"].Start.Equal(testStart) {
		t.Fatalf("A.Start = %v", res.Tasks["A"].Start)
	}
}

func TestStartToFinishDependency(t *testing.T) {
	in := baseInput()
	in.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour}
	in.Tasks["B"] = &Task{ID: "B", Duration: 4 * time.Hour, Predecessors: []Dependency{
		{PredecessorID: "A", Type: StartToFinish, Lag: time.Hour}}}
	res, err := Schedule(in)
	if err != nil {
		t.Fatal(err)
	}
	// B 完成 >= A 开始 + 1h = 周一 10:00；B 4h 回推 -> 周一 9:00? 10:00-4h 回推：
	// 周一 9:00 开始 4h = 13:00 完成 >= 10:00 满足；但下界是"完成 >= 10:00"，
	// 最早完成 10:00 -> 开始 = 10:00 - 4h = 周一 9:00 前一小时？回推 4h 工作：
	// 10:00 前推 1h = 9:00，还需 3h -> 上周五 14:00。但项目开始于周一 9:00，
	// 因此 B 被项目开始顶住：周一 9:00 开始。
	b := res.Tasks["B"]
	if !b.Start.Equal(testStart) {
		t.Fatalf("B.Start = %v, want %v (clamped by project start)", b.Start, testStart)
	}
	if b.Finish.Before(time.Date(2025, 3, 3, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("B.Finish %v violates SF+1h constraint", b.Finish)
	}
}

// TestOutOfOrderEditsConverge 对互不冲突的编辑，乱序到达与重复提交后
// 最终状态必须收敛一致。
func TestOutOfOrderEditsConverge(t *testing.T) {
	edits := []Edit{
		{ID: "e1", Type: EditUpsertTask, Task: &Task{ID: "A", Duration: 12 * time.Hour, Priority: 1, Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}},
		{ID: "e2", Type: EditUpsertTask, Task: &Task{ID: "B", Duration: 6 * time.Hour, Priority: 2, Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}},
		{ID: "e3", Type: EditUpsertTask, Task: &Task{ID: "C", Duration: 8 * time.Hour, Predecessors: []Dependency{{PredecessorID: "B"}}}},
		{ID: "e4", Type: EditUpsertResource, Resource: &Resource{ID: "R1", Capacity: 2}},
	}
	applyAll := func(order []int) *Plan {
		p := samplePlan(t)
		for _, i := range order {
			if _, err := p.ApplyEdit(edits[i]); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	base := applyAll([]int{0, 1, 2, 3}).Snapshot()
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		order := []int{0, 1, 2, 3}
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		// 随机重复提交部分编辑。
		if rng.Intn(2) == 0 {
			order = append(order, order[rng.Intn(len(order))])
		}
		got := applyAll(order).Snapshot()
		if got.Result.ProjectFinish != base.Result.ProjectFinish &&
			!got.Result.ProjectFinish.Equal(base.Result.ProjectFinish) {
			t.Fatalf("order %v: project finish diverged", order)
		}
		for id, tr := range base.Result.Tasks {
			g := got.Result.Tasks[id]
			if !tr.Start.Equal(g.Start) || !tr.Finish.Equal(g.Finish) {
				t.Fatalf("order %v: task %s diverged: %v-%v vs %v-%v",
					order, id, g.Start, g.Finish, tr.Start, tr.Finish)
			}
		}
		if fmt.Sprint(got.Result.CriticalPath) != fmt.Sprint(base.Result.CriticalPath) {
			t.Fatalf("order %v: critical path diverged", order)
		}
	}
}

func TestRemoveTaskCleansEdges(t *testing.T) {
	p := samplePlan(t)
	v, err := p.ApplyEdit(Edit{ID: "rm", Type: EditRemoveTask, TaskID: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Input.Tasks["A"]; ok {
		t.Fatal("A should be removed")
	}
	for _, d := range v.Input.Tasks["C"].Predecessors {
		if d.PredecessorID == "A" {
			t.Fatal("dangling edge to removed task")
		}
	}
	// 与全量重算一致。
	full, err := Schedule(v.Input)
	if err != nil {
		t.Fatal(err)
	}
	assertSameResult(t, full, v.Result)
	// 重复删除同一任务：幂等。
	v2, err := p.ApplyEdit(Edit{ID: "rm2", Type: EditRemoveTask, TaskID: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if v2 != v {
		t.Fatal("removing a missing task should be a no-op")
	}
}
