package scheduler

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// samplePlan 构造一个带依赖与资源的计划用于编辑测试。
func samplePlan(t *testing.T) *Plan {
	t.Helper()
	in := baseInput()
	in.Resources["R1"] = &Resource{ID: "R1", Capacity: 1}
	in.Tasks["A"] = &Task{ID: "A", Duration: 8 * time.Hour, Priority: 1, Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}
	in.Tasks["B"] = &Task{ID: "B", Duration: 8 * time.Hour, Priority: 2, Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}
	in.Tasks["C"] = &Task{ID: "C", Duration: 4 * time.Hour, Predecessors: []Dependency{{PredecessorID: "A"}}}
	p, err := NewPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// assertSameResult 断言两个结果逐项一致。
func assertSameResult(t *testing.T, want, got *Result) {
	t.Helper()
	if len(want.Tasks) != len(got.Tasks) {
		t.Fatalf("task count differs: %d vs %d", len(want.Tasks), len(got.Tasks))
	}
	for id, w := range want.Tasks {
		g, ok := got.Tasks[id]
		if !ok {
			t.Fatalf("task %s missing in incremental result", id)
		}
		if !w.Start.Equal(g.Start) || !w.Finish.Equal(g.Finish) ||
			!w.EarlyStart.Equal(g.EarlyStart) || !w.EarlyFinish.Equal(g.EarlyFinish) ||
			!w.LateStart.Equal(g.LateStart) || !w.LateFinish.Equal(g.LateFinish) ||
			w.TotalFloat != g.TotalFloat || w.Critical != g.Critical ||
			w.NegativeFloat != g.NegativeFloat || w.Delayed != g.Delayed {
			t.Fatalf("task %s mismatch:\nfull:        %+v\nincremental: %+v", id, w, g)
		}
		if len(w.DelayReasons) != len(g.DelayReasons) {
			t.Fatalf("task %s delay reasons differ: %v vs %v", id, w.DelayReasons, g.DelayReasons)
		}
		for i := range w.DelayReasons {
			if w.DelayReasons[i] != g.DelayReasons[i] {
				t.Fatalf("task %s delay reason %d differs: %v vs %v", id, i, w.DelayReasons[i], g.DelayReasons[i])
			}
		}
	}
	if fmt.Sprint(want.CriticalPath) != fmt.Sprint(got.CriticalPath) {
		t.Fatalf("critical path differs: %v vs %v", want.CriticalPath, got.CriticalPath)
	}
	if !want.ProjectFinish.Equal(got.ProjectFinish) {
		t.Fatalf("project finish differs: %v vs %v", want.ProjectFinish, got.ProjectFinish)
	}
}

func TestIncrementalMatchesFull(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 30; trial++ {
		in := baseInput()
		in.Resources["R1"] = &Resource{ID: "R1", Capacity: 1 + float64(rng.Intn(2))}
		in.Resources["R2"] = &Resource{ID: "R2", Capacity: 1}
		ids := []string{"A", "B", "C", "D", "E", "F"}
		for i, id := range ids {
			task := &Task{ID: id, Duration: time.Duration(2+rng.Intn(8)) * time.Hour, Priority: rng.Intn(3)}
			if i > 0 && rng.Intn(2) == 0 {
				task.Predecessors = []Dependency{{PredecessorID: ids[rng.Intn(i)], Lag: time.Duration(rng.Intn(4)) * time.Hour}}
			}
			if rng.Intn(2) == 0 {
				rid := "R1"
				if rng.Intn(2) == 0 {
					rid = "R2"
				}
				task.Resources = []ResourceAssignment{{ResourceID: rid, Units: 1}}
			}
			in.Tasks[id] = task
		}
		p, err := NewPlan(in)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		// 随机编辑：改工期、加依赖、改资源、钉住。
		for step := 0; step < 12; step++ {
			id := ids[rng.Intn(len(ids))]
			cur := p.Snapshot().Input.Tasks[id]
			nt := cur.Clone()
			switch rng.Intn(4) {
			case 0:
				nt.Duration = time.Duration(1+rng.Intn(10)) * time.Hour
			case 1:
				nt.Priority = rng.Intn(4)
			case 2:
				// 随机加/换一条指向前序任务的依赖（保持无环）。
				idx := 0
				for i, x := range ids {
					if x == id {
						idx = i
					}
				}
				if idx > 0 {
					nt.Predecessors = []Dependency{{PredecessorID: ids[rng.Intn(idx)]}}
				}
			case 3:
				if len(nt.Resources) == 0 {
					nt.Resources = []ResourceAssignment{{ResourceID: "R1", Units: 1}}
				} else {
					nt.Resources = nil
				}
			}
			v, err := p.ApplyEdit(Edit{ID: fmt.Sprintf("t%ds%d", trial, step), Type: EditUpsertTask, Task: nt})
			if err != nil {
				t.Fatalf("trial %d step %d: %v", trial, step, err)
			}
			full, err := Schedule(v.Input)
			if err != nil {
				t.Fatalf("trial %d step %d full reschedule: %v", trial, step, err)
			}
			assertSameResult(t, full, v.Result)
		}
	}
}

func TestDuplicateEditIdempotent(t *testing.T) {
	p := samplePlan(t)
	e := Edit{ID: "edit-1", Type: EditUpsertTask, Task: &Task{ID: "A", Duration: 16 * time.Hour, Priority: 1, Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}}
	v1, err := p.ApplyEdit(e)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := p.ApplyEdit(e) // 同一编辑 ID 重复提交
	if err != nil {
		t.Fatal(err)
	}
	if v1 != v2 {
		t.Fatal("duplicate edit ID must be a no-op returning the same version")
	}
	// 内容相同的编辑（不同 ID）也不应产生变化。
	v3, err := p.ApplyEdit(Edit{ID: "edit-2", Type: EditUpsertTask, Task: e.Task})
	if err != nil {
		t.Fatal(err)
	}
	if v3 != v2 {
		t.Fatal("identical edit content should not produce a new version")
	}
}

func TestFailedEditLeavesPlanUnchanged(t *testing.T) {
	p := samplePlan(t)
	before := p.Snapshot()
	// 引入环路的编辑必须失败。
	bad := p.Snapshot().Input.Tasks["A"].Clone()
	bad.Predecessors = []Dependency{{PredecessorID: "C"}}
	if _, err := p.ApplyEdit(Edit{ID: "bad", Type: EditUpsertTask, Task: bad}); err == nil {
		t.Fatal("cycle-introducing edit must fail")
	}
	var cyc *CycleError
	if _, err := p.ApplyEdit(Edit{ID: "bad2", Type: EditUpsertTask, Task: bad}); err == nil || !asCycle(err, &cyc) {
		t.Fatalf("expected CycleError, got %v", err)
	}
	after := p.Snapshot()
	if after != before {
		t.Fatal("failed edit must not change the committed version")
	}
}

func asCycle(err error, out **CycleError) bool {
	for err != nil {
		if c, ok := err.(*CycleError); ok {
			*out = c
			return true
		}
		return false
	}
	return false
}

func TestPinnedTaskNotRescheduled(t *testing.T) {
	p := samplePlan(t)
	v0 := p.Snapshot()
	aStart := v0.Result.Tasks["A"].Start
	// 报告 A 进行中 -> 自动钉住。
	v1, err := p.ApplyEdit(Edit{ID: "prog", Type: EditReportProgress, TaskID: "A",
		Task: &Task{ID: "A", Status: StatusInProgress, PercentComplete: 50}})
	if err != nil {
		t.Fatal(err)
	}
	if !v1.Input.Tasks["A"].Pinned {
		t.Fatal("in-progress task should be pinned")
	}
	// 把项目开始时间大幅后移：钉住的 A 不得移动。
	later := testStart.AddDate(0, 1, 0)
	v2, err := p.ApplyEdit(Edit{ID: "shift", Type: EditSetProjectStart, ProjectStart: &later})
	if err != nil {
		t.Fatal(err)
	}
	if !v2.Result.Tasks["A"].Start.Equal(aStart) {
		t.Fatalf("pinned task moved: %v -> %v", aStart, v2.Result.Tasks["A"].Start)
	}
	// 未钉住的 B/C 应随后移。
	if !v2.Result.Tasks["C"].Start.After(v0.Result.Tasks["C"].Start) {
		t.Fatal("unpinned task C should move with project start")
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	p := samplePlan(t)
	const writers = 4
	const readers = 8
	const rounds = 50
	var wg sync.WaitGroup
	errCh := make(chan error, writers*rounds)
	// 写方：串行提交不同编辑。
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				d := time.Duration(2+((w+i)%6)) * time.Hour
				_, err := p.ApplyEdit(Edit{
					ID:   fmt.Sprintf("w%d-%d", w, i),
					Type: EditUpsertTask,
					Task: &Task{ID: "B", Duration: d, Priority: 2, Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}},
				})
				if err != nil {
					errCh <- err
				}
			}
		}(w)
	}
	// 读方：任何时刻都必须读到完整一致的版本。
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds*4; i++ {
				v := p.Snapshot()
				res := v.Result
				in := v.Input
				for id, tr := range res.Tasks {
					if _, ok := in.Tasks[id]; !ok {
						errCh <- fmt.Errorf("result references unknown task %s", id)
						return
					}
					if tr.Start.IsZero() || tr.Finish.Before(tr.Start) {
						errCh <- fmt.Errorf("task %s has inconsistent times %v-%v", id, tr.Start, tr.Finish)
						return
					}
				}
				// 视图读取也必须一致。
				pg := v.View(ViewQuery{Limit: 2})
				if pg.Total != len(in.Tasks) {
					errCh <- fmt.Errorf("view total %d != task count %d", pg.Total, len(in.Tasks))
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	// 最终版本必须与全量重算一致。
	final := p.Snapshot()
	full, err := Schedule(final.Input)
	if err != nil {
		t.Fatal(err)
	}
	assertSameResult(t, full, final.Result)
}

func TestBaselineVariance(t *testing.T) {
	p := samplePlan(t)
	vb := p.SetBaseline("v1")
	if vb.Baseline == nil {
		t.Fatal("baseline not set")
	}
	// 推迟 A：B/C 随之偏移。
	if _, err := p.ApplyEdit(Edit{ID: "grow", Type: EditUpsertTask,
		Task: &Task{ID: "A", Duration: 24 * time.Hour, Priority: 1, Resources: []ResourceAssignment{{ResourceID: "R1", Units: 1}}}}); err != nil {
		t.Fatal(err)
	}
	rep := p.Snapshot().Variance()
	if rep == nil {
		t.Fatal("variance report missing")
	}
	if len(rep.MostDeviated) == 0 {
		t.Fatal("most deviated list empty")
	}
	top := rep.MostDeviated[0]
	if rep.Tasks[top].FinishVariance <= 0 {
		t.Fatalf("top deviated task should have positive finish variance: %+v", rep.Tasks[top])
	}
	// 偏移必须等于 计划-基线。
	for id, tv := range rep.Tasks {
		want := p.Snapshot().Result.Tasks[id].Finish.Sub(vb.Baseline.Tasks[id].Finish)
		if tv.FinishVariance != want {
			t.Fatalf("task %s variance %v != %v", id, tv.FinishVariance, want)
		}
	}
}

func TestViewPaginationDeterministic(t *testing.T) {
	p := samplePlan(t)
	v := p.Snapshot()
	pg1 := v.View(ViewQuery{Offset: 0, Limit: 2})
	pg2 := v.View(ViewQuery{Offset: 2, Limit: 2})
	if pg1.Total != 3 {
		t.Fatalf("total = %d, want 3", pg1.Total)
	}
	seen := map[string]bool{}
	for _, r := range append(append([]ViewRow{}, pg1.Rows...), pg2.Rows...) {
		if seen[r.TaskID] {
			t.Fatalf("task %s appears on two pages", r.TaskID)
		}
		seen[r.TaskID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("pages cover %d tasks, want 3", len(seen))
	}
	// 同一查询重复执行输出一致。
	again := v.View(ViewQuery{Offset: 0, Limit: 2})
	if fmt.Sprint(again.Rows) != fmt.Sprint(pg1.Rows) {
		t.Fatal("view output not reproducible")
	}
	// 时间窗口过滤。
	win := v.View(ViewQuery{WindowStart: testStart, WindowEnd: testStart.Add(9 * time.Hour)})
	for _, r := range win.Rows {
		if !r.Start.Before(testStart.Add(9*time.Hour)) || !r.Finish.After(testStart) {
			t.Fatalf("row %s outside window", r.TaskID)
		}
	}
	// 资源过滤。
	byRes := v.View(ViewQuery{ResourceID: "R1"})
	if byRes.Total != 2 {
		t.Fatalf("resource filter total = %d, want 2", byRes.Total)
	}
}
